package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"
)

func TestSourceFormat(t *testing.T) {
	for _, tc := range []struct {
		version, format, want, err string
	}{
		{SchemaVersion, SourceFormatXML, SourceFormatXML, ""},
		{SchemaVersion, SourceFormatJSON, SourceFormatJSON, ""},
		{SchemaVersion, "", "", "requires source.format"},
		{SchemaVersion, "ndjson", "", `source.format "ndjson" is not supported`},
		{SzstStoreVersion, SourceFormatJSON, SourceFormatJSON, ""},
		{SzstStoreVersion, "", "", "requires source.format"},
		{LegacySchemaVersionV012, "", SourceFormatXML, ""},
		{LegacySchemaVersion, "", SourceFormatXML, ""},
		{LegacySchemaVersionV010, "", SourceFormatXML, ""},
		{LegacySzstStoreVersion, "", SourceFormatXML, ""},
		{LegacySzstStoreVersionV010, "", SourceFormatXML, ""},
		{LegacySchemaVersionV012, SourceFormatJSON, "", "predates source.format and is XML only"},
		{LegacySzstStoreVersion, SourceFormatJSON, "", "predates source.format and is XML only"},
		{LegacySchemaVersionV012, SourceFormatXML, "", "predates source.format and is XML only"},
		{"record-index/v9.9", SourceFormatXML, "", "unsupported index version"},
	} {
		got, err := SourceFormat(&RecordIndex{Version: tc.version, Source: SourceInfo{Format: tc.format}})
		if tc.err != "" {
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Errorf("%s/%q: error %v, want %q", tc.version, tc.format, err, tc.err)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("%s/%q: %q, %v; want %q", tc.version, tc.format, got, err, tc.want)
		}
	}
}

func TestWritersRefuseMissingFormat(t *testing.T) {
	idx := &RecordIndex{Source: SourceInfo{Path: "x", SHA256: strings.Repeat("0", 64)}, Selector: SelectorInfo{XPath: "r", ElementName: "r"}}
	if err := NewBuilder(BuildOptions{}).WriteToFile(idx, filepath.Join(t.TempDir(), "i.json")); err == nil || !strings.Contains(err.Error(), "requires source.format") {
		t.Fatalf("WriteToFile: %v", err)
	}
	w := NewJSONIndexWriter(filepath.Join(t.TempDir(), "j.json"))
	defer func() { _ = w.Close() }()
	if err := w.Start(idx); err == nil || !strings.Contains(err.Error(), "requires source.format") {
		t.Fatalf("JSONIndexWriter.Start: %v", err)
	}
}

