package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestExtractMultiAggregate_FinalizedRecipeNotOverwritten pins the per-recipe
// isolation fix: when one recipe finalized cleanly (wrote its successful manifest)
// and a SIBLING recipe later fails, the run-level failure handler must NOT rewrite
// the finalized recipe's manifest as incomplete:true.
func TestExtractMultiAggregate_FinalizedRecipeNotOverwritten(t *testing.T) {
	outDir := t.TempDir()
	st := &recipeRunState{
		finalized: true, // this recipe already committed + wrote a successful manifest
		aggWriter: &aggregateWriter{
			cloud:  true,
			shards: []provenance.AggregateOutput{{Path: "records-00001.jsonl", RecordCount: 1, SHA256: "sha256:" + strings.Repeat("0", 64)}},
		},
		plan: &RecipePlan{opts: &ExtractOptions{OutputPath: outDir}},
	}
	// Even though the run failed (handler invoked) and this recipe has committed cloud
	// shards, a finalized recipe must be left untouched.
	st.writeIncompleteAggregateManifestOnFailure(time.Now())
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err == nil {
		t.Error("a finalized recipe's manifest was overwritten with incomplete on a sibling's failure")
	}
}

func TestExtractMultiAggregate_PerRecipeStreamAndProvenance(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, _ := writeMultiInputSet(t, 3) // 3 inputs, 1 record each
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{
		FileList:   fileList,
		OutputPath: outRoot,
		RunID:      testMultiRunID,
		OutputMode: "aggregate",
	}
	if err := runExtractMulti(shared, []string{wsA, wsB}, io.Discard, time.Now()); err != nil {
		t.Fatalf("aggregate extract-multi: %v", err)
	}

	for _, id := range []string{"summary", "line-items"} {
		recDir := filepath.Join(outRoot, id)
		// One aggregate records.jsonl per recipe, 3 lines (1 per input), no per-input files.
		lines := strings.Split(strings.TrimRight(readFileOrFail(t, filepath.Join(recDir, "records.jsonl")), "\n"), "\n")
		if len(lines) != 3 {
			t.Errorf("recipe %q records.jsonl has %d lines, want 3", id, len(lines))
		}
		entries, _ := os.ReadDir(recDir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "extract-") {
				t.Errorf("recipe %q wrote a per-input file in aggregate mode: %s", id, e.Name())
			}
		}
		// Per-recipe isolation: each recipe's stream carries ONLY its own record type.
		concat := readDirConcat(t, recDir)
		if !strings.Contains(concat, id+"_record") {
			t.Errorf("recipe %q output missing its own record type", id)
		}
		other := "line-items"
		if id == "line-items" {
			other = "summary"
		}
		if strings.Contains(readFileOrFail(t, filepath.Join(recDir, "records.jsonl")), other+"_record") {
			t.Errorf("recipe %q records leaked the other recipe's record type", id)
		}

		m := readManifest(t, filepath.Join(recDir, "manifest.json"))
		if m.OutputMode != "aggregate" {
			t.Errorf("recipe %q manifest output_mode = %q, want aggregate", id, m.OutputMode)
		}
		if len(m.AggregateOutputs) != 1 || m.AggregateOutputs[0].RecordCount != 3 || m.AggregateOutputs[0].InputOrdinalStart != 1 || m.AggregateOutputs[0].InputOrdinalEnd != 3 {
			t.Errorf("recipe %q aggregate_outputs wrong: %+v", id, m.AggregateOutputs)
		}
		if len(m.Inputs) != 3 {
			t.Errorf("recipe %q inventory len = %d, want 3", id, len(m.Inputs))
		}
		// Per-recipe input accounting: each recipe applies to all 3 inputs and the
		// counts stay isolated per recipe (3/3/0/0).
		assertInputAccounting(t, m, 3, 3, 0, 0)
		// Manifest argv records the aggregate mode.
		if !strings.Contains(strings.Join(m.CLI.ArgvSanitized, " "), "--output-mode=aggregate") {
			t.Errorf("recipe %q manifest argv missing --output-mode=aggregate", id)
		}
	}
}

