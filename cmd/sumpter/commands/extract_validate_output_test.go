package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

func TestNormalizeValidateOutput(t *testing.T) {
	if got := normalizeValidateOutput(""); got != validateOutputOff {
		t.Fatalf("empty = %q, want off", got)
	}
	if got := normalizeValidateOutput(" STRICT "); got != validateOutputStrict {
		t.Fatalf("strict = %q, want strict", got)
	}
}

func TestValidateOutputIncludesLadder(t *testing.T) {
	if validateOutputIncludes(validateOutputOff, validateOutputSidecars) {
		t.Fatal("off must not include sidecars")
	}
	if !validateOutputIncludes(validateOutputSidecars, validateOutputSidecars) {
		t.Fatal("sidecars should include sidecars")
	}
	if validateOutputIncludes(validateOutputSidecars, validateOutputArtifact) {
		t.Fatal("sidecars must not include artifact")
	}
	if !validateOutputIncludes(validateOutputArtifact, validateOutputSidecars) {
		t.Fatal("artifact should include sidecars")
	}
	if !validateOutputIncludes(validateOutputEnvelopeSample, validateOutputArtifact) {
		t.Fatal("envelope-sample should include artifact")
	}
	if !validateOutputIncludes(validateOutputStrict, validateOutputEnvelopeSample) {
		t.Fatal("strict should include envelope-sample rung membership")
	}
}

func TestValidateValidateOutputOptionsRejectsUnknown(t *testing.T) {
	err := validateValidateOutputOptions(&ExtractOptions{
		ValidateOutput: "payload-all",
		OutputPath:     "/tmp/out",
	})
	if err == nil || !strings.Contains(err.Error(), "must be one of") {
		t.Fatalf("error = %v, want unknown mode rejection", err)
	}
}

func TestValidateValidateOutputOptionsRequiresArtifactForHigherRungs(t *testing.T) {
	err := validateValidateOutputOptions(&ExtractOptions{
		ValidateOutput: validateOutputArtifact,
		OutputPath:     "/tmp/out",
	})
	if err == nil || !strings.Contains(err.Error(), "--artifact-descriptor") {
		t.Fatalf("error = %v, want artifact-descriptor requirement", err)
	}
}

func TestRunExtractValidateOutputStrictSucceeds(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	contractBase := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")

	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Format:               "json",
		OutputPath:           outputDir,
		OutputPattern:        "records.jsonl",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
		ValidateOutput:       validateOutputStrict,
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract with --validate-output strict: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, provenance.ManifestFileName)); err != nil {
		t.Fatalf("manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, dataartifact.DescriptorFileName)); err != nil {
		t.Fatalf("descriptor missing: %v", err)
	}
}

func TestRunExtractValidateOutputSidecarsWithoutDescriptor(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.jsonl",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RunID:           testMultiRunID,
		ValidateOutput:  validateOutputSidecars,
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract with --validate-output sidecars: %v", err)
	}
}

func TestRunExtractValidateOutputDefaultOffIsNoop(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.jsonl",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RunID:           testMultiRunID,
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract default: %v", err)
	}
}

func TestExtractFilesFlagExposesValidateOutput(t *testing.T) {
	cmd := newExtractFilesCommand()
	if flag := cmd.Flags().Lookup("validate-output"); flag == nil {
		t.Fatal("extract files missing --validate-output flag")
	}
}

func TestMaybeValidateExtractOutputSkipsCloudSessionReopen(t *testing.T) {
	// Simulate a cloud output session after Publish has already removed staging.
	// End-of-run re-open must be a no-op; write-time validation is the cloud path.
	opts := &ExtractOptions{
		ValidateOutput: validateOutputSidecars,
		OutputPath:     "s3://example-bucket/out",
		outputSession:  &uriio.Session{},
	}
	if err := maybeValidateExtractOutput(opts, provenance.Manifest{}); err != nil {
		t.Fatalf("cloud end-of-run validate should no-op, got %v", err)
	}
}

func TestValidateOutputSidecarBytesBeforePublish(t *testing.T) {
	// Minimal valid provenance document shape for embedded schema check.
	data := []byte(`{
  "schema_version": "sumpter.provenance/v1",
  "run_id": "0190a3f4-1c2d-7abc-9def-0123456789ab",
  "sumpter_version": "0.3.0-dev",
  "started_at": "2026-07-09T00:00:00Z",
  "completed_at": "2026-07-09T00:00:01Z",
  "cli": {"command": "sumpter extract files", "argv_sanitized": ["sumpter", "extract", "files"]},
  "inputs": [],
  "outputs": [],
  "counts_by_record_type": {}
}
`)
	validateFn, err := provenanceSidecarValidator()
	if err != nil {
		t.Fatalf("provenanceSidecarValidator: %v", err)
	}
	opts := &ExtractOptions{ValidateOutput: validateOutputSidecars}
	if err := validateOutputSidecarBytes(opts, data, provenance.ManifestFileName, validateFn); err != nil {
		t.Fatalf("valid sidecar bytes rejected: %v", err)
	}
	if err := validateOutputSidecarBytes(opts, []byte(`{"schema_version":"nope"}`), provenance.ManifestFileName, validateFn); err == nil {
		t.Fatal("invalid sidecar bytes accepted")
	}
}

func TestMaybeValidateEnvelopeFileBeforePublishOnStagingPath(t *testing.T) {
	dir := t.TempDir()
	// One envelope-shaped NDJSON line on a staging path (cloud Publish would delete this).
	line := `{"_runtime":{"envelope_schema":"extract-record-envelope/v0","generated_at":"2026-07-09T00:00:00Z","source_file":"a.xml","record_type":"item","summaries_included":false,"validation_included":false},"extract":{"data":{"id":"1"}}}` + "\n"
	path := filepath.Join(dir, "stage-records.jsonl")
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	opts := &ExtractOptions{ValidateOutput: validateOutputEnvelopeSample}
	if err := maybeValidateEnvelopeFileBeforePublish(opts, path, "s3://bucket/records.jsonl"); err != nil {
		t.Fatalf("pre-publish envelope validate: %v", err)
	}
	// Staging can be removed after Publish; end-of-run cloud path must not depend on it.
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove staging: %v", err)
	}
	cloudOpts := &ExtractOptions{
		ValidateOutput: validateOutputEnvelopeSample,
		outputSession:  &uriio.Session{},
	}
	if err := maybeValidateExtractOutput(cloudOpts, provenance.Manifest{
		Outputs: []provenance.Output{{Path: "s3://bucket/records.jsonl", Format: "json", RecordCount: 1}},
	}); err != nil {
		t.Fatalf("post-publish cloud validate should no-op, got %v", err)
	}
}
