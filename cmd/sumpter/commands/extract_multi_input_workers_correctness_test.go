package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// Slice 2 (parse-parallelism correctness): prove that the concurrent parse path
// preserves the SUM-063 aggregate-output semantics that the serial path already
// guarantees — continue-on-error attribution, fail-fast, min_occurrences floors,
// shard caps — by asserting the on-disk output at worker counts > 1 is EQUIVALENT
// to the single-worker run, plus the new concurrency-only invariants (bounded
// look-ahead, worker-panic containment).

// stableManifest extracts the deterministic, provenance-load-bearing subset of a
// recipe's manifest.json: run id, output mode, incomplete flag, the per-input
// inventory (path/disposition/record_count/record_type), per-shard aggregate
// summaries (path/record_count/sha256/ordinal span), and the record-type counts.
// Volatile fields (timestamps, the cli argv — which legitimately differs by
// --input-workers value) are deliberately excluded.
func stableManifest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read manifest %s: %v", path, err)
	}
	var m provenance.Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal manifest %s: %v", path, err)
	}
	// The per-shard SHA256 digests the fully-written bytes, which include the
	// volatile _runtime.generated_at timestamp — by design it is a tamper/integrity
	// stamp, not a cross-run determinism check (see AggregateOutput doc). Blank it so
	// the equivalence comparison rests on the record CONTENT (compared separately with
	// generated_at normalized) plus the stable record_count / ordinal spans, not a
	// timestamp-inclusive digest that legitimately differs between any two runs.
	for i := range m.AggregateOutputs {
		m.AggregateOutputs[i].SHA256 = ""
	}
	stable := struct {
		RunID            string
		OutputMode       string
		Incomplete       bool
		Inputs           []provenance.Input
		AggregateOutputs []provenance.AggregateOutput
		Counts           map[string]int
	}{
		RunID:            m.RunID,
		OutputMode:       m.OutputMode,
		Incomplete:       m.Incomplete,
		Inputs:           m.Inputs,
		AggregateOutputs: m.AggregateOutputs,
		Counts:           m.CountsByRecordType,
	}
	out, err := json.MarshalIndent(stable, "", "  ")
	if err != nil {
		t.Fatalf("marshal stable manifest: %v", err)
	}
	return string(out)
}

// aggRecipeSnapshot is the comparable output of one recipe's aggregate run: the
// concatenated record shards (shard name + normalized content, in shard order), the
// stable manifest subset, and failures.json (empty if absent).
type aggRecipeSnapshot struct {
	records  string
	manifest string
	failures string
}

func snapshotAggRecipe(t *testing.T, recipeDir string) aggRecipeSnapshot {
	t.Helper()
	entries, err := os.ReadDir(recipeDir)
	if err != nil {
		t.Fatalf("read recipe dir %s: %v", recipeDir, err)
	}
	shardNames := make([]string, 0)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "records") && strings.HasSuffix(n, ".jsonl") {
			shardNames = append(shardNames, n)
		}
	}
	sort.Strings(shardNames)
	var b strings.Builder
	for _, n := range shardNames {
		data, err := os.ReadFile(filepath.Join(recipeDir, n))
		if err != nil {
			t.Fatalf("read shard %s: %v", n, err)
		}
		// _runtime.generated_at is wall-clock per record; blank it so the comparison
		// reflects only stable record content and deterministic emit order.
		normalized := volatileGeneratedAtRE.ReplaceAllString(string(data), `"generated_at":""`)
		b.WriteString("=== " + n + " ===\n")
		b.WriteString(normalized)
	}
	failures := ""
	if data, err := os.ReadFile(filepath.Join(recipeDir, "failures.json")); err == nil {
		failures = string(data)
	}
	return aggRecipeSnapshot{
		records:  b.String(),
		manifest: stableManifest(t, filepath.Join(recipeDir, provenance.ManifestFileName)),
		failures: failures,
	}
}

