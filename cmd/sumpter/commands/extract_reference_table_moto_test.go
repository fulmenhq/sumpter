//go:build s3integration

// S3 live-integration test for cloud (s3://) reference-table sources: a recipe
// declares a reference table whose source is an s3:// object read via a named
// credential handle. Excluded from the default/CI build; run with
// `-tags s3integration` (shares the moto harness in extract_moto_test.go).

package commands

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// writeCloudReferenceWorkspace builds a recipe with LOCAL inputs/output but a CLOUD
// (s3://) reference table (a key→value lookup), declared with a credential handle.
// maxBytes (when > 0) is written as the table's max_bytes cap.
func writeCloudReferenceWorkspace(t *testing.T, moleculeURI, handle string, maxBytes int) string {
	t.Helper()
	ws := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(ws, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	maxBytesLine := ""
	if maxBytes > 0 {
		maxBytesLine = "      max_bytes: " + strconv.Itoa(maxBytes) + "\n"
	}
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: reftable_cloud
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/nm.xml
      - testdata/xr.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  reference_tables:
    - name: molecule
      source: `+moleculeURI+`
      credentials_handle: `+handle+`
      format: csv
      header: true
      key_column: accession
      value_column: molecule_type
      max_rows: 100
`+maxBytesLine+`  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(ws, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: reftable_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: accession
    xpath: Accession
    type: string
  - output_field: molecule_type
    expression: "lookup_reference('molecule', accession, 'unknown')"
    type: string
output_schema:
  type: object
  properties:
    accession:
      type: string
    molecule_type:
      type: string
`)
	mustWriteFile(t, filepath.Join(ws, "testdata", "nm.xml"),
		"<root><item><Accession>NM_000546</Accession></item></root>")
	mustWriteFile(t, filepath.Join(ws, "testdata", "xr.xml"),
		"<root><item><Accession>XR_999999</Accession></item></root>")
	return ws
}

// TestMotoReferenceTableCloudSource resolves a cloud reference table end to end and
// asserts the provenance sidecar records the s3:// source + handle name with no row
// values leaked. "mRNA"/"ncRNA" exist only in the cloud table, so they are the
// no-leak probes.
func TestMotoReferenceTableCloudSource(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	molKey := runKeyPrefix() + "refdata/molecule.csv"
	m.putObject(t, molKey, []byte("accession,molecule_type\nNM_000546,mRNA\nNR_001234,ncRNA\n"))
	molURI := "s3://" + m.bucket + "/" + molKey

	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")
	ws := writeCloudReferenceWorkspace(t, molURI, "reader", 0)

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:    "recipe.yaml",
		CredentialsPath: credPath,
		Progress:        false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (cloud reference table): %v", err)
	}

	// The cloud lookup resolved for the hit and the declared default for the miss.
	if got := readReferenceRecord(t, ws, "nm")["molecule_type"]; got != "mRNA" {
		t.Errorf("nm molecule_type = %#v, want mRNA", got)
	}
	if got := readReferenceRecord(t, ws, "xr")["molecule_type"]; got != "unknown" {
		t.Errorf("xr molecule_type = %#v, want unknown", got)
	}

	manifestPath := filepath.Join(ws, "outputs", provenance.ManifestFileName)
	manifest := readManifest(t, manifestPath)
	if len(manifest.ReferenceTables) != 1 {
		t.Fatalf("manifest.ReferenceTables len = %d, want 1", len(manifest.ReferenceTables))
	}
	rt := manifest.ReferenceTables[0]
	if rt.Source != molURI {
		t.Errorf("provenance source = %q, want logical s3 URI %q", rt.Source, molURI)
	}
	if rt.CredentialsHandle != "reader" {
		t.Errorf("provenance credentials_handle = %q, want reader (logical handle name)", rt.CredentialsHandle)
	}
	if rt.Mode != "lookup" || rt.RowCount != 2 || !strings.HasPrefix(rt.ContentSHA256, "sha256:") {
		t.Errorf("provenance metrics wrong: %#v", rt)
	}

	// No-leak: neither the sidecar nor the staging path retain row values, and the
	// staged file is cleaned up.
	raw, err := os.ReadFile(manifestPath) // #nosec G304 - test-owned temp path
	if err != nil {
		t.Fatalf("read manifest bytes: %v", err)
	}
	for _, secret := range []string{"mRNA", "ncRNA"} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("sidecar leaked reference-table value %q", secret)
		}
	}
	if entries, _ := filepath.Glob(filepath.Join(home, "work", "cloud", "*")); len(entries) != 0 {
		t.Errorf("staging directory not cleaned up: %v", entries)
	}
}

// TestMotoReferenceTableCloudSizeCap proves the C2 staging-disk DoS guard: an object
// larger than the table's max_bytes is rejected before/without filling staging disk.
func TestMotoReferenceTableCloudSizeCap(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	// A well-formed table whose byte size exceeds the tiny cap below.
	body := "accession,molecule_type\n"
	for i := 0; i < 50; i++ {
		body += "NM_" + strconv.Itoa(i) + ",mRNA\n"
	}
	molKey := runKeyPrefix() + "refdata/big.csv"
	m.putObject(t, molKey, []byte(body))
	molURI := "s3://" + m.bucket + "/" + molKey

	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")
	ws := writeCloudReferenceWorkspace(t, molURI, "reader", 16) // 16-byte cap << object

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:    "recipe.yaml",
		CredentialsPath: credPath,
		Progress:        false,
	})
	if err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("err = %v, want size-cap rejection before staging", err)
	}
	// Nothing should have been staged.
	if entries, _ := filepath.Glob(filepath.Join(home, "work", "cloud", "*", "*")); len(entries) != 0 {
		t.Errorf("oversized object left staged files: %v", entries)
	}
}
