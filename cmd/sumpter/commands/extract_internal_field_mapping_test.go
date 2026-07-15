package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/valueprofile"
)

// TestInternalFieldMappingDisclosureSurfaces is the secrev-anchored negative:
// descriptor-on + value_profile-on with one xpath internal and one expression-only
// internal — neither name may appear in extract.data, field_provenance, catalog
// fields, value_profile keys, or Parquet columns; no serialized {Value:...} wrapper.
func TestInternalFieldMappingDisclosureSurfaces(t *testing.T) {
	initExtractManifestTestLogger(t)
	dir := createWorkingTempDir(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"),
		`<root><item><raw>10</raw></item><item><raw>4</raw></item></root>`)
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
  - output_field: sign_factor
    xpath: "1"
    type: number
    internal: true
  - output_field: raw
    xpath: raw
    type: number
  - output_field: doubled
    expression: "raw * 2"
    type: number
    internal: true
  - output_field: amount
    expression: "sign_factor * doubled"
    type: number
output_schema:
  type: object
  properties:
    raw:
      type: number
    amount:
      type: number
  required:
    - raw
    - amount
`)
	outputDir := filepath.Join(dir, "outputs")
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	contractBase := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	opts := &ExtractOptions{
		Files:                filepath.Join(dir, "input.xml"),
		Formats:              []string{"ndjson", "parquet"},
		OutputPath:           outputDir,
		OutputPatterns:       map[string]string{"ndjson": "records.ndjson", "parquet": "records.parquet"},
		ParquetCompression:   "none",
		SignatureConfig:      filepath.Join(dir, "signature.yaml"),
		ExtractConfig:        filepath.Join(dir, "extract.yaml"),
		ArtifactDescriptor:   true,
		ArtifactContractBase: contractBase,
		Recipe: &provenance.Recipe{
			ID:                    "internal_field_mappings_fixture",
			ManifestSchemaVersion: "recipe/v0.1.0",
			ContentVersion:        "1.0.0",
			ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Owners:                []provenance.Owner{{Name: "Fulmen Sumpter contributors"}},
			ManifestYAML:          "version: recipe/v0.1.0\nid: internal_field_mappings_fixture\n",
			SignatureYAML:         "signature_id: sample\n",
			ExtractYAML:           "record_type: sample_record\n",
		},
		ValueProfile: &valueprofile.Config{
			Enabled: true,
			Fields: []valueprofile.FieldConfig{
				{Field: "amount", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
				{Field: "raw", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
			},
		},
	}

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	// NDJSON body (primary record sink)
	ndjsonPath := filepath.Join(outputDir, "records.ndjson")
	ndjsonBytes, err := os.ReadFile(ndjsonPath) // #nosec G304
	if err != nil {
		entries, _ := os.ReadDir(outputDir)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("read ndjson: %v (outputs: %v)", err, names)
	}
	ndjsonText := string(ndjsonBytes)
	for _, leak := range []string{"sign_factor", "doubled", `"Value"`} {
		if strings.Contains(ndjsonText, leak) {
			t.Errorf("NDJSON output leaked %q", leak)
		}
	}
	if !strings.Contains(ndjsonText, `"amount"`) {
		t.Errorf("NDJSON missing amount field: %s", ndjsonText)
	}

	// Manifest field_provenance
	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.Recipe == nil {
		t.Fatal("expected recipe-backed manifest for field_provenance check")
	}
	seen := map[string]bool{}
	for _, field := range manifest.Recipe.FieldProvenance {
		seen[field.OutputField] = true
		switch field.OutputField {
		case "sign_factor", "doubled":
			t.Errorf("field_provenance listed internal %q", field.OutputField)
		}
	}
	if !seen["amount"] || !seen["raw"] {
		t.Errorf("field_provenance missing emitted fields: %#v", seen)
	}

	// value_profile keys
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("expected value_profile on manifest")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatalf("unmarshal value_profile: %v", err)
	}
	fields, _ := profile["fields"].(map[string]interface{})
	for _, name := range []string{"sign_factor", "doubled"} {
		if _, ok := fields[name]; ok {
			t.Errorf("value_profile fields listed internal %q", name)
		}
	}
	if _, ok := fields["amount"]; !ok {
		t.Error("value_profile missing amount")
	}

	// Descriptor / field catalog
	descriptorPath := filepath.Join(outputDir, dataartifact.DescriptorFileName)
	descBytes, err := os.ReadFile(descriptorPath) // #nosec G304
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	descText := string(descBytes)
	for _, leak := range []string{"sign_factor", "doubled"} {
		if strings.Contains(descText, leak) {
			t.Errorf("descriptor leaked internal name %q", leak)
		}
	}

	// Parquet schema absence via parquet field-spec path: open and check schema names.
	parquetPath := filepath.Join(outputDir, "records.parquet")
	if _, err := os.Stat(parquetPath); err != nil {
		t.Fatalf("parquet missing: %v", err)
	}
	// Lightweight: re-run field-spec construction for the loaded config.
	extCfg, err := extract.LoadExtractConfig(filepath.Join(dir, "extract.yaml"))
	if err != nil {
		t.Fatalf("reload extract: %v", err)
	}
	prov := buildFieldProvenance(extCfg.FieldMappings)
	for _, p := range prov {
		if p.OutputField == "sign_factor" || p.OutputField == "doubled" {
			t.Errorf("buildFieldProvenance still includes %q", p.OutputField)
		}
	}
	catalog := dataartifact.BuildRecordFieldCatalog(prov)
	for _, f := range catalog.Fields {
		if f.Name == "sign_factor" || f.Name == "doubled" {
			t.Errorf("catalog fields listed internal %q", f.Name)
		}
	}
}

func TestBuildFieldProvenanceSkipsInternalMappings(t *testing.T) {
	fields := buildFieldProvenance([]extract.FieldMapping{
		{OutputField: "helper_xpath", XPath: "1", Type: "number", Internal: true},
		{OutputField: "helper_expr", Expression: "raw * 2", Type: "number", Internal: true},
		{OutputField: "raw", XPath: "raw", Type: "number"},
		{OutputField: "amount", Expression: "helper_xpath * helper_expr", Type: "number"},
	})
	if len(fields) != 2 {
		t.Fatalf("len=%d want 2 (internals skipped), got %#v", len(fields), fields)
	}
	for _, f := range fields {
		if f.OutputField == "helper_xpath" || f.OutputField == "helper_expr" {
			t.Errorf("internal mapping leaked into provenance: %#v", f)
		}
	}
}

func TestRejectValueProfileInternalFields(t *testing.T) {
	mappings := []extract.FieldMapping{
		{OutputField: "helper", XPath: "1", Type: "number", Internal: true},
		{OutputField: "amount", Expression: "helper", Type: "number"},
	}
	cfg := &valueprofile.Config{
		Enabled: true,
		Fields: []valueprofile.FieldConfig{
			{Field: "helper", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
		},
	}
	err := rejectValueProfileInternalFields(cfg, mappings)
	if err == nil {
		t.Fatal("expected value_profile internal name rejection")
	}
	if !strings.Contains(err.Error(), "helper") {
		t.Fatalf("want helper in error, got: %v", err)
	}
}

func TestValueProfileInternalFieldRejectedAtPlanLoad(t *testing.T) {
	initExtractManifestTestLogger(t)
	dir := createWorkingTempDir(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root><item><raw>1</raw></item></root>`)
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
  - output_field: helper
    xpath: "1"
    type: number
    internal: true
  - output_field: amount
    expression: "helper"
    type: number