// assertWorkerCountEquivalence runs scenario at worker count 1 and at each of the
// given higher counts, and asserts every recipe's record shards, stable manifest,
// and failures.json are identical to the single-worker baseline. scenario returns
// the output root for a run at the given worker count; the SAME recipe workspaces
// and input set must back every run (only the worker count and out root vary), so
// source paths/digests match and any difference is a genuine concurrency defect.
func assertWorkerCountEquivalence(t *testing.T, scenario func(workers int) string, recipeIDs []string, higher ...int) {
	t.Helper()
	baseRoot := scenario(1)
	for _, w := range higher {
		root := scenario(w)
		for _, id := range recipeIDs {
			base := snapshotAggRecipe(t, filepath.Join(baseRoot, id))
			got := snapshotAggRecipe(t, filepath.Join(root, id))
			if got.records != base.records {
				t.Errorf("workers=%d recipe %q: record shards differ from serial\n--- serial ---\n%s\n--- got ---\n%s", w, id, base.records, got.records)
			}
			if got.manifest != base.manifest {
				t.Errorf("workers=%d recipe %q: stable manifest differs from serial\n--- serial ---\n%s\n--- got ---\n%s", w, id, base.manifest, got.manifest)
			}
			if got.failures != base.failures {
				t.Errorf("workers=%d recipe %q: failures.json differs from serial\n--- serial ---\n%s\n--- got ---\n%s", w, id, base.failures, got.failures)
			}
		}
	}
}

// writeMixedInputSet writes n valid inputs (inA.xml..) plus a malformed input at the
// given 0-based ordinal position (truncated XML that fails to parse), returning the
// file-list path. The malformed file keeps its lexical position so the input ordinal
// of the failure is fixed across worker counts.
func writeMixedInputSet(t *testing.T, n, badIdx int) string {
	t.Helper()
	dir := t.TempDir()
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, "in"+string(rune('A'+i))+".xml")
		var body string
		if i == badIdx {
			body = `<root><TargetElement><Name>oops` // unterminated: parse error
		} else {
			body = `<root><TargetElement><Name>val` + string(rune('A'+i)) + `</Name></TargetElement></root>`
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write input: %v", err)
		}
		paths = append(paths, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(paths, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
	}
	return fileList
}

func TestExtractMulti_InputWorkersContinueOnErrorEquivalence(t *testing.T) {
	// Two recipes, 6 inputs with a malformed input at ordinal 3, continue-on-error.
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList := writeMixedInputSet(t, 6, 2) // inC malformed (ordinal 3)

	scenario := func(workers int) string {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:        fileList,
			OutputPath:      outRoot,
			OutputMode:      outputModeAggregate,
			RunID:           testMultiRunID,
			ContinueOnError: true,
			InputWorkers:    workers,
		}
		// continue-on-error returns a partial-failure error; that's expected, not fatal.
		_ = runExtractMulti(shared, []string{wsA, wsB}, io.Discard, time.Now())
		return outRoot
	}

	assertWorkerCountEquivalence(t, scenario, []string{"summary", "line-items"}, 2, 4, 8)

	// Spot-check the load-bearing attribution: every recipe attributes the SAME input
	// (inC) as failed, and the surviving inputs remain present and in order.
	root := scenario(4)
	for _, id := range []string{"summary", "line-items"} {
		failures := readFileOrFail(t, filepath.Join(root, id, "failures.json"))
		if !strings.Contains(failures, "inC.xml") {
			t.Errorf("recipe %q failures.json does not attribute the malformed input inC: %s", id, failures)
		}
		records := readFileOrFail(t, filepath.Join(root, id, "records.jsonl"))
		for _, good := range []string{"valA", "valB", "valD", "valE", "valF"} {
			if !strings.Contains(records, good) {
				t.Errorf("recipe %q missing surviving input record %s", id, good)
			}
		}
		if strings.Contains(records, "oops") {
			t.Errorf("recipe %q committed a record from the malformed input: %s", id, records)
		}
	}
}