func TestExtractMultiAggregate_DefaultPerInputUnchanged(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	outRoot := filepath.Join(t.TempDir(), "out")

	// Default (no OutputMode) — per-input files, no aggregate manifest fields.
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot}, []string{wsA}, io.Discard, time.Now()); err != nil {
		t.Fatalf("per-input extract-multi: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "summary", "records.jsonl")); err == nil {
		t.Error("per-input extract-multi unexpectedly wrote records.jsonl")
	}
	entries, _ := os.ReadDir(filepath.Join(outRoot, "summary"))
	hasPerInput := false
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "extract-") {
			hasPerInput = true
		}
	}
	if !hasPerInput {
		t.Error("per-input extract-multi did not write per-input files")
	}
	m := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if m.OutputMode != "" || len(m.AggregateOutputs) != 0 {
		t.Errorf("per-input extract-multi manifest should have no aggregate fields, got mode=%q", m.OutputMode)
	}
}

func TestExtractMultiAggregate_ShardRollingPerRecipe(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, _ := writeMultiInputSet(t, 5)
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{
		FileList:            fileList,
		OutputPath:          outRoot,
		RunID:               testMultiRunID,
		OutputMode:          "aggregate",
		AggregateMaxRecords: 2,
	}
	if err := runExtractMulti(shared, []string{wsA, wsB}, io.Discard, time.Now()); err != nil {
		t.Fatalf("sharded aggregate extract-multi: %v", err)
	}
	// Each recipe independently shards 5 records into 2,2,1.
	for _, id := range []string{"summary", "line-items"} {
		m := readManifest(t, filepath.Join(outRoot, id, "manifest.json"))
		if len(m.AggregateOutputs) != 3 {
			t.Fatalf("recipe %q want 3 shards, got %d", id, len(m.AggregateOutputs))
		}
		total := 0
		for i, shard := range m.AggregateOutputs {
			want := []int{2, 2, 1}[i]
			if shard.RecordCount != want {
				t.Errorf("recipe %q shard %d record_count = %d, want %d", id, i, shard.RecordCount, want)
			}
			total += shard.RecordCount
		}
		if total != 5 {
			t.Errorf("recipe %q Σ shard records = %d, want 5", id, total)
		}
	}
}

