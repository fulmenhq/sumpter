package commands

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

func TestAggregateWriter_CloudRejectsOversizedRecord(t *testing.T) {
	// A single cloud record larger than the byte cap can never be published (each
	// shard is one object under the single-PUT limit), so OnRecord must reject it
	// before any staging work (R7), not discover it at Publish.
	w := &aggregateWriter{cloud: true, maxBytes: 5, sharded: true, opts: &ExtractOptions{}}
	rec := extract.NewEmittedRecord(map[string]interface{}{"name": "much-larger-than-five-bytes"})
	err := w.OnRecord(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "single record cannot fit") {
		t.Fatalf("want oversized cloud-record rejection from OnRecord, got %v", err)
	}
	if w.open || w.shardOrd != 0 {
		t.Errorf("oversized cloud record opened a shard (open=%v shardOrd=%d); must reject before any staging", w.open, w.shardOrd)
	}
}

// writeAggregateWorkspace builds a recipe workspace with n synthetic inputs, each
// producing exactly one record (//item), and a JSON-output recipe. Input order is
// the listed defaults.input.files order (the resolved aggregate ordinal order).
func writeAggregateWorkspace(t *testing.T, n int) string {
	t.Helper()
	ws := createWorkingTempDir(t)
	for _, d := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	var fileLines strings.Builder
	for i := 1; i <= n; i++ {
		name := "in-" + string(rune('a'+i-1)) + ".xml"
		mustWriteFile(t, filepath.Join(ws, "testdata", name),
			`<root><item><name>val`+string(rune('a'+i-1))+`</name></item></root>`)
		fileLines.WriteString("      - testdata/" + name + "\n")
	}
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: aggregate_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
`+fileLines.String()+`  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
`)
	mustWriteFile(t, filepath.Join(ws, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: agg_record
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
	return ws
}

func runAggregateRecipe(t *testing.T, ws, outDir string, opts *recipeRunExtractOptions) error {
	t.Helper()
	initExtractManifestTestLogger(t)
	if opts == nil {
		opts = &recipeRunExtractOptions{}
	}
	opts.ManifestPath = "recipe.yaml"
	opts.OutputPath = outDir
	return executeExtractRecipe(recipeRunExtractTestCommand(), ws, opts)
}

func readNDJSONLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test temp path
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	out := []string{}
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

var aggRuntimeRE = regexp.MustCompile(`"_runtime":\{[^}]*\}`)

func TestAggregateOutput_SingleFileHappyPath(t *testing.T) {
	ws := writeAggregateWorkspace(t, 3)
	out := filepath.Join(t.TempDir(), "agg")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate"}); err != nil {
		t.Fatalf("aggregate run: %v", err)
	}

	// One records.jsonl, one line per single-record input, in listed order.
	lines := readNDJSONLines(t, filepath.Join(out, "records.jsonl"))
	if len(lines) != 3 {
		t.Fatalf("records.jsonl has %d lines, want 3", len(lines))
	}
	for i, want := range []string{"vala", "valb", "valc"} {
		if !strings.Contains(lines[i], `"name":"`+want+`"`) {
			t.Errorf("line %d = %s, want name %q (deterministic input order)", i, lines[i], want)
		}
	}

	// No per-input files in aggregate mode.
	if entries, _ := os.ReadDir(out); len(entries) > 0 {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "extract-") {
				t.Errorf("aggregate mode wrote a per-input file: %s", e.Name())
			}
		}
	}

	m := readManifest(t, filepath.Join(out, "manifest.json"))
	if m.OutputMode != "aggregate" {
		t.Errorf("manifest output_mode = %q, want aggregate", m.OutputMode)
	}
	if len(m.AggregateOutputs) != 1 {
		t.Fatalf("aggregate_outputs len = %d, want 1", len(m.AggregateOutputs))
	}
	shard := m.AggregateOutputs[0]
	if shard.Path != "records.jsonl" || shard.RecordCount != 3 || shard.InputOrdinalStart != 1 || shard.InputOrdinalEnd != 3 {
		t.Errorf("shard summary wrong: %+v", shard)
	}
	if !strings.HasPrefix(shard.SHA256, "sha256:") {
		t.Errorf("shard sha256 missing/format: %q", shard.SHA256)
	}
	// Inventory: every input present once, with record_count + disposition.
	if len(m.Inputs) != 3 {
		t.Fatalf("inventory len = %d, want 3", len(m.Inputs))
	}
	total := 0
	for _, in := range m.Inputs {
		if in.RecordCount == nil || *in.RecordCount != 1 {
			t.Errorf("input %s record_count = %v, want 1", in.Path, in.RecordCount)
			continue
		}
		if in.Disposition == "" {
			t.Errorf("input %s missing disposition", in.Path)
		}
		total += *in.RecordCount
	}
	// Global completeness invariant: Σ shard record_count == Σ input record_count.
	// (Here there is one shard, so it equals this shard's count; see the boundary
	// test for the multi-shard case where an input straddles a roll.)
	shardTotal := 0
	for _, s := range m.AggregateOutputs {
		shardTotal += s.RecordCount
	}
	if total != shardTotal {
		t.Errorf("Σ shard record_count %d != Σ input record_count %d", shardTotal, total)
	}
}

func TestAggregateOutput_DefaultPerInputUnchanged(t *testing.T) {
	ws := writeAggregateWorkspace(t, 2)
	out := filepath.Join(t.TempDir(), "perinput")
	// Default mode (OutputMode empty) — per-input files, no aggregate manifest fields.
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{}); err != nil {
		t.Fatalf("per-input run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "records.jsonl")); err == nil {
		t.Error("per-input mode unexpectedly wrote records.jsonl")
	}
	if _, err := os.Stat(filepath.Join(out, "extract-in-a.xml.json")); err != nil {
		t.Errorf("per-input mode did not write the expected per-input file: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	if m.OutputMode != "" || len(m.AggregateOutputs) != 0 {
		t.Errorf("per-input manifest should have no aggregate fields, got mode=%q outputs=%d", m.OutputMode, len(m.AggregateOutputs))
	}
	// Byte-stability: per-input inputs[] must not gain a record_count (the tri-state
	// pointer stays nil → omitted), and no aggregate-only top-level fields appear.
	// (outputs[].record_count is existing per-input behavior and is unaffected.)
	for _, in := range m.Inputs {
		if in.RecordCount != nil {
			t.Errorf("per-input input %s gained a record_count (%d); should be omitted", in.Path, *in.RecordCount)
		}
	}
	raw, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatalf("read raw manifest: %v", err)
	}
	if strings.Contains(string(raw), "output_mode") || strings.Contains(string(raw), "aggregate_outputs") {
		t.Errorf("per-input manifest leaked an aggregate-only top-level field:\n%s", raw)
	}
}

func TestAggregateOutput_ShardRolling(t *testing.T) {
	ws := writeAggregateWorkspace(t, 5)
	out := filepath.Join(t.TempDir(), "shards")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", AggregateMaxRecords: 2}); err != nil {
		t.Fatalf("sharded run: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	// 5 records, cap 2 -> shards of 2,2,1.
	if len(m.AggregateOutputs) != 3 {
		t.Fatalf("want 3 shards, got %d: %+v", len(m.AggregateOutputs), m.AggregateOutputs)
	}
	wantCounts := []int{2, 2, 1}
	wantNames := []string{"records-00001.jsonl", "records-00002.jsonl", "records-00003.jsonl"}
	totalShardRecords := 0
	for i, shard := range m.AggregateOutputs {
		if shard.Path != wantNames[i] {
			t.Errorf("shard %d path = %q, want %q (lexical order)", i, shard.Path, wantNames[i])
		}
		if shard.RecordCount != wantCounts[i] {
			t.Errorf("shard %d record_count = %d, want %d", i, shard.RecordCount, wantCounts[i])
		}
		if lines := readNDJSONLines(t, filepath.Join(out, shard.Path)); len(lines) != wantCounts[i] {
			t.Errorf("shard %d file has %d lines, want %d", i, len(lines), wantCounts[i])
		}
		totalShardRecords += shard.RecordCount
	}
	if totalShardRecords != 5 {
		t.Errorf("total shard records = %d, want 5", totalShardRecords)
	}
	// No leftover .partial staging files.
	entries, _ := os.ReadDir(out)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".partial") {
			t.Errorf("leftover staging file: %s", e.Name())
		}
	}
}

// TestAggregateOutput_InputStraddlesShardBoundary is the secrev-F1 repro: a single
// input whose records straddle a record cap appears in two adjacent shards' ordinal
// spans. Per-shard ordinal ranges are coverage, not a partition; only the GLOBAL
// invariant holds (Σ shard record_count == the input's record_count). Records, byte
// integrity, and ordering remain correct.
func TestAggregateOutput_InputStraddlesShardBoundary(t *testing.T) {
	ws := writeAggregateWorkspace(t, 1)
	// Rewrite the single input to hold three records.
	mustWriteFile(t, filepath.Join(ws, "testdata", "in-a.xml"),
		`<root><item><name>r1</name></item><item><name>r2</name></item><item><name>r3</name></item></root>`)
	out := filepath.Join(t.TempDir(), "straddle")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", AggregateMaxRecords: 2}); err != nil {
		t.Fatalf("run: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))

	if len(m.AggregateOutputs) != 2 {
		t.Fatalf("3 records cap 2 -> want 2 shards, got %d", len(m.AggregateOutputs))
	}
	// Both shards' ordinal spans are [1,1] — the one input straddles the boundary.
	for i, s := range m.AggregateOutputs {
		if s.InputOrdinalStart != 1 || s.InputOrdinalEnd != 1 {
			t.Errorf("shard %d ordinal span = [%d,%d], want [1,1] (single straddling input)", i, s.InputOrdinalStart, s.InputOrdinalEnd)
		}
	}
	// Shard counts split 2 + 1; the per-shard sum-of-inputs check would be wrong here.
	if m.AggregateOutputs[0].RecordCount != 2 || m.AggregateOutputs[1].RecordCount != 1 {
		t.Errorf("shard counts = %d,%d, want 2,1", m.AggregateOutputs[0].RecordCount, m.AggregateOutputs[1].RecordCount)
	}
	// Global invariant: Σ shard == the single input's record_count (3) == 3.
	shardTotal := m.AggregateOutputs[0].RecordCount + m.AggregateOutputs[1].RecordCount
	if len(m.Inputs) != 1 || m.Inputs[0].RecordCount == nil || *m.Inputs[0].RecordCount != 3 {
		t.Fatalf("want one input with record_count 3, got %+v", m.Inputs)
	}
	if shardTotal != *m.Inputs[0].RecordCount {
		t.Errorf("Σ shard record_count %d != input record_count %d (global invariant)", shardTotal, *m.Inputs[0].RecordCount)
	}
}

func TestAggregateOutput_PayloadDeterminismStripRuntime(t *testing.T) {
	ws := writeAggregateWorkspace(t, 4)
	read := func() string {
		out := filepath.Join(t.TempDir(), "det")
		if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate"}); err != nil {
			t.Fatalf("run: %v", err)
		}
		data, err := os.ReadFile(filepath.Join(out, "records.jsonl"))
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		// Strip volatile _runtime (run_id/timestamps) before comparing payload.
		return aggRuntimeRE.ReplaceAllString(string(data), `"_runtime":{}`)
	}
	if a, b := read(), read(); a != b {
		t.Errorf("aggregate payload not deterministic across runs:\n A: %s\n B: %s", a, b)
	}
}

func TestAggregateOutput_PlanTimeRejections(t *testing.T) {
	cases := []struct {
		name string
		opts *recipeRunExtractOptions
		want string
	}{
		{"no-manifest", &recipeRunExtractOptions{OutputMode: "aggregate", NoManifest: true}, "cannot be combined with --no-manifest"},
		{"invalid-mode", &recipeRunExtractOptions{OutputMode: "bogus"}, "invalid --output-mode"},
		{"caps-without-aggregate", &recipeRunExtractOptions{AggregateMaxRecords: 5}, "require --output-mode aggregate"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws := writeAggregateWorkspace(t, 1)
			out := filepath.Join(t.TempDir(), "rej")
			err := runAggregateRecipe(t, ws, out, tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

// writeFlooredAggregateWorkspace builds an aggregate workspace whose recipe declares a
// min_occurrences floor on //item, with the middle input emptied so it misses the floor.
func writeFlooredAggregateWorkspace(t *testing.T) string {
	t.Helper()
	ws := writeAggregateWorkspace(t, 3)
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: agg_record
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
	// in-b.xml has zero //item → misses the floor; in-a/in-c each have one.
	mustWriteFile(t, filepath.Join(ws, "testdata", "in-b.xml"), `<root></root>`)
	return ws
}

// TestAggregateOutput_FloorMissContinueOnError pins folded-in floor support (4a): a
// min_occurrences miss is enforced per input at completion and routed through the same
// barrier — under --continue-on-error the floor-missing input is discarded (zero rows in
// the shard) and recorded as failed, while inputs that meet the floor are committed.
func TestAggregateOutput_FloorMissContinueOnError(t *testing.T) {
	ws := writeFlooredAggregateWorkspace(t)
	out := filepath.Join(t.TempDir(), "floor-coe")
	err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", ContinueOnError: true})
	if err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}
	lines := readNDJSONLines(t, filepath.Join(out, "records.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("records.jsonl has %d lines, want 2 (floor-missing input discarded)", len(lines))
	}
	for i, want := range []string{"vala", "valc"} {
		if !strings.Contains(lines[i], `"name":"`+want+`"`) {
			t.Errorf("line %d = %s, want name %q", i, lines[i], want)
		}
	}
	failData, ferr := os.ReadFile(filepath.Join(out, "failures.json")) // #nosec G304 - test temp path
	if ferr != nil {
		t.Fatalf("read failures.json: %v", ferr)
	}
	if !strings.Contains(string(failData), "min_occurrences") {
		t.Errorf("failures.json does not record the floor violation: %s", failData)
	}
}

// TestAggregateOutput_FloorMissFailFast pins floor enforcement in fail-fast mode: a
// floor miss aborts the whole run, leaving no committed output.
func TestAggregateOutput_FloorMissFailFast(t *testing.T) {
	ws := writeFlooredAggregateWorkspace(t)
	out := filepath.Join(t.TempDir(), "floor-ff")
	err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate"})
	if err == nil || !strings.Contains(err.Error(), "min_occurrences") {
		t.Fatalf("want min_occurrences failure, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "records.jsonl")); statErr == nil {
		t.Error("fail-fast floor miss left committed output")
	}
}

// TestAggregateOutput_RejectsCloudContinueOnError pins the one remaining
// continue-on-error guard: cloud shards publish incrementally, so continue-on-error
// interacts with R8 partial-publish and is held back a slice. Local continue-on-error
// is supported (see TestAggregateOutput_ContinueOnErrorDiscardsFailedInput).
func TestAggregateOutput_RejectsCloudContinueOnError(t *testing.T) {
	opts := &ExtractOptions{
		OutputMode:        outputModeAggregate,
		OutputPath:        "s3://bucket/prefix/",
		AggregateMaxBytes: 1 << 20,
		ContinueOnError:   true,
	}
	err := validateAggregateOptions(opts, []string{recipesmanifest.OutputFormatJSON})
	if err == nil || !strings.Contains(err.Error(), "--continue-on-error") {
		t.Fatalf("want cloud continue-on-error rejection, got %v", err)
	}
}

// TestAggregateOutput_ContinueOnErrorDiscardsFailedInput is the headline slice-4
// safety test: under --continue-on-error a failed input contributes ZERO rows to the
// shared shard (its records are buffered and discarded, never committed), while the
// surrounding successful inputs' rows are preserved in deterministic order, the run
// exits with a partial-failure error, and failures.json + the manifest record the
// failed input.
func TestAggregateOutput_ContinueOnErrorDiscardsFailedInput(t *testing.T) {
	ws := writeAggregateWorkspace(t, 3)
	// Make the middle input (in-b.xml) fail mid-parse; a + c stay valid.
	mustWriteFile(t, filepath.Join(ws, "testdata", "in-b.xml"), `<root><item><name>valb`)

	out := filepath.Join(t.TempDir(), "coe")
	err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", ContinueOnError: true})
	if err == nil || !strings.Contains(err.Error(), "partial extraction failure") {
		t.Fatalf("want partial-failure error, got %v", err)
	}

	// The shared shard holds ONLY the two successful inputs' rows, in listed order —
	// the failed input's rows were discarded, never committed.
	lines := readNDJSONLines(t, filepath.Join(out, "records.jsonl"))
	if len(lines) != 2 {
		t.Fatalf("records.jsonl has %d lines, want 2 (failed input discarded)", len(lines))
	}
	for i, want := range []string{"vala", "valc"} {
		if !strings.Contains(lines[i], `"name":"`+want+`"`) {
			t.Errorf("line %d = %s, want name %q", i, lines[i], want)
		}
	}
	if strings.Contains(strings.Join(lines, "\n"), "valb") {
		t.Error("failed input's row (valb) leaked into the committed shard")
	}

	// failures.json records the failed input.
	failData, ferr := os.ReadFile(filepath.Join(out, "failures.json")) // #nosec G304 - test temp path
	if ferr != nil {
		t.Fatalf("read failures.json: %v", ferr)
	}
	if !strings.Contains(string(failData), "in-b.xml") {
		t.Errorf("failures.json does not record the failed input in-b.xml: %s", failData)
	}

	// The manifest records the failed input with a failed disposition, and shard
	// record counts sum to the successful rows only (R4).
	var man provenance.Manifest
	manData, merr := os.ReadFile(filepath.Join(out, "manifest.json")) // #nosec G304 - test temp path
	if merr != nil {
		t.Fatalf("read manifest: %v", merr)
	}
	if err := json.Unmarshal(manData, &man); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	shardTotal := 0
	for _, s := range man.AggregateOutputs {
		shardTotal += s.RecordCount
	}
	if shardTotal != 2 {
		t.Errorf("Σ shard record_count = %d, want 2 (only successful inputs)", shardTotal)
	}
	failed := 0
	for _, in := range man.Inputs {
		if in.Disposition == string(extract.DispositionFailed) {
			failed++
		}
	}
	if failed != 1 {
		t.Errorf("manifest records %d failed inputs, want 1", failed)
	}
}

// TestAggregateOutput_ContinueOnErrorHappyPathTransparent proves the per-input spool
// barrier is transparent when nothing fails: a --continue-on-error run with all-valid
// inputs produces the same rows in the same order as the fail-fast path, writes no
// failures.json, and exits cleanly (no partial-failure error).
func TestAggregateOutput_ContinueOnErrorHappyPathTransparent(t *testing.T) {
	ws := writeAggregateWorkspace(t, 3)
	out := filepath.Join(t.TempDir(), "coe-ok")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", ContinueOnError: true}); err != nil {
		t.Fatalf("continue-on-error happy path should exit cleanly, got %v", err)
	}
	lines := readNDJSONLines(t, filepath.Join(out, "records.jsonl"))
	if len(lines) != 3 {
		t.Fatalf("records.jsonl has %d lines, want 3", len(lines))
	}
	for i, want := range []string{"vala", "valb", "valc"} {
		if !strings.Contains(lines[i], `"name":"`+want+`"`) {
			t.Errorf("line %d = %s, want name %q (buffering must preserve order)", i, lines[i], want)
		}
	}
	if _, statErr := os.Stat(filepath.Join(out, "failures.json")); statErr == nil {
		t.Error("a clean continue-on-error run wrote failures.json")
	}
}

func TestAggregateOutput_ManifestArgvRecordsMode(t *testing.T) {
	ws := writeAggregateWorkspace(t, 3)
	out := filepath.Join(t.TempDir(), "argv")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", AggregateMaxRecords: 2}); err != nil {
		t.Fatalf("run: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	argv := strings.Join(m.CLI.ArgvSanitized, " ")
	if !strings.Contains(argv, "--output-mode=aggregate") {
		t.Errorf("manifest argv missing --output-mode=aggregate: %s", argv)
	}
	if !strings.Contains(argv, "--aggregate-max-records=2") {
		t.Errorf("manifest argv missing --aggregate-max-records=2: %s", argv)
	}
}

func TestAggregateOutput_ByteCapRollsPerRecord(t *testing.T) {
	ws := writeAggregateWorkspace(t, 3)
	out := filepath.Join(t.TempDir(), "bytecap")
	// A tiny byte cap: each single-record line exceeds it, so every record lands in
	// its own shard (one-record-over-cap rolls; no record is dropped or split).
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate", AggregateMaxBytes: 10}); err != nil {
		t.Fatalf("byte-cap run: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	if len(m.AggregateOutputs) != 3 {
		t.Fatalf("want 3 shards (one record each over the tiny cap), got %d", len(m.AggregateOutputs))
	}
	for i, shard := range m.AggregateOutputs {
		if shard.RecordCount != 1 {
			t.Errorf("shard %d record_count = %d, want 1", i, shard.RecordCount)
		}
		if lines := readNDJSONLines(t, filepath.Join(out, shard.Path)); len(lines) != 1 {
			t.Errorf("shard %d file has %d lines, want 1", i, len(lines))
		}
	}
}

func TestAggregateOutput_CloudPlanTimeCap(t *testing.T) {
	cloudOpts := func(maxBytes int64) *ExtractOptions {
		return &ExtractOptions{OutputMode: "aggregate", OutputPath: "s3://bucket/prefix/", AggregateMaxBytes: maxBytes}
	}
	// R7: cloud aggregate without a byte cap is rejected at plan time (each shard is
	// one object subject to the single-PUT limit).
	if err := validateAggregateOptions(cloudOpts(0), []string{"json"}); err == nil || !strings.Contains(err.Error(), "requires --aggregate-max-bytes") {
		t.Fatalf("uncapped cloud aggregate must be rejected, got %v", err)
	}
	// R7: a cap above the 5 GiB single-PUT limit is rejected.
	if err := validateAggregateOptions(cloudOpts(uriio.MaxSinglePutBytes+1), []string{"json"}); err == nil || !strings.Contains(err.Error(), "exceeds the cloud single-PUT limit") {
		t.Fatalf(">5GiB cloud aggregate must be rejected, got %v", err)
	}
	// A cap at or below the limit passes the cloud check.
	if err := validateAggregateOptions(cloudOpts(uriio.MaxSinglePutBytes), []string{"json"}); err != nil {
		t.Fatalf("cloud aggregate at the limit should pass plan-time validation, got %v", err)
	}
	// Local aggregate has no mandatory cap (uncapped local is allowed).
	if err := validateAggregateOptions(&ExtractOptions{OutputMode: "aggregate", OutputPath: t.TempDir()}, []string{"json"}); err != nil {
		t.Fatalf("uncapped local aggregate should pass, got %v", err)
	}
}

func TestAggregateOutput_ZeroRecord(t *testing.T) {
	ws := writeAggregateWorkspace(t, 2)
	// Rewrite inputs so the signature matches (/root) but there is no //item -> zero records.
	for _, name := range []string{"in-a.xml", "in-b.xml"} {
		mustWriteFile(t, filepath.Join(ws, "testdata", name), `<root></root>`)
	}
	out := filepath.Join(t.TempDir(), "zero")
	if err := runAggregateRecipe(t, ws, out, &recipeRunExtractOptions{OutputMode: "aggregate"}); err != nil {
		t.Fatalf("zero-record run: %v", err)
	}
	// One empty records.jsonl covering the input set.
	data, err := os.ReadFile(filepath.Join(out, "records.jsonl"))
	if err != nil {
		t.Fatalf("read records.jsonl: %v", err)
	}
	if strings.TrimSpace(string(data)) != "" {
		t.Errorf("zero-record aggregate should be empty, got: %q", data)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	if len(m.AggregateOutputs) != 1 || m.AggregateOutputs[0].RecordCount != 0 {
		t.Errorf("want one zero-record shard, got %+v", m.AggregateOutputs)
	}
	if len(m.Inputs) != 2 {
		t.Errorf("inventory should still record both zero-record inputs, got %d", len(m.Inputs))
	}
	// Per-input record_count must be an EXPLICIT 0 in aggregate mode (not dropped by
	// omitempty) for zero-record inputs — assert on the raw JSON.
	for _, in := range m.Inputs {
		if in.RecordCount == nil || *in.RecordCount != 0 {
			t.Errorf("zero-record input %s record_count = %v, want explicit 0", in.Path, in.RecordCount)
		}
	}
	rawManifest, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil {
		t.Fatalf("read raw manifest: %v", err)
	}
	if !strings.Contains(string(rawManifest), `"record_count": 0`) {
		t.Errorf("zero-record aggregate manifest must serialize explicit \"record_count\": 0:\n%s", rawManifest)
	}
}