func TestExtractMulti_InputWorkersFailFastEquivalence(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList := writeMixedInputSet(t, 6, 2) // inC malformed, NO continue-on-error

	for _, workers := range []int{1, 2, 4, 8} {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:     fileList,
			OutputPath:   outRoot,
			OutputMode:   outputModeAggregate,
			RunID:        testMultiRunID,
			InputWorkers: workers,
		}
		err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
		if err == nil {
			t.Errorf("workers=%d: expected fail-fast error on a malformed input, got nil", workers)
		}
		// Local aggregate is all-or-nothing: a fail-fast run must leave NO committed
		// records.jsonl (only discarded .partial staging), at every worker count.
		if _, statErr := os.Stat(filepath.Join(outRoot, "summary", "records.jsonl")); statErr == nil {
			t.Errorf("workers=%d: fail-fast left a committed records.jsonl (should be none)", workers)
		}
	}
}

func TestExtractMulti_InputWorkersFloorEquivalence(t *testing.T) {
	// Strict recipe requires >=1 //TargetElement; lax recipe has no floor. One input
	// matches the signature but lacks the element (floor miss), the rest are normal.
	strict := writeMinOccursRecipe(t, "strict", 1)
	lax := writeMultiRecipeWorkspace(t, "lax")

	dir := t.TempDir()
	paths := make([]string, 0, 5)
	for i := 0; i < 5; i++ {
		p := filepath.Join(dir, "in"+string(rune('A'+i))+".xml")
		body := `<root><TargetElement><Name>val` + string(rune('A'+i)) + `</Name></TargetElement></root>`
		if i == 2 {
			body = `<root></root>` // matches /root signature, no TargetElement -> strict floor miss
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(paths, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scenario := func(workers int) string {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:        fileList,
			OutputPath:      outRoot,
			OutputMode:      outputModeAggregate,
			RunID:           testMultiRunID,
			ContinueOnError: true,
			InputWorkers:    workers,
		}
		_ = runExtractMulti(shared, []string{strict, lax}, io.Discard, time.Now())
		return outRoot
	}
	assertWorkerCountEquivalence(t, scenario, []string{"strict", "lax"}, 2, 4, 8)
}

func TestExtractMulti_InputWorkersShardCapEquivalence(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 8) // 8 inputs, 1 record each

	t.Run("max-records", func(t *testing.T) {
		scenario := func(workers int) string {
			outRoot := filepath.Join(t.TempDir(), "out")
			shared := &multiSharedOptions{
				FileList:            fileList,
				OutputPath:          outRoot,
				OutputMode:          outputModeAggregate,
				RunID:               testMultiRunID,
				AggregateMaxRecords: 3, // roll every 3 records -> 3 shards (3,3,2)
				InputWorkers:        workers,
			}
			if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
				t.Fatalf("workers=%d: %v", workers, err)
			}
			return outRoot
		}
		assertWorkerCountEquivalence(t, scenario, []string{"summary"}, 2, 4, 8)
		// Sanity: the cap actually rolled multiple shards.
		entries, _ := os.ReadDir(filepath.Join(scenario(4), "summary"))
		shards := 0
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "records-") {
				shards++
			}
		}
		if shards < 2 {
			t.Errorf("expected the record cap to roll multiple shards, got %d", shards)
		}
	})

	t.Run("max-bytes", func(t *testing.T) {
		scenario := func(workers int) string {
			outRoot := filepath.Join(t.TempDir(), "out")
			shared := &multiSharedOptions{
				FileList:          fileList,
				OutputPath:        outRoot,
				OutputMode:        outputModeAggregate,
				RunID:             testMultiRunID,
				AggregateMaxBytes: 900, // a few records per shard
				InputWorkers:      workers,
			}
			if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
				t.Fatalf("workers=%d: %v", workers, err)
			}
			return outRoot
		}
		assertWorkerCountEquivalence(t, scenario, []string{"summary"}, 2, 4, 8)
	})
}

