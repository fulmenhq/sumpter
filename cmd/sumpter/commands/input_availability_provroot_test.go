package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProvenanceRootInputAvailabilityDiagnosticsAreOpaque runs the no-match and
// missing-input failures under --provenance-root on both entry routes, with the
// inputs below a username-shaped directory that sits beneath the root, and
// requires that neither the error nor the logs carry that directory or the root.
func TestProvenanceRootInputAvailabilityDiagnosticsAreOpaque(t *testing.T) {
	for _, route := range []string{"extract files", "recipes run extract"} {
		for _, scenario := range []string{"no match", "no match relative", "no match root at segment", "missing fail-fast", "missing continue-on-error"} {
			t.Run(route+"/"+scenario, func(t *testing.T) {
				f := newAvailabilityFixture(t)
				f.sig, f.ext = absPath(t, f.sig), absPath(t, f.ext) // one case changes directory
				root := filepath.Join(f.dir, "prov")
				private := filepath.Join(root, "Users", "jdoe")
				empty := filepath.Join(private, "empty")
				missing := filepath.Join(private, "missing.json")
				good := filepath.Join(root, "good.json")
				if err := os.MkdirAll(empty, 0o750); err != nil {
					t.Fatal(err)
				}
				copyFile(t, f.good, good)
				out := filepath.Join(f.dir, "fresh-out")

				var input []string
				flagRoot := root
				want := `no input files matched under <input> (include "*.json")`
				switch scenario {
				case "no match":
					input = []string{"--input-path", empty}
				case "no match root at segment":
					flagRoot = private
					input = []string{"--input-path", empty}
				case "no match relative":
					chdir(t, root)
					input = []string{"--input-path", filepath.Join("Users", "jdoe", "empty")}
				case "missing fail-fast":
					input = []string{"--file-list", f.writeList(t, "list.txt", good, missing)}
					want = "input unavailable: <input>: not found"
				default:
					// The provenance-root containment check cannot resolve a
					// missing path, so it stops the run before the input is
					// recorded as unavailable.
					input = []string{"--file-list", f.writeList(t, "list.txt", good, missing), "--continue-on-error"}
					want = "input 2 is outside the provenance root"
				}
				args := f.filesArgs()
				if route == "recipes run extract" {
					ws := recipeWorkspace(t, f, "defaults:\n  input:\n    format: json\n  output:\n    format: json\n    pattern: records.jsonl\n")
					args = []string{"recipes", "run", "extract", ws}
				}
				args = append(args, input...)
				args = append(args, "--provenance-root", flagRoot, "--output-path", out)

				var runErr error
				logs := captureLoggingStderr(t, func() { runErr = runSumpter(t, args) })
				if runErr == nil || !strings.Contains(runErr.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", runErr, want)
				}
				if strings.Contains(runErr.Error(), "relative to") {
					t.Fatalf("error names a base for an opaque input: %v", runErr)
				}
				forbidden := []string{"jdoe", root, private, missing, empty}
				assertNoProvenanceInputFragments(t, "error", runErr.Error(), forbidden...)
				assertNoProvenanceInputFragments(t, "logs", logs, forbidden...)
				if _, err := os.Stat(filepath.Join(out, "failures.json")); !os.IsNotExist(err) {
					t.Fatalf("failures.json written for a run stopped before processing: %v", err)
				}
			})
		}
	}
}

// TestProvenanceRootMultiZeroDiscoveryIsOpaque is the extract-multi form of the
// no-match case above.
func TestProvenanceRootMultiZeroDiscoveryIsOpaque(t *testing.T) {
	f := newMultiAvailabilityFixture(t)
	root := filepath.Join(f.dir, "prov")
	empty := filepath.Join(root, "Users", "jdoe", "empty")
	if err := os.MkdirAll(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	var runErr error
	logs := captureLoggingStderr(t, func() {
		runErr = f.run(multiSharedOptions{InputPath: empty, ProvenanceRoot: root, ProvenanceRootSet: true, ProvenanceRootFromFlag: true})
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "no input files matched under <input>") {
		t.Fatalf("error = %v, want an opaque no-match failure", runErr)
	}
	for _, text := range []string{runErr.Error(), logs} {
		assertNoProvenanceInputFragments(t, "extract-multi", text, "jdoe", root, empty)
	}
}

func absPath(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
}