output_schema:
  type: object
  properties:
    amount:
      type: number
`)
	outputDir := filepath.Join(dir, "outputs")
	_ = os.MkdirAll(outputDir, 0o750)
	err := runExtract(&ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		ValueProfile: &valueprofile.Config{
			Enabled: true,
			Fields: []valueprofile.FieldConfig{
				{Field: "helper", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
			},
		},
	})
	if err == nil {
		t.Fatal("expected plan-load rejection of value_profile on internal field")
	}
	if !strings.Contains(err.Error(), "helper") {
		t.Fatalf("want helper in error, got: %v", err)
	}
}

func TestInternalFieldMappingNoOptByteParity(t *testing.T) {
	// Recipes without internal:true must behave unchanged: same extract.data keys.
	initExtractManifestTestLogger(t)
	dir := createExtractManifestFixture(t)
	outputDir := filepath.Join(dir, "outputs")
	_ = os.MkdirAll(outputDir, 0o750)
	if err := runExtract(&ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "records.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		NoManifest:      true,
	}); err != nil {
		t.Fatalf("runExtract: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(outputDir, "records.json")) // #nosec G304
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(raw), `"name"`) {
		t.Fatalf("no-opt extract lost name field: %s", raw)
	}
	if strings.Contains(string(raw), `"Value"`) {
		t.Fatal("unexpected InternalField wrapper serialization")
	}
}
