package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/valueprofile"
	"github.com/parquet-go/parquet-go"
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

	// Parquet: physical schema inspection + provenance/catalog config path.
	parquetPath := filepath.Join(outputDir, "records.parquet")
	assertParquetSchemaOmitsInternalColumns(t, parquetPath, []string{"sign_factor", "doubled"}, []string{"amount", "raw"})
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
	// Recipes without internal:true (and with internal:false explicitly) must
	// produce identical extract.data payloads (golden parity on the grain).
	// Runtime ids/paths differ per run, so compare decoded extract.data only.
	initExtractManifestTestLogger(t)
	baseDir := createExtractManifestFixture(t)
	falseDir := createWorkingTempDir(t)
	for _, name := range []string{"input.xml", "signature.yaml"} {
		data, err := os.ReadFile(filepath.Join(baseDir, name)) // #nosec G304
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		mustWriteFile(t, filepath.Join(falseDir, name), string(data))
	}
	mustWriteFile(t, filepath.Join(falseDir, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
    description: Sample name
    internal: false
output_schema:
  type: object
  properties:
    name:
      type: string
  required:
    - name
`)

	extractDataPayloads := func(t *testing.T, dir string) string {
		t.Helper()
		out := filepath.Join(dir, "outputs")
		_ = os.MkdirAll(out, 0o750)
		if err := runExtract(&ExtractOptions{
			Files:           filepath.Join(dir, "input.xml"),
			Format:          "json",
			OutputPath:      out,
			OutputPattern:   "records.json",
			SignatureConfig: filepath.Join(dir, "signature.yaml"),
			ExtractConfig:   filepath.Join(dir, "extract.yaml"),
			NoManifest:      true,
			RunID:           testMultiRunID,
		}); err != nil {
			t.Fatalf("runExtract: %v", err)
		}
		raw, err := os.ReadFile(filepath.Join(out, "records.json")) // #nosec G304
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		// NDJSON/JSONL: one envelope per line — extract only extract.data.
		var grains []json.RawMessage
		for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var env map[string]interface{}
			if err := json.Unmarshal([]byte(line), &env); err != nil {
				t.Fatalf("decode line: %v", err)
			}
			extractObj, _ := env["extract"].(map[string]interface{})
			b, err := json.Marshal(extractObj["data"])
			if err != nil {
				t.Fatalf("marshal data: %v", err)
			}
			grains = append(grains, b)
		}
		outBytes, err := json.Marshal(grains)
		if err != nil {
			t.Fatalf("marshal grains: %v", err)
		}
		if strings.Contains(string(raw), `"Value"`) {
			t.Fatal("unexpected InternalField wrapper serialization")
		}
		return string(outBytes)
	}

	baseline := extractDataPayloads(t, baseDir)
	explicitFalse := extractDataPayloads(t, falseDir)
	if baseline != explicitFalse {
		t.Fatalf("internal:false / omitted extract.data not golden-identical\n--- omitted ---\n%s\n--- false ---\n%s", baseline, explicitFalse)
	}
	if !strings.Contains(baseline, `"name"`) {
		t.Fatalf("no-opt extract lost name field: %s", baseline)
	}
}

func TestLoadExtractConfigRejectsInvalidInternalShapes(t *testing.T) {
	initExtractManifestTestLogger(t)
	cases := []struct {
		name string
		yaml string
	}{
		{
			name: "top-level array internal",
			yaml: `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: lines
    xpath: line
    type: array
    internal: true
    item_mapping:
      - output_field: x
        xpath: x
        type: string
output_schema:
  type: object
  properties: {}
`,
		},
		{
			name: "nested item_mapping internal",
			yaml: `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: lines
    xpath: line
    type: array
    item_mapping:
      - output_field: hidden
        xpath: x
        type: string
        internal: true
output_schema:
  type: object
  properties: {}
`,
		},
		{
			name: "nested polymorphic internal",
			yaml: `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: lines
    xpath: line
    type: array
    polymorphic_mapping:
      - item_type: kind
        field_mappings:
          - output_field: hidden
            xpath: x
            type: string
            internal: true
output_schema:
  type: object
  properties: {}
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := createWorkingTempDir(t)
			path := filepath.Join(dir, "extract.yaml")
			mustWriteFile(t, path, tc.yaml)
			_, err := extract.LoadExtractConfig(path)
			if err == nil {
				t.Fatal("expected LoadExtractConfig rejection")
			}
			// Schema additionalProperties / not clauses, or prepare nested reject.
			msg := strings.ToLower(err.Error())
			if !strings.Contains(msg, "internal") && !strings.Contains(msg, "additional property") && !strings.Contains(msg, "validation") {
				t.Fatalf("want schema/prepare rejection, got: %v", err)
			}
		})
	}
}

