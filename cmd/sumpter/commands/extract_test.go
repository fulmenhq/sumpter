package commands

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestDiscoverInputFiles(t *testing.T) {
	t.Parallel()

	tempDir := createWorkingTempDir(t)

	included := filepath.Join(tempDir, "sample.xml")
	if err := os.WriteFile(included, []byte("<root/>"), 0o644); err != nil {
		t.Fatalf("failed to write include fixture: %v", err)
	}

	excluded := filepath.Join(tempDir, "ignore.txt")
	if err := os.WriteFile(excluded, []byte("skip"), 0o644); err != nil {
		t.Fatalf("failed to write exclude fixture: %v", err)
	}

	opts := &ExtractOptions{
		InputPath:      tempDir,
		IncludePattern: "*.xml",
		ExcludePattern: "*.bak",
	}

	files, err := discoverInputFiles(opts)
	if err != nil {
		t.Fatalf("discoverInputFiles directory scan error: %v", err)
	}

	sort.Strings(files)
	if len(files) != 1 || files[0] != included {
		t.Fatalf("unexpected discovery result: %v", files)
	}

	fileOnly := &ExtractOptions{
		InputPath:      included,
		IncludePattern: "*.xml",
	}

	files, err = discoverInputFiles(fileOnly)
	if err != nil {
		t.Fatalf("discoverInputFiles file scan error: %v", err)
	}

	if len(files) != 1 || files[0] != included {
		t.Fatalf("unexpected single file result: %v", files)
	}
}

func createWorkingTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "extract-test-")
	if err != nil {
		t.Fatalf("failed to create temp directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("failed to resolve temp directory: %v", err)
	}
	return abs
}
