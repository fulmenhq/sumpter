//go:build s3integration

// S3 live-integration tests for extract-multi cloud aggregate output UNDER INPUT-WORKER
// CONCURRENCY: require a live S3-compatible endpoint, excluded from the default/CI build.
// Run with `-tags s3integration` (see `make test-integration-s3`). Shares the moto harness
// in extract_moto_test.go.
//
// These prove the SUM-063 cloud invariants (R1 write-boundary publish, R7 proactive byte
// cap, R8 incomplete-on-partial-publish, S9 publish-fatal) still hold under --input-workers
// > 1. Originally written for SUM-066 (workers parsed only); since SUM-068 the workers run
// the FULL per-input recipe application (parse + signature/applicability/extraction/
// min_occurrences) into worker-local bundles, so these now re-prove the invariants with
// per-input APPLICATION running concurrently — not just parse. The guarantee is structural:
// workers only BUILD (no cloud calls); every shard stage/publish happens in
// commitAggregateApplication on the single ordered committer, so durable publishes stay
// drain-owned in deterministic shard order and concurrency multiplies neither cloud PUTs
// nor GETs.

package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// writeMultiCloudRecipeWorkspace is writeMultiRecipeWorkspace plus a cloud output
// credential handle: extract-multi reads each recipe's defaults.output.credentials_handle
// to authorize publishing its shards/manifest under the shared --output-path root.
func writeMultiCloudRecipeWorkspace(t *testing.T, id, outHandle string) string {
	t.Helper()
	ws := t.TempDir()
	for _, d := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: `+id+`
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
    credentials_handle: `+outHandle+`
  workers: 1
`)
	mustWriteFile(t, filepath.Join(ws, "signature/signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract/extract.yaml"), `record_type: `+id+`_record
match_selectors:
  - xpath: //TargetElement
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	return ws
}

// cloudShardSnapshot reads one recipe's published manifest and concatenates its shard
// objects in manifest (deterministic) order, with the volatile _runtime.generated_at
// blanked. It returns the normalized content plus a stable manifest projection that
// EXCLUDES the per-shard sha256 (which digests the written bytes including
// generated_at, so it is a tamper stamp, not a cross-run determinism check).
func cloudShardSnapshot(t *testing.T, m motoEnv, recipePrefix string) (content string, stable string) {
	t.Helper()
	manData, ok := m.getObject(t, recipePrefix+"manifest.json")
	if !ok {
		t.Fatalf("manifest not published at %smanifest.json", recipePrefix)
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest %s: %v", recipePrefix, err)
	}
	if man.OutputMode != outputModeAggregate {
		t.Errorf("%s manifest output_mode = %q, want aggregate", recipePrefix, man.OutputMode)
	}
	if man.Incomplete {
		t.Errorf("%s successful run wrote incomplete:true", recipePrefix)
	}
	var b strings.Builder
	prevName := ""
	for _, shard := range man.AggregateOutputs {
		if prevName != "" && shard.Path <= prevName {
			t.Errorf("%s shards not in ascending order: %q then %q", recipePrefix, prevName, shard.Path)
		}
		prevName = shard.Path
		data, ok := m.getObject(t, recipePrefix+shard.Path)
		if !ok {
			t.Errorf("%s shard %s not published (R1)", recipePrefix, shard.Path)
			continue
		}
		b.WriteString("=== " + shard.Path + " ===\n")
		b.WriteString(volatileGeneratedAtRE.ReplaceAllString(string(data), `"generated_at":""`))
	}
	// Stable projection: zero the timestamp-inclusive shard digest; keep record_count,
	// ordinal spans, the per-input inventory, and the record-type counts.
	for i := range man.AggregateOutputs {
		man.AggregateOutputs[i].SHA256 = ""
	}
	proj := struct {
		Inputs           []provenance.Input
		AggregateOutputs []provenance.AggregateOutput
		Counts           map[string]int
	}{man.Inputs, man.AggregateOutputs, man.CountsByRecordType}
	out, err := json.MarshalIndent(proj, "", "  ")
	if err != nil {
		t.Fatalf("marshal stable projection: %v", err)
	}
	return b.String(), string(out)
}

