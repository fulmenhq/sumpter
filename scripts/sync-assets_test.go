package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncDirCopiesContainedFiles(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "embedded")

	inputPath := filepath.Join(source, "nested", "asset.txt")
	if err := os.MkdirAll(filepath.Dir(inputPath), 0o750); err != nil {
		t.Fatalf("MkdirAll source: %v", err)
	}
	if err := os.WriteFile(inputPath, []byte("asset\n"), 0o600); err != nil {
		t.Fatalf("WriteFile source: %v", err)
	}

	if err := syncDir(source, target); err != nil {
		t.Fatalf("syncDir: %v", err)
	}

	outputPath := filepath.Join(target, "nested", "asset.txt")
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("ReadFile output: %v", err)
	}
	if string(data) != "asset\n" {
		t.Fatalf("output data = %q, want asset newline", data)
	}
}

func TestContainedJoinRejectsEscapes(t *testing.T) {
	root := t.TempDir()
	if _, err := containedJoin(root, "../escape.txt"); err == nil {
		t.Fatal("containedJoin accepted path traversal")
	}
	if _, err := containedJoin(root, filepath.Join("nested", "asset.txt")); err != nil {
		t.Fatalf("containedJoin rejected local path: %v", err)
	}
}

func TestSyncDirRejectsSymlinks(t *testing.T) {
	source := t.TempDir()
	target := filepath.Join(t.TempDir(), "embedded")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatalf("WriteFile outside: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(source, "asset-link.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if err := syncDir(source, target); err == nil {
		t.Fatal("syncDir accepted symlink")
	}
}
