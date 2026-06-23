package extract

import (
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
)

// TestInternalFieldVisibleInScopeNotEmitted is the focused seam test for
// derive-only (internal) external fields: an InternalField value must be visible
// to a field_mappings[].expression (so it can derive an emitted field) but must
// never appear in the emitted record body, while ordinary external fields still
// emit. Exercises the DOM record path (extractRecords) — its emission merge loop
// and buildExpressionScope.
func TestInternalFieldVisibleInScopeNotEmitted(t *testing.T) {
	doc, err := xmlquery.Parse(strings.NewReader(`<root><item><name>Alpha</name></item></root>`))
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	base := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		FieldMappings: []FieldMapping{
			{OutputField: "name", XPath: "name", Type: "string"},
			// Derives an emitted field FROM the internal capture, proving scope visibility.
			{OutputField: "grain_class", Expression: `grain == "unit" ? "fine" : "coarse"`, Type: "string"},
		},
	}
	cfg := CloneRecordMatch(base)
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare config: %v", err)
	}

	external := map[string]interface{}{
		"grain":   InternalField{Value: "unit"}, // derive-only: in scope, never emitted
		"site_id": "store-17",                   // ordinary external field: emitted
	}
	records, err := extractRecords(doc, cfg, external)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	rec := records[0]

	// The internal capture reached expression scope (the derived field proves it).
	if rec["grain_class"] != "fine" {
		t.Errorf("grain_class = %#v, want \"fine\" (internal capture must be visible in expression scope)", rec["grain_class"])
	}
	// ...but is NOT emitted into the record body.
	if v, ok := rec["grain"]; ok {
		t.Errorf("internal capture leaked into the emitted record: grain = %#v", v)
	}
	// An ordinary external field still emits.
	if rec["site_id"] != "store-17" {
		t.Errorf("site_id = %#v, want \"store-17\"", rec["site_id"])
	}
	// No InternalField wrapper escapes into the record.
	for k, v := range rec {
		if _, wrapped := v.(InternalField); wrapped {
			t.Errorf("record field %q is still wrapped in InternalField (wrapper leaked)", k)
		}
	}
}

// TestInternalFieldNotEmittedOnZeroRecordPath covers the zero-record branch of
// ExtractFieldsWithExternal, where external fields are returned as the record:
// internal captures must be dropped there too, ordinary fields kept.
func TestInternalFieldNotEmittedOnZeroRecordPath(t *testing.T) {
	doc, err := xmlquery.Parse(strings.NewReader(`<root></root>`)) // no //item -> zero records
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	base := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		FieldMappings:  []FieldMapping{{OutputField: "name", XPath: "name", Type: "string"}},
	}
	cfg := CloneRecordMatch(base)
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare config: %v", err)
	}

	external := map[string]interface{}{
		"grain":   InternalField{Value: "unit"},
		"site_id": "store-17",
	}
	fields, err := ExtractFieldsWithExternal(doc, cfg, external)
	if err != nil {
		t.Fatalf("ExtractFieldsWithExternal: %v", err)
	}
	if _, ok := fields["grain"]; ok {
		t.Errorf("internal capture leaked on the zero-record path: %#v", fields["grain"])
	}
	if fields["site_id"] != "store-17" {
		t.Errorf("ordinary external field missing on zero-record path: %#v", fields)
	}
}
