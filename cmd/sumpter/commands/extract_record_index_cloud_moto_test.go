//go:build s3integration

// S3 live-integration tests for the cloud record-index / parallel read boundary
// (PR-3b). They require a live S3-compatible endpoint and are excluded from the
// default/CI build. Run with `-tags s3integration` (see `make test-integration-s3`
// and docs/sop/cicd-and-local-gates.md). They share the moto harness, env
// contract, and leak/identity assertions in extract_moto_test.go.
//
// What these prove for the record-index path specifically:
//   - `index build` against an s3:// source stages to a local working copy, builds
//     against the staged bytes, and records the LOGICAL s3:// URI in the index
//     header (never the staging path).
//   - `extract --record-index` re-acquires the cloud source, verifies the staged
//     bytes against the index header (size + SHA-256), reads byte ranges locally,
//     and threads the logical URI into provenance, output naming, _runtime, and
//     manifests — never the staging path — across both parallel paths (JSON
//     streaming and the in-memory/parquet path).
//   - A source object mutated after the index was built fails fast (stale offsets),
//     for both `extract --record-index` and `index verify`.

package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index/store"
)

// buildCloudIndex drives the real `index build` command against a cloud source,
// exercising the PR-3b acquire/stage/record-logical-URI path. It returns the
// resulting local index file path. indexBase is a suffixless local base path.
func buildCloudIndex(t *testing.T, logicalURI, indexBase, credPath string) string {
	t.Helper()
	cmd := newIndexBuildCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{
		logicalURI,
		"--selector", "//item",
		"--output", indexBase,
		"--progress=false",
		"--credentials", credPath,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("index build (cloud source %s) error = %v", logicalURI, err)
	}
	return indexBase + ".recordindex.json"
}

// assertIndexHeaderRecordsLogicalURI fails unless the built index records the
// logical s3:// URI as its source path (not the staging path).
func assertIndexHeaderRecordsLogicalURI(t *testing.T, indexPath, logicalURI string) {
	t.Helper()
	st, err := store.Open(indexPath)
	if err != nil {
		t.Fatalf("open index %s: %v", indexPath, err)
	}
	defer func() { _ = st.Close() }()
	header, err := st.Header()
	if err != nil {
		t.Fatalf("read index header: %v", err)
	}
	if header.Source.Path != logicalURI {
		t.Errorf("index header source.path = %q, want logical URI %q", header.Source.Path, logicalURI)
	}
	stageRoot := filepath.Join(os.Getenv("SUMPTER_HOME"), "work", "cloud")
	if strings.Contains(header.Source.Path, stageRoot) {
		t.Errorf("index header source.path leaked the staging root %q", stageRoot)
	}
}

// recordIndexExtractOptions builds extract options that route to the parallel
// record-index path against a cloud-sourced index. Files points at the local
// fixture only to satisfy input-presence wiring; the parallel path reads its
// source from the index header and re-acquires it via the credentials config.
func recordIndexExtractOptions(dir, indexPath, outDir, credPath, format string) *ExtractOptions {
	return &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          format,
		OutputPath:      outDir,
		OutputPattern:   "extract-{}." + format,
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RecordIndex:     indexPath,
		Workers:         2,
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
		CredentialsPath: credPath,
	}
}

// TestMotoRecordIndexExtractJSONStreamingNoLeak builds an index from a cloud
// source, then runs `extract --record-index` on the JSON-streaming parallel path.
// It asserts the logical URI is what reaches the index header, _runtime, and the
// manifest — never the staging path — and that staging is cleaned up.
func TestMotoRecordIndexExtractJSONStreamingNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "record-index/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key
	credPath := m.writeCredentialsConfig(t, dir)

	indexPath := buildCloudIndex(t, logicalURI, filepath.Join(dir, "cloud-index"), credPath)
	assertIndexHeaderRecordsLogicalURI(t, indexPath, logicalURI)

	outDir := filepath.Join(dir, "out-record-index-json")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := runExtract(recordIndexExtractOptions(dir, indexPath, outDir, credPath, "json")); err != nil {
		t.Fatalf("runExtract(record-index json streaming) error = %v", err)
	}

	assertLogicalIdentityNoLeak(t, outDir, home, logicalURI)
}

