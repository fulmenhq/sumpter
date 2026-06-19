//go:build s3integration

// S3 live-integration test for a cloud (s3://) batch file-list: a --file-list whose
// entries are s3:// URIs is acquired through the cloud read boundary (session created
// because the cloud-need check reads the list). Run with `-tags s3integration`;
// shares the moto harness in extract_moto_test.go.

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMotoFileListCloudRefs extracts a batch whose --file-list contains s3:// inputs.
func TestMotoFileListCloudRefs(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aKey := runKeyPrefix() + "inputs/a.xml"
	bKey := runKeyPrefix() + "inputs/b.xml"
	m.putObject(t, aKey, []byte("<root><item><Name>A</Name></item></root>"))
	m.putObject(t, bKey, []byte("<root><item><Name>B</Name></item></root>"))
	aURI := "s3://" + m.bucket + "/" + aKey
	bURI := "s3://" + m.bucket + "/" + bKey

	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := createWorkingTempDir(t)
	for _, d := range []string{"signature", "extract", "outputs"} {
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
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: file_list_cloud
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    credentials_handle: reader
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  progress: false
`)
	// The file-list (local) carries the s3:// inputs.
	mustWriteFile(t, filepath.Join(ws, "cloud-inputs.list"), "# cloud batch\n"+aURI+"\n"+bURI+"\n")

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:           "recipe.yaml",
		FileList:               "cloud-inputs.list",
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
		Progress:               false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (cloud --file-list): %v", err)
	}

	// Both cloud inputs in the list must have produced an output (named per the
	// recipe pattern from each ref's basename: extract-a.xml.json, extract-b.xml.json).
	outs, err := filepath.Glob(filepath.Join(ws, "outputs", "extract-*.json"))
	if err != nil {
		t.Fatalf("glob outputs: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("got %d outputs %v, want 2 (one per cloud file-list entry)", len(outs), outs)
	}
	names := map[string]bool{}
	for _, p := range outs {
		body, rerr := os.ReadFile(p) // #nosec G304 - test-controlled output path
		if rerr != nil {
			t.Fatalf("read %s: %v", p, rerr)
		}
		if !strings.Contains(string(body), `"name"`) {
			t.Errorf("%s missing extracted field: %s", filepath.Base(p), body)
		}
		names[filepath.Base(p)] = true
	}
	for _, want := range []string{"extract-a.xml.json", "extract-b.xml.json"} {
		if !names[want] {
			t.Errorf("missing expected output %s; got %v", want, names)
		}
	}
}
