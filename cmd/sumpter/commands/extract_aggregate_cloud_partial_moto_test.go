//go:build s3integration

// S3 live-integration test for the R8 partial-publish contract: a mid-run cloud
// publish failure must leave a discoverable incomplete manifest. Excluded from the
// default/CI build; run with `-tags s3integration` (see `make test-integration-s3`).

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

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestMotoAggregateCloudPartialPublishIncompleteManifest exercises the full R8
// partial-publish contract end to end: it forces a mid-run publish failure AFTER
// some aggregate shards have already committed, then asserts the run fails, an
// incomplete:true manifest is published that enumerates EXACTLY the committed shard
// objects, and those objects actually exist in the bucket while the failed shard
// does not.
//
// The fault is injected with a transparent reverse proxy in front of the endpoint
// that returns 500 for the PUT of one specific shard object. Matching on the
// deterministic shard key (records-00003.jsonl) rather than a global request
// counter is retry-safe: SDK retries of the failed shard keep failing while earlier
// shards and the incomplete-manifest PUT pass through untouched. The writer is
// pointed at the proxy; all verification reads the real endpoint directly.
func TestMotoAggregateCloudPartialPublishIncompleteManifest(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	// Four records at --aggregate-max-records 1 produce four shards. Failing the
	// third shard's PUT commits shards 1-2, aborts on shard 3, and never reaches 4.
	mustWriteFile(t, filepath.Join(dir, "input.xml"),
		`<root><item><name>A</name></item><item><name>B</name></item><item><name>C</name></item><item><name>D</name></item></root>`)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	const failShard = "records-00003.jsonl" // committed: 00001, 00002
	const wantCommitted = 2

	upstream, err := url.Parse(m.endpoint)
	if err != nil {
		t.Fatalf("parse endpoint %q: %v", m.endpoint, err)
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

	// Point the writer at the proxy (insecure http); verify against real moto via m.
	viaProxy := m
	viaProxy.endpoint = srv.URL
	credPath := viaProxy.writeCredentialsConfig(t, dir)

	prefix := runKeyPrefix() + "agg-partial/"
	outURI := "s3://" + m.bucket + "/" + prefix

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.OutputMode = "aggregate"
	opts.AggregateMaxBytes = 1 << 20
	opts.AggregateMaxRecords = 1

	if err := runExtract(opts); err == nil {
		t.Fatal("runExtract(injected shard-3 publish failure) error = nil, want a fatal publish failure")
	}
	if !failFired.Load() {
		t.Fatal("the injected shard-3 PUT never fired: the test did not exercise a mid-run publish failure (shard naming may have changed)")
	}

	// R8: an incomplete:true manifest must be published listing the committed shards.
	manifestData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatal("R8: no manifest was published to the destination after a mid-run publish failure")
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manifestData, &man); err != nil {
		t.Fatalf("decode published incomplete manifest: %v", err)
	}
	if !man.Incomplete {
		t.Error("R8: manifest published after a failed run is not incomplete:true")
	}
	if man.OutputMode != "aggregate" {
		t.Errorf("R8: incomplete manifest output_mode = %q, want aggregate", man.OutputMode)
	}

	// It must enumerate exactly the committed shards (1-2), and each listed object
	// must actually exist in the bucket — the point of R8 is orphan discoverability.
	if len(man.AggregateOutputs) != wantCommitted {
		t.Errorf("R8: incomplete manifest lists %d shards, want %d committed (%v)",
			len(man.AggregateOutputs), wantCommitted, shardPaths(man.AggregateOutputs))
	}
	for _, shard := range man.AggregateOutputs {
		if shard.Path == failShard {
			t.Errorf("R8: incomplete manifest lists the failed shard %s as committed", failShard)
		}
		if _, ok := m.getObject(t, prefix+shard.Path); !ok {
			t.Errorf("R8: incomplete manifest lists %s but the object is not in the bucket", shard.Path)
		}
	}

	// The failed shard must NOT have been published.
	if _, ok := m.getObject(t, prefix+failShard); ok {
		t.Errorf("R8: the failed shard %s was published despite the PUT failure", failShard)
	}

	// Staging must be cleaned up even on the failure path.
	assertStagingCleanedUp(t, home)
}

// shardPaths flattens shard paths for a readable assertion message.
func shardPaths(shards []provenance.AggregateOutput) []string {
	paths := make([]string, 0, len(shards))
	for _, s := range shards {
		paths = append(paths, s.Path)
	}
	return paths
}