// TestMotoExtractMultiCloudInputWorkersEquivalence proves a cloud extract-multi
// aggregate run is EQUIVALENT at --input-workers 1 and >1: every recipe's published
// shard content (generated_at normalized), shard ordering, per-input inventory, and
// record counts match the single-worker run. Multiple shards (max-records 2 over 6
// inputs) exercise the deterministic multi-shard cloud publish path under concurrency.
func TestMotoExtractMultiCloudInputWorkersEquivalence(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	credPath := m.writeCredentialsConfig(t, dir)
	wsA := writeMultiCloudRecipeWorkspace(t, "summary", "default")
	wsB := writeMultiCloudRecipeWorkspace(t, "line-items", "default")
	fileList, _ := writeMultiInputSet(t, 6)
	recipeIDs := []string{"summary", "line-items"}

	run := func(workers int) string {
		home := t.TempDir()
		t.Setenv("SUMPTER_HOME", home)
		prefix := runKeyPrefix() + "mc-equiv/"
		shared := &multiSharedOptions{
			FileList:            fileList,
			OutputPath:          "s3://" + m.bucket + "/" + prefix,
			OutputMode:          outputModeAggregate,
			RunID:               testMultiRunID,
			AggregateMaxRecords: 2,
			AggregateMaxBytes:   1 << 20, // R7: cloud requires a byte cap
			CredentialsPath:     credPath,
			InputWorkers:        workers,
		}
		if err := runExtractMulti(shared, []string{wsA, wsB}, io.Discard, time.Now()); err != nil {
			t.Fatalf("workers=%d cloud extract-multi: %v", workers, err)
		}
		return prefix
	}

	basePrefix := run(1)
	wPrefix := run(4)

	for _, id := range recipeIDs {
		baseContent, baseStable := cloudShardSnapshot(t, m, basePrefix+id+"/")
		gotContent, gotStable := cloudShardSnapshot(t, m, wPrefix+id+"/")
		if gotContent != baseContent {
			t.Errorf("recipe %q: cloud shard content differs between workers=1 and workers=4\n--- serial ---\n%s\n--- got ---\n%s", id, baseContent, gotContent)
		}
		if gotStable != baseStable {
			t.Errorf("recipe %q: cloud manifest inventory/shards differ between workers=1 and workers=4\n--- serial ---\n%s\n--- got ---\n%s", id, baseStable, gotStable)
		}
	}
}

// TestMotoExtractMultiCloudByteCapPreflightWithWorkers proves the R7 proactive byte-cap
// preflight still fires BEFORE any worker/publish when --input-workers > 1: an uncapped
// or over-limit cloud aggregate run is rejected, and nothing is published.
func TestMotoExtractMultiCloudByteCapPreflightWithWorkers(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	credPath := m.writeCredentialsConfig(t, dir)
	ws := writeMultiCloudRecipeWorkspace(t, "summary", "default")
	fileList, _ := writeMultiInputSet(t, 4)

	cases := []struct {
		name string
		cap  int64
	}{
		{"uncapped", 0},
		{"over-limit", 6 << 30}, // > 5 GiB single-PUT limit
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prefix := runKeyPrefix() + "mc-preflight/"
			shared := &multiSharedOptions{
				FileList:          fileList,
				OutputPath:        "s3://" + m.bucket + "/" + prefix,
				OutputMode:        outputModeAggregate,
				RunID:             testMultiRunID,
				AggregateMaxBytes: tc.cap,
				CredentialsPath:   credPath,
				InputWorkers:      4,
			}
			err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
			if err == nil {
				t.Fatalf("%s cloud aggregate with workers should be rejected at preflight, got nil", tc.name)
			}
			// Nothing should have been published — the preflight fails before workers start.
			if _, ok := m.getObject(t, prefix+"summary/manifest.json"); ok {
				t.Errorf("%s: a manifest was published despite preflight rejection", tc.name)
			}
		})
	}
}

