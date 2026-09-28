package extract

import (
	"reflect"
	"strings"
	"testing"
)

// Node-set array mappings keep their existing output: an attribute path yields
// the owning elements and a text() path yields the text nodes.
func TestNodeSetArrayOutputsUnchanged(t *testing.T) {
	doc, err := parseXMLDoc(strings.NewReader(`<root><rec><item a="1">t1</item><item a="2">t2</item></rec></root>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := &ExtractRecordMatch{
		RecordType:     "probe",
		MatchSelectors: []MatchSelector{{XPath: "//rec"}},
		FieldMappings: []FieldMapping{
			{OutputField: "attr_array", XPath: "item/@a", Type: "array"},
			{OutputField: "text_array", XPath: "item/text()", Type: "array"},
			{OutputField: "attr_items", XPath: "item/@a", Type: "array", ItemMapping: []FieldMapping{
				{OutputField: "v", XPath: ".", Type: "string"},
			}},
		},
	}
	if err := PrepareRecordMatch(cfg); err != nil {
		t.Fatalf("PrepareRecordMatch: %v", err)
	}
	records, err := extractRecords(doc, cfg, nil)
	if err != nil || len(records) != 1 {
		t.Fatalf("extractRecords: %v (%d records)", err, len(records))
	}
	data := records[0]

	want := map[string]interface{}{
		"attr_array": []interface{}{"t1", "t2"},
		"text_array": []interface{}{"t1", "t2"},
		"attr_items": []map[string]interface{}{{"v": "t1"}, {"v": "t2"}},
	}
	for field, expected := range want {
		if !reflect.DeepEqual(data[field], expected) {
			t.Fatalf("%s = %#v, want %#v", field, data[field], expected)
		}
	}
}
