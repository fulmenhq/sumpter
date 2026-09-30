package examples

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	binaryOnce sync.Once
	binaryPath string
	binaryErr  error
)

func TestExamples(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("example harness runs from the source examples tree, not the embedded asset mirror")
	}
	repoRoot := repoRoot(t)
	entries := listCases(t, repoRoot)

	for _, entry := range entries {
		entry := entry
		t.Run(entry, func(t *testing.T) {
			t.Parallel()
			name, variant, _ := strings.Cut(entry, ":")
			args := []string{filepath.Join(repoRoot, "examples", "cases", name)}
			if variant != "" {
				args = append(args, "--variant", variant)
			}
			cmd := exec.Command(filepath.Join(repoRoot, "examples", "scripts", "run-case.sh"), args...)
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "SUMPTER_BIN="+exampleBinary(t, repoRoot))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("example failed: %v\n%s", err, output)
			}
		})
	}
}

// requiredCases pins entries the enumerator must always yield, so a discovery
// regression cannot silently shrink the suite.
var requiredCases = []string{
	"01-basic-extraction",
	"05b-validation-metadata-grouped-reconciliation",
	"06b-derived-field-ternary",
	"14-json-basic-extraction",
	"02-multi-record-line-items:json",
	"08-polymorphic-line-items:json",
	"09-predicate-match-selector:json",
	"10-optional-fields:json",
	"05b-validation-metadata-grouped-reconciliation:json",
	"06b-derived-field-ternary:json",
	"07-declared-parameters-injection:json",
	"13-fixture-document:json",
	"91-negative-missing-required:json",
	"15-ndjson-records",
	"15-ndjson-records:ndjson",
	"95-negative-malformed-json",
	"96-negative-truncated-json",
	"97-negative-ndjson-bad-line:ndjson",
	"90-negative-malformed-xml",
}

func TestExampleInventory(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("example harness runs from the source examples tree, not the embedded asset mirror")
	}
	entries := listCases(t, repoRoot(t))
	seen := make(map[string]bool, len(entries))
	for _, e := range entries {
		if seen[e] {
			t.Errorf("duplicate inventory entry %q", e)
		}
		seen[e] = true
	}
	for _, want := range requiredCases {
		if !seen[want] {
			t.Errorf("inventory missing %q", want)
		}
	}
}

func TestExampleVariantRefusal(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("example harness runs from the source examples tree, not the embedded asset mirror")
	}
	repoRoot := repoRoot(t)
	bin := exampleBinary(t, repoRoot)
	for _, tc := range []struct {
		name, caseName, variant, want string
	}{
		{"unknown", "14-json-basic-extraction", "yaml", "unknown variant"},
		{"missing", "01-basic-extraction", "json", "variant not found"},
		{"missing-ndjson", "14-json-basic-extraction", "ndjson", "variant not found"},
		{"variant-only-default", "97-negative-ndjson-bad-line", "", "no default run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{filepath.Join(repoRoot, "examples", "cases", tc.caseName)}
			if tc.variant != "" {
				args = append(args, "--variant", tc.variant)
			}
			cmd := exec.Command(filepath.Join(repoRoot, "examples", "scripts", "run-case.sh"), args...)
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "SUMPTER_BIN="+bin)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected refusal, got success:\n%s", output)
			}
			if !strings.Contains(string(output), tc.want) {
				t.Fatalf("expected %q in output:\n%s", tc.want, output)
			}
		})
	}
}

