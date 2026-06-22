package commands

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/spf13/cobra"
)

// volatileGeneratedAtRE matches the wall-clock _runtime.generated_at field so
// tests can blank it before comparing otherwise-deterministic record output.
var volatileGeneratedAtRE = regexp.MustCompile(`"generated_at":"[^"]*"`)

// writeMultiInputSet writes n input XML files and a newline-delimited file list,
// returning the file-list path and the input file paths.
func writeMultiInputSet(t *testing.T, n int) (fileList string, inputs []string) {
	t.Helper()
	dir := t.TempDir()
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "in"+string(rune('A'+i))+".xml")
		body := `<root><TargetElement><Name>val` + string(rune('A'+i)) + `</Name></TargetElement></root>`
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write input: %v", err)
		}
		inputs = append(inputs, p)
	}
	fileList = filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(inputs, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
	}
	return fileList, inputs
}

func TestRunExtractMulti_ParsesEachFileOnceForAllRecipes(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, inputs := writeMultiInputSet(t, 3)
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{FileList: fileList, OutputPath: outRoot} // RunID resolved by the dispatcher

	d := newMultiDispatcher(shared, io.Discard)
	parseCounts := make(map[string]int)
	realParse := d.parseFile
	d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
		parseCounts[p]++
		return realParse(p, a)
	}

	if err := d.run([]string{wsA, wsB}, time.Now()); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	// The headline guarantee: each input file is parsed exactly ONCE even though
	// TWO recipes consume it — not once per (file, recipe).
	if len(parseCounts) != len(inputs) {
		t.Fatalf("parsed %d distinct files, want %d", len(parseCounts), len(inputs))
	}
	total := 0
	for f, c := range parseCounts {
		if c != 1 {
			t.Errorf("file %s parsed %d times, want exactly 1", f, c)
		}
		total += c
	}
	if total != len(inputs) {
		t.Errorf("total parses = %d, want %d (one per file, shared across recipes)", total, len(inputs))
	}

	// Each recipe wrote output to its OWN validated subdirectory.
	for _, id := range []string{"summary", "line-items"} {
		entries, err := os.ReadDir(filepath.Join(outRoot, id))
		if err != nil {
			t.Fatalf("recipe %q output dir: %v", id, err)
		}
		if len(entries) == 0 {
			t.Errorf("recipe %q produced no output", id)
		}
	}
}

func TestRunExtractMulti_NoCrossRecipeOutputBleed(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, _ := writeMultiInputSet(t, 2)
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{FileList: fileList, OutputPath: outRoot}
	if err := runExtractMulti(shared, []string{wsA, wsB}, io.Discard, time.Now()); err != nil {
		t.Fatalf("runExtractMulti: %v", err)
	}

	// Each recipe's records carry ITS record type and never the other's, proving
	// no output bleed across the per-recipe output directories.
	summary := readDirConcat(t, filepath.Join(outRoot, "summary"))
	lineItems := readDirConcat(t, filepath.Join(outRoot, "line-items"))
	if !strings.Contains(summary, "summary_record") {
		t.Errorf("summary output missing its own record type; got: %s", summary)
	}
	if strings.Contains(summary, "line-items_record") {
		t.Errorf("summary output leaked the other recipe's record type: %s", summary)
	}
	if !strings.Contains(lineItems, "line-items_record") {
		t.Errorf("line-items output missing its own record type; got: %s", lineItems)
	}
	if strings.Contains(lineItems, "summary_record") {
		t.Errorf("line-items output leaked the other recipe's record type: %s", lineItems)
	}
}

func TestRunExtractMulti_RequiresOutputRootAndInput(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)

	if err := runExtractMulti(&multiSharedOptions{FileList: fileList}, []string{ws}, io.Discard, time.Now()); err == nil {
		t.Error("expected error when --output-path is missing, got nil")
	}
	if err := runExtractMulti(&multiSharedOptions{OutputPath: filepath.Join(t.TempDir(), "o")}, []string{ws}, io.Discard, time.Now()); err == nil {
		t.Error("expected error when no input mode is set, got nil")
	}
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: filepath.Join(t.TempDir(), "o")}, nil, io.Discard, time.Now()); err == nil {
		t.Error("expected error when no recipe workspaces given, got nil")
	}
}

// writeMinOccursRecipe writes a recipe like writeMultiRecipeWorkspace but with a
// match_selectors[].min_occurrences floor, so inputs lacking the element fail.
func writeMinOccursRecipe(t *testing.T, id string, floor int) string {
	t.Helper()
	ws := writeMultiRecipeWorkspace(t, id)
	extract := `record_type: ` + id + `_record
match_selectors:
  - xpath: //TargetElement
    min_occurrences: ` + string(rune('0'+floor)) + `
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`
	if err := os.WriteFile(filepath.Join(ws, "extract", "extract.yaml"), []byte(extract), 0o600); err != nil {
		t.Fatalf("rewrite extract.yaml: %v", err)
	}
	return ws
}

