package parallel

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

const indexedStringRegexNSXML = `<?xml version="1.0" encoding="UTF-8"?>
<Ledger xmlns="urn:example:sumpter-records">
  <Record id="R-1">
    <SKU>NM_001234.5</SKU>
    <RawLabel>  Alpha   Beta  </RawLabel>
    <FileName>report-2026.xml</FileName>
  </Record>
  <Record id="R-2">
    <SKU>XP_998877.1</SKU>
    <RawLabel>Gamma</RawLabel>
    <FileName>notes.txt</FileName>
  </Record>
</Ledger>
`

// TestIndexedParallelXPathStringAndRegex verifies end-to-end record indexing +
// parallel extraction evaluates the promised XPath string and regex functions.
func TestIndexedParallelXPathStringAndRegex(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "string-regex.xml")
	if err := os.WriteFile(xmlPath, []byte(indexedStringRegexNSXML), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	indexPath := buildNamespaceTestIndex(t, xmlPath)

	ns := map[string]string{
		"n": testCoreURI,
	}
	cfg := loadNamespaceExtractConfig(t, ns, []extract.FieldMapping{
		{OutputField: "id", XPath: "@id", Type: "string"},
		{OutputField: "sku_lower", XPath: "lower-case(n:SKU)", Type: "string"},
		{OutputField: "label_norm", XPath: "normalize-space(n:RawLabel)", Type: "string"},
		{OutputField: "is_xml_file", XPath: "ends-with(n:FileName, '.xml')", Type: "boolean"},
		{OutputField: "is_curated_sku", XPath: "matches(n:SKU, '^N[MR]_[0-9]+\\.[0-9]+$')", Type: "boolean"},
		{OutputField: "sku_stem", XPath: "replace(n:SKU, '^([A-Z]+_[0-9]+)\\.[0-9]+$', '$1')", Type: "string"},
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
		"id":             "R-1",
		"sku_lower":      "nm_001234.5",
		"label_norm":     "Alpha Beta",
		"is_xml_file":    true,
		"is_curated_sku": true,
		"sku_stem":       "NM_001234",
	}
	for k, w := range want1 {
		if !reflect.DeepEqual(r1[k], w) {
			t.Errorf("record 1 %s = %#v, want %#v", k, r1[k], w)
		}
	}

	r2 := envelopeData(t, records[1])
	want2 := map[string]interface{}{
		"id":             "R-2",
		"sku_lower":      "xp_998877.1",
		"label_norm":     "Gamma",
		"is_xml_file":    false,
		"is_curated_sku": false,
		"sku_stem":       "XP_998877",
	}
	for k, w := range want2 {
		if !reflect.DeepEqual(r2[k], w) {
			t.Errorf("record 2 %s = %#v, want %#v", k, r2[k], w)
		}
	}
}
