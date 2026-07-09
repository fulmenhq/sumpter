package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/provenance"
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
