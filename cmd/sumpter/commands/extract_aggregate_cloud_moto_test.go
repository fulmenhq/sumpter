//go:build s3integration

// S3 live-integration tests for aggregate cloud output: require a live
// S3-compatible endpoint, excluded from the default/CI build. Run with
// `-tags s3integration` (see `make test-integration-s3`).

package commands

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestMotoAggregateOutputToCloud publishes aggregate NDJSON shards + the sidecar
// manifest to s3:// through the SUM-005 write boundary (R1). It asserts every shard
// object is published, the manifest records output_mode=aggregate with a per-shard
// content digest and the credential-handle NAME (R2/S8), and the staging path never
// leaks into any published artifact and is cleaned up.
func TestMotoAggregateOutputToCloud(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "agg/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	opts := cloudOutputExtractOptions(dir, outURI, credPath, "json")
	opts.OutputMode = "aggregate"
	// A modest byte cap satisfies the R7 proactive cloud requirement; max-records 1
	// forces a shard per record so the multi-shard cloud publish path is exercised.
	opts.AggregateMaxBytes = 1 << 20
	opts.AggregateMaxRecords = 1

	if err := runExtract(opts); err != nil {
		t.Fatalf("aggregate->cloud run error = %v", err)
	}

	manifestData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatalf("aggregate provenance sidecar %smanifest.json was not published", prefix)
	}
	var man provenance.Manifest
	if err := json.Unmarshal(manifestData, &man); err != nil {
		t.Fatalf("decode published manifest: %v", err)
	}
	if man.OutputMode != "aggregate" || len(man.AggregateOutputs) == 0 {
		t.Fatalf("manifest is not aggregate: mode=%q shards=%d", man.OutputMode, len(man.AggregateOutputs))
	}
	if man.Incomplete {
		t.Errorf("successful run wrote incomplete=true")
	}

	stageRoot := filepath.Join(home, "work", "cloud")
	for _, shard := range man.AggregateOutputs {
		shardData, ok := m.getObject(t, prefix+shard.Path)
		if !ok {
			t.Errorf("shard object %s%s was not published (R1)", prefix, shard.Path)
			continue
		}
		if !strings.HasPrefix(shard.SHA256, "sha256:") {
			t.Errorf("shard %s missing content digest (R3)", shard.Path)
		}
		if shard.CredentialsHandle != "default" {
			t.Errorf("shard %s credentials_handle = %q, want the resolved handle name (S8)", shard.Path, shard.CredentialsHandle)
		}
		if strings.Contains(string(shardData), stageRoot) {
			t.Errorf("published shard %s leaked the staging path %q", shard.Path, stageRoot)
		}
	}
	if strings.Contains(string(manifestData), stageRoot) {
		t.Errorf("published manifest leaked the staging path %q", stageRoot)
	}
	assertStagingCleanedUp(t, home)
}