// writeApplicabilityRecipe writes a recipe with an applicability XPath predicate,
// so a run can mix applicable / not-applicable recipes over the same input.
func writeApplicabilityRecipe(t *testing.T, id, predicate string) string {
	t.Helper()
	ws := writeMultiRecipeWorkspace(t, id)
	if err := os.MkdirAll(filepath.Join(ws, "applicability"), 0o750); err != nil {
		t.Fatalf("mkdir applicability: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ws, "applicability", "applicability.yaml"),
		[]byte("applicability:\n  type: xpath\n  expression: \""+predicate+"\"\n"), 0o600); err != nil {
		t.Fatalf("write applicability: %v", err)
	}
	recipe := `version: recipe/v0.1.0
kind: extract
id: ` + id + `
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
  applicability: applicability/applicability.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
`
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(recipe), 0o600); err != nil {
		t.Fatalf("rewrite recipe.yaml: %v", err)
	}
	return ws
}

func TestRunExtractMulti_SeamEquivalenceAndOrderIndependence(t *testing.T) {
	// One input with a TargetElement: the "applies" recipe's predicate is true,
	// the "skips" recipe's predicate is false — different dispositions, one pass.
	applies := writeApplicabilityRecipe(t, "applies", "count(//TargetElement) > 0")
	skips := writeApplicabilityRecipe(t, "skips", "count(//Missing) > 0")
	dir := t.TempDir()
	in := filepath.Join(dir, "in.xml")
	if err := os.WriteFile(in, []byte(`<root><TargetElement><Name>x</Name></TargetElement></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(in+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run := func(order []string) string {
		root := filepath.Join(t.TempDir(), "out")
		// Fix the run id so the only thing that could differ between orders is the
		// extracted record content itself (record provenance carries the run id).
		if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: root, ContinueOnError: true, RunID: testMultiRunID}, order, io.Discard, time.Now()); err != nil {
			t.Fatalf("runExtractMulti %v: %v", order, err)
		}
		return root
	}

	// Order A and order B must yield byte-identical RECORD output for the
	// applicable recipe: recipe outcomes are isolated and order-independent over
	// the shared read-only doc. (Compare the record file, not the manifest, whose
	// generated_at timestamp is wall-clock.)
	readRecords := func(root string) string {
		data, err := os.ReadFile(filepath.Join(root, "applies", "extract-in.xml.json"))
		if err != nil {
			t.Fatalf("read applies records: %v", err)
		}
		// _runtime.generated_at is wall-clock per extraction; blank it so the
		// comparison reflects only the stable extracted record content (run id is
		// already pinned), not which second the two runs happened to straddle.
		return volatileGeneratedAtRE.ReplaceAllString(string(data), `"generated_at":""`)
	}
	outAB := readRecords(run([]string{applies, skips}))
	rootBA := run([]string{skips, applies})
	outBA := readRecords(rootBA)
	if outAB != outBA {
		t.Errorf("applicable recipe record output depends on recipe order:\n A,B: %s\n B,A: %s", outAB, outBA)
	}
	if strings.TrimSpace(outAB) == "" {
		t.Error("applicable recipe produced no records")
	}
	// The not-applicable recipe records its disposition (dispositions.json) and
	// does not abort the applicable recipe.
	if _, err := os.Stat(filepath.Join(rootBA, "skips", "dispositions.json")); err != nil {
		t.Errorf("not-applicable recipe should write dispositions.json: %v", err)
	}
}

// writeSignatureMismatchRecipe writes a recipe whose signature cannot match the
// shared input, so it produces no records (but does not fail) — a distinct
// disposition from applied/extraction-failure.
func writeSignatureMismatchRecipe(t *testing.T, id string) string {
	t.Helper()
	ws := writeMultiRecipeWorkspace(t, id)
	if err := os.WriteFile(filepath.Join(ws, "signature", "signature.yaml"), []byte(`signature_id: nomatch
name: NoMatch
match_patterns:
  - pattern_id: x
    name: X
    selector: /no-such-root
    weight: 1
confidence_threshold: 1
`), 0o600); err != nil {
		t.Fatalf("rewrite signature.yaml: %v", err)
	}
	return ws
}

// writeExtractionErrorRecipe writes a recipe whose output schema requires a field
// the records never produce, so extraction fails per matched file (a recipe-level
// extraction error, distinct from a min_occurrences floor).
func writeExtractionErrorRecipe(t *testing.T, id string) string {
	t.Helper()
	ws := writeMultiRecipeWorkspace(t, id)
	if err := os.WriteFile(filepath.Join(ws, "extract", "extract.yaml"), []byte(`record_type: `+id+`_record
match_selectors:
  - xpath: //TargetElement
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  required:
    - must_have
  properties:
    name:
      type: string
    must_have:
      type: string
`), 0o600); err != nil {
		t.Fatalf("rewrite extract.yaml: %v", err)
	}
	return ws
}

func TestRunExtractMulti_IsolatesSignatureMismatchAndExtractionError(t *testing.T) {
	good := writeMultiRecipeWorkspace(t, "good")            // matches + extracts
	mismatch := writeSignatureMismatchRecipe(t, "mismatch") // signature never matches -> empty, no failure
	errRecipe := writeExtractionErrorRecipe(t, "errr")      // output-schema validation fails -> recipe-level failure
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")

	// continue-on-error: each recipe's outcome is isolated in one parse-once pass.
	_ = runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, ContinueOnError: true}, []string{good, mismatch, errRecipe}, io.Discard, time.Now())

	// good extracted records despite the others' outcomes.
	if !strings.Contains(readDirConcat(t, filepath.Join(outRoot, "good")), "good_record") {
		t.Error("good recipe produced no records (should be isolated from the others)")
	}
	// signature mismatch produced output but no failure (no failures.json).
	if _, err := os.Stat(filepath.Join(outRoot, "mismatch", "failures.json")); err == nil {
		t.Error("signature-mismatch recipe should not record a failure")
	}
	// extraction error is recorded as that recipe's own failure, not the others'.
	if _, err := os.Stat(filepath.Join(outRoot, "errr", "failures.json")); err != nil {
		t.Errorf("extraction-error recipe should record its own failures.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "good", "failures.json")); err == nil {
		t.Error("good recipe must not inherit the extraction-error recipe's failure")
	}
}

func TestRecipeRunExtractMultiCommand_ManifestRecordsMultiArgv(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")

	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(newRecipeRunExtractMultiCommand())
	root.SetArgs([]string{"extract-multi", ws, "--file-list", fileList, "--output-path", outRoot})
	if err := root.Execute(); err != nil {
		t.Fatalf("extract-multi command: %v", err)
	}

	// The recipe's provenance manifest must record the actual extract-multi
	// invocation, never the single-recipe `extract files` fallback argv.
	data, err := os.ReadFile(filepath.Join(outRoot, "summary", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest := string(data)
	if !strings.Contains(manifest, "recipes run extract-multi") {
		t.Errorf("manifest does not record the extract-multi command/argv:\n%s", manifest)
	}
	if strings.Contains(manifest, "extract files") {
		t.Errorf("manifest recorded the single-recipe fallback argv:\n%s", manifest)
	}
}

func TestRunExtractMulti_NormalizesFileURIOutputRoot(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	localRoot := filepath.Join(t.TempDir(), "out")
	// A file:// output root must normalize to its local path (single-recipe
	// parity) — output lands under the real dir, not a literal "file:" path.
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: "file://" + localRoot}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("runExtractMulti with file:// output root: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(localRoot, "summary"))
	if err != nil || len(entries) == 0 {
		t.Errorf("expected output under the normalized local root %s/summary (err=%v)", localRoot, err)
	}
}

func TestRunExtractMulti_EnforcesMinOccurrences(t *testing.T) {
	strict := writeMinOccursRecipe(t, "strict", 1) // requires >=1 //TargetElement
	lax := writeMultiRecipeWorkspace(t, "lax")     // no floor

	// Input matches the signature (/root) but has NO TargetElement, so the strict
	// recipe's floor is violated while the lax recipe is fine (0 records, no floor).
	dir := t.TempDir()
	in := filepath.Join(dir, "empty.xml")
	if err := os.WriteFile(in, []byte(`<root></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(in+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Fail-fast: a min_occurrences violation must fail the run (not publish a
	// successful zero-record output), matching single-recipe ADR-0007 behavior.
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: filepath.Join(t.TempDir(), "o")}, []string{strict, lax}, io.Discard, time.Now()); err == nil {
		t.Fatal("expected min_occurrences violation to fail the run, got nil")
	}

	// continue-on-error: the strict recipe records a failure, but the lax recipe
	// for the SAME parsed file still proceeds (recipe-level isolation).
	outRoot := filepath.Join(t.TempDir(), "o2")
	_ = runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, ContinueOnError: true}, []string{strict, lax}, io.Discard, time.Now())
	if _, err := os.Stat(filepath.Join(outRoot, "strict", "failures.json")); err != nil {
		t.Errorf("strict recipe should have recorded a failures.json under continue-on-error: %v", err)
	}
	laxEntries, err := os.ReadDir(filepath.Join(outRoot, "lax"))
	if err != nil || len(laxEntries) == 0 {
		t.Errorf("lax recipe should still produce output despite strict's failure (got err=%v, entries=%d)", err, len(laxEntries))
	}
}

func readDirConcat(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	var b strings.Builder
	for _, e := range entries {
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String()
}
