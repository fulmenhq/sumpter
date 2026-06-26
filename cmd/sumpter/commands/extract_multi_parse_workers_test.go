package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"
	"github.com/spf13/cobra"
)

// newExtractMultiRoot builds a cobra root carrying the inherited allow-large-files
// flag so the extract-multi command's flag validation runs as it does in production.
func newExtractMultiRoot() *cobra.Command {
	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(newRecipeRunExtractMultiCommand())
	return root
}

// TestExtractMulti_ParseWorkersValidation covers the --parse-workers surface contract:
// below 1 is a user error at the CLI, a negative value is rejected by the dispatcher,
// and >1 with a cloud destination is deferred (rejected) until the cloud slice lands.
func TestExtractMulti_ParseWorkersValidation(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)

	t.Run("cli rejects below 1", func(t *testing.T) {
		for _, n := range []string{"0", "-1"} {
			root := newExtractMultiRoot()
			root.SetArgs([]string{"extract-multi", ws, "--file-list", fileList, "--output-path", filepath.Join(t.TempDir(), "o"), "--parse-workers", n})
			if err := root.Execute(); err == nil {
				t.Errorf("--parse-workers %s: expected validation error, got nil", n)
			}
		}
	})

	t.Run("cli accepts >1", func(t *testing.T) {
		root := newExtractMultiRoot()
		out := filepath.Join(t.TempDir(), "o")
		root.SetArgs([]string{"extract-multi", ws, "--file-list", fileList, "--output-path", out, "--parse-workers", "4"})
		if err := root.Execute(); err != nil {
			t.Fatalf("--parse-workers 4: %v", err)
		}
		if entries, err := os.ReadDir(filepath.Join(out, "summary")); err != nil || len(entries) == 0 {
			t.Errorf("--parse-workers 4 produced no output (err=%v)", err)
		}
	})

	t.Run("default unset runs serially", func(t *testing.T) {
		root := newExtractMultiRoot()
		out := filepath.Join(t.TempDir(), "o")
		root.SetArgs([]string{"extract-multi", ws, "--file-list", fileList, "--output-path", out})
		if err := root.Execute(); err != nil {
			t.Fatalf("default --parse-workers: %v", err)
		}
	})

	t.Run("dispatcher rejects negative", func(t *testing.T) {
		shared := &multiSharedOptions{FileList: fileList, OutputPath: filepath.Join(t.TempDir(), "o"), ParseWorkers: -2}
		if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err == nil {
			t.Error("expected dispatcher to reject negative ParseWorkers, got nil")
		}
	})

	// Note: --parse-workers > 1 with a cloud (s3://) destination is supported as of the
	// cloud slice; the concurrency × cloud-publish invariants are covered by the
	// s3integration moto suite (extract_multi_aggregate_cloud_moto_test.go), not here.
}

// TestExtractMulti_ParseWorkersConcurrencyProof uses the injectable parseFile seam to
// prove that more than one parse worker is active when --parse-workers > 1, and that
// --parse-workers 1 never overlaps parse calls. This is a counting/structural proof,
// not a wall-clock throughput assertion (kept out of the mandatory gate per the brief).
func TestExtractMulti_ParseWorkersConcurrencyProof(t *testing.T) {
	maxConcurrentParses := func(workers, n int) int {
		ws := writeMultiRecipeWorkspace(t, "summary")
		fileList, _ := writeMultiInputSet(t, n)
		shared := &multiSharedOptions{FileList: fileList, OutputPath: filepath.Join(t.TempDir(), "o"), ParseWorkers: workers, RunID: testMultiRunID}
		d := newMultiDispatcher(shared, io.Discard)
		realParse := d.parseFile

		var mu sync.Mutex
		active, maxActive := 0, 0
		barrier := make(chan struct{})
		var once sync.Once
		d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			cur := active
			mu.Unlock()
			if cur >= 2 {
				// Two parses are concurrently in-flight: release everyone.
				once.Do(func() { close(barrier) })
			}
			select {
			case <-barrier:
				// concurrent path: a second worker reached the seam.
			case <-time.After(300 * time.Millisecond):
				// serial path: a second worker never arrives; proceed one at a time.
			}
			mu.Lock()
			active--
			mu.Unlock()
			return realParse(p, a)
		}
		if err := d.run([]string{ws}, time.Now()); err != nil {
			t.Fatalf("workers=%d run: %v", workers, err)
		}
		return maxActive
	}

	if got := maxConcurrentParses(1, 3); got != 1 {
		t.Errorf("--parse-workers 1: max concurrent parses = %d, want exactly 1 (serial)", got)
	}
	if got := maxConcurrentParses(2, 4); got < 2 {
		t.Errorf("--parse-workers 2: max concurrent parses = %d, want >= 2", got)
	}
}

