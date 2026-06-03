package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
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

func TestExtractFilesCommandRegistersFormatsFlag(t *testing.T) {
	cmd := newExtractFilesCommand()
	if flag := cmd.Flags().Lookup("formats"); flag == nil {
		t.Fatalf("extract files command missing --formats flag")
	}
	if flag := cmd.Flags().Lookup("continue-on-error"); flag == nil {
		t.Fatalf("extract files command missing --continue-on-error flag")
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
		Format:          "ndjson",
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

func TestSequentialJSONStreamingRouteSelection(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType:     "sample_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//item"}},
	}
	if !shouldUseSequentialJSONStreaming(&ExtractOptions{}, cfg, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("plain sequential JSON output should use the streaming sink path")
	}
	if shouldUseSequentialJSONStreaming(&ExtractOptions{}, cfg, []string{recipesmanifest.OutputFormatParquet}) {
		t.Fatal("parquet output must stay on the buffered path in this slice")
	}
	if shouldUseSequentialJSONStreaming(&ExtractOptions{}, cfg, []string{recipesmanifest.OutputFormatJSON, recipesmanifest.OutputFormatParquet}) {
		t.Fatal("mixed JSON+parquet output must stay on the buffered path")
	}
	if shouldUseSequentialJSONStreaming(&ExtractOptions{RecordIndex: "records.index"}, cfg, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("record-index parallel output is handled by a later SUM-035 slice")
	}

	cfgWithFloor := &extract.ExtractRecordMatch{
		RecordType:     "sample_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//item", MinOccurrences: 1}},
	}
	if shouldUseSequentialJSONStreaming(&ExtractOptions{}, cfgWithFloor, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("min_occurrences recipes stay buffered so output is not published before floor enforcement")
	}
}

func TestSequentialJSONOutputFailureClassificationUsesSentinel(t *testing.T) {
	if !isSequentialJSONOutputFailure(fmt.Errorf("renamed wrapper: %w", errSequentialJSONOutput)) {
		t.Fatal("sentinel-wrapped output failure was not classified as output failure")
	}
	if isSequentialJSONOutputFailure(errors.New("failed to emit record 1: text-only legacy wording")) {
		t.Fatal("text-only error wording must not drive output failure classification")
	}
}

func TestSequentialJSONOutputTargetMatchesBufferedJSONLBytes(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	records := []map[string]interface{}{
		{"_runtime": map[string]interface{}{"record_num": 1}, "extract": map[string]interface{}{"data": map[string]interface{}{"name": "A"}}},
		{"_runtime": map[string]interface{}{"record_num": 2}, "extract": map[string]interface{}{"data": map[string]interface{}{"name": "B"}}},
	}
	expectedFile := filepath.Join(dir, "expected.json")
	if err := writeRecordsToFile(expectedFile, records); err != nil {
		t.Fatalf("writeRecordsToFile: %v", err)
	}

	opts := &ExtractOptions{
		OutputPath:    outputDir,
		OutputPattern: "extract-{}.json",
	}
	target, err := newSequentialJSONOutputTarget(opts, filepath.Join(dir, "input.xml"))
	if err != nil {
		t.Fatalf("newSequentialJSONOutputTarget: %v", err)
	}
	for _, record := range records {
		if err := target.OnRecord(context.Background(), extract.NewEmittedRecord(record)); err != nil {
			t.Fatalf("OnRecord: %v", err)
		}
	}
	if err := target.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := target.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	expected, err := os.ReadFile(expectedFile)
	if err != nil {
		t.Fatalf("ReadFile expected: %v", err)
	}
	actual, err := os.ReadFile(filepath.Join(outputDir, "extract-input.xml.json"))
	if err != nil {
		t.Fatalf("ReadFile actual: %v", err)
	}
	if string(actual) != string(expected) {
		t.Fatalf("streaming JSONL bytes = %q, want %q", string(actual), string(expected))
	}
}

