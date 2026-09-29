package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const jsonIndexedSignature = `signature_id: rows
name: Rows
match_patterns:
  - pattern_id: row
    name: row element
    selector: /rows
    weight: 1
confidence_threshold: 1
format_type: json
match_scope: record
`

const jsonIndexedExtract = `record_type: row
match_selectors:
  - xpath: //rows
field_mappings:
  - output_field: id
    xpath: id
    type: string
output_schema:
  type: object
  properties:
    id:
      type: string
  required: [id]
`

func jsonIndexedFixture(t *testing.T) (dir, src, sig, ext, idx string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src = filepath.Join(dir, "in.json")
	if err := os.WriteFile(src, []byte(`{"meta":{"n":4},"rows":[{"id":"a"},{"id":"b"},{"id":"c"},{"id":"d"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sig = filepath.Join(dir, "sig.yaml")
	ext = filepath.Join(dir, "ext.yaml")
	if err := os.WriteFile(sig, []byte(jsonIndexedSignature), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ext, []byte(jsonIndexedExtract), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "in")
	if _, err := runIndex(t, "build", src, "--input-format", "json", "--selector", "//rows", "--progress=false", "--output", base); err != nil {
		t.Fatal(err)
	}
	return dir, src, sig, ext, base + ".recordindex.json"
}

func TestExtractRecordIndexJSONMatchesSequential(t *testing.T) {
	dir, src, sig, ext, idx := jsonIndexedFixture(t)
	seq := filepath.Join(dir, "seq")
	par := filepath.Join(dir, "par")
	args := []string{"extract", "files", "--signature-config-path", sig, "--extract-config-path", ext, "--files", src}
	if err := runSumpter(t, append(append([]string{}, args...), "--output-path", seq)); err != nil {
		t.Fatalf("sequential: %v", err)
	}
	if err := runSumpter(t, append(append([]string{}, args...), "--output-path", par, "--record-index", idx, "--workers", "3")); err != nil {
		t.Fatalf("indexed: %v", err)
	}
	seqRows := recordData(t, seq, "extract-*.json")
	parRows := recordData(t, par, "extract-*.json")
	if len(seqRows) != 4 || len(parRows) != 4 {
		t.Fatalf("rows: sequential %d, indexed %d", len(seqRows), len(parRows))
	}
	for i := range seqRows {
		if seqRows[i]["id"] != parRows[i]["id"] {
			t.Fatalf("row %d: sequential %v, indexed %v", i, seqRows[i], parRows[i])
		}
	}
}

func TestExtractRecordIndexJSONRefusals(t *testing.T) {
	dir, src, sig, ext, idx := jsonIndexedFixture(t)
	docScope := filepath.Join(dir, "doc-sig.yaml")
	if err := os.WriteFile(docScope, []byte(strings.Replace(jsonIndexedSignature, "match_scope: record\n", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "out-doc")
	err := runSumpter(t, []string{"extract", "files", "--signature-config-path", docScope, "--extract-config-path", ext, "--files", src, "--record-index", idx, "--output-path", out})
	if err == nil || !strings.Contains(err.Error(), `declare "match_scope: record"`) {
		t.Fatalf("document scope: %v", err)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatalf("refused run created %s", out)
	}

	// A record whose bytes changed after the index was built fails the
	// input, and none of its rows are published.
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(strings.Replace(string(raw), `"id":"c"`, `"id":"x"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out = filepath.Join(dir, "out-late")
	err = runSumpter(t, []string{"extract", "files", "--signature-config-path", sig, "--extract-config-path", ext, "--files", src, "--record-index", idx, "--output-path", out, "--workers", "2"})
	if err == nil || !strings.Contains(err.Error(), "record 3 bytes do not match the index hash") {
		t.Fatalf("changed record: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(out, "extract-*.json")); len(matches) != 0 {
		t.Fatalf("failed input published %v", matches)
	}
	if _, serr := os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(serr) {
		t.Fatalf("failed input published a manifest: %v", serr)
	}
}
