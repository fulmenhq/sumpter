package commands

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/spf13/cobra"
)

// TestResolveMaybeRelativeSchemes proves the recipe path resolver never
// workspace-joins a scheme-qualified URI: cloud, file://, and as-yet-unsupported
// schemes are returned verbatim (so uriio classifies/rejects them downstream),
// while bare relative paths are joined under the workspace.
func TestResolveMaybeRelativeSchemes(t *testing.T) {
	base := filepath.FromSlash("/work/space")
	cases := []struct {
		candidate string
		want      string
	}{
		{"s3://bucket/key.xml", "s3://bucket/key.xml"},
		{"gs://bucket/key.xml", "gs://bucket/key.xml"},
		{"azblob://container/blob.xml", "azblob://container/blob.xml"},
		{"file:///abs/in.xml", "file:///abs/in.xml"},
		{"relative/in.xml", filepath.Join(base, "relative/in.xml")},
		{"", ""},
	}
	for _, tc := range cases {
		if got := resolveMaybeRelative(base, tc.candidate); got != tc.want {
			t.Errorf("resolveMaybeRelative(%q) = %q, want %q", tc.candidate, got, tc.want)
		}
	}
}

// writeSchemeRecipeWorkspace builds a minimal recipe workspace whose input/output
// paths are caller-supplied (used to inject unsupported-scheme URIs).
func writeSchemeRecipeWorkspace(t *testing.T, inputPath, outputPath string) string {
	t.Helper()
	ws := t.TempDir()
	mustWriteFile(t, filepath.Join(ws, "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: "recipe/v0.1.0"
kind: "extract"
id: scheme_recipe
created_at: "2026-06-14T00:00:00Z"
content_version: "0.0.1"
assets:
  signature: signature.yaml
  extract: extract.yaml
defaults:
  input:
    mode: path
    path: `+inputPath+`
  output:
    format: json
    path: `+outputPath+`
    pattern: out.json
  workers: 1
  progress: false
`)
	return ws
}

// runRecipeExtract executes `recipes run extract <ws> [extra...]` under a parent
// that defines the inherited --allow-large-files flag, returning the run error.
func runRecipeExtract(t *testing.T, ws string, extra ...string) error {
	t.Helper()
	initExtractManifestTestLogger(t)
	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().Bool("allow-large-files", false, "")
	parent.AddCommand(newRecipeRunExtractCommand())
	parent.SetOut(io.Discard)
	parent.SetErr(io.Discard)
	parent.SetArgs(append([]string{"extract", ws, "--progress=false"}, extra...))
	return parent.Execute()
}

// assertUnsupportedScheme fails unless err is the actionable unsupported-scheme
// rejection (uriio.ErrUnsupportedScheme sentinel or the equivalent wording from
// the URI parser), never nil and never a silent local-path mangle.
func assertUnsupportedScheme(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want an unsupported-scheme rejection")
	}
	if errors.Is(err, uriio.ErrUnsupportedScheme) || strings.Contains(err.Error(), "unsupported") {
		return
	}
	t.Fatalf("error = %v, want an unsupported-scheme rejection", err)
}

// TestRecipeRunRejectsUnsupportedScheme proves an unsupported cloud scheme in a
// recipe (input or output) or via a CLI override is rejected with the actionable
// unsupported-scheme error, never silently mangled into a workspace-local path.
func TestRecipeRunRejectsUnsupportedScheme(t *testing.T) {
	local := filepath.Join(t.TempDir(), "outputs")

	t.Run("recipe output scheme", func(t *testing.T) {
		ws := writeSchemeRecipeWorkspace(t, filepath.Join(t.TempDir(), "in.xml"), "gs://bucket/out/")
		assertUnsupportedScheme(t, runRecipeExtract(t, ws))
	})

	t.Run("recipe input scheme", func(t *testing.T) {
		ws := writeSchemeRecipeWorkspace(t, "azblob://container/in.xml", local)
		assertUnsupportedScheme(t, runRecipeExtract(t, ws))
	})

	t.Run("CLI output override scheme", func(t *testing.T) {
		ws := writeSchemeRecipeWorkspace(t, filepath.Join(t.TempDir(), "in.xml"), local)
		assertUnsupportedScheme(t, runRecipeExtract(t, ws, "--output-path", "gs://bucket/out/"))
	})

	// Sanity: a gs:// candidate must be returned verbatim, never workspace-joined.
	if got := resolveMaybeRelative(t.TempDir(), "gs://bucket/key.xml"); got != "gs://bucket/key.xml" {
		t.Errorf("gs:// candidate mangled to %q", got)
	}
	_ = os.Remove(local)
}
