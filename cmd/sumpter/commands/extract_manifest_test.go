package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/validation"
)

func TestRunExtractWritesDirectProvenanceManifest(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files", "--output-path=" + outputDir, "--api-token=secret"},
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.Recipe != nil {
		t.Fatalf("direct manifest recipe = %#v, want nil", manifest.Recipe)
	}
	if manifest.RunID == "" {
		t.Fatal("run_id is empty")
	}
	if manifest.SumpterVersion == "" {
		t.Fatal("sumpter_version is empty")
	}
	if len(manifest.Inputs) != 1 {
		t.Fatalf("inputs len = %d, want 1", len(manifest.Inputs))
	}
	if manifest.Inputs[0].Path != "input.xml" {
		t.Fatalf("input path = %q, want input.xml", manifest.Inputs[0].Path)
	}
	if len(manifest.Outputs) != 1 {
		t.Fatalf("outputs len = %d, want 1", len(manifest.Outputs))
	}
	if manifest.Outputs[0].RecordCount != 2 {
		t.Fatalf("record_count = %d, want 2", manifest.Outputs[0].RecordCount)
	}
	if manifest.CountsByRecordType["sample_record"] != 2 {
		t.Fatalf("counts_by_record_type = %#v, want sample_record=2", manifest.CountsByRecordType)
	}
	for _, arg := range manifest.CLI.ArgvSanitized {
		if arg == "--api-token=secret" || arg == outputDir {
			t.Fatalf("argv leaked unsanitized value: %#v", manifest.CLI.ArgvSanitized)
		}
	}
}

func TestRunExtractManifestRecordsEffectiveSequentialFormat(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "csv",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if got := manifest.Outputs[0].Format; got != "json" {
		t.Fatalf("output format = %q, want json", got)
	}
}

func TestRunExtractWritesRecipeBackedManifest(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		CommandName:     "sumpter recipes run extract",
		Recipe: &provenance.Recipe{
			ID:                    "sample_extract",
			ManifestSchemaVersion: "recipe/v0.1.0",
			ContentVersion:        "1.0.0",
			ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Owners:                []provenance.Owner{{Name: "Fulmen Sumpter contributors"}},
			ManifestYAML:          "version: recipe/v0.1.0\nid: sample_extract\n",
			SignatureYAML:         "signature_id: sample\n",
			ExtractYAML:           "record_type: sample_record\n",
		},
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.Recipe == nil {
		t.Fatal("recipe-backed manifest omitted recipe")
	}
	if manifest.Recipe.SignatureYAML != opts.Recipe.SignatureYAML {
		t.Fatalf("signature_yaml = %q, want %q", manifest.Recipe.SignatureYAML, opts.Recipe.SignatureYAML)
	}
	if len(manifest.Recipe.FieldProvenance) != 1 {
		t.Fatalf("field provenance len = %d, want 1", len(manifest.Recipe.FieldProvenance))
	}
	field := manifest.Recipe.FieldProvenance[0]
	if field.OutputField != "name" || field.XPath != "name" || field.Description != "Sample name" {
		t.Fatalf("unexpected field provenance: %#v", field)
	}
}

func TestBuildFieldProvenanceIncludesExpressionMappings(t *testing.T) {
	fields := buildFieldProvenance([]extract.FieldMapping{
		{
			OutputField: "a_count",
			XPath:       "A",
			Type:        "integer",
		},
		{
			OutputField: "total_count",
			Expression:  "a_count + b_count",
			Type:        "integer",
			Description: "Derived total count.",
		},
	})

	if len(fields) != 2 {
		t.Fatalf("field provenance len = %d, want 2", len(fields))
	}
	if fields[1].OutputField != "total_count" || fields[1].Expression != "a_count + b_count" ||
		fields[1].XPath != "" || fields[1].Description != "Derived total count." {
		t.Fatalf("unexpected expression provenance: %#v", fields[1])
	}
}

func TestRunExtractNoManifestSkipsSidecar(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		NoManifest:      true,
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, provenance.ManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("manifest stat error = %v, want not exists", err)
	}
}

func TestRunExtractWritesParallelProvenanceManifest(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	xmlPath := filepath.Join(dir, "input.xml")
	indexPath := filepath.Join(dir, "input.recordindex.json")
	builder := index.NewBuilder(index.BuildOptions{
		InputPath:  xmlPath,
		OutputPath: indexPath,
		Selector:   "//item",
	})
	recordIndex, err := builder.Build()
	if err != nil {
		t.Fatalf("Build index: %v", err)
	}
	if err := builder.WriteToFile(recordIndex, indexPath); err != nil {
		t.Fatalf("WriteToFile index: %v", err)
	}

	opts := &ExtractOptions{
		Files:           xmlPath,
		Format:          "json",
		OutputPath:      outputDir,
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RecordIndex:     indexPath,
		Workers:         2,
		CommandName:     "sumpter extract files",
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.Outputs) != 1 {
		t.Fatalf("outputs len = %d, want 1", len(manifest.Outputs))
	}
	if manifest.Outputs[0].Path != "extract-parallel.json" {
		t.Fatalf("output path = %q, want extract-parallel.json", manifest.Outputs[0].Path)
	}
	if manifest.Outputs[0].RecordCount != 2 {
		t.Fatalf("record_count = %d, want 2", manifest.Outputs[0].RecordCount)
	}
	if manifest.CountsByRecordType["sample_record"] != 2 {
		t.Fatalf("counts_by_record_type = %#v, want sample_record=2", manifest.CountsByRecordType)
	}
}

func TestRunExtractManifestRecordsEffectiveParallelFormat(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	xmlPath := filepath.Join(dir, "input.xml")
	indexPath := filepath.Join(dir, "input.recordindex.json")
	builder := index.NewBuilder(index.BuildOptions{
		InputPath:  xmlPath,
		OutputPath: indexPath,
		Selector:   "//item",
	})
	recordIndex, err := builder.Build()
	if err != nil {
		t.Fatalf("Build index: %v", err)
	}
	if err := builder.WriteToFile(recordIndex, indexPath); err != nil {
		t.Fatalf("WriteToFile index: %v", err)
	}

	opts := &ExtractOptions{
		Files:           xmlPath,
		Format:          "parquet",
		OutputPath:      outputDir,
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RecordIndex:     indexPath,
		Workers:         2,
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if got := manifest.Outputs[0].Format; got != "json" {
		t.Fatalf("output format = %q, want json", got)
	}
}

func createExtractManifestFixture(t *testing.T) string {
	t.Helper()
	initExtractManifestTestLogger(t)

	dir := createWorkingTempDir(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root><item><name>A</name></item><item><name>B</name></item></root>`)
	mustWriteFile(t, filepath.Join(dir, "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
    description: Sample name
output_schema:
  type: object
  properties:
    name:
      type: string
  required:
    - name
`)
	return dir
}

func initExtractManifestTestLogger(t *testing.T) {
	t.Helper()
	config := logging.DefaultConfig()
	config.Level = logging.ErrorLevel
	config.UseColor = false
	config.Component = "sumpter-test"
	config.LogRotation.Enabled = false
	if err := logging.Initialize(config); err != nil {
		t.Fatalf("Initialize logging: %v", err)
	}
	t.Cleanup(func() {
		_ = logging.Sync()
	})
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", path, err)
	}
}

func readManifest(t *testing.T, path string) provenance.Manifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile manifest: %v", err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, filepath.Base(path))
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if !result.IsValid() {
		t.Fatalf("generated manifest did not validate: %s", result.ErrorSummary())
	}
	var manifest provenance.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest: %v", err)
	}
	return manifest
}
