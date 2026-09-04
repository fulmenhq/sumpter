package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFixtureDocumentRecipe(t *testing.T) string {
	t.Helper()
	ws := t.TempDir()
	for _, d := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: fixture-records
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
`)
	mustWriteFile(t, filepath.Join(ws, "signature/signature.yaml"), `signature_id: fulseed-fixture-document-v1
name: FixtureDocument v1
match_patterns:
  - pattern_id: root
    name: FixtureDocument root
    selector: /FixtureDocument[@schemaVersion='1']
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract/extract.yaml"), `record_type: fixture_record
match_selectors:
  - xpath: "//Record[@enabled='1' or @enabled='true']"
field_mappings:
  - output_field: record_id
    xpath: RecordId
    type: string
output_schema:
  type: object
  properties:
    record_id:
      type: string
  required: [record_id]
`)
	return ws
}

func TestExtractMultiDenseFixtureDocumentWithinBundle(t *testing.T) {
	const n = 2000
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<FixtureDocument schemaVersion="1"><Header><RecordCount>`)
	fmt.Fprintf(&b, "%d", n)
	b.WriteString(`</RecordCount></Header>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, `<Record ordinal="%d" enabled="1"><RecordId>r-%06d</RecordId></Record>`, i, i)
	}
	b.WriteString(`</FixtureDocument>`)
	dir := t.TempDir()
	in := filepath.Join(dir, "dense.xml")
	if err := os.WriteFile(in, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	list := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(list, []byte(in+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := writeFixtureDocumentRecipe(t)
	out := t.TempDir()
	shared := &multiSharedOptions{
		FileList:   list,
		OutputPath: out,
		OutputMode: outputModeAggregate,
		RunID:      testMultiRunID,
	}
	if err := runExtractMulti(shared, []string{ws}, os.Stderr, time.Now()); err != nil {
		t.Fatal(err)
	}
	records, err := os.ReadFile(filepath.Join(out, "fixture-records", "records.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(records), "\n"); got != n {
		t.Fatalf("records %d want %d", got, n)
	}
}
