package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"
)

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
