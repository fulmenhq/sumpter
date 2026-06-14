//go:build s3integration

// S3 live-integration test for recipe-driven cloud I/O (PR-6): a `sumpter recipes
// run extract` run whose recipe declares cloud (s3://) input and output with
// named credential handles. Excluded from the default/CI build; run with
// `-tags s3integration`. Shares the moto harness in extract_moto_test.go.

package commands

import (
	"io"

	"github.com/spf13/cobra"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeNamedCredentialsConfig writes a credentials config with the given handle
// names, all pointing at the configured endpoint (distinct pooled providers).
func (m motoEnv) writeNamedCredentialsConfig(t *testing.T, dir string, names ...string) string {
	t.Helper()
	insecure := "false"
	if motoInsecure(m.endpoint) {
		insecure = "true"
	}
	var b strings.Builder
	b.WriteString("handles:\n")
	for _, name := range names {
		b.WriteString("  " + name + ":\n")
		b.WriteString("    region: " + m.region + "\n")
		b.WriteString("    endpoint: " + m.endpoint + "\n")
		b.WriteString("    force_path_style: true\n")
		b.WriteString("    insecure: " + insecure + "\n")
		if m.profile != "" {
			b.WriteString("    profile: " + m.profile + "\n")
		} else {
			b.WriteString("    access_key_id: " + m.keyID + "\n")
			b.WriteString("    secret_access_key: " + m.secret + "\n")
		}
	}
	path := filepath.Join(dir, "credentials.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write credentials config: %v", err)
	}
	return path
}

// writeCloudRecipeWorkspace creates a recipe workspace whose recipe.yaml declares
// cloud input/output with named credential handles, plus the signature/extract
// assets. Returns the workspace dir.
func writeCloudRecipeWorkspace(t *testing.T, srcURI, outURI, inHandle, outHandle string) string {
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
    description: Sample name
output_schema:
  type: object
  properties:
    name:
      type: string
  required:
    - name
`)
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: "recipe/v0.1.0"
kind: "extract"
id: cloud_recipe
created_at: "2026-06-14T00:00:00Z"
content_version: "0.0.1"
assets:
  signature: signature.yaml
  extract: extract.yaml
defaults:
  input:
    mode: path
    path: `+srcURI+`
    credentials_handle: `+inHandle+`
  output:
    format: json
    path: `+outURI+`
    pattern: out.json
    credentials_handle: `+outHandle+`
  workers: 1
  progress: false
`)
	return ws
}

// TestMotoRecipeRunCloudInOut runs an extract recipe that reads a cloud source and
// publishes results to a cloud destination, both via recipe-declared named
// handles, proving recipe-driven cloud I/O end to end (PR-6).
func TestMotoRecipeRunCloudInOut(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	srcKey := runKeyPrefix() + "recipe/src.xml"
	m.putObject(t, srcKey, []byte(motoSourceXML))
	srcURI := "s3://" + m.bucket + "/" + srcKey

	outPrefix := runKeyPrefix() + "recipe-out/"
	outURI := "s3://" + m.bucket + "/" + outPrefix

	credDir := t.TempDir()
	credPath := m.writeNamedCredentialsConfig(t, credDir, "reader", "writer")

	ws := writeCloudRecipeWorkspace(t, srcURI, outURI, "reader", "writer")

	// The recipe extract command reads the root-level persistent --allow-large-files
	// flag via InheritedFlags; wrap it in a parent that defines it.
	parent := &cobra.Command{Use: "root"}
	parent.PersistentFlags().Bool("allow-large-files", false, "")
	parent.AddCommand(newRecipeRunExtractCommand())
	parent.SetOut(io.Discard)
	parent.SetErr(io.Discard)
	parent.SetArgs([]string{"extract", ws, "--credentials", credPath, "--progress=false"})
	if err := parent.Execute(); err != nil {
		t.Fatalf("recipes run extract (cloud in/out) error = %v", err)
	}

	if _, ok := m.getObject(t, outPrefix+"out.json"); !ok {
		t.Fatalf("recipe-run output %sout.json was not published", outPrefix)
	}
	manifestData, ok := m.getObject(t, outPrefix+"manifest.json")
	if !ok {
		t.Fatalf("recipe-run sidecar %smanifest.json was not published", outPrefix)
	}
	stageRoot := filepath.Join(home, "work", "cloud")
	if strings.Contains(string(manifestData), stageRoot) {
		t.Errorf("recipe-run manifest leaked the staging path %q", stageRoot)
	}
	// Both logical cloud identities should appear; the staged path must not.
	manifest := string(manifestData)
	if !strings.Contains(manifest, srcKey) {
		t.Errorf("manifest missing logical source identity %q", srcURI)
	}
	if !strings.Contains(manifest, outPrefix+"out.json") && !strings.Contains(manifest, outURI) {
		t.Errorf("manifest missing logical output destination %q", outURI)
	}
	assertStagingCleanedUp(t, home)
}
