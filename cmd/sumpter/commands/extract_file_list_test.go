package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fileListWorkspace builds a recipe workspace with two input XMLs and a newline
// file-list referencing them (relative to the workspace, where the list lives).
func fileListWorkspace(t *testing.T) string {
	t.Helper()
	initExtractManifestTestLogger(t)
	ws := createWorkingTempDir(t)
	for _, d := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mustWriteFile(t, filepath.Join(ws, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: rec
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	mustWriteFile(t, filepath.Join(ws, "testdata", "a.xml"), "<root><item><Name>A</Name></item></root>")
	mustWriteFile(t, filepath.Join(ws, "testdata", "b.xml"), "<root><item><Name>B</Name></item></root>")
	// A file-list with relative entries (resolve against the list's dir = workspace),
	// blank lines and a comment.
	mustWriteFile(t, filepath.Join(ws, "inputs.list"), "# batch inputs\ntestdata/a.xml\n\ntestdata/b.xml\n")
	return ws
}

func fileListRecipe(t *testing.T, ws, inputBlock string) {
	t.Helper()
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: file_list
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
`+inputBlock+`  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  progress: false
`)
}

func assertFileListOutputs(t *testing.T, ws string) {
	t.Helper()
	for _, name := range []string{"a", "b"} {
		p := filepath.Join(ws, "outputs", "extract-"+name+".xml.json")
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected output %s: %v", p, err)
		}
	}
}

// TestRecipeFileListCLI runs extraction with the --file-list CLI input over an
// explicit batch (no directory walk).
func TestRecipeFileListCLI(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    mode: files\n    files: []\n") // manifest input unused; CLI overrides
	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		FileList:     "inputs.list",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (--file-list): %v", err)
	}
	assertFileListOutputs(t, ws)
}

// TestRecipeFilesFromManifest runs extraction with manifest defaults.input.files_from.
func TestRecipeFilesFromManifest(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    files_from: inputs.list\n")
	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (files_from): %v", err)
	}
	assertFileListOutputs(t, ws)
}

// TestFileListMutualExclusivity proves only one input mode is accepted.
func TestFileListMutualExclusivity(t *testing.T) {
	initExtractManifestTestLogger(t)
	err := runExtract(&ExtractOptions{Files: "a.xml", FileList: "in.list"})
	if err == nil || !strings.Contains(err.Error(), "specify only one") {
		t.Fatalf("err = %v, want single-input-mode rejection", err)
	}
}
