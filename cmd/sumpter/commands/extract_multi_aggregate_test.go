package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestExtractMultiAggregate_Rejections(t *testing.T) {
	fileList, _ := writeMultiInputSet(t, 2)

	t.Run("continue-on-error", func(t *testing.T) {
		ws := writeMultiRecipeWorkspace(t, "summary")
		outRoot := filepath.Join(t.TempDir(), "out")
		err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
		if err == nil || !strings.Contains(err.Error(), "--continue-on-error") {
			t.Fatalf("want continue-on-error rejection, got %v", err)
		}
	})

	t.Run("min-occurrences-floor", func(t *testing.T) {
		ws := writeMinOccursRecipe(t, "strict", 1)
		outRoot := filepath.Join(t.TempDir(), "out")
		err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate"}, []string{ws}, io.Discard, time.Now())
		if err == nil || !strings.Contains(err.Error(), "min_occurrences") {
			t.Fatalf("want min_occurrences rejection, got %v", err)
		}
		// Rejected before any output session — no records written.
		if _, statErr := os.Stat(filepath.Join(outRoot, "strict", "records.jsonl")); statErr == nil {
			t.Error("floored aggregate recipe wrote output despite rejection")
		}
	})

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
	// NOT run the writer while skipping the aggregate-only floor rejection.
	t.Run("padded-mode-still-rejects-floor", func(t *testing.T) {
		ws := writeMinOccursRecipe(t, "strict", 1)
		outRoot := filepath.Join(t.TempDir(), "out")
		err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "  aggregate  "}, []string{ws}, io.Discard, time.Now())
		if err == nil || !strings.Contains(err.Error(), "min_occurrences") {
			t.Fatalf("padded aggregate mode must still reject floors, got %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(outRoot, "strict", "records.jsonl")); statErr == nil {
			t.Error("padded aggregate + floor wrote output despite rejection")
		}
	})
}

func readFileOrFail(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
