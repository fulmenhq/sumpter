package json

import (
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStandardExamplesAreConformanceRows keeps the published node-model
// standard's examples in step with the conformance table.
func TestStandardExamplesAreConformanceRows(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join(fixtureDir(), "..", "..", "..", "..", "docs", "standards", "document-node-model.md"))
	if err != nil {
		t.Fatalf("read standard: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(fixtureDir(), "conformance.json"))
	if err != nil {
		t.Fatalf("read conformance table: %v", err)
	}
	var rows []conformanceRow
	if err := stdjson.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("conformance.json: %v", err)
	}
	known := map[string]bool{}
	for _, r := range rows {
		expr := r.XPath
		if expr == "" {
			expr = r.Eval
		}
		known[r.Fixture+"\x00"+expr] = true
	}

	text := string(doc)
	start := strings.Index(text, "<!-- conformance:begin -->")
	end := strings.Index(text, "<!-- conformance:end -->")
	if start < 0 || end < start {
		t.Fatal("standard lacks the conformance:begin/end block")
	}
	checked := 0
	for _, line := range strings.Split(text[start:end], "\n") {
		cells := strings.Split(line, "|")
		if len(cells) < 4 {
			continue
		}
		fixture := strings.TrimSpace(cells[1])
		expr := strings.Trim(strings.TrimSpace(cells[2]), "`")
		if !strings.HasSuffix(fixture, ".json") {
			continue
		}
		checked++
		if !known[fixture+"\x00"+expr] {
			t.Errorf("standard example (%s, %s) is not a conformance.json row", fixture, expr)
		}
	}
	if checked == 0 {
		t.Fatal("no examples found in the standard's conformance block")
	}
}