func TestRunExtractRejectsUnknownParquetWithholdColumn(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:                  filepath.Join(dir, "input.xml"),
		Format:                 "parquet",
		OutputPath:             outputDir,
		OutputPattern:          "extract-{}.parquet",
		ParquetCompression:     "none",
		ParquetWithholdColumns: []string{"missing_partition"},
		SignatureConfig:        filepath.Join(dir, "signature.yaml"),
		ExtractConfig:          filepath.Join(dir, "extract.yaml"),
	}

	err := runExtract(opts)
	if err == nil {
		t.Fatal("runExtract unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "output_schema.properties") || !strings.Contains(err.Error(), "missing_partition") {
		t.Fatalf("error = %v, want output_schema.properties missing_partition", err)
	}
}

func TestRunExtractWritesZeroRecordOutputsAndManifestCounts(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root></root>`)

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	outputFile := filepath.Join(outputDir, "extract-input.xml.json")
	info, err := os.Stat(outputFile)
	if err != nil {
		t.Fatalf("expected zero-record output file: %v", err)
	}
	if info.Size() != 0 {
		t.Fatalf("zero-record JSONL size = %d, want 0", info.Size())
	}
	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.Outputs) != 1 {
		t.Fatalf("outputs len = %d, want 1", len(manifest.Outputs))
	}
	if manifest.Outputs[0].RecordCount != 0 {
		t.Fatalf("record_count = %d, want 0", manifest.Outputs[0].RecordCount)
	}
	if got, ok := manifest.CountsByRecordType["sample_record"]; !ok || got != 0 {
		t.Fatalf("counts_by_record_type = %#v, want sample_record=0", manifest.CountsByRecordType)
	}
}

func TestRunExtractMinOccurrencesViolationFailsBeforeOutputAndManifest(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root></root>`)
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
    min_occurrences: 5
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

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		Recipe:          &provenance.Recipe{ID: "sample-recipe"},
	}

	err := runExtract(opts)
	if err == nil {
		t.Fatal("expected min_occurrences violation")
	}
	errText := err.Error()
	for _, want := range []string{`recipe "sample-recipe"`, "selector 0", `xpath="//item"`, "min_occurrences=5", "yielded 0", "input.xml"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error %q missing %q", errText, want)
		}
	}
	if _, err := os.Stat(filepath.Join(outputDir, "extract-input.xml.json")); !os.IsNotExist(err) {
		t.Fatalf("output stat error = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, provenance.ManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("manifest stat error = %v, want not exists", err)
	}
}

func TestRunExtractContinueOnErrorFilesWritesFailureManifestAndOutputs(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	validA := filepath.Join(dir, "valid-a.xml")
	malformed := filepath.Join(dir, "malformed.xml")
	validB := filepath.Join(dir, "valid-b.xml")
	mustWriteFile(t, validA, `<root><item><name>A</name></item></root>`)
	mustWriteFile(t, malformed, `<root><item><name>broken</name>`)
	mustWriteFile(t, validB, `<root><item><name>B</name></item></root>`)

	opts := &ExtractOptions{
		Files:           strings.Join([]string{validA, malformed, validB}, ","),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		ContinueOnError: true,
		Recipe:          &provenance.Recipe{ID: "sample-recipe"},
	}

	err := runExtract(opts)
	if err == nil {
		t.Fatal("expected partial extraction failure")
	}
	if !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("error = %v, want partial extraction failure", err)
	}
	for _, name := range []string{"extract-valid-a.xml.json", "extract-valid-b.xml.json"} {
		data, readErr := os.ReadFile(filepath.Join(outputDir, name))
		if readErr != nil {
			t.Fatalf("ReadFile %s: %v", name, readErr)
		}
		if strings.TrimSpace(string(data)) == "" {
			t.Fatalf("%s is empty, want one emitted record", name)
		}
	}
	if _, statErr := os.Stat(filepath.Join(outputDir, "extract-malformed.xml.json")); !os.IsNotExist(statErr) {
		t.Fatalf("malformed output stat error = %v, want not exists", statErr)
	}

	manifest := readFailureManifest(t, filepath.Join(outputDir, "failures.json"))
	if manifest.CohortSize != 3 || manifest.Applied != 2 || manifest.Failed != 1 {
		t.Fatalf("failure manifest counts = %#v, want cohort=3 applied=2 failed=1", manifest)
	}
	if len(manifest.Failures) != 1 {
		t.Fatalf("failures len = %d, want 1", len(manifest.Failures))
	}
	failure := manifest.Failures[0]
	if failure.Disposition != "failed" || failure.Reason != "parse_error" {
		t.Fatalf("failure row = %#v, want failed parse_error", failure)
	}
	if strings.Contains(failure.File, dir) || strings.Contains(failure.Detail, dir) {
		t.Fatalf("failure manifest leaked temp path: %#v", failure)
	}
}

