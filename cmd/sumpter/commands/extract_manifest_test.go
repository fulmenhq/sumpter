package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/fulmenhq/sumpter/internal/validation"
	"github.com/fulmenhq/sumpter/internal/valueprofile"
	"github.com/parquet-go/parquet-go"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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
	if flag := cmd.Flags().Lookup("artifact-descriptor"); flag == nil {
		t.Fatalf("extract files command missing --artifact-descriptor flag")
	}
	if flag := cmd.Flags().Lookup("contract-base"); flag == nil {
		t.Fatalf("extract files command missing --contract-base flag")
	}
}

func TestRunExtractWritesArtifactDescriptorWhenRequested(t *testing.T) {
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
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	descriptorPath := filepath.Join(outputDir, dataartifact.DescriptorFileName)
	result, _, err := artifactcontract.ValidateDescriptorFile(contractBase, descriptorPath)
	if err != nil {
		t.Fatalf("validate generated descriptor: %v", err)
	}
	if !result.Valid {
		t.Fatalf("generated descriptor did not validate: %+v", result.Errors)
	}

	var descriptor map[string]interface{}
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatalf("unmarshal descriptor: %v", err)
	}
	if got := descriptor["lifecycle"]; got != "complete" {
		t.Fatalf("lifecycle = %#v, want complete", got)
	}
	if _, ok := descriptor["field_catalogs"]; ok {
		t.Fatalf("generated B2.1 descriptor embedded field_catalogs; want refs only")
	}
	producer := descriptor["producer"].(map[string]interface{})
	if got := producer["run_id"]; got != testMultiRunID {
		t.Fatalf("producer.run_id = %#v, want %q", got, testMultiRunID)
	}
	grains := descriptor["grains"].([]interface{})
	grain := grains[0].(map[string]interface{})
	if got := grain["kind"]; got != "record_stream" {
		t.Fatalf("grain kind = %#v, want record_stream", got)
	}
	if got := grain["row_count"]; got != float64(2) {
		t.Fatalf("grain row_count = %#v, want 2", got)
	}
	if got := grain["field_catalog_ref"]; got != dataartifact.FieldCatalogRef {
		t.Fatalf("grain field_catalog_ref = %#v, want %q", got, dataartifact.FieldCatalogRef)
	}
	catalogPath := filepath.Join(outputDir, filepath.FromSlash(dataartifact.FieldCatalogRef))
	var catalog map[string]interface{}
	catalogData, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read field catalog: %v", err)
	}
	if err := json.Unmarshal(catalogData, &catalog); err != nil {
		t.Fatalf("unmarshal field catalog: %v", err)
	}
	if got := catalog["id"]; got != dataartifact.FieldCatalogRef {
		t.Fatalf("catalog id = %#v, want %q", got, dataartifact.FieldCatalogRef)
	}
	if got := catalog["grain"]; got != "records" {
		t.Fatalf("catalog grain = %#v, want records", got)
	}
	if got := catalog["withheld_field_count"]; got != float64(1) {
		t.Fatalf("catalog withheld_field_count = %#v, want 1", got)
	}
	if fields, ok := catalog["fields"].([]interface{}); !ok || len(fields) != 0 {
		t.Fatalf("catalog fields = %#v, want empty because source-structure key is withheld", catalog["fields"])
	}
	reps := descriptor["representations"].([]interface{})
	rep := reps[0].(map[string]interface{})
	if got := rep["format"]; got != "ndjson" {
		t.Fatalf("representation format = %#v, want ndjson", got)
	}
	if got := rep["uri"]; got != "records.jsonl" {
		t.Fatalf("representation uri = %#v, want records.jsonl", got)
	}
}

