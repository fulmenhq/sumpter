package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexBuildCommandRejectsUnsupportedSelector(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "input.xml")
	if err := os.WriteFile(xmlPath, []byte(`<root><Record type="sale"/></root>`), 0o600); err != nil {
		t.Fatalf("write xml: %v", err)
	}

	cmd := NewIndexCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{
		"build",
		xmlPath,
		"--selector",
		`//Record[@type='sale']`,
		"--output",
		filepath.Join(tmpDir, "input.recordindex.json"),
	})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected unsupported selector error")
	}
	if !strings.Contains(err.Error(), "not yet supported for streaming/index mode") {
		t.Fatalf("error = %q, want streaming/index mode wording", err.Error())
	}
}
