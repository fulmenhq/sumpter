package commands

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

func runIndex(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := NewIndexCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func TestIndexCommandsJSONInput(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.json")
	if err := os.WriteFile(src, []byte(`{"meta":{"results":{"n":2}},"results":[{"id":"a"},{"id":"b"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "events")
	if _, err := runIndex(t, "build", src, "--input-format", "json", "--selector", "results", "--progress=false", "--output", base); err != nil {
		t.Fatalf("build: %v", err)
	}
	idx := base + ".recordindex.json"
	out, err := runIndex(t, "verify", src, "--input-format", "json", "--index", idx, "--progress=false")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	out, err = runIndex(t, "stream", "--input-format", "json", idx)
	if err != nil || !strings.Contains(out, "Format: json") || !strings.Contains(out, "Scanned records: 2") || !strings.Contains(out, "Summary verification: ok") {
		t.Fatalf("stream: %v\n%s", err, out)
	}

	// The declared format must match the index; it is never inferred.
	for _, args := range [][]string{
		{"verify", src, "--index", idx, "--progress=false"},
		{"stream", idx},
	} {
		if _, err := runIndex(t, args...); err == nil || !strings.Contains(err.Error(), "built from json input, but --input-format is xml") {
			t.Fatalf("%v: error %v", args, err)
		}
	}

	// A tampered source fails semantic verification.
	if err := os.WriteFile(src, []byte(`{"meta":{"results":{"n":2}},"results":[{"id":"a"},{"id":"c"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := runIndex(t, "verify", src, "--input-format", "json", "--index", idx, "--progress=false"); err == nil {
		t.Fatalf("tampered source verified:\n%s", out)
	}
}

func TestIndexCommandsRefuseNDJSONAndUnknownFormats(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.ndjson")
	if err := os.WriteFile(src, []byte("{\"id\":\"a\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"build", src, "--input-format", "ndjson", "--selector", "r", "--output", filepath.Join(dir, "x")},
		{"verify", src, "--input-format", "ndjson"},
		{"stream", "--input-format", "ndjson", filepath.Join(dir, "x.recordindex.json")},
	} {
		_, err := runIndex(t, args...)
		if !errors.Is(err, docnode.ErrRouteUnsupported) || !strings.Contains(err.Error(), "not supported for ndjson input") {
			t.Fatalf("%v: error %v", args, err)
		}
	}
	if _, err := runIndex(t, "build", src, "--input-format", "yaml", "--selector", "r"); err == nil || !strings.Contains(err.Error(), `unknown --input-format "yaml"`) {
		t.Fatalf("unknown format: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "x.recordindex.json")); !os.IsNotExist(err) {
		t.Fatal("refused build wrote an index")
	}
}

// TestIndexCommandsRefuseMissingNamespaceTable requires index stream and
// verify to refuse a current JSON index with no namespace table before any
// success output, rather than supplying one.
func TestIndexCommandsRefuseMissingNamespaceTable(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "events.json")
	if err := os.WriteFile(src, []byte(`{"results":[{"id":"a"},{"id":"b"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "events")
	if _, err := runIndex(t, "build", src, "--input-format", "json", "--selector", "results", "--progress=false", "--output", base); err != nil {
		t.Fatalf("build: %v", err)
	}
	idx := base + ".recordindex.json"
	raw, err := os.ReadFile(idx)
	if err != nil {
		t.Fatal(err)
	}
	stripped := strings.Replace(string(raw), `"namespace_contexts": [
    {
      "id": 0,
      "declarations": []
    }
  ],`, "", 1)
	if stripped == string(raw) {
		t.Fatalf("namespace table not found in index:\n%s", raw)
	}
	if err := os.WriteFile(idx, []byte(stripped), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"stream", "--input-format", "json", idx},
		{"verify", src, "--input-format", "json", "--index", idx, "--progress=false"},
	} {
		out, err := runIndex(t, args...)
		if err == nil || !strings.Contains(err.Error(), "requires a namespace_contexts table") {
			t.Fatalf("%v: error %v", args, err)
		}
		if strings.Contains(out, "Scanned records") || strings.Contains(out, "valid") {
			t.Fatalf("%v: success output before refusal:\n%s", args, out)
		}
	}
}
