package extract

import (
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
)

// empty-element-bind (v0.2.1): a present-but-empty XML element binds its string field
// as "" (a defined value) so the boolean-guard-then-reference recipe pattern no longer
// hard-aborts, while an absent element stays undefined.

func eebParse(t *testing.T, xml string) *xmlquery.Node {
	t.Helper()
	doc, err := xmlquery.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func eebExtract(t *testing.T, doc *xmlquery.Node, cfg *ExtractRecordMatch, ext map[string]interface{}) []map[string]interface{} {
	t.Helper()
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	recs, err := extractRecords(doc, cfg, ext)
	if err != nil {
		t.Fatalf("extractRecords hard-aborted (the bug): %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("records = %d, want 1", len(recs))
	}
	return recs
}

// guardCfg is the natural boolean-guard-then-reference pattern from the field report.
func guardCfg() *ExtractRecordMatch {
	return &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "has_accession", XPath: "boolean(Variant/Accession)", Type: "boolean"},
			{OutputField: "accession", XPath: "Variant/Accession", Type: "string"},
			{OutputField: "is_curated", Type: "boolean",
				Expression: "has_accession ? (string_length(accession) >= 5 && starts_with_any(accession, curated_prefixes)) : false"},
		},
	}
}

// TestEmptyElementBindsAsEmptyString covers the present-but-empty element variants:
// self-closing, open-close, and whitespace-only (which trims to ""). In every case the
// boolean guard sees the node present, the string field binds "", and the ternary
// evaluates without a hard abort (string_length("") >= 5 is false → not curated).
func TestEmptyElementBindsAsEmptyString(t *testing.T) {
	ext := map[string]interface{}{"curated_prefixes": []string{"NM_", "NR_"}}
	for _, tc := range []struct{ name, content string }{
		{"self_closing", "<Accession/>"},
		{"open_close", "<Accession></Accession>"},
		{"whitespace_only", "<Accession>   </Accession>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := eebParse(t, "<Envelope><Record><Variant>"+tc.content+"</Variant></Record></Envelope>")
			rec := eebExtract(t, doc, guardCfg(), ext)[0]
			if rec["has_accession"] != true {
				t.Errorf("has_accession = %#v, want true (node present)", rec["has_accession"])
			}
			if rec["accession"] != "" {
				t.Errorf("accession = %#v, want \"\" (present-empty binds empty string)", rec["accession"])
			}
			if rec["is_curated"] != false {
				t.Errorf("is_curated = %#v, want false", rec["is_curated"])
			}
		})
	}
}

// TestEmptyElementBindAbsentStaysUndefined pins the other half of the contract: an
// ABSENT element is not bound — boolean(node) is false, the string field is omitted,
// and the ternary guard is still the correct pattern for maybe-absent fields (it takes
// the false branch and never references the undefined variable).
func TestEmptyElementBindAbsentStaysUndefined(t *testing.T) {
	ext := map[string]interface{}{"curated_prefixes": []string{"NM_", "NR_"}}
	doc := eebParse(t, "<Envelope><Record><Variant></Variant></Record></Envelope>")
	rec := eebExtract(t, doc, guardCfg(), ext)[0]
	if rec["has_accession"] != false {
		t.Errorf("has_accession = %#v, want false (node absent)", rec["has_accession"])
	}
	if _, present := rec["accession"]; present {
		t.Errorf("absent element should stay undefined/omitted, got accession = %#v", rec["accession"])
	}
	if rec["is_curated"] != false {
		t.Errorf("is_curated = %#v, want false", rec["is_curated"])
	}
}

// TestEmptyElementBindAbsentUnguardedStillErrors proves absent remains undefined: an
// UNGUARDED reference to an absent field still fails loud (the guard is required for
// maybe-absent fields) — the fix did not turn absence into "".
func TestEmptyElementBindAbsentUnguardedStillErrors(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "accession", XPath: "Variant/Accession", Type: "string"},
			{OutputField: "len", Expression: "string_length(accession)", Type: "integer"},
		},
	}
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	doc := eebParse(t, "<Envelope><Record><Variant></Variant></Record></Envelope>")
	_, err := extractRecords(doc, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "undefined variable") {
		t.Fatalf("err = %v, want undefined-variable for an unguarded absent reference", err)
	}
}

// TestEmptyElementBindExpressionResultEmptyString covers the shared coerceString
// contract on the expression path: a type:string expression that evaluates to "" binds
// "" too (not nil).
func TestEmptyElementBindExpressionResultEmptyString(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "accession", XPath: "Variant/Accession", Type: "string"},
			{OutputField: "derived", Expression: "lower(accession)", Type: "string"},
		},
	}
	doc := eebParse(t, "<Envelope><Record><Variant><Accession/></Variant></Record></Envelope>")
	rec := eebExtract(t, doc, cfg, nil)[0]
	if rec["accession"] != "" {
		t.Errorf("accession = %#v, want \"\"", rec["accession"])
	}
	if rec["derived"] != "" {
		t.Errorf("derived (type:string expression result) = %#v, want \"\"", rec["derived"])
	}
}