// TestExtractMultiAggregate_ContinueOnError pins the slice-4 barrier for extract-multi:
// under --continue-on-error a failed input contributes zero rows to the recipe's shared
// shard (buffered then discarded), the surviving inputs' rows are preserved, the pass
// exits with a partial-failure error, and failures.json + the manifest record the
// failed input — all within the recipe's own <output-root>/<recipe-id>/ isolation.
func TestExtractMultiAggregate_ContinueOnError(t *testing.T) {
	fileList, inputs := writeMultiInputSet(t, 3)
	// Make the middle input (inB.xml) fail mid-parse; inA + inC stay valid.
	mustWriteFile(t, inputs[1], `<root><TargetElement><Name>valB`)

	ws := writeMultiRecipeWorkspace(t, "summary")
	outRoot := filepath.Join(t.TempDir(), "out")
	err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "partial failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}

	recipeDir := filepath.Join(outRoot, "summary")
	lines := readNDJSONLines(t, filepath.Join(recipeDir, "records.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("records.jsonl has %d lines, want 2 (failed input discarded)", len(lines))
	}
	if strings.Contains(strings.Join(lines, "\n"), "valB") {
		t.Error("failed input's row (valB) leaked into the committed shard")
	}
	for i, want := range []string{"valA", "valC"} {
		if !strings.Contains(lines[i], `"name":"`+want+`"`) {
			t.Errorf("line %d = %s, want name %q", i, lines[i], want)
		}
	}

	failData, ferr := os.ReadFile(filepath.Join(recipeDir, "failures.json")) // #nosec G304 - test temp path
	if ferr != nil {
		t.Fatalf("read failures.json: %v", ferr)
	}
	if !strings.Contains(string(failData), "inB.xml") {
		t.Errorf("failures.json does not record the failed input inB.xml: %s", failData)
	}

	// The manifest records the failed input with an explicit record_count: 0 — part of
	// the aggregate input-set provenance contract (R4/R5), not omitted.
	var man provenance.Manifest
	manData, merr := os.ReadFile(filepath.Join(recipeDir, "manifest.json")) // #nosec G304 - test temp path
	if merr != nil {
		t.Fatalf("read manifest: %v", merr)
	}
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	var failed *provenance.Input
	for i := range man.Inputs {
		if strings.Contains(man.Inputs[i].Path, "inB.xml") {
			failed = &man.Inputs[i]
		}
	}
	if failed == nil {
		t.Fatal("manifest is missing the failed input inB.xml")
	}
	if failed.RecordCount == nil || *failed.RecordCount != 0 {
		t.Errorf("failed input record_count = %v, want explicit 0 (R4/R5)", failed.RecordCount)
	}
	if failed.Disposition != string(extract.DispositionFailed) {
		t.Errorf("failed input disposition = %q, want failed", failed.Disposition)
	}
}

// TestExtractMultiAggregate_TerminalOutputErrorAborts pins the Finding-1 fix: a terminal
// output/sink error from inside the shard writer must abort the run even under
// --continue-on-error (ADR-0009), never be swallowed as a recoverable input failure. The
// failure is injected INSIDE commitInput by pre-creating the shard's staging path
// (records.jsonl.partial) as a directory so openShard's OpenFile fails — directly
// exercising the terminalDispatch sentinel path (per entarch's test-quality note).
func TestExtractMultiAggregate_TerminalOutputErrorAborts(t *testing.T) {
	fileList, _ := writeMultiInputSet(t, 2)
	ws := writeMultiRecipeWorkspace(t, "summary")
	outRoot := t.TempDir()
	// <output-root>/summary/records.jsonl.partial exists as a DIRECTORY, so when the
	// writer flushes (commitInput → openShard → OpenFile of that path) it fails terminally.
	if err := os.MkdirAll(filepath.Join(outRoot, "summary", "records.jsonl.partial"), 0o750); err != nil {
		t.Fatal(err)
	}
	err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("a terminal output error under --continue-on-error must abort the run, not be swallowed")
	}
	if !strings.Contains(err.Error(), "commit aggregate output") {
		t.Errorf("error did not come through the aggregate commit/terminal-dispatch path: %v", err)
	}
}

