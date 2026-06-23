package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// writeInternalCaptureWorkspace builds an extract recipe workspace with two
// source_extraction captures over the same inputs: a `grain` filename prefix
// (optionally declared internal:true) that a field_mappings expression turns
// into an emitted `grain_class`, and an emitted `site_id` relative-path capture.
// Both captures are required. With internal:true the grain value must drive the
// expression but never appear in extract.data; the emitted site_id is the
// non-internal control.
func writeInternalCaptureWorkspace(t *testing.T, internal bool) string {
	t.Helper()
	return writeInternalCaptureWorkspaceFormat(t, internal, "json")
}

// writeInternalCaptureWorkspaceFormat is writeInternalCaptureWorkspace with a
// selectable record-sink format (json | ndjson), so the omit-but-derive behavior
// can be asserted on the literal motivating NDJSON sink as well as JSON.
func writeInternalCaptureWorkspaceFormat(t *testing.T, internal bool, format string) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{
		"signature", "extract",
		"testdata/sites/store-17", "testdata/sites/store-22",
		"outputs",
	} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	internalLine := ""
	if internal {
		internalLine = "\n      internal: true"
	}
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: internal_capture_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/sites/store-17/unit-001.xml
      - testdata/sites/store-22/batch-002.xml
  output:
    format: `+format+`
    path: outputs
    pattern: extract-{}.`+format+`
  source_extraction:
    - id: grain-prefix
      source: filename
      pattern: '^(?P<grain>unit|batch)-'`+internalLine+`
    - id: path-site-identifier
      source: relative_path
      pattern: '^sites/(?P<site_id>[a-z0-9-]+)/'
  source_extraction_required:
    - grain
    - site_id
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
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
  - output_field: grain_class
    expression: 'grain == "unit" ? "fine_grain" : "coarse_grain"'
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    grain_class:
      type: string
    grain:
      type: string
    site_id:
      type: string
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "sites", "store-17", "unit-001.xml"), `<root><item><name>Alpha</name></item></root>`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "sites", "store-22", "batch-002.xml"), `<root><item><name>Beta</name></item></root>`)
	return workspace
}

func decodeExtractData(t *testing.T, outputPath string) map[string]interface{} {
	t.Helper()
	file, err := os.Open(outputPath) // #nosec G304 - test-owned temp path
	if err != nil {
		t.Fatalf("open output %s: %v", outputPath, err)
	}
	defer func() { _ = file.Close() }()
	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	return extractData(t, record)
}

// TestInternalSourceCaptureNotEmitted: an internal:true capture drives an
// expression-derived emitted field but is itself absent from extract.data, while
// the sibling non-internal capture still emits.
func TestInternalSourceCaptureNotEmitted(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := writeInternalCaptureWorkspace(t, true)

	if err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	cases := []struct {
		output         string
		wantName       string
		wantGrainClass string
		wantSiteID     string
	}{
		{"extract-unit-001.xml.json", "Alpha", "fine_grain", "store-17"},
		{"extract-batch-002.xml.json", "Beta", "coarse_grain", "store-22"},
	}
	for _, tc := range cases {
		t.Run(tc.output, func(t *testing.T) {
			data := decodeExtractData(t, filepath.Join(workspace, "outputs", tc.output))
			if data["name"] != tc.wantName {
				t.Errorf("name = %#v, want %q", data["name"], tc.wantName)
			}
			// The internal capture reached expression scope (derived field proves it)...
			if data["grain_class"] != tc.wantGrainClass {
				t.Errorf("grain_class = %#v, want %q (internal capture must be visible in expression scope)", data["grain_class"], tc.wantGrainClass)
			}
			// ...but is NOT emitted.
			if v, ok := data["grain"]; ok {
				t.Errorf("internal capture grain leaked into extract.data: %#v", v)
			}
			// The sibling non-internal capture still emits.
			if data["site_id"] != tc.wantSiteID {
				t.Errorf("site_id = %#v, want %q", data["site_id"], tc.wantSiteID)
			}
		})
	}
}