// TestRunExtractParquetArtifactDescriptorSuppressesPageMetadata connects the
// B2 opt-in (--artifact-descriptor) to physical Parquet metadata suppression
// and the descriptor's column protection floor.
func TestRunExtractParquetArtifactDescriptorSuppressesPageMetadata(t *testing.T) {
	dir := createExtractManifestFixture(t)
	// Distinctive marker for leak-census (must compress out of data pages under zstd).
	const marker = "PARQPROT-AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	mustWriteFile(t, filepath.Join(dir, "input.xml"),
		fmt.Sprintf(`<root><item><name>%s</name></item><item><name>%s</name></item></root>`, marker, marker))
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	contractBase := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")

	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Format:               "parquet",
		OutputPath:           outputDir,
		OutputPattern:        "records.parquet",
		ParquetCompression:   "zstd",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	parquetPath := filepath.Join(outputDir, "records.parquet")
	fileBytes, err := os.ReadFile(parquetPath) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("read parquet: %v", err)
	}
	if n := bytes.Count(fileBytes, []byte(marker)); n != 0 {
		t.Fatalf("marker leaked in parquet bytes %d time(s) with --artifact-descriptor; want 0", n)
	}

	f, err := os.Open(parquetPath) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("stat parquet: %v", err)
	}
	pqFile, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	for _, rg := range pqFile.Metadata().RowGroups {
		for _, col := range rg.Columns {
			if col.MetaData.BloomFilterOffset != 0 {
				t.Fatalf("BloomFilterOffset=%d on %v; must never wire Bloom filters",
					col.MetaData.BloomFilterOffset, col.MetaData.PathInSchema)
			}
			stats := col.MetaData.Statistics
			if len(stats.MinValue) > 0 || len(stats.MaxValue) > 0 ||
				len(stats.Min) > 0 || len(stats.Max) > 0 {
				t.Fatalf("column %v has footer stats with --artifact-descriptor", col.MetaData.PathInSchema)
			}
		}
	}

	descriptorPath := filepath.Join(outputDir, dataartifact.DescriptorFileName)
	result, _, err := artifactcontract.ValidateDescriptorFile(contractBase, descriptorPath)
	if err != nil {
		t.Fatalf("validate descriptor: %v", err)
	}
	if !result.Valid {
		t.Fatalf("descriptor invalid: %+v", result.Errors)
	}
	var descriptor map[string]interface{}
	data, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		t.Fatalf("unmarshal descriptor: %v", err)
	}
	reps := descriptor["representations"].([]interface{})
	foundParquet := false
	for _, raw := range reps {
		rep := raw.(map[string]interface{})
		if rep["format"] != "parquet" {
			continue
		}
		foundParquet = true
		if got := rep["protection_enforceable_granularity"]; got != "column" {
			t.Fatalf("parquet protection floor = %#v, want column", got)
		}
		readPath := rep["read_path"].(map[string]interface{})
		if got := readPath["gateable_unit_granularity"]; got != "column" {
			t.Fatalf("parquet gateable unit = %#v, want column", got)
		}
		for _, cap := range readPath["scan_capabilities"].([]interface{}) {
			if cap == "predicate_pushdown" {
				t.Fatal("parquet rep must not claim predicate_pushdown")
			}
		}
	}
	if !foundParquet {
		t.Fatal("descriptor missing parquet representation")
	}
}

// TestRunExtractParquetWithoutDescriptorRetainsPageStats locks the no-opt path:
// ordinary Parquet extract keeps pre-B2 footer min/max metadata.
func TestRunExtractParquetWithoutDescriptorRetainsPageStats(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:              filepath.Join(dir, "input.xml"),
		Format:             "parquet",
		OutputPath:         outputDir,
		OutputPattern:      "records.parquet",
		ParquetCompression: "none",
		SignatureConfig:    filepath.Join(dir, "signature.yaml"),
		ExtractConfig:      filepath.Join(dir, "extract.yaml"),
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	parquetPath := filepath.Join(outputDir, "records.parquet")
	f, err := os.Open(parquetPath) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("open parquet: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("stat parquet: %v", err)
	}
	pqFile, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	foundStats := false
	for _, rg := range pqFile.Metadata().RowGroups {
		for _, col := range rg.Columns {
			stats := col.MetaData.Statistics
			if len(stats.MinValue) > 0 || len(stats.MaxValue) > 0 ||
				len(stats.Min) > 0 || len(stats.Max) > 0 {
				foundStats = true
			}
		}
	}
	if !foundStats {
		t.Fatal("no-opt parquet path lost footer min/max; want pre-B2 page stats retained")
	}
	if _, err := os.Stat(filepath.Join(outputDir, dataartifact.DescriptorFileName)); !os.IsNotExist(err) {
		t.Fatal("no-opt path must not write artifact-descriptor.json")
	}
}

