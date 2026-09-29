package extract

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

func parseJSONDoc(t *testing.T, src string) docnode.Document {
	t.Helper()
	format, ok := docnode.Lookup(FormatJSON)
	if !ok {
		t.Fatal("json format not registered")
	}
	doc, err := format.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func preparedConfig(t *testing.T, cfg *ExtractRecordMatch) *ExtractRecordMatch {
	t.Helper()
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return cfg
}

func TestJSONNullAndEmptyArrayBindAbsent(t *testing.T) {
	doc := parseJSONDoc(t, `{"Data":{"Order":[{"id":"a","note":null,"tags":[]},{"id":"b","note":"x","tags":["t1",null,"t2"]}]}}`)
	cfg := preparedConfig(t, &ExtractRecordMatch{
		RecordType:     "order",
		MatchSelectors: []MatchSelector{{XPath: "//Order"}},
		FieldMappings: []FieldMapping{
			{OutputField: "id", XPath: "id", Type: "string"},
			{OutputField: "note", XPath: "note", Type: "string"},
			{OutputField: "tags", XPath: "tags", Type: "array"},
		},
	})
	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	if v, ok := records[0]["note"]; ok && v != nil {
		t.Errorf("null note bound %#v, want absent", v)
	}
	if v, ok := records[0]["tags"]; ok && v != nil {
		t.Errorf("empty array bound %#v, want absent", v)
	}
	if records[1]["note"] != "x" {
		t.Errorf("note = %#v, want x", records[1]["note"])
	}
	tags, _ := records[1]["tags"].([]interface{})
	if len(tags) != 2 || tags[0] != "t1" || tags[1] != "t2" {
		t.Errorf("tags = %#v, want [t1 t2] with the null item dropped", records[1]["tags"])
	}
}

func TestJSONNumberLexemesSurviveAsText(t *testing.T) {
	doc := parseJSONDoc(t, `{"R":[{"big":9007199254740993,"e":1e3,"d":2.50}]}`)
	cfg := preparedConfig(t, &ExtractRecordMatch{
		RecordType:     "r",
		MatchSelectors: []MatchSelector{{XPath: "//R"}},
		FieldMappings: []FieldMapping{
			{OutputField: "big", XPath: "big", Type: "string"},
			{OutputField: "e", XPath: "e", Type: "string"},
			{OutputField: "d", XPath: "d", Type: "string"},
		},
	})
	records, err := extractRecords(doc, cfg, nil)
	if err != nil || len(records) != 1 {
		t.Fatalf("extract: %v, %d records", err, len(records))
	}
	for field, want := range map[string]string{"big": "9007199254740993", "e": "1e3", "d": "2.50"} {
		if records[0][field] != want {
			t.Errorf("%s = %#v, want %q", field, records[0][field], want)
		}
	}
}

func polymorphicEventsConfig() *ExtractRecordMatch {
	return &ExtractRecordMatch{
		RecordType:     "order",
		MatchSelectors: []MatchSelector{{XPath: "//Order"}},
		FieldMappings: []FieldMapping{{
			OutputField: "events",
			XPath:       "Events",
			Type:        "array",
			Polymorphic: []PolymorphicMapping{
				{ItemType: "order_line", ElementType: "OrderLine", FieldMappings: []FieldMapping{{OutputField: "sku", XPath: "sku"}}},
				{ItemType: "return_line", ElementType: "ReturnLine", FieldMappings: []FieldMapping{{OutputField: "sku", XPath: "sku"}}},
			},
		}},
	}
}

func TestJSONPolymorphicWrapperIdiom(t *testing.T) {
	doc := parseJSONDoc(t, `{"D":{"Order":{"Events":[{"OrderLine":{"sku":"A"}},{"ReturnLine":{"sku":"R"}}]}}}`)
	records, err := extractRecords(doc, preparedConfig(t, polymorphicEventsConfig()), nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	events, _ := records[0]["events"].([]map[string]interface{})
	if len(events) != 2 || events[0]["item_type"] != "order_line" || events[1]["sku"] != "R" {
		t.Fatalf("events = %#v", records[0]["events"])
	}
}

func TestJSONPolymorphicBareArrayFails(t *testing.T) {
	doc := parseJSONDoc(t, `{"D":{"Order":{"Events":[{"sku":"A"},{"sku":"R"}]}}}`)
	_, err := extractRecords(doc, preparedConfig(t, polymorphicEventsConfig()), nil)
	if err == nil {
		t.Fatal("bare array under element_type extracted without error")
	}
	for _, part := range []string{"no polymorphic branch matched 2 items", "wrapper idiom"} {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q lacks %q", err, part)
		}
	}
}

func TestXMLPolymorphicUnmatchedStaysEmpty(t *testing.T) {
	doc, err := parseXMLDoc(strings.NewReader(`<D><Order><Events><Line sku="A"/></Events></Order></D>`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	records, err := extractRecords(doc, preparedConfig(t, polymorphicEventsConfig()), nil)
	if err != nil {
		t.Fatalf("xml unmatched polymorphic errored: %v", err)
	}
	if v, ok := records[0]["events"]; ok && v != nil {
		t.Fatalf("events = %#v, want absent", v)
	}
}

func jsonProcessConfigs() (*FileSignature, *ExtractRecordMatch) {
	sig := &FileSignature{
		SignatureID:         "j",
		FormatType:          FormatJSON,
		ConfidenceThreshold: 1,
		MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/D", Weight: 1}},
	}
	ext := &ExtractRecordMatch{
		RecordType:     "order",
		MatchSelectors: []MatchSelector{{XPath: "//Order"}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "id", Type: "string"}},
	}
	return sig, ext
}

func writeTempFile(t *testing.T, name, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func TestJSONProcessFileEndToEnd(t *testing.T) {
	sig, ext := jsonProcessConfigs()
	path := writeTempFile(t, "in.json", `{"D":{"Order":[{"id":"1"},{"id":"2"}]}}`)
	res := ProcessFile(path, sig, ext, nil, false)
	if res.Error != nil {
		t.Fatalf("ProcessFile: %v", res.Error)
	}
	if len(res.Records) != 2 {
		t.Fatalf("records = %d, want 2", len(res.Records))
	}
}

func TestJSONParseFailuresEmitZeroRecords(t *testing.T) {
	deep := strings.Repeat(`{"a":`, 1100) + "1" + strings.Repeat("}", 1100)
	for name, src := range map[string]string{
		"malformed":       `{"D":{"Order":[{"id":"1"},{"id":]}}`,
		"duplicate key":   `{"D":{"Order":[{"id":"1"},{"id":"2","id":"3"}]}}`,
		"invalid utf-8":   "{\"D\":{\"Order\":[{\"id\":\"1\"},{\"id\":\"a\xffb\"}]}}",
		"second value":    `{"D":{"Order":[{"id":"1"}]}} {"D":{}}`,
		"truncated":       `{"D":{"Order":[{"id":"1"},{"id":"2"`,
		"over-cap depth":  `{"D":{"Order":[{"id":"1"}],"x":` + deep + `}}`,
		"top-level value": `42`,
	} {
		t.Run(name, func(t *testing.T) {
			sig, ext := jsonProcessConfigs()
			res := ProcessFile(writeTempFile(t, "bad.json", src), sig, ext, nil, false)
			if res.Error == nil {
				t.Fatal("expected a parse error")
			}
			if len(res.Records) != 0 {
				t.Fatalf("emitted %d records despite parse failure", len(res.Records))
			}
			if strings.Contains(res.Error.Error(), "XML") {
				t.Fatalf("json parse error mentions XML: %v", res.Error)
			}
		})
	}
}

func TestJSONDuplicateKeyErrorNamesKeyAndOffset(t *testing.T) {
	sig, ext := jsonProcessConfigs()
	res := ProcessFile(writeTempFile(t, "dup.json", `{"D":{"Order":{"id":"1","id":"2"}}}`), sig, ext, nil, false)
	if res.Error == nil || !strings.Contains(res.Error.Error(), `duplicate key "id"`) || !strings.Contains(res.Error.Error(), "byte offset") {
		t.Fatalf("error = %v", res.Error)
	}
}

func TestJSONOverThresholdRequiresAllowLargeFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.json")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(101 * 1024 * 1024); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	sig, ext := jsonProcessConfigs()
	res := ProcessFile(path, sig, ext, nil, false)
	if !errors.Is(res.Error, docnode.ErrRouteUnsupported) {
		t.Fatalf("error = %v, want route unsupported", res.Error)
	}
	if !strings.Contains(res.Error.Error(), "--allow-large-files") {
		t.Fatalf("error %q does not name --allow-large-files", res.Error)
	}
	if len(res.Records) != 0 {
		t.Fatalf("emitted %d records", len(res.Records))
	}
	sig, ext = jsonProcessConfigs()
	res = ProcessFile(path, sig, ext, nil, true)
	if res.Error == nil || errors.Is(res.Error, docnode.ErrRouteUnsupported) {
		t.Fatalf("with --allow-large-files: error = %v, want a whole-document parse error", res.Error)
	}
}
