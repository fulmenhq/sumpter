package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// createReferenceTableWorkspace builds a recipe that declares a membership table
// (curated) and a key→value lookup table (molecule), both read by field-mapping
// expressions. extraFields/extraDecls let individual scenarios swap the expressions
// or table declarations. Identifiers are synthetic (public RefSeq accession scheme),
// per the OSS confidentiality posture.
func createReferenceTableWorkspace(t *testing.T, referenceTablesYAML, fieldMappingsYAML, outputSchemaYAML string) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "refdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: reftable_e2e
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/nm.xml
      - testdata/xr.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
`+referenceTablesYAML+`  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: reftable_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: accession
    xpath: Accession
    type: string
`+fieldMappingsYAML+`output_schema:
  type: object
  properties:
    accession:
      type: string
`+outputSchemaYAML)

	// Reference tables. "mRNA"/"ncRNA" appear ONLY here — never in inputs or paths —
	// so they are the no-leak probes for the provenance sidecar.
	mustWriteFile(t, filepath.Join(workspace, "refdata", "curated.csv"), "accession\nNM_000546\nNR_001234\n")
	mustWriteFile(t, filepath.Join(workspace, "refdata", "molecule.csv"), "accession,molecule_type\nNM_000546,mRNA\nNR_001234,ncRNA\n")

	mustWriteFile(t, filepath.Join(workspace, "testdata", "nm.xml"),
		"<root><item><Accession>NM_000546</Accession></item></root>")
	mustWriteFile(t, filepath.Join(workspace, "testdata", "xr.xml"),
		"<root><item><Accession>XR_999999</Accession></item></root>")
	return workspace
}

const refTablesBothYAML = `  reference_tables:
    - name: curated
      source: refdata/curated.csv
      format: csv
      header: true
      column: accession
      max_rows: 100
    - name: molecule
      source: refdata/molecule.csv
      format: csv
      header: true
      key_column: accession
      value_column: molecule_type
      max_rows: 100
`

const refFieldsBothYAML = `  - output_field: is_curated
    expression: "in_reference('curated', accession)"
    type: boolean
  - output_field: molecule_type
    expression: "lookup_reference('molecule', accession, 'unknown')"
    type: string
`

const refSchemaBothYAML = `    is_curated:
      type: boolean
    molecule_type:
      type: string