func TestRunExtractValueProfileGuardedEmission(t *testing.T) {
	dir := createExtractManifestFixture(t)
	// Two values for name — Tier A when gated public+safe_to_profile.
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.jsonl",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		ValueProfile: &valueprofile.Config{
			Enabled: true,
			Fields: []valueprofile.FieldConfig{
				{Field: "name", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
				{Field: "secret", Sensitivity: valueprofile.SensitivityRestricted},
			},
		},
	}
	// secret is not in extract data — only name is profiled from data; secret
	// field still appears with zero/null aggregates when never observed... actually
	// only fields present in config are in the profile; name is observed.

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("expected value_profile on manifest")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatalf("unmarshal value_profile: %v", err)
	}
	if profile["version"] != valueprofile.ProfileVersion {
		t.Fatalf("version = %#v", profile["version"])
	}
	fields := profile["fields"].(map[string]interface{})
	nameField := fields["name"].(map[string]interface{})
	if nameField["tier"] != valueprofile.TierEnumeration {
		t.Fatalf("name tier = %#v, want enumeration", nameField["tier"])
	}
	distinct := nameField["distinct"].(map[string]interface{})
	if distinct["A"] == nil || distinct["B"] == nil {
		t.Fatalf("name distinct = %#v, want A and B", distinct)
	}
	// secret never appeared in data — still listed with aggregates only
	secretField := fields["secret"].(map[string]interface{})
	if secretField["tier"] != valueprofile.TierAggregates {
		t.Fatalf("secret tier = %#v, want aggregates", secretField["tier"])
	}
	if secretField["distinct"] != nil {
		t.Fatalf("restricted field must not emit distinct: %#v", secretField["distinct"])
	}

	// Schema validation of the full manifest still passes with value_profile.
	data, err := os.ReadFile(filepath.Join(outputDir, provenance.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if !result.Valid {
		t.Fatalf("manifest with value_profile invalid: %+v", result.Errors)
	}
}

func TestRunExtractValueProfileNotDoubledForMultiFormat(t *testing.T) {
	// Representation duplication must not cross small_cell_threshold.
	dir := createExtractManifestFixture(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"),
		`<root><item><name>only</name></item></root>`)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	opts := &ExtractOptions{
		Files:              filepath.Join(dir, "input.xml"),
		Formats:            []string{"json", "parquet"},
		OutputPath:         outputDir,
		OutputPattern:      "records.jsonl",
		OutputPatterns:     map[string]string{"json": "records.jsonl", "parquet": "records.parquet"},
		SignatureConfig:    filepath.Join(dir, "signature.yaml"),
		ExtractConfig:      filepath.Join(dir, "extract.yaml"),
		ParquetCompression: "none",
		ValueProfile: &valueprofile.Config{
			Enabled:            true,
			SmallCellThreshold: 2,
			Fields: []valueprofile.FieldConfig{
				{
					Field:          "name",
					SafeToProfile:  true,
					Sensitivity:    valueprofile.SensitivityPublic,
					ProtectionTags: []string{valueprofile.TagQuasiIdentifier},
				},
			},
		},
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}
	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("expected value_profile")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatal(err)
	}
	nameField := profile["fields"].(map[string]interface{})["name"].(map[string]interface{})
	// Singleton frequency is 1 (not 2 from dual formats) → suppressed under threshold 2.
	if nameField["tier"] != valueprofile.TierEnumeration {
		t.Fatalf("tier = %#v", nameField["tier"])
	}
	distinct, _ := nameField["distinct"].(map[string]interface{})
	if _, ok := distinct["only"]; ok {
		t.Fatalf("singleton quasi cell must stay suppressed; dual-format must not double-count: %#v", distinct)
	}
}

func TestRunExtractWithoutValueProfileOmitsField(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	opts := &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.jsonl",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}
	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if len(manifest.ValueProfile) != 0 {
		t.Fatalf("no-opt path must omit value_profile, got %s", string(manifest.ValueProfile))
	}
}

func TestRunExtractWritesMixedArtifactFieldCatalog(t *testing.T) {
	dir := createExtractManifestFixture(t)
	writeExtractManifestMixedCatalogFixture(t, dir)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Format:               "json",
		OutputPath:           outputDir,
		OutputPattern:        "records.jsonl",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0"),
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}
	catalogPath := filepath.Join(outputDir, filepath.FromSlash(dataartifact.FieldCatalogRef))
	var catalog map[string]interface{}
	catalogData, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read field catalog: %v", err)
	}
	if err := json.Unmarshal(catalogData, &catalog); err != nil {
		t.Fatalf("unmarshal field catalog: %v", err)
	}
	fields, ok := catalog["fields"].([]interface{})
	if !ok || len(fields) != 1 {
		t.Fatalf("catalog fields = %#v, want one disclosed derived field", catalog["fields"])
	}
	field := fields[0].(map[string]interface{})
	if got := field["name"]; got != "name_copy" {
		t.Fatalf("catalog field name = %#v, want name_copy", got)
	}
	if got := field["sensitivity"]; got != "unknown" {
		t.Fatalf("catalog field sensitivity = %#v, want unknown", got)
	}
	if got := field["export_action"]; got != "block_export" {
		t.Fatalf("catalog field export_action = %#v, want block_export", got)
	}
}

