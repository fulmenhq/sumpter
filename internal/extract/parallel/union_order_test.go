package parallel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

const indexedUnionOrderNSXML = `<?xml version="1.0" encoding="UTF-8"?>
<Ledger xmlns="urn:example:sumpter-records" xmlns:n1="urn:example:sumpter-union-n1" xmlns:n2="urn:example:sumpter-union-n2">
  <Record id="R-1">
    <n1:Entry>A1</n1:Entry>
    <n2:Entry>B1</n2:Entry>
    <n1:Entry>A2</n1:Entry>
  </Record>
  <Record id="R-2">
    <n2:Entry>B2</n2:Entry>
    <n1:Entry>A3</n1:Entry>
  </Record>
</Ledger>
`

// TestIndexedParallelUnionDocumentOrder verifies end-to-end record indexing +
// parallel extraction preserves document order on XPath unions (including
// namespace-bound unions inside indexed records).
func TestIndexedParallelUnionDocumentOrder(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "union-ns.xml")
	if err := os.WriteFile(xmlPath, []byte(indexedUnionOrderNSXML), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	indexPath := buildNamespaceTestIndex(t, xmlPath)

	ns := map[string]string{
		"n":  testCoreURI,
		"n1": "urn:example:sumpter-union-n1",
		"n2": "urn:example:sumpter-union-n2",
	}
	cfg := loadNamespaceExtractConfig(t, ns, []extract.FieldMapping{
		{OutputField: "id", XPath: "@id", Type: "string"},
		{OutputField: "first_n2_n1", XPath: "(n2:Entry | n1:Entry)[1]", Type: "string"},
		{OutputField: "second_n1_n2", XPath: "(n1:Entry | n2:Entry)[2]", Type: "string"},
		{OutputField: "second_n2_n1", XPath: "(n2:Entry | n1:Entry)[2]", Type: "string"},
		{OutputField: "string_n2_n1", XPath: "string(n2:Entry | n1:Entry)", Type: "string"},
		{OutputField: "entries", XPath: "n2:Entry | n1:Entry", Type: "array"},
	})

	records, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    xmlPath,
		Workers:       2,
		ExtractConfig: cfg,
		VerifyIndex:   true,
		ReorderWindow: 2,
	}).Extract()
	if err != nil {
		t.Fatalf("parallel extract: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}

	r1 := envelopeData(t, records[0])
	want1 := map[string]interface{}{
		"id":           "R-1",
		"first_n2_n1":  "A1",
		"second_n1_n2": "B1",
		"second_n2_n1": "B1",
		"string_n2_n1": "A1",
		"entries":      []interface{}{"A1", "B1", "A2"},
	}
	for k, w := range want1 {
		if !reflect.DeepEqual(r1[k], w) {
			t.Errorf("record 1 %s = %#v, want %#v", k, r1[k], w)
		}
	}

	r2 := envelopeData(t, records[1])
	want2 := map[string]interface{}{
		"id":           "R-2",
		"first_n2_n1":  "B2",
		"second_n1_n2": "A3",
		"second_n2_n1": "A3",
		"string_n2_n1": "B2",
		"entries":      []interface{}{"B2", "A3"},
	}
	for k, w := range want2 {
		if !reflect.DeepEqual(r2[k], w) {
			t.Errorf("record 2 %s = %#v, want %#v", k, r2[k], w)
		}
	}
}