// TestInternalSourceCaptureNotEmittedNDJSON asserts the same omit-but-derive
// behavior on the literal motivating sink — NDJSON — that the field report named.
// (Structurally the skip is upstream of sink format, but the reported case
// deserves a direct assertion.)
func TestInternalSourceCaptureNotEmittedNDJSON(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := writeInternalCaptureWorkspaceFormat(t, true, "ndjson")

	if err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	// One record per file -> one NDJSON line; decode it and inspect extract.data.
	raw := readFileString(t, filepath.Join(workspace, "outputs", "extract-unit-001.xml.ndjson"))
	line := strings.TrimSpace(raw)
	if line == "" {
		t.Fatalf("NDJSON output is empty")
	}
	if i := strings.IndexByte(line, '\n'); i >= 0 {
		line = line[:i] // first record line
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		t.Fatalf("decode NDJSON line: %v\nline: %s", err, line)
	}
	data := extractData(t, record)
	if data["grain_class"] != "fine_grain" {
		t.Errorf("grain_class = %#v, want \"fine_grain\" (internal capture must drive the expression)", data["grain_class"])
	}
	if v, ok := data["grain"]; ok {
		t.Errorf("internal capture grain leaked into NDJSON extract.data: %#v", v)
	}
	if data["site_id"] != "store-17" {
		t.Errorf("site_id = %#v, want \"store-17\"", data["site_id"])
	}
}

// TestSourceCaptureEmittedWithoutInternal is the non-regression control: with no
// internal flag, the same grain capture emits into extract.data as before.
func TestSourceCaptureEmittedWithoutInternal(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := writeInternalCaptureWorkspace(t, false)

	if err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	data := decodeExtractData(t, filepath.Join(workspace, "outputs", "extract-unit-001.xml.json"))
	if data["grain"] != "unit" {
		t.Errorf("grain = %#v, want \"unit\" emitted when internal is not set (non-regression)", data["grain"])
	}
	if data["grain_class"] != "fine_grain" {
		t.Errorf("grain_class = %#v, want \"fine_grain\"", data["grain_class"])
	}
}

// TestInternalCaptureSatisfiesRequiredButMissFails: an internal capture listed in
// source_extraction_required still fails per-file when absent (required-
// enforcement reads the raw capture, independent of emit visibility).
func TestInternalCaptureRequiredMissFails(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := writeInternalCaptureWorkspace(t, true)
	// Add a third input whose filename does NOT match the grain prefix, so the
	// required internal capture is absent for that file.
	if err := os.MkdirAll(filepath.Join(workspace, "testdata", "sites", "store-99"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustWriteFile(t, filepath.Join(workspace, "testdata", "sites", "store-99", "nograin.xml"), `<root><item><name>Gamma</name></item></root>`)
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), strings.Replace(
		readFileString(t, filepath.Join(workspace, "recipe.yaml")),
		"      - testdata/sites/store-22/batch-002.xml",
		"      - testdata/sites/store-22/batch-002.xml\n      - testdata/sites/store-99/nograin.xml",
		1,
	))

	err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "required source_extraction field \"grain\" not provided") {
		t.Fatalf("expected required-miss failure for the internal capture, got %v", err)
	}
}

// TestInternalCaptureCollisionFailsPlan: an internal capture name that collides
// with a field_mappings output_field still fails the collision check before
// extraction — internal visibility does not relax collision discipline.
func TestInternalCaptureCollisionFailsPlan(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	// The internal capture is named `name`, colliding with the `name` output_field.
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: internal_collision_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/unit-001.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  source_extraction:
    - id: name-grabber
      source: filename
      pattern: '^(?P<name>[a-z0-9-]+)\.xml$'
      internal: true
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
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
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
	mustWriteFile(t, filepath.Join(workspace, "testdata", "unit-001.xml"), `<root><item><name>Alpha</name></item></root>`)

	err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "collides with field_mappings output_field") {
		t.Fatalf("expected internal-capture/output_field collision to fail, got %v", err)
	}
}

// TestMixedVisibilityDuplicateCaptureRejected: a capture name declared on both an
// internal:true and a non-internal pattern is ambiguous (emission would depend on
// declaration, not on which pattern actually matched) and must fail loud at plan
// validation — even when the internal pattern does not match the input. This is
// the regression for the duplicate-capture visibility blocker.
func TestMixedVisibilityDuplicateCaptureRejected(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	// `grain` is captured by an emitted filename pattern AND an internal
	// relative_path pattern (which will not even match unit-001.xml). The mix is
	// rejected regardless of which matches.
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: mixed_visibility_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/unit-001.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  source_extraction:
    - id: emitted-grain
      source: filename
      pattern: '^(?P<grain>unit|batch)-'
    - id: internal-grain-fallback
      source: relative_path
      pattern: '^NO_MATCH/(?P<grain>[^/]+)'
      internal: true
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
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
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
	mustWriteFile(t, filepath.Join(workspace, "testdata", "unit-001.xml"), `<root><item><name>Alpha</name></item></root>`)

	err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "declared on both an internal:true and a non-internal pattern") {
		t.Fatalf("expected mixed-visibility duplicate capture to be rejected, got %v", err)
	}
}

