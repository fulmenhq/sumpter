package commands

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// SUM-068 slice 3a: the per-input build/commit split must preserve the output-file
// lifecycle for SUCCESSFUL ZERO-RECORD inputs. Pre-split, ProcessParsedDocument called the
// durable target's OnFileBoundary, which opened the output before Close()/Commit(); the
// split routes extraction through a worker-local collecting sink, so the committer now
// replays that boundary onto the real target before Close(). Without that, Commit() would
// ensureOpen() AFTER Close() and rename a still-open file (an FD leak; non-portable).
//
// These tests pin that a zero-record-but-successful per-input output is committed as a real
// (empty) file across both zero-record success shapes. (The leaked-FD symptom itself is not
// portably observable — POSIX still renames the open file — so the lifecycle correctness is
// the OnFileBoundary-before-Close fix; these guard the functional path end to end.)
func TestExtractMulti_PerInputZeroRecordOutputCommitted(t *testing.T) {
	t.Run("signature mismatch (zero records, no failure)", func(t *testing.T) {
		ws := writeSignatureMismatchRecipe(t, "mismatch")
		fileList, inputs := writeMultiInputSet(t, 1)
		outRoot := filepath.Join(t.TempDir(), "out")
		if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot}, []string{ws}, io.Discard, time.Now()); err != nil {
			t.Fatalf("runExtractMulti: %v", err)
		}
		out := filepath.Join(outRoot, "mismatch", "extract-"+filepath.Base(inputs[0])+".json")
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatalf("zero-record (signature-mismatch) output file not committed: %v", err)
		}
		if fi.Size() != 0 {
			t.Errorf("expected an empty zero-record output file, got %d bytes", fi.Size())
		}
		if _, err := os.Stat(filepath.Join(outRoot, "mismatch", "failures.json")); err == nil {
			t.Error("signature-mismatch (zero records) must not record a failure")
		}
	})

	t.Run("applied zero-record", func(t *testing.T) {
		ws := writeMultiRecipeWorkspace(t, "lax") // matches /root signature, no min_occurrences floor
		dir := t.TempDir()
		in := filepath.Join(dir, "empty.xml")
		// Matches the /root signature but has no TargetElement -> applied, zero records.
		if err := os.WriteFile(in, []byte(`<root></root>`), 0o600); err != nil {
			t.Fatal(err)
		}
		fileList := filepath.Join(dir, "files.txt")
		if err := os.WriteFile(fileList, []byte(in+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		outRoot := filepath.Join(t.TempDir(), "out")
		if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot}, []string{ws}, io.Discard, time.Now()); err != nil {
			t.Fatalf("runExtractMulti: %v", err)
		}
		out := filepath.Join(outRoot, "lax", "extract-empty.xml.json")
		fi, err := os.Stat(out)
		if err != nil {
			t.Fatalf("applied zero-record output file not committed: %v", err)
		}
		if fi.Size() != 0 {
			t.Errorf("expected an empty zero-record output file, got %d bytes", fi.Size())
		}
	})
}

// SUM-068 slice 3b: per-input output mode now runs the full per-input application on the
// workers (not just parse), via the same worker-build skeleton as aggregate. These tests
// are the per-input analogs of the aggregate slice-2 proofs: application overlaps across
// workers, a worker application panic is contained, and output is byte-identical across
// worker counts (the ordered committer preserves determinism).

// snapshotPerInputRecipe captures a per-input recipe's deterministic output: every
// extract-*.json file (name + generated_at-normalized content, in name order), the stable
// manifest subset, and failures.json (empty if absent).
func snapshotPerInputRecipe(t *testing.T, recipeDir string) string {
	t.Helper()
	entries, err := os.ReadDir(recipeDir)
	if err != nil {
		t.Fatalf("read recipe dir %s: %v", recipeDir, err)
	}
	names := make([]string, 0)
	for _, e := range entries {
		n := e.Name()
		if strings.HasPrefix(n, "extract-") && strings.HasSuffix(n, ".json") {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(recipeDir, n))
		if err != nil {
			t.Fatalf("read %s: %v", n, err)
		}
		// _runtime.generated_at is wall-clock per record; blank it so the comparison reflects
		// only stable record content and deterministic per-input ordering.
		normalized := volatileGeneratedAtRE.ReplaceAllString(string(data), `"generated_at":""`)
		b.WriteString("=== " + n + " ===\n")
		b.WriteString(normalized)
		b.WriteString("\n")
	}
	b.WriteString("=== manifest ===\n")
	b.WriteString(stableManifest(t, filepath.Join(recipeDir, provenance.ManifestFileName)))
	if data, err := os.ReadFile(filepath.Join(recipeDir, "failures.json")); err == nil {
		b.WriteString("\n=== failures ===\n")
		b.WriteString(string(data))
	}
	return b.String()
}