// TestListCasesFailsClosed runs a copy of the enumerator against synthetic
// trees: an empty or malformed inventory must exit non-zero.
func TestListCasesFailsClosed(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("example harness runs from the source examples tree, not the embedded asset mirror")
	}
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "examples", "scripts", "list-cases.sh"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(t *testing.T, path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	validCase := func(t *testing.T, root string) string {
		dir := filepath.Join(root, "cases", "01-ok")
		write(t, filepath.Join(dir, "recipe", "recipe.yaml"), "x")
		write(t, filepath.Join(dir, "input.xml"), "x")
		write(t, filepath.Join(dir, "expected", "output.json"), "x")
		return dir
	}
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, root string)
	}{
		{"empty", func(t *testing.T, root string) {
			if err := os.MkdirAll(filepath.Join(root, "cases"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing-golden", func(t *testing.T, root string) {
			dir := validCase(t, root)
			if err := os.Remove(filepath.Join(dir, "expected", "output.json")); err != nil {
				t.Fatal(err)
			}
		}},
		{"unknown-variant", func(t *testing.T, root string) {
			dir := validCase(t, root)
			write(t, filepath.Join(dir, "variants", "yaml", "input.yaml"), "x")
		}},
		{"empty-variants", func(t *testing.T, root string) {
			dir := validCase(t, root)
			if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"variant-only-without-variants", func(t *testing.T, root string) {
			dir := filepath.Join(root, "cases", "97-neg")
			if err := os.MkdirAll(filepath.Join(dir, "variants"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{"incomplete-variant", func(t *testing.T, root string) {
			dir := validCase(t, root)
			write(t, filepath.Join(dir, "variants", "json", "input.json"), "x")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, filepath.Join(root, "scripts", "list-cases.sh"), string(script))
			tc.setup(t, root)
			cmd := exec.Command("sh", filepath.Join(root, "scripts", "list-cases.sh"))
			if out, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("expected enumerator failure, got success:\n%s", out)
			}
		})
	}
}

// TestNegativeRejectsPublishedOutput pins that a negative case fails when the
// refused run still publishes output, using a stub binary that prints the
// expected error, writes each artifact and exits non-zero.
func TestNegativeRejectsPublishedOutput(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("example harness runs from the source examples tree, not the embedded asset mirror")
	}
	repoRoot := repoRoot(t)
	caseDir := filepath.Join(repoRoot, "examples", "cases", "95-negative-malformed-json")
	want, err := os.ReadFile(filepath.Join(caseDir, "expected", "error.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{"records.jsonl", "manifest.json", "failures.json", "dispositions.json"} {
		t.Run(artifact, func(t *testing.T) {
			stub := filepath.Join(t.TempDir(), "sumpter")
			body := "#!/bin/sh\n" +
				"out=\"\"\n" +
				"while [ \"$#\" -gt 0 ]; do [ \"$1\" = --output-path ] && out=\"$2\"; shift; done\n" +
				"mkdir -p \"$out\" && : >\"$out/" + artifact + "\"\n" +
				"printf '%s\\n' '" + strings.TrimSpace(string(want)) + "'\n" +
				"exit 1\n"
			if err := os.WriteFile(stub, []byte(body), 0o700); err != nil { // #nosec G306 -- test stub must be executable.
				t.Fatal(err)
			}
			cmd := exec.Command(filepath.Join(repoRoot, "examples", "scripts", "run-case.sh"), caseDir)
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "SUMPTER_BIN="+stub)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected failure for published %s, got success:\n%s", artifact, output)
			}
			if !strings.Contains(string(output), "refused run published output") {
				t.Fatalf("expected published-output failure, got:\n%s", output)
			}
		})
	}
}

// listCases runs the shared enumerator; any failure or empty result fails the test.
func listCases(t *testing.T, repoRoot string) []string {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot, "examples", "scripts", "list-cases.sh"))
	cmd.Dir = repoRoot
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("list-cases.sh failed: %v\n%s", err, stderr.String())
	}
	entries := strings.Fields(string(out))
	if len(entries) == 0 {
		t.Fatal("no example cases found")
	}
	return entries
}

func runningFromEmbeddedMirror() bool {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return false
	}
	return strings.Contains(filepath.ToSlash(file), "/internal/assets/embedded_examples/")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to locate test file")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), ".."))
	if err != nil {
		t.Fatalf("failed to resolve repo root: %v", err)
	}
	return root
}

func exampleBinary(t *testing.T, repoRoot string) string {
	t.Helper()
	if existing := os.Getenv("SUMPTER_BIN"); existing != "" {
		return existing
	}
	binaryOnce.Do(func() {
		dir, err := os.MkdirTemp("", "sumpter-examples-bin-")
		if err != nil {
			binaryErr = err
			return
		}
		binaryPath = filepath.Join(dir, "sumpter")
		cmd := exec.Command("go", "build", "-buildvcs=false", "-o", binaryPath, "./cmd/sumpter")
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(),
			"GOCACHE="+filepath.Join(repoRoot, ".cache", "go-build"),
			"GOMODCACHE="+filepath.Join(repoRoot, ".cache", "go-mod"),
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			binaryErr = &buildError{err: err, output: output}
		}
	})
	if binaryErr != nil {
		t.Fatalf("failed to build sumpter test binary: %v", binaryErr)
	}
	return binaryPath
}

type buildError struct {
	err    error
	output []byte
}

func (e *buildError) Error() string {
	return e.err.Error() + "\n" + string(e.output)
}