func TestWriteOutputTargetBytesAtomicallyReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dataartifact.DescriptorFileName)
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	tgt := &uriio.OutputTarget{
		LogicalURI: dataartifact.DescriptorFileName,
		LocalPath:  path,
		Scheme:     uriio.SchemeLocal,
	}
	if err := writeOutputTargetBytesAtomically(tgt, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("writeOutputTargetBytesAtomically: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read replaced file: %v", err)
	}
	if string(data) != "new\n" {
		t.Fatalf("replaced file = %q, want new payload", string(data))
	}
	assertNoOutputTargetTempFiles(t, dir, dataartifact.DescriptorFileName)
}

func TestWriteOutputTargetBytesAtomicallyCleansTempOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, dataartifact.DescriptorFileName)
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}

	tgt := &uriio.OutputTarget{
		LogicalURI: dataartifact.DescriptorFileName,
		LocalPath:  path,
		Scheme:     uriio.SchemeLocal,
	}
	err := writeOutputTargetBytesAtomically(tgt, []byte("new\n"), 0o600)
	if err == nil {
		t.Fatal("writeOutputTargetBytesAtomically succeeded; want replace failure")
	}
	if !strings.Contains(err.Error(), "replace output") {
		t.Fatalf("error = %v, want replace output context", err)
	}
	assertNoOutputTargetTempFiles(t, dir, dataartifact.DescriptorFileName)
	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat blocking directory: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("blocking path was replaced with file; want directory preserved")
	}
}

func assertNoOutputTargetTempFiles(t *testing.T, dir, finalName string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "."+finalName+".tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files = %#v, want none", matches)
	}
}

func TestRunExtractDoesNotPublishArtifactDescriptorWhenManifestWriteFails(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	if err := os.Mkdir(filepath.Join(outputDir, provenance.ManifestFileName), 0o750); err != nil {
		t.Fatalf("Mkdir manifest path: %v", err)
	}

	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Format:               "json",
		OutputPath:           outputDir,
		OutputPattern:        "records.jsonl",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0"),
	}

	if err := runExtract(opts); err == nil {
		t.Fatal("runExtract succeeded; want manifest write failure")
	}
	if _, err := os.Stat(filepath.Join(outputDir, dataartifact.DescriptorFileName)); !os.IsNotExist(err) {
		t.Fatalf("descriptor stat error = %v, want not exist", err)
	}
}

func TestRunExtractDoesNotPublishArtifactDescriptorWhenFieldCatalogWriteFails(t *testing.T) {
	dir := createExtractManifestFixture(t)
	writeExtractManifestMixedCatalogFixture(t, dir)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outputDir, "fields"), []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("write blocking fields path: %v", err)
	}

	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Format:               "json",
		OutputPath:           outputDir,
		OutputPattern:        "records.jsonl",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0"),
	}

	if err := runExtract(opts); err == nil {
		t.Fatal("runExtract succeeded; want field catalog write failure")
	}
	if _, err := os.Stat(filepath.Join(outputDir, dataartifact.DescriptorFileName)); !os.IsNotExist(err) {
		t.Fatalf("descriptor stat error = %v, want not exist", err)
	}
}

