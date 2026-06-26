package commands

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runMultiAggregateForStats runs a fixed-RunID aggregate extract-multi over the
// given recipe workspace + shared input set into a fresh output root, returning
// the root. ws/fileList are passed in so two runs can share identical inputs and
// differ ONLY by the stats flag. warnOut receives the run's stderr-class output
// (warnings + the optional --stats block).
func runMultiAggregateForStats(t *testing.T, ws, fileList string, stats bool, warnOut io.Writer) string {
	t.Helper()
	outRoot := filepath.Join(t.TempDir(), "out")
	shared := &multiSharedOptions{
		FileList:     fileList,
		OutputPath:   outRoot,
		RunID:        testMultiRunID,
		OutputMode:   "aggregate",
		ParseWorkers: 2,
		Stats:        stats,
	}
	if err := runExtractMulti(shared, []string{ws}, warnOut, time.Now()); err != nil {
		t.Fatalf("extract-multi (stats=%v): %v", stats, err)
	}
	return outRoot
}

// --stats off emits no stats block; --stats on emits exactly one diagnostic block
// to the stderr-class writer (cmd.ErrOrStderr() in production). Records go to
// files, never the stats writer, so there is no data on this channel either way.
func TestExtractMultiStats_StderrBlockOnlyWhenEnabled(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 3)

	var off bytes.Buffer
	runMultiAggregateForStats(t, ws, fileList, false, &off)
	if strings.Contains(off.String(), "extract-multi --stats") {
		t.Errorf("stats off must emit no stats block, got:\n%s", off.String())
	}

	var on bytes.Buffer
	runMultiAggregateForStats(t, ws, fileList, true, &on)
	got := on.String()
	if strings.Count(got, "extract-multi --stats") != 1 {
		t.Fatalf("stats on must emit exactly one block, got:\n%s", got)
	}
	for _, want := range []string{"wall:", "inputs:", "parse-workers: 2", "GOMAXPROCS:", "effective CPU:"} {
		if !strings.Contains(got, want) {
			t.Errorf("stats block missing %q:\n%s", want, got)
		}
	}
	// No leak: the diagnostic block must not carry input paths, URIs, or recipe ids.
	for _, forbidden := range []string{"/", "summary", ".xml", "s3://"} {
		// "/" appears legitimately in "inputs/s" and "MiB/s"; only flag path-like "/".
		if forbidden == "/" {
			if strings.Contains(got, "/Users") || strings.Contains(got, "/tmp") || strings.Contains(got, "/var") {
				t.Errorf("stats block leaked a filesystem path:\n%s", got)
			}
			continue
		}
		if strings.Contains(got, forbidden) {
			t.Errorf("stats block leaked %q:\n%s", forbidden, got)
		}
	}
}

// --stats must not perturb the deterministic artifact path: records, the stable
// manifest subset, and failures.json are identical with stats off vs on (same
// fixed RunID, same inputs, same worker count).
func TestExtractMultiStats_OutputByteIdenticalOnOff(t *testing.T) {
	// Same recipe + same inputs for both runs; only the stats flag differs.
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 3)
	offRoot := runMultiAggregateForStats(t, ws, fileList, false, io.Discard)
	onRoot := runMultiAggregateForStats(t, ws, fileList, true, io.Discard)

	off := snapshotAggRecipe(t, filepath.Join(offRoot, "summary"))
	on := snapshotAggRecipe(t, filepath.Join(onRoot, "summary"))
	if on.records != off.records {
		t.Errorf("records differ with --stats on vs off\n--- off ---\n%s\n--- on ---\n%s", off.records, on.records)
	}
	if on.manifest != off.manifest {
		t.Errorf("stable manifest differs with --stats on vs off\n--- off ---\n%s\n--- on ---\n%s", off.manifest, on.manifest)
	}
	if on.failures != off.failures {
		t.Errorf("failures.json differs with --stats on vs off")
	}
}

// --stats is a diagnostic, not a data-shape/replay input, so it must never enter
// the sanitized argv recorded in cli.argv_sanitized (else stats on/off manifests
// would diverge, violating the brief).
func TestBuildExtractMultiArgv_OmitsStats(t *testing.T) {
	argv := buildExtractMultiArgv([]string{"ws"}, &recipeRunExtractMultiOptions{
		Stats:        true,
		OutputPath:   "out",
		ParseWorkers: 4,
	})
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "--stats") {
		t.Errorf("buildExtractMultiArgv must omit --stats, got: %s", joined)
	}
}