func TestRunExtractContinueOnErrorMissingFileSkipsFailedLedger(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	validA := filepath.Join(dir, "valid-a.xml")
	missing := filepath.Join(dir, "missing.xml")
	validB := filepath.Join(dir, "valid-b.xml")
	mustWriteFile(t, validA, `<root><item><name>A</name></item></root>`)
	mustWriteFile(t, validB, `<root><item><name>B</name></item></root>`)

	opts := &ExtractOptions{
		Files:           strings.Join([]string{validA, missing, validB}, ","),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		ContinueOnError: true,
	}

	err := runExtract(opts)
	if err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("error = %v, want partial extraction failure", err)
	}
	for _, name := range []string{"extract-valid-a.xml.json", "extract-valid-b.xml.json"} {
		if _, readErr := os.ReadFile(filepath.Join(outputDir, name)); readErr != nil {
			t.Fatalf("ReadFile %s: %v", name, readErr)
		}
	}
	failureManifest := readFailureManifest(t, filepath.Join(outputDir, "failures.json"))
	if failureManifest.CohortSize != 3 || failureManifest.Applied != 2 || failureManifest.Failed != 1 {
		t.Fatalf("failure manifest counts = %#v, want cohort=3 applied=2 failed=1", failureManifest)
	}
	failure := failureManifest.Failures[0]
	if failure.Reason != "internal_error" || !strings.Contains(failure.Detail, "failed to read file") {
		t.Fatalf("failure row = %#v, want internal_error read failure", failure)
	}
	if strings.Contains(failure.File, dir) || strings.Contains(failure.Detail, dir) {
		t.Fatalf("failure manifest leaked temp path: %#v", failure)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.Inputs) != 2 {
		t.Fatalf("manifest inputs len = %d, want only the two ledger-buildable successes", len(manifest.Inputs))
	}
	if len(manifest.Outputs) != 2 {
		t.Fatalf("manifest outputs len = %d, want 2", len(manifest.Outputs))
	}
}

func TestRunExtractContinueOnErrorRequiresOutputPath(t *testing.T) {
	dir := createExtractManifestFixture(t)
	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		ContinueOnError: true,
	}

	err := runExtract(opts)
	if err == nil || !strings.Contains(err.Error(), "--continue-on-error requires --output-path") {
		t.Fatalf("error = %v, want output-path requirement", err)
	}
}

func TestRunExtractContinueOnErrorInputPathProducerFailureIsIsolated(t *testing.T) {
	dir := createExtractManifestFixture(t)
	inputDir := filepath.Join(dir, "inputs")
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(inputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll input: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	missingToken := filepath.Join(inputDir, "missing-token.xml")
	valid := filepath.Join(inputDir, "2026-05-26-valid.xml")
	mustWriteFile(t, missingToken, `<root><item><name>A</name></item></root>`)
	mustWriteFile(t, valid, `<root><item><name>B</name></item></root>`)

	opts := &ExtractOptions{
		InputPath:                inputDir,
		IncludePattern:           "*.xml",
		Format:                   "json",
		OutputPath:               outputDir,
		OutputPattern:            "extract-{}.json",
		SignatureConfig:          filepath.Join(dir, "signature.yaml"),
		ExtractConfig:            filepath.Join(dir, "extract.yaml"),
		ContinueOnError:          true,
		SourceExtraction:         []recipesmanifest.SourceExtractionPattern{sourcePattern("date-token", recipesmanifest.SourceExtractionFilename, `^(?P<event_date>\d{4}-\d{2}-\d{2})`)},
		SourceExtractionRequired: []string{"event_date"},
		SourceExtractionInput:    recipesmanifest.InputDefaults{Path: inputDir},
	}

	err := runExtract(opts)
	if err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("error = %v, want partial extraction failure", err)
	}
	if _, readErr := os.ReadFile(filepath.Join(outputDir, "extract-2026-05-26-valid.xml.json")); readErr != nil {
		t.Fatalf("valid output missing after producer-side failure isolation: %v", readErr)
	}
	manifest := readFailureManifest(t, filepath.Join(outputDir, "failures.json"))
	if manifest.Failed != 1 || manifest.Failures[0].Reason != "validation_error" {
		t.Fatalf("failure manifest = %#v, want one validation_error", manifest)
	}
}