// TestExtractMulti_InputWorkersBackpressureBound proves the bounded look-ahead: while
// the earliest-ordinal input is blocked in parse, the number of inputs whose parse has
// STARTED never exceeds the scheduler window (2×workers) — later workers cannot run
// ahead and buffer the whole input set behind a slow head-of-line input.
func TestExtractMulti_InputWorkersBackpressureBound(t *testing.T) {
	const (
		n       = 20
		workers = 2
		window  = workers * 2
	)
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, n)
	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   filepath.Join(t.TempDir(), "out"),
		OutputMode:   outputModeAggregate,
		RunID:        testMultiRunID,
		InputWorkers: workers,
	}
	d := newMultiDispatcher(shared, io.Discard)
	realParse := d.parseFile

	var started int32
	block := make(chan struct{})
	d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
		atomic.AddInt32(&started, 1)
		if filepath.Base(p) == "inA.xml" { // ordinal 1 — the head of line
			<-block // hold the earliest input until we have measured
		}
		return realParse(p, a)
	}

	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()

	// Let the workers/feeder reach steady state behind the blocked head input.
	time.Sleep(250 * time.Millisecond)
	peak := atomic.LoadInt32(&started)
	close(block) // release the head input; the run drains to completion
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	if int(peak) > window {
		t.Errorf("started %d parses while the head input was blocked; window bound is %d (backpressure not enforced)", peak, window)
	}
	if int(peak) < workers {
		t.Errorf("started only %d parses; expected at least %d workers active", peak, workers)
	}
	if int(peak) >= n {
		t.Errorf("started %d parses with the head input blocked — the whole input set ran ahead (no backpressure)", peak)
	}
}

// TestExtractMulti_InputWorkersPanicContainment proves a worker panic on one input is
// contained as an input-level failure rather than crashing the invocation: under
// continue-on-error the panicking input is recorded failed and siblings survive; under
// fail-fast the run aborts cleanly with an error (no panic escapes, no committed output).
func TestExtractMulti_InputWorkersPanicContainment(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 5)

	parseWithPanicOn := func(d *multiDispatcher, badBase string) {
		realParse := d.parseFile
		d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
			if filepath.Base(p) == badBase {
				panic("synthetic parse panic for " + badBase)
			}
			return realParse(p, a)
		}
	}

	t.Run("continue-on-error records input failure", func(t *testing.T) {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:        fileList,
			OutputPath:      outRoot,
			OutputMode:      outputModeAggregate,
			RunID:           testMultiRunID,
			ContinueOnError: true,
			InputWorkers:    4,
		}
		d := newMultiDispatcher(shared, io.Discard)
		parseWithPanicOn(d, "inC.xml")
		// A partial-failure error is expected; the point is it does not panic/crash.
		_ = d.run([]string{ws}, time.Now())

		failures := readFileOrFail(t, filepath.Join(outRoot, "summary", "failures.json"))
		if !strings.Contains(failures, "inC.xml") {
			t.Errorf("panicking input inC not recorded as an input-level failure: %s", failures)
		}
		records := readFileOrFail(t, filepath.Join(outRoot, "summary", "records.jsonl"))
		for _, good := range []string{"valA", "valB", "valD", "valE"} {
			if !strings.Contains(records, good) {
				t.Errorf("sibling input %s lost after a worker panic: %s", good, records)
			}
		}
	})

	t.Run("fail-fast aborts cleanly", func(t *testing.T) {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:     fileList,
			OutputPath:   outRoot,
			OutputMode:   outputModeAggregate,
			RunID:        testMultiRunID,
			InputWorkers: 4,
		}
		d := newMultiDispatcher(shared, io.Discard)
		parseWithPanicOn(d, "inC.xml")
		if err := d.run([]string{ws}, time.Now()); err == nil {
			t.Error("expected a fail-fast error from a panicking input, got nil")
		}
		if _, statErr := os.Stat(filepath.Join(outRoot, "summary", "records.jsonl")); statErr == nil {
			t.Error("fail-fast after a worker panic left a committed records.jsonl")
		}
	})
}