func TestRunExtractArtifactDescriptorRequiresContractBase(t *testing.T) {
	err := runExtract(&ExtractOptions{
		Files:              "input.xml",
		OutputPath:         t.TempDir(),
		ArtifactDescriptor: true,
	})
	if err == nil || !strings.Contains(err.Error(), "--artifact-descriptor requires --contract-base") {
		t.Fatalf("runExtract error = %v, want contract-base requirement", err)
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
		t.Fatal("record-index parallel output must use the parallel streaming route")
	}
	if !shouldUseParallelJSONStreaming(&ExtractOptions{RecordIndex: "records.index"}, cfg, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("record-index parallel JSON output should use the parallel streaming sink path")
	}
	if shouldUseParallelJSONStreaming(&ExtractOptions{RecordIndex: "records.index"}, cfg, []string{recipesmanifest.OutputFormatParquet}) {
		t.Fatal("parallel parquet output must stay on the buffered path")
	}

	cfgWithFloor := &extract.ExtractRecordMatch{
		RecordType:     "sample_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//item", MinOccurrences: 1}},
	}
	if shouldUseSequentialJSONStreaming(&ExtractOptions{}, cfgWithFloor, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("min_occurrences recipes stay buffered so output is not published before floor enforcement")
	}
	if !shouldUseParallelJSONStreaming(&ExtractOptions{RecordIndex: "records.index"}, cfgWithFloor, []string{recipesmanifest.OutputFormatJSON}) {
		t.Fatal("parallel min_occurrences recipes can stream because indexed counts are enforced before output publication")
	}
}

func TestWarnSequentialMinOccurrencesBufferedFallback(t *testing.T) {
	core, logs := observer.New(zapcore.WarnLevel)
	logger := zap.New(core)
	cfgWithFloor := &extract.ExtractRecordMatch{
		RecordType:     "sample_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//item", MinOccurrences: 1}},
	}
	opts := &ExtractOptions{
		ExtractConfig: "extract.yaml",
	}

	warnSequentialMinOccurrencesBufferedFallback(logger, opts, cfgWithFloor, []string{recipesmanifest.OutputFormatJSON})

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("warning count = %d, want 1", len(entries))
	}
	if got := entries[0].Message; got != "Sequential JSON/NDJSON min_occurrences uses buffered extraction path" {
		t.Fatalf("warning message = %q", got)
	}
	fields := entries[0].ContextMap()
	if got := fields["record_type"]; got != "sample_record" {
		t.Fatalf("record_type field = %#v, want sample_record", got)
	}
	if got := fields["bounded_alternative"]; got == "" {
		t.Fatalf("bounded_alternative field missing: %#v", fields)
	}

	warnSequentialMinOccurrencesBufferedFallback(logger, &ExtractOptions{RecordIndex: "records.index"}, cfgWithFloor, []string{recipesmanifest.OutputFormatJSON})
	warnSequentialMinOccurrencesBufferedFallback(logger, opts, cfgWithFloor, []string{recipesmanifest.OutputFormatParquet})
	warnSequentialMinOccurrencesBufferedFallback(logger, opts, &extract.ExtractRecordMatch{MatchSelectors: []extract.MatchSelector{{XPath: "//item"}}}, []string{recipesmanifest.OutputFormatJSON})
	if len(logs.All()) != 1 {
		t.Fatalf("non-fallback cases emitted extra warnings: %d", len(logs.All()))
	}
}

func TestJSONOutputFailureClassificationUsesSentinel(t *testing.T) {
	if !isJSONOutputFailure(fmt.Errorf("renamed wrapper: %w", errJSONOutput)) {
		t.Fatal("sentinel-wrapped output failure was not classified as output failure")
	}
	if isJSONOutputFailure(errors.New("failed to emit record 1: text-only legacy wording")) {
		t.Fatal("text-only error wording must not drive output failure classification")
	}
}