func TestRunExtractContinueOnErrorOutputAndFailureManifestWritesAreTerminal(t *testing.T) {
	t.Run("output write failure", func(t *testing.T) {
		dir := createExtractManifestFixture(t)
		outputPath := filepath.Join(dir, "output-as-file")
		mustWriteFile(t, outputPath, "not a directory")

		opts := &ExtractOptions{
			Files:           filepath.Join(dir, "input.xml"),
			Format:          "json",
			OutputPath:      outputPath,
			OutputPattern:   "extract-{}.json",
			SignatureConfig: filepath.Join(dir, "signature.yaml"),
			ExtractConfig:   filepath.Join(dir, "extract.yaml"),
			ContinueOnError: true,
		}

		err := runExtract(opts)
		if err == nil || !strings.Contains(err.Error(), "failed to write output") {
			t.Fatalf("error = %v, want terminal output write failure", err)
		}
		if !errors.Is(err, errSequentialJSONOutput) {
			t.Fatalf("error = %v, want sequential JSON output sentinel", err)
		}
		if strings.Contains(err.Error(), "partial extraction failure") {
			t.Fatalf("error = %v, output failure must not be recoverable partial failure", err)
		}
	})

	t.Run("failure manifest write failure", func(t *testing.T) {
		dir := createExtractManifestFixture(t)
		outputPath := filepath.Join(dir, "output-as-file")
		malformed := filepath.Join(dir, "malformed.xml")
		mustWriteFile(t, outputPath, "not a directory")
		mustWriteFile(t, malformed, `<root><item><name>broken</name>`)

		opts := &ExtractOptions{
			Files:           malformed,
			Format:          "json",
			OutputPath:      outputPath,
			OutputPattern:   "extract-{}.json",
			SignatureConfig: filepath.Join(dir, "signature.yaml"),
			ExtractConfig:   filepath.Join(dir, "extract.yaml"),
			ContinueOnError: true,
		}

		err := runExtract(opts)
		if err == nil || !strings.Contains(err.Error(), "create extraction failure manifest directory") {
			t.Fatalf("error = %v, want terminal failure-manifest write failure", err)
		}
		if strings.Contains(err.Error(), "partial extraction failure") {
			t.Fatalf("error = %v, failure-manifest write failure must not be recoverable partial failure", err)
		}
	})
}

func TestRunExtractSignatureMismatchRoutesBeforeMinOccurrences(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: document
    name: Document
    selector: /Document
    weight: 1
confidence_threshold: 1
`)
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

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		Recipe:          &provenance.Recipe{ID: "sample-recipe"},
	}

	err := runExtract(opts)
	if err == nil {
		t.Fatal("expected signature mismatch error")
	}
	errText := err.Error()
	for _, want := range []string{"signature mismatch", `recipe "sample-recipe"`, "confidence=0.000", "threshold=1.000"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error %q missing %q", errText, want)
		}
	}
	if strings.Contains(errText, "min_occurrences violation") {
		t.Fatalf("error %q should not mention min_occurrences violation", errText)
	}
}

func TestRunExtractMinOccurrencesIsPerSelectorNotAggregate(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root><item><name>A</name></item><item><name>B</name></item><item><name>C</name></item><item><name>D</name></item><item><name>E</name></item></root>`)
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
    min_occurrences: 3
  - xpath: //missing
    min_occurrences: 3
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

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		Recipe:          &provenance.Recipe{ID: "sample-recipe"},
	}

	err := runExtract(opts)
	if err == nil {
		t.Fatal("expected selector-specific min_occurrences violation")
	}
	errText := err.Error()
	for _, want := range []string{"selector 1", `xpath="//missing"`, "min_occurrences=3", "yielded 0"} {
		if !strings.Contains(errText, want) {
			t.Fatalf("error %q missing %q", errText, want)
		}
	}
}