// TestExtractMulti_PerInputWorkersDeterministic proves per-input output is byte-identical
// across worker counts (serial baseline included), including a continue-on-error failure —
// the ordered committer preserves per-input file content, ledger order, and failure
// attribution regardless of worker scheduling.
func TestExtractMulti_PerInputWorkersDeterministic(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList := writeMixedInputSet(t, 6, 2) // inC (ordinal 3) malformed; continue-on-error

	scenario := func(workers int) string {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:        fileList,
			OutputPath:      outRoot,
			RunID:           testMultiRunID,
			ContinueOnError: true,
			InputWorkers:    workers,
		}
		_ = runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
		return outRoot
	}

	base := snapshotPerInputRecipe(t, filepath.Join(scenario(1), "summary"))
	for _, w := range []int{2, 4, 8} {
		got := snapshotPerInputRecipe(t, filepath.Join(scenario(w), "summary"))
		if got != base {
			t.Errorf("workers=%d: per-input output differs from serial\n--- serial ---\n%s\n--- got ---\n%s", w, base, got)
		}
	}
}

// TestExtractMulti_PerInputWorkersApplicationOverlap proves per-input application (not just
// parse) overlaps across workers — the parse-only path could never have two per-input
// applications in flight at once.
func TestExtractMulti_PerInputWorkersApplicationOverlap(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 8)
	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   filepath.Join(t.TempDir(), "out"),
		RunID:        testMultiRunID,
		InputWorkers: 4, // per-input mode (no OutputMode)
	}
	d := newMultiDispatcher(shared, io.Discard)

	release := make(chan struct{})
	var concurrent, peak int32
	d.onBuildApplication = func(ordinal int) {
		c := atomic.AddInt32(&concurrent, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if c <= p || atomic.CompareAndSwapInt32(&peak, p, c) {
				break
			}
		}
		<-release
		atomic.AddInt32(&concurrent, -1)
	}

	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()

	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&peak) < 2 {
		select {
		case <-deadline:
			close(release)
			<-done
			t.Fatalf("per-input application never reached 2 concurrent workers (peak=%d); application is not overlapping", atomic.LoadInt32(&peak))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestExtractMulti_PerInputWorkersApplicationPanicContainment proves G3 for per-input mode:
// a worker application panic is contained as an input-level failure, not a crash.
func TestExtractMulti_PerInputWorkersApplicationPanicContainment(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, inputs := writeMultiInputSet(t, 5) // ordinals 1..5

	t.Run("continue-on-error records input failure", func(t *testing.T) {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:        fileList,
			OutputPath:      outRoot,
			RunID:           testMultiRunID,
			ContinueOnError: true,
			InputWorkers:    4,
		}
		d := newMultiDispatcher(shared, io.Discard)
		d.onBuildApplication = func(ordinal int) {
			if ordinal == 3 {
				panic("synthetic per-input application panic for ordinal 3")
			}
		}
		_ = d.run([]string{ws}, time.Now())

		failures := readFileOrFail(t, filepath.Join(outRoot, "summary", "failures.json"))
		if !strings.Contains(failures, filepath.Base(inputs[2])) {
			t.Errorf("panicking input (ordinal 3) not recorded as a failure: %s", failures)
		}
		// Sibling inputs each produced their own per-input output file.
		for _, i := range []int{0, 1, 3, 4} {
			out := filepath.Join(outRoot, "summary", "extract-"+filepath.Base(inputs[i])+".json")
			if _, err := os.Stat(out); err != nil {
				t.Errorf("sibling input %s lost after an application panic: %v", filepath.Base(inputs[i]), err)
			}
		}
	})

	t.Run("fail-fast aborts cleanly", func(t *testing.T) {
		outRoot := filepath.Join(t.TempDir(), "out")
		shared := &multiSharedOptions{
			FileList:     fileList,
			OutputPath:   outRoot,
			RunID:        testMultiRunID,
			InputWorkers: 4,
		}
		d := newMultiDispatcher(shared, io.Discard)
		d.onBuildApplication = func(ordinal int) {
			if ordinal == 3 {
				panic("synthetic per-input application panic for ordinal 3")
			}
		}
		if err := d.run([]string{ws}, time.Now()); err == nil {
			t.Error("expected a fail-fast error from a panicking application, got nil")
		}
	})
}