func TestExtractMultiPlanRejectsValueProfileInternalField(t *testing.T) {
	initExtractManifestTestLogger(t)
	ws := writeMultiRecipeWorkspaceWithDefaults(t, "summary", `  input:
    mode: files
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  value_profile:
    enabled: true
    fields:
      - field: helper
        safe_to_profile: true
        sensitivity: public
`)
	// Override extract asset with an internal helper + emitted amount.
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: summary_record
match_selectors:
  - xpath: //TargetElement
field_mappings:
  - output_field: helper
    xpath: "1"
    type: number
    internal: true
  - output_field: name
    xpath: Name
    type: string
  - output_field: amount
    expression: "helper"
    type: number
output_schema:
  type: object
  properties:
    name:
      type: string
    amount:
      type: number
`)
	shared := &multiSharedOptions{
		FileList: filepath.Join(t.TempDir(), "files.txt"),
		RunID:    "0190a3f4-1c2d-7abc-9def-0123456789ab",
		Workers:  1,
	}
	_, err := loadRecipePlan(ws, shared, filepath.Join(t.TempDir(), "summary"), io.Discard)
	if err == nil {
		t.Fatal("expected extract-multi plan-load rejection for value_profile on internal field")
	}
	if !strings.Contains(err.Error(), "helper") {
		t.Fatalf("want helper in error, got: %v", err)
	}
}

func TestInternalFieldMappingZeroRecordDisclosureClean(t *testing.T) {
	// Zero matching records: internals must still be absent from provenance,
	// catalog, value_profile keys, and Parquet schema (config-derived paths).
	initExtractManifestTestLogger(t)
	dir := createWorkingTempDir(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root></root>`)
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
  - output_field: doubled
    expression: "1 * 2"
    type: number
    internal: true
  - output_field: amount
    expression: "sign_factor * doubled"
    type: number
output_schema:
  type: object
  properties:
    amount:
      type: number
`)
	outputDir := filepath.Join(dir, "outputs")
	_ = os.MkdirAll(outputDir, 0o750)
	contractBase := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	if err := runExtract(&ExtractOptions{
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
			ID:                    "zero_record_internal_fixture",
			ManifestSchemaVersion: "recipe/v0.1.0",
			ContentVersion:        "1.0.0",
			ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			Owners:                []provenance.Owner{{Name: "Fulmen Sumpter contributors"}},
			ManifestYAML:          "version: recipe/v0.1.0\nid: zero_record_internal_fixture\n",
			SignatureYAML:         "signature_id: sample\n",
			ExtractYAML:           "record_type: sample_record\n",
		},
		ValueProfile: &valueprofile.Config{
			Enabled: true,
			Fields: []valueprofile.FieldConfig{
				{Field: "amount", SafeToProfile: true, Sensitivity: valueprofile.SensitivityPublic},
			},
		},
	}); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	// Zero records → empty/minimal NDJSON
	ndjson, err := os.ReadFile(filepath.Join(outputDir, "records.ndjson")) // #nosec G304
	if err != nil {
		t.Fatalf("read ndjson: %v", err)
	}
	for _, leak := range []string{"sign_factor", "doubled"} {
		if strings.Contains(string(ndjson), leak) {
			t.Errorf("zero-record NDJSON leaked %q", leak)
		}
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.Recipe == nil {
		t.Fatal("expected recipe on manifest")
	}
	for _, field := range manifest.Recipe.FieldProvenance {
		if field.OutputField == "sign_factor" || field.OutputField == "doubled" {
			t.Errorf("zero-record field_provenance listed internal %q", field.OutputField)
		}
	}
	if len(manifest.ValueProfile) > 0 {
		var profile map[string]interface{}
		if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
			t.Fatalf("unmarshal value_profile: %v", err)
		}
		if fields, ok := profile["fields"].(map[string]interface{}); ok {
			for _, name := range []string{"sign_factor", "doubled"} {
				if _, ok := fields[name]; ok {
					t.Errorf("zero-record value_profile listed internal %q", name)
				}
			}
		}
	}

	descBytes, err := os.ReadFile(filepath.Join(outputDir, dataartifact.DescriptorFileName)) // #nosec G304
	if err != nil {
		t.Fatalf("read descriptor: %v", err)
	}
	for _, leak := range []string{"sign_factor", "doubled"} {
		if strings.Contains(string(descBytes), leak) {
			t.Errorf("zero-record descriptor leaked %q", leak)
		}
	}

	// Config-derived Parquet schema must not include internals even with zero rows.
	extCfg, err := extract.LoadExtractConfig(filepath.Join(dir, "extract.yaml"))
	if err != nil {
		t.Fatalf("reload extract: %v", err)
	}
	for _, p := range buildFieldProvenance(extCfg.FieldMappings) {
		if p.OutputField == "sign_factor" || p.OutputField == "doubled" {
			t.Errorf("zero-record provenance includes %q", p.OutputField)
		}
	}

	// Direct Parquet schema inspection (not only provenance/config helper).
	parquetPath := filepath.Join(outputDir, "records.parquet")
	assertParquetSchemaOmitsInternalColumns(t, parquetPath, []string{"sign_factor", "doubled"}, []string{"amount"})
}

// assertParquetSchemaOmitsInternalColumns opens a written Parquet file and
// asserts internal helper names are absent from the physical schema while
// expected emitted columns remain present.
func assertParquetSchemaOmitsInternalColumns(t *testing.T, path string, internals, emitted []string) {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("open parquet %s: %v", path, err)
	}
	t.Cleanup(func() { _ = f.Close() })
	st, err := f.Stat()
	if err != nil {
		t.Fatalf("stat parquet: %v", err)
	}
	pqFile, err := parquet.OpenFile(f, st.Size())
	if err != nil {
		t.Fatalf("OpenFile parquet: %v", err)
	}
	schema := pqFile.Schema().String()
	for _, leak := range internals {
		if strings.Contains(schema, leak) {
			t.Errorf("parquet schema leaked internal column %q: %s", leak, schema)
		}
	}
	for _, want := range emitted {
		if !strings.Contains(schema, want) {
			t.Errorf("parquet schema missing emitted column %q: %s", want, schema)
		}
	}
}