// TestMotoExtractMultiCloudPublishFailureFatalUnderWorkers proves S9 publish-fatal
// survives concurrency: with --input-workers > 1 AND --continue-on-error, a shard PUT
// failure still aborts the whole run and leaves an incomplete:true (R8) manifest
// recording the already-published shards. A reverse proxy fails one shard's PUT.
func TestMotoExtractMultiCloudPublishFailureFatalUnderWorkers(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	fileList, _ := writeMultiInputSet(t, 5)

	const failShard = "records-00003.jsonl"
	upstream, err := url.Parse(m.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	var failFired atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "summary/"+failShard) {
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
	ws := writeMultiCloudRecipeWorkspace(t, "summary", "default")

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)
	prefix := runKeyPrefix() + "mc-pubfail/"
	shared := &multiSharedOptions{
		FileList:            fileList,
		OutputPath:          "s3://" + m.bucket + "/" + prefix,
		OutputMode:          outputModeAggregate,
		RunID:               testMultiRunID,
		AggregateMaxRecords: 1, // one shard per input -> shard-3 PUT is injected to fail
		AggregateMaxBytes:   1 << 20,
		ContinueOnError:     true, // a publish failure must STILL abort despite this
		CredentialsPath:     credPath,
		InputWorkers:        4,
	}
	if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err == nil {
		t.Fatal("a publish failure must abort even under --continue-on-error + workers, got nil")
	}
	if !failFired.Load() {
		t.Fatal("the injected shard-3 PUT never fired; test did not exercise a publish failure")
	}
	manData, ok := m.getObject(t, prefix+"summary/manifest.json")
	if !ok {
		t.Fatal("no manifest published after a publish failure")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !man.Incomplete {
		t.Error("publish failure under workers did not write incomplete:true (R8)")
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractMultiCloudContinueInputFailureUnderWorkers proves the input-failure vs
// publish-failure boundary holds under concurrency: a recoverable INPUT failure under
// --input-workers > 1 + --continue-on-error publishes only the successful inputs' rows,
// writes a NORMAL (not incomplete) manifest plus failures.json, records the failed input
// with record_count 0, and exits partial-failure — the failed input's rows are never PUT.
func TestMotoExtractMultiCloudContinueInputFailureUnderWorkers(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	credPath := m.writeCredentialsConfig(t, dir)
	ws := writeMultiCloudRecipeWorkspace(t, "summary", "default")

	// Six inputs with a malformed one at ordinal 3 (inC).
	fileList := writeMixedInputSet(t, 6, 2)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)
	prefix := runKeyPrefix() + "mc-coe/"
	shared := &multiSharedOptions{
		FileList:          fileList,
		OutputPath:        "s3://" + m.bucket + "/" + prefix,
		OutputMode:        outputModeAggregate,
		RunID:             testMultiRunID,
		AggregateMaxBytes: 1 << 20,
		ContinueOnError:   true,
		CredentialsPath:   credPath,
		InputWorkers:      4,
	}
	err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "partial failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}

	manData, ok := m.getObject(t, prefix+"summary/manifest.json")
	if !ok {
		t.Fatal("manifest not published")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if man.Incomplete {
		t.Error("completed continue run wrote incomplete:true; want a normal manifest + failures.json")
	}
	for _, shard := range man.AggregateOutputs {
		data, ok := m.getObject(t, prefix+"summary/"+shard.Path)
		if !ok {
			t.Errorf("shard %s not published", shard.Path)
			continue
		}
		if strings.Contains(string(data), "oops") {
			t.Errorf("failed input's row was published in %s", shard.Path)
		}
	}
	failData, ok := m.getObject(t, prefix+"summary/failures.json")
	if !ok {
		t.Fatal("failures.json was not published to the cloud destination")
	}
	if !strings.Contains(string(failData), "inC.xml") {
		t.Errorf("failures.json missing the failed input: %s", failData)
	}
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

// TestMotoExtractMultiCloudApplicationConcurrentAndDigests is the SUM-068 slice-4 G5
// re-proof. It proves two things on the cloud aggregate path at --input-workers > 1:
//
//  1. Per-input APPLICATION (not just parse) overlaps across workers — a blocking hook in
//     the worker application stage observes >= 2 inputs in-stage concurrently, which the
//     pre-SUM-068 parse-only worker path could never do.
//  2. Each published shard's bytes match its manifest digest (the tracked digest-vs-
//     published-object integrity check): the per-shard manifest SHA256 equals the SHA256 of
//     the object actually fetched back from the store. This ties the drain-owned, ordered
//     publish to a verifiable content digest under application concurrency.
func TestMotoExtractMultiCloudApplicationConcurrentAndDigests(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	credPath := m.writeCredentialsConfig(t, dir)
	ws := writeMultiCloudRecipeWorkspace(t, "summary", "default")
	fileList, _ := writeMultiInputSet(t, 6)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)
	prefix := runKeyPrefix() + "mc-appconc-digest/"
	shared := &multiSharedOptions{
		FileList:            fileList,
		OutputPath:          "s3://" + m.bucket + "/" + prefix,
		OutputMode:          outputModeAggregate,
		RunID:               testMultiRunID,
		AggregateMaxRecords: 2, // multiple shards
		AggregateMaxBytes:   1 << 20,
		CredentialsPath:     credPath,
		InputWorkers:        4, // application-concurrent
	}

	d := newMultiDispatcher(shared, io.Discard)
	// Hold every input inside the worker application stage until we have observed overlap,
	// then release: this both proves application concurrency and guarantees the publishes
	// that follow are drain-owned (they cannot start until the held applications release).
	release := make(chan struct{})
	var concurrent, peak int32
	d.onBuildApplication = func(ordinal int) {
		c := atomic.AddInt32(&concurrent, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
				break
			}
		}
		<-release
		atomic.AddInt32(&concurrent, -1)
	}

	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()
	deadline := time.After(10 * time.Second)
	for atomic.LoadInt32(&peak) < 2 {
		select {
		case <-deadline:
			close(release)
			<-done
			t.Fatalf("cloud per-input application never reached 2 concurrent workers (peak=%d)", atomic.LoadInt32(&peak))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("cloud extract-multi: %v", err)
	}

	manData, ok := m.getObject(t, prefix+"summary/manifest.json")
	if !ok {
		t.Fatal("manifest not published")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if man.Incomplete {
		t.Error("successful application-concurrent run wrote incomplete:true")
	}
	if len(man.AggregateOutputs) < 2 {
		t.Fatalf("expected multiple published shards, got %d", len(man.AggregateOutputs))
	}
	for _, shard := range man.AggregateOutputs {
		data, ok := m.getObject(t, prefix+"summary/"+shard.Path)
		if !ok {
			t.Errorf("shard %s not published (R1)", shard.Path)
			continue
		}
		sum := sha256.Sum256(data)
		want := "sha256:" + hex.EncodeToString(sum[:])
		if shard.SHA256 != want {
			t.Errorf("shard %s: manifest digest %q != sha256 of the published object %q", shard.Path, shard.SHA256, want)
		}
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractMultiBoundedCloudURIListDistinctHandles proves bounded
// cloud URI-list input to aggregate cloud output uses the shared CLI reader
// handle and the recipe-owned writer handle independently.
func TestMotoExtractMultiBoundedCloudURIListDistinctHandles(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	dir := t.TempDir()
	credPath := m.writeNamedCredentialsConfig(t, dir, "reader", "writer")
	ws := writeMultiCloudRecipeWorkspace(t, "summary", "writer")

	aKey := runKeyPrefix() + "in/a.xml"
	bKey := runKeyPrefix() + "in/b.xml"
	m.putObject(t, aKey, []byte(`<root><TargetElement><Name>A</Name></TargetElement></root>`))
	m.putObject(t, bKey, []byte(`<root><TargetElement><Name>B</Name></TargetElement></root>`))
	list := filepath.Join(dir, "uris.txt")
	mustWriteFile(t, list, "s3://"+m.bucket+"/"+aKey+"\ns3://"+m.bucket+"/"+bKey+"\n")

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)
	prefix := runKeyPrefix() + "mc-handles/"
	shared := &multiSharedOptions{
		FileList:               list,
		OutputPath:             "s3://" + m.bucket + "/" + prefix,
		OutputMode:             outputModeAggregate,
		RunID:                  testMultiRunID,
		AggregateMaxBytes:      1 << 20,
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
		CloudInputMode:         cloudInputModeBounded,
		CloudStagingMaxFiles:   4,
		CloudStagingMaxBytes:   1 << 20,
		CloudObjectMaxBytes:    1 << 20,
	}
	if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatal(err)
	}
	manData, ok := m.getObject(t, prefix+"summary/manifest.json")
	if !ok {
		t.Fatal("manifest not published")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatal(err)
	}
	if len(man.Inputs) != 2 {
		t.Fatalf("inputs %d", len(man.Inputs))
	}
	for _, in := range man.Inputs {
		if in.CredentialsHandle != "reader" {
			t.Errorf("input handle %q want reader", in.CredentialsHandle)
		}
	}
	if len(man.AggregateOutputs) == 0 {
		t.Fatal("no aggregate shards")
	}
	for _, shard := range man.AggregateOutputs {
		if shard.CredentialsHandle != "writer" {
			t.Errorf("shard handle %q want writer", shard.CredentialsHandle)
		}
	}
	assertStagingCleanedUp(t, home)
}
