package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// createRefSeqClassificationWorkspace builds a synthetic RefSeq-style recipe
// (SUM-040): list-typed parameters drive a membership/prefix classification in an
// expression: helper, with a length/shape guard composed alongside it. Every
// identifier is synthetic (public RefSeq accession scheme + ClinVar-style review
// statuses), per the OSS confidentiality posture.
func createRefSeqClassificationWorkspace(t *testing.T) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}

	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: refseq_classification
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/nm.xml
      - testdata/nr.xml
      - testdata/nc.xml
      - testdata/short.xml
      - testdata/xr.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters:
    curated_prefixes: ["NM_", "NR_"]
    accepted_statuses: ["reviewed", "validated"]
  workers: 1
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
	// is_curated_molecule composes the length/shape guard with the prefix test, both
	// reading list-typed parameters; is_accepted is exact membership.
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: refseq_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: accession
    xpath: Accession
    type: string
  - output_field: review_status
    xpath: ReviewStatus
    type: string
  - output_field: is_curated_molecule
    expression: '(string_length(accession) >= 5) && starts_with_any(accession, curated_prefixes)'
    type: boolean
  - output_field: is_accepted
    expression: 'value_in(review_status, accepted_statuses)'
    type: boolean
output_schema:
  type: object
  properties:
    accession:
      type: string
    review_status:
      type: string
    is_curated_molecule:
      type: boolean
    is_accepted:
      type: boolean
    curated_prefixes:
      type: array
      items:
        type: string
    accepted_statuses:
      type: array
      items:
        type: string
`)

	// One input file per record so each yields its own decodable output JSON.
	records := map[string]struct{ accession, status string }{
		"nm":    {"NM_000546", "reviewed"},  // curated (NM_, len 9), accepted
		"nr":    {"NR_001234", "validated"}, // curated (NR_, len 9), accepted
		"nc":    {"NC_000001", "review"},    // not curated by default; near-miss status
		"short": {"NM_1", "reviewed"},       // prefix matches but length guard (>=5) excludes; accepted
		"xr":    {"XR_999999", "reviewed"},  // not curated (XR_); accepted
	}
	for name, rec := range records {
		mustWriteFile(t, filepath.Join(workspace, "testdata", name+".xml"),
			"<root><item><Accession>"+rec.accession+"</Accession><ReviewStatus>"+rec.status+"</ReviewStatus></item></root>")
	}
	return workspace
}

// readRefSeqRecord decodes the per-input output JSON and returns its extract.data.
func readRefSeqRecord(t *testing.T, workspace, name string) map[string]interface{} {
	t.Helper()
	path := filepath.Join(workspace, "outputs", "extract-"+name+".xml.json")
	file, err := os.Open(path) // #nosec G304 - test-owned temp path
	if err != nil {
		t.Fatalf("open output %s: %v", name, err)
	}
	defer func() { _ = file.Close() }()
	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("decode output %s: %v", name, err)
	}
	return extractData(t, record)
}

func assertBoolField(t *testing.T, data map[string]interface{}, field string, want bool) {
	t.Helper()
	got, ok := data[field].(bool)
	if !ok {
		t.Fatalf("%s = %#v (%T), want bool", field, data[field], data[field])
	}
	if got != want {
		t.Fatalf("%s = %v, want %v (data: %#v)", field, got, want, data)
	}
}

// TestRecipeListParamClassification exercises the four SUM-040 acceptance fixtures
// end-to-end through `recipes run extract`: prefix classification + run-time
// override, the length-guard look-alike exclusion, exact membership (value_in),
// and an empty-list parameter matching nothing — all config-not-code.
func TestRecipeListParamClassification(t *testing.T) {
	initExtractManifestTestLogger(t)

	run := func(t *testing.T, params []string) string {
		workspace := createRefSeqClassificationWorkspace(t)
		cmd := recipeRunExtractTestCommand()
		if err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
			ManifestPath: "recipe.yaml",
			Parameters:   params,
			Progress:     false,
		}); err != nil {
			t.Fatalf("executeExtractRecipe: %v", err)
		}
		return workspace
	}

	t.Run("default prefixes + exact membership", func(t *testing.T) {
		ws := run(t, nil)
		// Fixture 1 (positive prefix) + Fixture 2 (look-alike exclusion).
		assertBoolField(t, readRefSeqRecord(t, ws, "nm"), "is_curated_molecule", true)
		assertBoolField(t, readRefSeqRecord(t, ws, "nr"), "is_curated_molecule", true)
		assertBoolField(t, readRefSeqRecord(t, ws, "nc"), "is_curated_molecule", false)
		assertBoolField(t, readRefSeqRecord(t, ws, "xr"), "is_curated_molecule", false)
		// Look-alike: prefix matches but the length guard (>=5) excludes NM_1.
		assertBoolField(t, readRefSeqRecord(t, ws, "short"), "is_curated_molecule", false)
		// Fixture 3 (value_in exact): reviewed/validated accepted; "review" near-miss not.
		assertBoolField(t, readRefSeqRecord(t, ws, "nm"), "is_accepted", true)
		assertBoolField(t, readRefSeqRecord(t, ws, "nr"), "is_accepted", true)
		assertBoolField(t, readRefSeqRecord(t, ws, "nc"), "is_accepted", false)
	})

	t.Run("runtime override flips NC_ with no recipe edit", func(t *testing.T) {
		ws := run(t, []string{`curated_prefixes=["NM_","NR_","NC_"]`})
		assertBoolField(t, readRefSeqRecord(t, ws, "nc"), "is_curated_molecule", true) // flipped
		assertBoolField(t, readRefSeqRecord(t, ws, "nm"), "is_curated_molecule", true)
		assertBoolField(t, readRefSeqRecord(t, ws, "short"), "is_curated_molecule", false) // guard still excludes
	})

	t.Run("empty list matches nothing", func(t *testing.T) {
		ws := run(t, []string{`curated_prefixes=[]`})
		assertBoolField(t, readRefSeqRecord(t, ws, "nm"), "is_curated_molecule", false)
		assertBoolField(t, readRefSeqRecord(t, ws, "nr"), "is_curated_molecule", false)
	})
}

// TestRecipeListParamParquetEmission proves a list-typed parameter flows through
// the existing Parquet emission path (no new path): the derived boolean field and
// the list parameter (as an array column) are written without error.
func TestRecipeListParamParquetEmission(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := createRefSeqClassificationWorkspace(t)
	// Switch the recipe output to parquet (and json, to keep a decodable record).
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: refseq_classification
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/nm.xml
    include_pattern: "*.xml"
  output:
    formats: [json, parquet]
    path: outputs
    patterns:
      json: extract-{}.json
      parquet: extract-{}.parquet
    parquet:
      compression: none
  parameters:
    curated_prefixes: ["NM_", "NR_"]
    accepted_statuses: ["reviewed", "validated"]
  workers: 1
  progress: false
`)

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false}); err != nil {
		t.Fatalf("executeExtractRecipe (parquet): %v", err)
	}

	// JSON record proves the classification; Parquet proves the array column wrote.
	assertBoolField(t, readRefSeqRecord(t, workspace, "nm"), "is_curated_molecule", true)

	pq := openCommandParquetFile(t, filepath.Join(workspace, "outputs", "extract-nm.xml.parquet"))
	fields := commandParquetFieldNames(pq)
	for _, want := range []string{"is_curated_molecule", "curated_prefixes"} {
		if !fields[want] {
			t.Fatalf("parquet missing column %q (have %#v)", want, fields)
		}
	}
}
