package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// SUM-068 slice 2 (input-workers): the aggregate concurrent path now runs the full
// per-input recipe application on the workers and commits in input order on a single
// drain. Byte-identical determinism across worker counts is already proven by the
// TestExtractMulti_InputWorkers*Equivalence suite (which runs aggregate mode through this
// path at worker counts 1/2/4/8). These tests cover the NEW concurrency surface this slice
// introduces: that application (not just parse) overlaps across workers, that the in-flight
// record bound throttles scheduling, and that a worker application panic is contained.

// TestExtractMulti_InputWorkersApplicationOverlap proves the slice's whole point: with
// input-workers > 1, per-input recipe APPLICATION runs concurrently across workers. The
// pre-SUM-068 path parsed concurrently but applied serially on the drain, so it could never
// have two inputs inside the application stage at once. We block every input inside the
// application stage and assert at least two are there concurrently.
func TestExtractMulti_InputWorkersApplicationOverlap(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 8)
	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   filepath.Join(t.TempDir(), "out"),
		OutputMode:   outputModeAggregate,
		RunID:        testMultiRunID,
		InputWorkers: 4,
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
		<-release // hold every application in-stage until the test has measured overlap
		atomic.AddInt32(&concurrent, -1)
	}

	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()

	// Succeed as soon as two applications overlap; fail on a generous deadline so a
	// regression to serial application does not hang the suite.
	deadline := time.After(5 * time.Second)
	for atomic.LoadInt32(&peak) < 2 {
		select {
		case <-deadline:
			close(release)
			<-done
			t.Fatalf("per-input application never reached 2 concurrent workers (peak=%d); application is not overlapping — parse-only behavior", atomic.LoadInt32(&peak))
		default:
			time.Sleep(5 * time.Millisecond)
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}

// TestExtractMulti_InputWorkersRecordsBackpressureBound proves the G4 in-flight RECORD
// bound is real and distinct from the input-count window. With the head input's
// application held (so nothing commits and in-flight records never drain), a tiny record
// ceiling stops the feeder well before the input set runs ahead — and below what the
// window alone (2×workers) would allow.
func TestExtractMulti_InputWorkersRecordsBackpressureBound(t *testing.T) {
	const (
		n       = 24
		workers = 4
		ceiling = int64(2)
	)
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, n) // one record per input
	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   filepath.Join(t.TempDir(), "out"),
		OutputMode:   outputModeAggregate,
		RunID:        testMultiRunID,
		InputWorkers: workers,
	}
	d := newMultiDispatcher(shared, io.Discard)
	d.maxInFlightRecords = ceiling

	var built int32
	block := make(chan struct{})
	var once sync.Once
	headHeld := make(chan struct{})
	d.onBuildApplication = func(ordinal int) {
		atomic.AddInt32(&built, 1)
		if ordinal == 1 {
			once.Do(func() { close(headHeld) })
			<-block // hold the head so its records never commit; in-flight records cannot drain
		}
	}

	done := make(chan error, 1)
	go func() { done <- d.run([]string{ws}, time.Now()) }()

	<-headHeld
	time.Sleep(250 * time.Millisecond) // let the feeder/workers reach steady state at the ceiling
	peak := atomic.LoadInt32(&built)
	close(block)
	if err := <-done; err != nil {
		t.Fatalf("run: %v", err)
	}

	if int(peak) >= n {
		t.Errorf("built %d/%d applications with the head held — record backpressure not enforced", peak, n)
	}
	// Bound: the ceiling's worth of committed-but-undrained records, plus at most one
	// in-progress build per worker (overshoot), plus the held head. Comfortably below the
	// window (2×workers = 8), so this proves the RECORD bound, not the input-count window.
	bound := int(ceiling) + workers + 1
	if int(peak) > bound {
		t.Errorf("built %d applications with the head held; record bound ~%d exceeded (ceiling=%d, workers=%d)", peak, bound, ceiling, workers)
	}
}

