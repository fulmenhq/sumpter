package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records.jsonl")
	const content = "{\"extract\":{}}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openInput(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	if err != nil || string(data) != content {
		t.Fatalf("read = %q, %v", data, err)
	}
	for _, invalid := range []string{dir, filepath.Join(dir, "missing.jsonl")} {
		file, err := openInput(invalid)
		if file != nil {
			_ = file.Close()
			t.Errorf("invalid input %q returned an open file", invalid)
		}
		if err == nil {
			t.Errorf("invalid input %q was accepted", invalid)
		}
	}
}
