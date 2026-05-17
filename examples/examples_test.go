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
	cases, err := filepath.Glob(filepath.Join(repoRoot, "examples", "cases", "*-*"))
	if err != nil {
		t.Fatalf("failed to list cases: %v", err)
	}
	if len(cases) == 0 {
		t.Fatal("no example cases found")
	}

	for _, caseDir := range cases {
		caseDir := caseDir
		t.Run(filepath.Base(caseDir), func(t *testing.T) {
			t.Parallel()
			cmd := exec.Command(filepath.Join(repoRoot, "examples", "scripts", "run-case.sh"), caseDir)
			cmd.Dir = repoRoot
			cmd.Env = append(os.Environ(), "SUMPTER_BIN="+exampleBinary(t, repoRoot))
			output, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("example failed: %v\n%s", err, output)
			}
		})
	}
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