// TestExtractMulti_InputWorkersBundleBudgetBoundedFailure proves the G4 per-input bundle
// budget: an input whose output exceeds the in-flight bundle cap trips a BOUNDED FAILURE
// during construction (recorded under --continue-on-error; siblings survive), and does so
// IDENTICALLY at every worker count — the cap is enforced in the shared build path, so the
// serial baseline and the worker path agree. This is the bound devrev required beyond the
// post-build scheduling throttle: it stops one dense input before its bundle grows.
func TestExtractMulti_InputWorkersBundleBudgetBoundedFailure(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	dir := t.TempDir()
	mk := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write input %s: %v", name, err)
		}
		return p
	}
	one := func(v string) string {
		return `<root><TargetElement><Name>` + v + `</Name></TargetElement></root>`
	}
	// inC emits two records — over a max-1-record bundle cap; the rest emit one.
	fat := `<root><TargetElement><Name>valC1</Name></TargetElement><TargetElement><Name>valC2</Name></TargetElement></root>`
	paths := []string{
		mk("inA.xml", one("valA")),
		mk("inB.xml", one("valB")),
		mk("inC.xml", fat),
		mk("inD.xml", one("valD")),
		mk("inE.xml", one("valE")),
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(paths, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
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
		d := newMultiDispatcher(shared, io.Discard)
		d.bundleMaxRecords = 1 // force inC's 2-record bundle over budget
		_ = d.run([]string{ws}, time.Now())
		return outRoot
	}

	// Bounded failure is deterministic across worker counts (serial baseline included).
	assertWorkerCountEquivalence(t, scenario, []string{"summary"}, 2, 4, 8)

	root := scenario(4)
	failures := readFileOrFail(t, filepath.Join(root, "summary", "failures.json"))
	if !strings.Contains(failures, "inC.xml") {
		t.Errorf("over-budget input inC not recorded as a bounded failure: %s", failures)
	}
	records := readFileOrFail(t, filepath.Join(root, "summary", "records.jsonl"))
	for _, good := range []string{"valA", "valB", "valD", "valE"} {
		if !strings.Contains(records, good) {
			t.Errorf("sibling input %s lost after a bundle-budget failure: %s", good, records)
		}
	}
	if strings.Contains(records, "valC") {
		t.Errorf("over-budget input committed records (should contribute none): %s", records)
	}
}

// TestExtractMulti_InputWorkersApplicationPanicContainment proves G3: a panic raised inside
// the worker application stage is contained as an input-level failure, not a process crash.
// Under continue-on-error the panicking input is recorded failed and siblings survive;
// under fail-fast the run aborts cleanly with an error and commits no output.
func TestExtractMulti_InputWorkersApplicationPanicContainment(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 5) // inA..inE, ordinals 1..5

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
		d.onBuildApplication = func(ordinal int) {
			if ordinal == 3 { // inC
				panic("synthetic application panic for ordinal 3")
			}
		}
		// A partial-failure error is expected; the point is the panic does not crash the run.
		_ = d.run([]string{ws}, time.Now())

		failures := readFileOrFail(t, filepath.Join(outRoot, "summary", "failures.json"))
		if !strings.Contains(failures, "inC.xml") {
			t.Errorf("panicking input inC not recorded as an input-level failure: %s", failures)
		}
		records := readFileOrFail(t, filepath.Join(outRoot, "summary", "records.jsonl"))
		for _, good := range []string{"valA", "valB", "valD", "valE"} {
			if !strings.Contains(records, good) {
				t.Errorf("sibling input %s lost after an application panic: %s", good, records)
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
		d.onBuildApplication = func(ordinal int) {
			if ordinal == 3 {
				panic("synthetic application panic for ordinal 3")
			}
		}
		if err := d.run([]string{ws}, time.Now()); err == nil {
			t.Error("expected a fail-fast error from a panicking application, got nil")
		}
		if _, statErr := os.Stat(filepath.Join(outRoot, "summary", "records.jsonl")); statErr == nil {
			t.Error("fail-fast after an application panic left a committed records.jsonl")
		}
	})
}