// TestBuiltXMLIndexMatchesV013Schema builds an XML index and validates it
// against the v0.1.3 schema.
func TestBuiltXMLIndexMatchesV013Schema(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "in.xml")
	if err := os.WriteFile(src, []byte(`<root><r a="1"/><r a="2"/></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "in.recordindex.json")
	b := NewBuilder(BuildOptions{InputPath: src, Selector: "//r", EmitJSON: true, OutputPath: out})
	idx, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.WriteToFile(idx, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["version"] != SchemaVersion || doc["source"].(map[string]any)["format"] != SourceFormatXML {
		t.Fatalf("header %v / %v", doc["version"], doc["source"])
	}
	schema, err := os.ReadFile("../../schemas/index/v0.1.3/record-index.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	res, err := goneatschema.ValidateFromBytes(schema, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("index does not match v0.1.3 schema: %+v", res.Errors)
	}
}

// TestV013XMLHeaderGolden builds an XML index over a namespaced fixture and
// pins what v0.1.3 changes (version, source.format, the empty context as an
// array) and what it does not (non-empty declarations and every record, byte
// for byte as the previous release wrote them).
func TestV013XMLHeaderGolden(t *testing.T) {
	src := "../../tests/fixtures/namespace-conformance/dual.xml"
	b := NewBuilder(BuildOptions{InputPath: src, Selector: "//Record"})
	idx, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "dual.recordindex.json")
	if err := b.WriteToFile(idx, out); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Version           string            `json:"version"`
		Source            map[string]any    `json:"source"`
		NamespaceContexts []json.RawMessage `json:"namespace_contexts"`
		Records           []json.RawMessage `json:"records"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	compact := func(m json.RawMessage) string {
		var v any
		_ = json.Unmarshal(m, &v)
		b, _ := json.Marshal(v)
		return string(b)
	}
	if doc.Version != SchemaVersion || doc.Source["format"] != SourceFormatXML {
		t.Fatalf("header %s %v", doc.Version, doc.Source["format"])
	}
	if len(doc.NamespaceContexts) != 2 || compact(doc.NamespaceContexts[0]) != `{"declarations":[],"id":0}` {
		t.Fatalf("empty context %s", doc.NamespaceContexts)
	}
	// Previous-release bytes for the non-empty context and the records.
	const wantContext = `{"declarations":[{"prefix":"","uri":"urn:example:sumpter-records"},{"prefix":"ext","uri":"urn:example:sumpter-records-ext"}],"id":1}`
	if got := compact(doc.NamespaceContexts[1]); got != wantContext {
		t.Fatalf("non-empty context changed:\n got %s\nwant %s", got, wantContext)
	}
	wantRecords := []string{
		`{"depth":2,"element_name":"Record","end_offset":411,"namespace_context_ref":1,"record_num":1,"sha256":"9e10bea3395e29723b12a57b8ee57065871cd2494e2d765837a44cb38ae3e861","size_bytes":320,"start_offset":91}`,
		`{"depth":2,"element_name":"Record","end_offset":732,"namespace_context_ref":1,"record_num":2,"sha256":"12270d1a0a9b5dd89fcd5bce0750cce1641ff0b7ee32360044affe51f2f3102d","size_bytes":318,"start_offset":414}`,
	}
	if len(doc.Records) != len(wantRecords) {
		t.Fatalf("%d records", len(doc.Records))
	}
	for i, r := range doc.Records {
		if got := compact(r); got != wantRecords[i] {
			t.Fatalf("record %d changed:\n got %s\nwant %s", i+1, got, wantRecords[i])
		}
	}
}

// TestLegacyNullContextStillReads reads a v0.1.2 index whose empty context
// was written as null, as earlier releases wrote it.
func TestLegacyNullContextStillReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.recordindex.json")
	legacy := `{"version":"record-index/v0.1.2","source":{"path":"in.xml","size_bytes":10,"sha256":"` + strings.Repeat("a", 64) + `","compressed":false,"offset_kind":"source_bytes","created_at":"2026-01-01T00:00:00Z"},"selector":{"xpath":"//r","element_name":"r"},"namespace_contexts":[{"id":0,"declarations":null}],"records":[],"summary":{"total_records":0,"total_bytes":0,"avg_record_size_bytes":0,"min_record_size_bytes":0,"max_record_size_bytes":0},"metadata":{"generator":"test"}}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	idx, err := LoadIndex(path)
	if err != nil {
		t.Fatalf("legacy null context refused: %v", err)
	}
	if format, err := SourceFormat(idx); err != nil || format != SourceFormatXML {
		t.Fatalf("format %q, %v", format, err)
	}
	if ctx := NamespaceContextByID(idx.NamespaceContexts); len(ctx[0]) != 0 {
		t.Fatalf("context 0 = %v", ctx[0])
	}
}

// TestCurrentHeaderNullContextRefused refuses a v0.1.3 header whose empty
// context is written as null: this release never writes one, and readers do
// not repair invalid new input.
func TestCurrentHeaderNullContextRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new.recordindex.json")
	body := `{"version":"record-index/v0.1.3","source":{"path":"in.json","size_bytes":10,"sha256":"` + strings.Repeat("a", 64) + `","compressed":false,"offset_kind":"source_bytes","format":"json","created_at":"2026-01-01T00:00:00Z"},"selector":{"xpath":"//r","element_name":"r"},"namespace_contexts":[{"id":0,"declarations":null}],"records":[],"summary":{"total_records":0,"total_bytes":0,"avg_record_size_bytes":0,"min_record_size_bytes":0,"max_record_size_bytes":0},"metadata":{"generator":"test"}}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIndex(path); err == nil || !strings.Contains(err.Error(), "has no declarations array") {
		t.Fatalf("LoadIndex: %v", err)
	}
}
