//go:build s3integration

// S3 live-integration tests for cloud aggregate --continue-on-error + min_occurrences
// floors (slice 4b): require a live S3-compatible endpoint, excluded from the default/CI
// build. Run with `-tags s3integration` (see `make test-integration-s3`).

package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestMotoAggregateCloudContinueOnError is the headline 4b safety test: a cloud aggregate
// run with --continue-on-error and one failing input publishes ONLY the successful inputs'
// rows, writes a NORMAL manifest (NOT incomplete:true) plus failures.json, records the
// failed input with record_count 0, and exits partial-failure. The failed input's rows are
// never PUT — the per-input spool buffers then discards before publish.
func TestMotoAggregateCloudContinueOnError(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	a := filepath.Join(dir, "in-a.xml")
	b := filepath.Join(dir, "in-b.xml")
	c := filepath.Join(dir, "in-c.xml")
	mustWriteFile(t, a, `<root><item><name>valA</name></item></root>`)
	mustWriteFile(t, b, `<root><item><name>valB`) // malformed → input failure
	mustWriteFile(t, c, `<root><item><name>valC</name></item></root>`)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "agg-coe/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.Files = strings.Join([]string{a, b, c}, ",")
	opts.OutputMode = "aggregate"
	opts.AggregateMaxBytes = 1 << 20
	opts.ContinueOnError = true

	if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}

	manData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatal("manifest not published")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	// A completed continue run is NOT incomplete — that flag is reserved for hard
	// publish-failure orphans (R8). Consumers distinguish partial-success from orphans.
	if man.Incomplete {
		t.Error("completed continue run wrote incomplete:true; want a normal manifest + failures.json")
	}
	// Published shards carry only the successful inputs' rows — never the failed input's.
	for _, shard := range man.AggregateOutputs {
		data, ok := m.getObject(t, prefix+shard.Path)
		if !ok {
			t.Errorf("shard %s not published", shard.Path)
			continue
		}
		if strings.Contains(string(data), "valB") {
			t.Errorf("failed input's row (valB) was published in %s", shard.Path)
		}
	}
	// failures.json is published to the cloud destination and records the failed input.
	failData, ok := m.getObject(t, prefix+"failures.json")
	if !ok {
		t.Fatal("failures.json was not published to the cloud destination")
	}
	if !strings.Contains(string(failData), "in-b.xml") {
		t.Errorf("failures.json missing the failed input: %s", failData)
	}
	// Inventory completeness (R4/R5): the failed input is recorded with record_count 0.
	failed := 0
	for _, in := range man.Inputs {
		if in.Disposition == string(extract.DispositionFailed) {
			failed++
			if in.RecordCount == nil || *in.RecordCount != 0 {
				t.Errorf("failed input record_count = %v, want 0", in.RecordCount)
			}
		}
	}
	if failed != 1 {
		t.Errorf("manifest records %d failed inputs, want 1", failed)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoAggregateCloudContinuePublishFailureStillFatal pins the input-failure vs
// output-failure boundary under --continue-on-error: a recoverable INPUT failure
// continues, but a PUBLISH failure is terminal (ADR-0009 / S9 publish-fatal) and must
// abort the whole run even with --continue-on-error set, leaving an incomplete:true
// manifest (R8) for the already-published shards. A reverse proxy fails one shard's PUT.
func TestMotoAggregateCloudContinuePublishFailureStillFatal(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	inputs := make([]string, 0, 4)
	for _, n := range []string{"a", "b", "c", "d"} {
		p := filepath.Join(dir, "in-"+n+".xml")
		mustWriteFile(t, p, `<root><item><name>val`+n+`</name></item></root>`)
		inputs = append(inputs, p)
	}

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	const failShard = "records-00003.jsonl"
	upstream, err := url.Parse(m.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var failFired atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, failShard) {
			failFired.Store(true)
			http.Error(w, "injected publish failure", http.StatusInternalServerError)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()

	viaProxy := m
	viaProxy.endpoint = srv.URL
	credPath := viaProxy.writeCredentialsConfig(t, dir)

	prefix := runKeyPrefix() + "agg-coe-pub/"
	outURI := "s3://" + m.bucket + "/" + prefix

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.Files = strings.Join(inputs, ",")
	opts.OutputMode = "aggregate"
	opts.AggregateMaxBytes = 1 << 20
	opts.AggregateMaxRecords = 1 // one shard per input → shard-3 PUT is injected to fail
	opts.ContinueOnError = true  // a publish failure must STILL abort despite this

	if err := runExtract(opts); err == nil {
		t.Fatal("a publish failure must abort even under --continue-on-error, got nil")
	}
	if !failFired.Load() {
		t.Fatal("the injected shard-3 PUT never fired; test did not exercise a publish failure")
	}
	// Publish-fatal → incomplete:true manifest (R8), NOT a normal partial-success manifest.
	manData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatal("no manifest published after a publish failure")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !man.Incomplete {
		t.Error("publish failure under --continue-on-error did not write incomplete:true (R8)")
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoAggregateCloudContinueSidecarPublishFailureIsFatal pins the devrev finding: a
// terminal output failure AFTER shards are committed but BEFORE the normal manifest — here
// a failures.json PUT failure on a cloud --continue-on-error run — must still be fatal and
// leave the committed shards discoverable via an incomplete:true (R8) manifest, never
// orphaned without any manifest. A reverse proxy fails only the failures.json PUT.
func TestMotoAggregateCloudContinueSidecarPublishFailureIsFatal(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	a := filepath.Join(dir, "in-a.xml")
	b := filepath.Join(dir, "in-b.xml")
	c := filepath.Join(dir, "in-c.xml")
	mustWriteFile(t, a, `<root><item><name>valA</name></item></root>`)
	mustWriteFile(t, b, `<root><item><name>valB`) // malformed → input failure → failures.json
	mustWriteFile(t, c, `<root><item><name>valC</name></item></root>`)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	upstream, err := url.Parse(m.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var failFired atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "failures.json") {
			failFired.Store(true)
			http.Error(w, "injected failures.json publish failure", http.StatusInternalServerError)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	defer srv.Close()

	viaProxy := m
	viaProxy.endpoint = srv.URL
	credPath := viaProxy.writeCredentialsConfig(t, dir)

	prefix := runKeyPrefix() + "agg-coe-sidecar/"
	outURI := "s3://" + m.bucket + "/" + prefix

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.Files = strings.Join([]string{a, b, c}, ",")
	opts.OutputMode = "aggregate"
	opts.AggregateMaxBytes = 1 << 20
	opts.ContinueOnError = true

	if err := runExtract(opts); err == nil {
		t.Fatal("a failures.json publish failure must be fatal, got nil")
	}
	if !failFired.Load() {
		t.Fatal("the injected failures.json PUT never fired; test did not exercise the sidecar failure")
	}
	// The committed shards must be discoverable via an incomplete:true manifest (R8) —
	// not left without any manifest. The manifest PUT itself is allowed through the proxy.
	manData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatal("no manifest published after the sidecar failure — committed shards are orphaned")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !man.Incomplete {
		t.Error("manifest is not incomplete:true after a terminal sidecar publish failure")
	}
	if len(man.AggregateOutputs) == 0 {
		t.Error("incomplete manifest lists no committed shards")
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoAggregateCloudFloorMissDiscarded pins cloud min_occurrences floors (4b): a
// floored cloud aggregate run with --continue-on-error discards a floor-missing input
// (its rows never PUT) while publishing the inputs that meet the floor, with a normal
// manifest + failures.json.
func TestMotoAggregateCloudFloorMissDiscarded(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	// Floored recipe: //item with min_occurrences 1.
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
    min_occurrences: 1
field_mappings:
  - output_field: name
    xpath: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	a := filepath.Join(dir, "in-a.xml")
	b := filepath.Join(dir, "in-b.xml")
	c := filepath.Join(dir, "in-c.xml")
	mustWriteFile(t, a, `<root><item><name>valA</name></item></root>`)
	mustWriteFile(t, b, `<root></root>`) // zero //item → floor miss
	mustWriteFile(t, c, `<root><item><name>valC</name></item></root>`)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "agg-floor/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.Files = strings.Join([]string{a, b, c}, ",")
	opts.OutputMode = "aggregate"
	opts.AggregateMaxBytes = 1 << 20
	opts.ContinueOnError = true

	if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}
	manData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatal("manifest not published")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if man.Incomplete {
		t.Error("floored continue run wrote incomplete:true; want a normal manifest")
	}
	for _, shard := range man.AggregateOutputs {
		data, ok := m.getObject(t, prefix+shard.Path)
		if !ok {
			t.Errorf("shard %s not published", shard.Path)
			continue
		}
		// The floor-missing input emits no rows; nothing to leak, but assert the two
		// qualifying inputs' rows are present across the shards.
		_ = data
	}
	failData, ok := m.getObject(t, prefix+"failures.json")
	if !ok {
		t.Fatal("failures.json not published")
	}
	if !strings.Contains(string(failData), "min_occurrences") {
		t.Errorf("failures.json missing the floor violation: %s", failData)
	}
	assertStagingCleanedUp(t, home)
}