func TestPerSelectorCountsForIndexedExtractionRejectsAmbiguousFloors(t *testing.T) {
	extCfg := &extract.ExtractRecordMatch{
		MatchSelectors: []extract.MatchSelector{
			{XPath: "//item", MinOccurrences: 1},
			{XPath: "//missing", MinOccurrences: 1},
		},
	}

	_, err := perSelectorCountsForIndexedExtraction("//item", extCfg, 5)
	if err == nil {
		t.Fatal("expected ambiguity error")
	}
	if !strings.Contains(err.Error(), "only accounts for extract selector 0") {
		t.Fatalf("error = %v", err)
	}
}

func TestOutputFileForFormatSwapsParquetExtension(t *testing.T) {
	opts := &ExtractOptions{
		OutputPath:    "/tmp/out",
		OutputPattern: "extract-{}.jsonl",
	}

	got := outputFileForFormat(opts, "parquet", "/tmp/input/source.xml")
	want := filepath.Join("/tmp/out", "extract-source.xml.parquet")
	if got != want {
		t.Fatalf("outputFileForFormat parquet = %q, want %q", got, want)
	}
}

func TestOutputFileForFormatUsesPatternMapAliases(t *testing.T) {
	opts := &ExtractOptions{
		OutputPath: "/tmp/out",
		OutputPatterns: map[string]string{
			"ndjson":  "records-{}.jsonl",
			"parquet": "records-{}.parquet",
		},
	}

	got := outputFileForFormat(opts, "json", "/tmp/input/source.xml")
	want := filepath.Join("/tmp/out", "records-source.xml.jsonl")
	if got != want {
		t.Fatalf("outputFileForFormat json alias = %q, want %q", got, want)
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

func TestRunExtractParallelMinOccurrencesUsesIndexedMatchCount(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	xmlPath := filepath.Join(dir, "input.xml")
	mustWriteFile(t, xmlPath, `<root><item><name>A</name></item><item><name>B</name><blob>`+strings.Repeat("x", 2*1024*1024)+`</blob></item></root>`)
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
    min_occurrences: 2
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
	if recordIndex.Summary.TotalRecords != 2 {
		t.Fatalf("index total_records = %d, want 2", recordIndex.Summary.TotalRecords)
	}
	if err := builder.WriteToFile(recordIndex, indexPath); err != nil {
		t.Fatalf("WriteToFile index: %v", err)
	}

	opts := &ExtractOptions{
		Files:            xmlPath,
		Format:           "json",
		OutputPath:       outputDir,
		SignatureConfig:  filepath.Join(dir, "signature.yaml"),
		ExtractConfig:    filepath.Join(dir, "extract.yaml"),
		RecordIndex:      indexPath,
		Workers:          2,
		MaxRecordSizeMB:  1,
		SkipLargeRecords: true,
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.Outputs[0].RecordCount != 1 {
		t.Fatalf("record_count = %d, want 1 emitted record", manifest.Outputs[0].RecordCount)
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
	if got := manifest.Outputs[0].Format; got != "parquet" {
		t.Fatalf("output format = %q, want parquet", got)
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

func readFailureManifest(t *testing.T, path string) extractFailureManifestFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failure manifest: %v", err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "..", "schemas"))
	result, err := validator.ValidateFailureManifest(data, filepath.Base(path))
	if err != nil {
		t.Fatalf("ValidateFailureManifest: %v", err)
	}
	if !result.IsValid() {
		t.Fatalf("failure manifest did not validate: %s", result.ErrorSummary())
	}
	var manifest extractFailureManifestFile
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("Unmarshal failure manifest: %v", err)
	}
	return manifest
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