// TestSameVisibilityDuplicateCaptureAllowed: a capture name repeated across
// patterns that AGREE on visibility (here both non-internal) is still allowed and
// keeps last-match-wins value semantics — the fail-loud check only rejects the
// mixed case.
func TestSameVisibilityDuplicateCaptureAllowed(t *testing.T) {
	initExtractManifestTestLogger(t)
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	// Two non-internal filename patterns both capture `grain`; the later pattern
	// wins (grain=001), and the value is emitted (no internal flag anywhere).
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: dup_visibility_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/unit-001.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  source_extraction:
    - id: grain-prefix
      source: filename
      pattern: '^(?P<grain>unit|batch)-'
    - id: grain-number
      source: filename
      pattern: '-(?P<grain>\d+)\.xml$'
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
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    grain:
      type: string
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "unit-001.xml"), `<root><item><name>Alpha</name></item></root>`)

	if err := executeExtractRecipe(recipeRunExtractTestCommand(), workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	}); err != nil {
		t.Fatalf("same-visibility duplicate capture should be allowed: %v", err)
	}
	data := decodeExtractData(t, filepath.Join(workspace, "outputs", "extract-unit-001.xml.json"))
	if data["grain"] != "001" {
		t.Errorf("grain = %#v, want \"001\" (last matching pattern wins, emitted)", data["grain"])
	}
}

// TestInternalSourceCaptureNotEmittedUnderExtractMulti: the same omit-but-derive
// behavior holds per recipe in the parse-once extract-multi dispatcher.
func TestInternalSourceCaptureNotEmittedUnderExtractMulti(t *testing.T) {
	wsA := writeInternalCaptureWorkspace(t, true)
	outRoot := filepath.Join(t.TempDir(), "out")

	// extract-multi shares one input set; point it at the recipe's own inputs.
	fileList := filepath.Join(t.TempDir(), "files.txt")
	inputs := []string{
		filepath.Join(wsA, "testdata", "sites", "store-17", "unit-001.xml"),
		filepath.Join(wsA, "testdata", "sites", "store-22", "batch-002.xml"),
	}
	if err := os.WriteFile(fileList, []byte(strings.Join(inputs, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	shared := &multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID}
	if err := runExtractMulti(shared, []string{wsA}, io.Discard, time.Now()); err != nil {
		t.Fatalf("runExtractMulti: %v", err)
	}

	records := readDirConcat(t, filepath.Join(outRoot, "internal_capture_recipe"))
	if !strings.Contains(records, `"grain_class":"fine_grain"`) {
		t.Errorf("extract-multi records missing the derived field:\n%s", records)
	}
	if strings.Contains(records, `"grain":`) {
		t.Errorf("extract-multi leaked the internal capture into records:\n%s", records)
	}
}

// TestSourceExtractionInternalDefaultsToEmitted pins the schema/manifest default:
// an omitted internal flag parses as false (today's emitted behavior), and
// internal:true round-trips.
func TestSourceExtractionInternalDefaultsToEmitted(t *testing.T) {
	parse := func(internal bool) recipesmanifest.SourceExtractionPattern {
		dir := t.TempDir()
		internalLine := ""
		if internal {
			internalLine = "\n      internal: true"
		}
		path := filepath.Join(dir, "recipe.yaml")
		mustWriteFile(t, path, `version: recipe/v0.1.0
kind: extract
id: internal_default_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/in.xml
  output:
    format: json
    path: outputs
  source_extraction:
    - id: grain-prefix
      source: filename
      pattern: '^(?P<grain>unit|batch)-'`+internalLine+`
`)
		manifest, err := recipesmanifest.LoadManifest(path)
		if err != nil {
			t.Fatalf("LoadManifest(internal=%v): %v", internal, err)
		}
		if len(manifest.Defaults.SourceExtraction) != 1 {
			t.Fatalf("want 1 source_extraction pattern, got %d", len(manifest.Defaults.SourceExtraction))
		}
		return manifest.Defaults.SourceExtraction[0]
	}

	if got := parse(false); got.Internal {
		t.Errorf("omitted internal flag = %v, want false (default emitted)", got.Internal)
	}
	if got := parse(true); !got.Internal {
		t.Errorf("internal: true did not round-trip, got %v", got.Internal)
	}
}