// TestExtractMulti_ParseWorkersDeterministic proves the load-bearing contract: aggregate
// records.jsonl is byte-identical across worker counts 1/2/4/8 under a fixed run id, even
// when parse completion is forced out of scheduling order (later inputs parse faster).
func TestExtractMulti_ParseWorkersDeterministic(t *testing.T) {
	const n = 8

	// One shared recipe workspace + input set across every worker count: source_file
	// (an absolute input path) is part of the record envelope, so the inputs must be
	// byte-for-byte the same files for the cross-worker-count comparison to be valid.
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, n)

	// runAt parses the shared input set at the given worker count, injecting a per-input
	// delay that is LONGER for earlier inputs so they complete out of order and the
	// ordered drain's reorder path is actually exercised.
	runAt := func(workers int) string {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:     fileList,
			OutputPath:   outRoot,
			OutputMode:   outputModeAggregate,
			RunID:        testMultiRunID,
			ParseWorkers: workers,
		}
		d := newMultiDispatcher(shared, io.Discard)
		realParse := d.parseFile
		d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
			if workers > 1 {
				// Reverse the completion order relative to the (alphabetical) schedule
				// order: 'inA' sleeps longest, later inputs finish first.
				base := filepath.Base(p) // inA.xml, inB.xml, ...
				if len(base) >= 3 {
					lag := time.Duration('Z'-base[2]) * time.Millisecond
					time.Sleep(lag)
				}
			}
			return realParse(p, a)
		}
		if err := d.run([]string{ws}, time.Now()); err != nil {
			t.Fatalf("workers=%d run: %v", workers, err)
		}
		data, err := os.ReadFile(filepath.Join(outRoot, "summary", "records.jsonl"))
		if err != nil {
			t.Fatalf("workers=%d read records: %v", workers, err)
		}
		// _runtime.generated_at is wall-clock per record; blank it so the comparison
		// reflects only the stable record content and deterministic emit order.
		return volatileGeneratedAtRE.ReplaceAllString(string(data), `"generated_at":""`)
	}

	want := runAt(1)
	if strings.TrimSpace(want) == "" {
		t.Fatal("serial run produced no records")
	}
	// The serial baseline must already be in input order valA..valH.
	for i := 0; i < n; i++ {
		if !strings.Contains(want, "val"+string(rune('A'+i))) {
			t.Fatalf("serial baseline missing val%c", 'A'+i)
		}
	}
	if idxA, idxH := strings.Index(want, "valA"), strings.Index(want, "valH"); idxA < 0 || idxH < 0 || idxA > idxH {
		t.Fatalf("serial baseline not in input order (valA at %d, valH at %d)", idxA, idxH)
	}

	for _, workers := range []int{2, 4, 8} {
		if got := runAt(workers); got != want {
			t.Errorf("--parse-workers %d: aggregate records.jsonl differs from serial:\n serial: %s\n got:    %s", workers, want, got)
		}
	}
}

// TestExtractMulti_ParseWorkersParseOnceFanToM proves SUM-057 survives concurrency: each
// input is parsed exactly once (not once per recipe) even across workers, and each
// recipe's aggregate output drains in input ordinal order.
func TestExtractMulti_ParseWorkersParseOnceFanToM(t *testing.T) {
	const (
		inputs  = 4
		workers = 4
	)
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, inputPaths := writeMultiInputSet(t, inputs)
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   outRoot,
		OutputMode:   outputModeAggregate,
		RunID:        testMultiRunID,
		ParseWorkers: workers,
	}
	d := newMultiDispatcher(shared, io.Discard)
	realParse := d.parseFile
	var mu sync.Mutex
	parseCounts := make(map[string]int)
	d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
		mu.Lock()
		parseCounts[p]++
		mu.Unlock()
		return realParse(p, a)
	}

	if err := d.run([]string{wsA, wsB}, time.Now()); err != nil {
		t.Fatalf("dispatcher run: %v", err)
	}

	// Parse-once fan-to-M: exactly one parse per distinct input, regardless of the two
	// recipes consuming it and the four workers.
	if len(parseCounts) != inputs {
		t.Fatalf("parsed %d distinct files, want %d", len(parseCounts), inputs)
	}
	total := 0
	for f, c := range parseCounts {
		if c != 1 {
			t.Errorf("file %s parsed %d times, want exactly 1", f, c)
		}
		total += c
	}
	if total != inputs {
		t.Errorf("total parses = %d, want %d (one per input, not input×recipe)", total, inputs)
	}
	_ = inputPaths

	// Each recipe's records drain in input order valA..valD despite concurrent parsing.
	for _, id := range []string{"summary", "line-items"} {
		data, err := os.ReadFile(filepath.Join(outRoot, id, "records.jsonl"))
		if err != nil {
			t.Fatalf("recipe %q read records: %v", id, err)
		}
		body := string(data)
		prev := -1
		for i := 0; i < inputs; i++ {
			at := strings.Index(body, fmt.Sprintf("val%c", 'A'+i))
			if at < 0 {
				t.Errorf("recipe %q output missing val%c", id, 'A'+i)
				continue
			}
			if at <= prev {
				t.Errorf("recipe %q records out of input order at val%c (pos %d <= prev %d)", id, 'A'+i, at, prev)
			}
			prev = at
		}
	}
}