`

func readReferenceRecord(t *testing.T, workspace, name string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(workspace, "outputs", "extract-"+name+".xml.json")
	data, err := os.ReadFile(path) // #nosec G304 - test-owned temp path
	if err != nil {
		t.Fatalf("read output %s: %v", name, err)
	}
	var record map[string]interface{}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("decode output %s: %v", name, err)
	}
	return extractData(t, record)
}

func TestRecipeReferenceTableEndToEnd(t *testing.T) {
	initExtractManifestTestLogger(t)
	ws := createReferenceTableWorkspace(t, refTablesBothYAML, refFieldsBothYAML, refSchemaBothYAML)

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false}); err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	// Membership (Pattern A) + key→value lookup (Pattern B) resolve end-to-end.
	nm := readReferenceRecord(t, ws, "nm")
	assertBoolField(t, nm, "is_curated", true)
	if got := nm["molecule_type"]; got != "mRNA" {
		t.Errorf("nm molecule_type = %#v, want mRNA", got)
	}
	xr := readReferenceRecord(t, ws, "xr")
	assertBoolField(t, xr, "is_curated", false)
	if got := xr["molecule_type"]; got != "unknown" { // lookup miss → declared default
		t.Errorf("xr molecule_type = %#v, want unknown", got)
	}

	// Provenance sidecar carries the reference-table entries (sorted by name),
	// sidecar-only, with NO row values.
	manifestPath := filepath.Join(ws, "outputs", provenance.ManifestFileName)
	manifest := readManifest(t, manifestPath)
	if len(manifest.ReferenceTables) != 2 {
		t.Fatalf("manifest.ReferenceTables len = %d, want 2", len(manifest.ReferenceTables))
	}
	curated := manifest.ReferenceTables[0]
	if curated.Name != "curated" || curated.Mode != "membership" || curated.Format != "csv" ||
		curated.Source != "refdata/curated.csv" || curated.RowCount != 2 || !strings.HasPrefix(curated.ContentSHA256, "sha256:") {
		t.Errorf("curated provenance wrong: %#v", curated)
	}
	molecule := manifest.ReferenceTables[1]
	if molecule.Name != "molecule" || molecule.Mode != "lookup" || molecule.RowCount != 2 {
		t.Errorf("molecule provenance wrong: %#v", molecule)
	}

	// No-leak: the raw sidecar must not contain any reference-table cell value.
	raw, err := os.ReadFile(manifestPath) // #nosec G304 - test-owned temp path
	if err != nil {
		t.Fatalf("read manifest bytes: %v", err)
	}
	for _, secret := range []string{"mRNA", "ncRNA"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("provenance sidecar leaked reference-table value %q", secret)
		}
	}
}

func TestRecipeReferenceTableOverride(t *testing.T) {
	initExtractManifestTestLogger(t)
	ws := createReferenceTableWorkspace(t, refTablesBothYAML, refFieldsBothYAML, refSchemaBothYAML)
	// An alternate curated set that includes XR_999999 (absent from the declared one).
	mustWriteFile(t, filepath.Join(ws, "refdata", "alt_curated.csv"), "accession\nXR_999999\n")

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:            "recipe.yaml",
		ReferenceTableOverrides: []string{"curated=refdata/alt_curated.csv"},
		Progress:                false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}
	// XR_999999 is now curated (override source); NM is not (no longer in the set).
	assertBoolField(t, readReferenceRecord(t, ws, "xr"), "is_curated", true)
	assertBoolField(t, readReferenceRecord(t, ws, "nm"), "is_curated", false)

	manifest := readManifest(t, filepath.Join(ws, "outputs", provenance.ManifestFileName))
	if manifest.ReferenceTables[0].Source != "refdata/alt_curated.csv" {
		t.Errorf("provenance source = %q, want effective overridden source", manifest.ReferenceTables[0].Source)
	}
}

func TestRecipeReferenceTableUnknownTablePreflight(t *testing.T) {
	initExtractManifestTestLogger(t)
	// Expression references a table that is not declared.
	fields := `  - output_field: is_curated
    expression: "in_reference('ghost', accession)"
    type: boolean
`
	schema := `    is_curated:
      type: boolean
`
	ws := createReferenceTableWorkspace(t, refTablesBothYAML, fields, schema)

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Fatalf("err = %v, want unknown-table preflight failure", err)
	}
	// Preflight aborts before extraction: no output, no manifest written.
	if _, statErr := os.Stat(filepath.Join(ws, "outputs", provenance.ManifestFileName)); !os.IsNotExist(statErr) {
		t.Errorf("manifest written despite preflight failure (err=%v)", statErr)
	}
}

func TestRecipeReferenceTableContainmentRejected(t *testing.T) {
	initExtractManifestTestLogger(t)
	// Declared source escapes the workspace.
	tables := `  reference_tables:
    - name: curated
      source: ../outside.csv
      format: csv
      header: true
      column: accession
      max_rows: 100
`
	fields := `  - output_field: is_curated
    expression: "in_reference('curated', accession)"
    type: boolean
`
	schema := `    is_curated:
      type: boolean
`
	ws := createReferenceTableWorkspace(t, tables, fields, schema)
	// A real file just outside the workspace, the escape target.
	if err := os.WriteFile(filepath.Join(filepath.Dir(ws), "outside.csv"), []byte("accession\nNM_000546\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(filepath.Join(filepath.Dir(ws), "outside.csv")) })

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil || !strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("err = %v, want containment rejection", err)
	}
}