// TestMotoRecordIndexExtractParquetInMemoryNoLeak covers the in-memory parallel
// path (non-streaming) via parquet output: the logical URI must land in the
// manifest and the parquet source_file metadata, and the staging path must not
// appear in any artifact.
func TestMotoRecordIndexExtractParquetInMemoryNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "record-index/doc-parquet.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key
	credPath := m.writeCredentialsConfig(t, dir)

	indexPath := buildCloudIndex(t, logicalURI, filepath.Join(dir, "cloud-index-pq"), credPath)

	outDir := filepath.Join(dir, "out-record-index-parquet")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := runExtract(recordIndexExtractOptions(dir, indexPath, outDir, credPath, "parquet")); err != nil {
		t.Fatalf("runExtract(record-index parquet in-memory) error = %v", err)
	}

	// Manifest records the logical URI as the input.
	manifest := readMotoArtifact(t, filepath.Join(outDir, "manifest.json"))
	if !strings.Contains(manifest, logicalURI) {
		t.Errorf("manifest does not record logical URI %q:\n%s", logicalURI, manifest)
	}
	// Parquet file metadata (sumpter.source_file) carries the logical URI.
	parquet := readMotoArtifact(t, filepath.Join(outDir, soleRecordOutputName(t, outDir)))
	if !strings.Contains(parquet, logicalURI) {
		t.Errorf("parquet output does not embed logical URI %q in metadata", logicalURI)
	}
	assertNoStagingLeak(t, outDir, home)
	assertStagingCleanedUp(t, home)
}

// TestMotoRecordIndexSourceMutationFails proves the read-boundary integrity guard:
// if the cloud source object changes after the index is built, the recorded byte
// offsets are stale and extraction must fail fast rather than read garbage.
func TestMotoRecordIndexSourceMutationFails(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "record-index/mutated.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key
	credPath := m.writeCredentialsConfig(t, dir)

	indexPath := buildCloudIndex(t, logicalURI, filepath.Join(dir, "cloud-index-mut"), credPath)

	// Mutate the source object after the index was built (different length + bytes).
	m.putObject(t, key, []byte(`<root><item><name>CHANGED</name></item></root>`))

	outDir := filepath.Join(dir, "out-record-index-mut")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	err := runExtract(recordIndexExtractOptions(dir, indexPath, outDir, credPath, "json"))
	if err == nil {
		t.Fatal("runExtract(mutated cloud source) error = nil, want a source-integrity mismatch failure")
	}
	if !strings.Contains(err.Error(), "mismatch") {
		t.Errorf("error = %v, want a size/SHA-256 mismatch failure", err)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoIndexVerifyCloud proves `index verify` on a cloud source acquires and
// verifies symmetrically with build: it passes for an unchanged object and fails
// once the object is mutated.
func TestMotoIndexVerifyCloud(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "record-index/verify.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key
	credPath := m.writeCredentialsConfig(t, dir)

	indexPath := buildCloudIndex(t, logicalURI, filepath.Join(dir, "cloud-index-verify"), credPath)

	runVerify := func() error {
		cmd := newIndexVerifyCommand()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs([]string{
			logicalURI,
			"--index", indexPath,
			"--progress=false",
			"--credentials", credPath,
		})
		return cmd.Execute()
	}

	if err := runVerify(); err != nil {
		t.Fatalf("index verify (unchanged cloud source) error = %v, want pass", err)
	}

	// Mutate and re-verify: must fail.
	m.putObject(t, key, []byte(`<root><item><name>CHANGED</name></item></root>`))
	if err := runVerify(); err == nil {
		t.Error("index verify (mutated cloud source) error = nil, want verification failure")
	}
	assertStagingCleanedUp(t, home)
}