func TestJSONOutputTargetMatchesBufferedJSONLBytes(t *testing.T) {
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
	if err := writeRecordsToFile(nil, expectedFile, records); err != nil {
		t.Fatalf("writeRecordsToFile: %v", err)
	}

	opts := &ExtractOptions{
		OutputPath:    outputDir,
		OutputPattern: "extract-{}.json",
	}
	target, err := newJSONOutputTarget(opts, filepath.Join(dir, "input.xml"))
	if err != nil {
		t.Fatalf("newJSONOutputTarget: %v", err)
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
		if !errors.Is(err, errJSONOutput) {
			t.Fatalf("error = %v, want JSON output sentinel", err)
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

func TestRunExtractArtifactDescriptorSanitizesRecordIndexURI(t *testing.T) {
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

	contractBase := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	opts := &ExtractOptions{
		Files:                xmlPath,
		Format:               "json",
		OutputPath:           outputDir,
		OutputPattern:        "records.jsonl",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		RecordIndex:          indexPath,
		Workers:              2,
		RunID:                testMultiRunID,
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
		CommandName:          "sumpter extract files",
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	descriptorPath := filepath.Join(outputDir, dataartifact.DescriptorFileName)
	result, _, err := artifactcontract.ValidateDescriptorFile(contractBase, descriptorPath)
	if err != nil {
		t.Fatalf("validate generated descriptor: %v", err)
	}
	if !result.Valid {
		t.Fatalf("generated descriptor did not validate: %+v", result.Errors)
	}

	raw, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	// Host-local absolute index paths (temp/home/workspace) must not appear.
	if strings.Contains(string(raw), indexPath) {
		t.Fatalf("descriptor contains absolute record-index path %q:\n%s", indexPath, raw)
	}
	if strings.Contains(string(raw), dir) {
		t.Fatalf("descriptor contains workspace absolute path %q:\n%s", dir, raw)
	}
	if strings.Contains(string(raw), os.TempDir()) {
		t.Fatalf("descriptor contains temp dir path:\n%s", raw)
	}

	var descriptor map[string]interface{}
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatalf("unmarshal descriptor: %v", err)
	}
	reps, ok := descriptor["representations"].([]interface{})
	if !ok {
		t.Fatalf("representations missing: %#v", descriptor["representations"])
	}
	foundIndex := false
	for _, rawRep := range reps {
		rep, ok := rawRep.(map[string]interface{})
		if !ok {
			continue
		}
		if rep["grain"] != dataartifact.GrainIDRecordIndex {
			continue
		}
		foundIndex = true
		uri, _ := rep["uri"].(string)
		if uri == "" || filepath.IsAbs(uri) {
			t.Fatalf("object_index uri = %q, want portable non-absolute ref", uri)
		}
		if uri != "input.recordindex.json" {
			// SanitizePath with Files parent dir as root should yield the basename/rel name.
			t.Fatalf("object_index uri = %q, want input.recordindex.json", uri)
		}
		if rep["role"] != "object_index" {
			t.Fatalf("object_index role = %#v", rep["role"])
		}
	}
	if !foundIndex {
		t.Fatal("descriptor missing object_index representation")
	}
	grains, ok := descriptor["grains"].([]interface{})
	if !ok || len(grains) < 2 {
		t.Fatalf("grains = %#v, want record_stream + object_index", descriptor["grains"])
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

func TestRunExtractParallelMinOccurrencesViolationFailsBeforeOutput(t *testing.T) {
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll output: %v", err)
	}

	xmlPath := filepath.Join(dir, "input.xml")
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
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
		Files:           xmlPath,
		Format:          "json",
		OutputPath:      outputDir,
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RecordIndex:     indexPath,
		Workers:         2,
	}

	err = runExtract(opts)
	if err == nil {
		t.Fatal("expected min_occurrences violation")
	}
	if !strings.Contains(err.Error(), "min_occurrences violation") {
		t.Fatalf("error = %v, want min_occurrences violation", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, "extract-parallel.json")); !os.IsNotExist(err) {
		t.Fatalf("output stat error = %v, want not exists", err)
	}
	if _, err := os.Stat(filepath.Join(outputDir, provenance.ManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("manifest stat error = %v, want not exists", err)
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

// TestExtractDryRunLocalPreview locks the local --dry-run contract after the B3
// refactor (which moved cloud dry-run to a list-only path): a local dry run
// previews the input by path, writes no output, and reads no records. Local
// behavior must stay byte-identical to the pre-refactor stage-then-list code.
func TestExtractDryRunLocalPreview(t *testing.T) {
	dir := createExtractManifestFixture(t)
	inputPath := filepath.Join(dir, "input.xml")
	outDir := filepath.Join(dir, "out-dryrun-local")

	opts := &ExtractOptions{
		Files:           inputPath,
		Format:          "json",
		OutputPath:      outDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		DryRun:          true,
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
	}

	var runErr error
	out := captureStdout(t, func() { runErr = runExtract(opts) })
	if runErr != nil {
		t.Fatalf("local dry-run error = %v", runErr)
	}
	if !strings.Contains(out, inputPath) {
		t.Errorf("local dry-run did not preview the input path %q:\n%s", inputPath, out)
	}
	// Dry run writes nothing.
	if _, err := os.Stat(outDir); !os.IsNotExist(err) {
		t.Errorf("local dry-run produced output directory %q (err=%v); dry-run must write nothing", outDir, err)
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

func writeExtractManifestMixedCatalogFixture(t *testing.T, dir string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(dir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
    description: Sample name
  - output_field: name_copy
    expression: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    name_copy:
      type: string
  required:
    - name
    - name_copy
`)
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