// TestExtractMultiAggregate_FloorMissContinueOnError pins folded-in floor support (4a)
// for extract-multi: a min_occurrences miss discards the input's buffered rows and
// records it as failed under --continue-on-error, while inputs that meet the floor commit.
func TestExtractMultiAggregate_FloorMissContinueOnError(t *testing.T) {
	fileList, inputs := writeMultiInputSet(t, 3)
	// inB has zero //TargetElement → misses the floor; inA/inC each have one.
	mustWriteFile(t, inputs[1], `<root></root>`)

	ws := writeMinOccursRecipe(t, "strict", 1)
	outRoot := filepath.Join(t.TempDir(), "out")
	err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "partial failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}
	lines := readNDJSONLines(t, filepath.Join(outRoot, "strict", "records.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("records.jsonl has %d lines, want 2 (floor-missing input discarded)", len(lines))
	}
	failData, ferr := os.ReadFile(filepath.Join(outRoot, "strict", "failures.json")) // #nosec G304 - test temp path
	if ferr != nil {
		t.Fatalf("read failures.json: %v", ferr)
	}
	if !strings.Contains(string(failData), "min_occurrences") {
		t.Errorf("failures.json does not record the floor violation: %s", failData)
	}
}

func TestExtractMultiAggregate_Rejections(t *testing.T) {
	fileList, _ := writeMultiInputSet(t, 2)

	// A typo in the mode (or a cap without aggregate) must FAIL, never silently run
	// per-input — the exact high-file-count fan-out aggregate exists to avoid.
	t.Run("invalid-mode", func(t *testing.T) {
		ws := writeMultiRecipeWorkspace(t, "summary")
		outRoot := filepath.Join(t.TempDir(), "out")
		err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "bogus"}, []string{ws}, io.Discard, time.Now())
		if err == nil || !strings.Contains(err.Error(), "invalid --output-mode") {
			t.Fatalf("want invalid-mode rejection, got %v", err)
		}
	})

	t.Run("caps-without-aggregate", func(t *testing.T) {
		ws := writeMultiRecipeWorkspace(t, "summary")
		outRoot := filepath.Join(t.TempDir(), "out")
		err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, AggregateMaxRecords: 5}, []string{ws}, io.Discard, time.Now())
		if err == nil || !strings.Contains(err.Error(), "require --output-mode aggregate") {
			t.Fatalf("want caps-without-aggregate rejection, got %v", err)
		}
	})

	// A whitespace-padded "aggregate" must be treated as aggregate everywhere — it must
	// select the aggregate writer (one records.jsonl), never silently fall through to
	// per-input fan-out (extract-*.json).
	t.Run("padded-mode-runs-aggregate", func(t *testing.T) {
		ws := writeMultiRecipeWorkspace(t, "summary")
		outRoot := filepath.Join(t.TempDir(), "out")
		if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "  aggregate  "}, []string{ws}, io.Discard, time.Now()); err != nil {
			t.Fatalf("padded aggregate mode should run, got %v", err)
		}
		recipeDir := filepath.Join(outRoot, "summary")
		if _, statErr := os.Stat(filepath.Join(recipeDir, "records.jsonl")); statErr != nil {
			t.Errorf("padded aggregate mode did not produce records.jsonl (not normalized to aggregate): %v", statErr)
		}
		entries, _ := os.ReadDir(recipeDir)
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "extract-") {
				t.Errorf("padded aggregate mode wrote per-input file %s (fell through to per-input)", e.Name())
			}
		}
	})
}

// TestExtractMultiAggregate_NotApplicableCountedPerRecipe exercises the primary
// extract-multi use case for input accounting: applicability dispatch legitimately
// skips some inputs, so not_applicable must be its own count and the invariant
// applied + failed + not_applicable == total must hold. The recipe's applicability
// predicate (count(//TargetElement) > 0) is true for two inputs (applied) and false
// for the third (not_applicable), with no failures.
func TestExtractMultiAggregate_NotApplicableCountedPerRecipe(t *testing.T) {
	ws := writeApplicabilityRecipe(t, "summary", "count(//TargetElement) > 0")
	dir := t.TempDir()
	inputs := []struct {
		name, body string
	}{
		{"inA.xml", `<root><TargetElement><Name>valA</Name></TargetElement></root>`}, // predicate true -> applied
		{"inB.xml", `<root><TargetElement><Name>valB</Name></TargetElement></root>`}, // predicate true -> applied
		{"inC.xml", `<root><Other><Name>valC</Name></Other></root>`},                 // /root matches, predicate false -> not_applicable
	}
	var paths []string
	for _, in := range inputs {
		p := filepath.Join(dir, in.name)
		if err := os.WriteFile(p, []byte(in.body), 0o600); err != nil {
			t.Fatalf("write input %s: %v", in.name, err)
		}
		paths = append(paths, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(paths, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
	}

	outRoot := filepath.Join(t.TempDir(), "out")
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate"}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("aggregate extract-multi: %v", err)
	}

	m := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if len(m.Inputs) != 3 {
		t.Fatalf("inventory len = %d, want 3 (gap-free, including not_applicable)", len(m.Inputs))
	}
	// 3 total, 2 applied, 1 not_applicable, 0 failed — not_applicable is counted,
	// never silently folded into applied or failed.
	assertInputAccounting(t, m, 3, 2, 1, 0)
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
