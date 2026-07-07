package parallel

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
)

const (
	testCoreURI = "urn:example:sumpter-records"
	testExtURI  = "urn:example:sumpter-records-ext"
)

func TestIndexedNamespaceContextResolvesAncestorDefaultNamespace(t *testing.T) {
	xmlPath := namespaceFixturePath(t, "default-ns.xml")
	indexPath := buildNamespaceTestIndex(t, xmlPath)
	cfg := loadNamespaceExtractConfig(t, map[string]string{"n": testCoreURI}, []extract.FieldMapping{
		{OutputField: "record_id", XPath: "@id", Type: "string"},
		{OutputField: "root_uri", XPath: "namespace-uri(.)", Type: "string"},
		{OutputField: "label", XPath: "n:Label", Type: "string"},
	})

	records, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    xmlPath,
		Workers:       2,
		ExtractConfig: cfg,
		VerifyIndex:   false,
		ReorderWindow: 2,
	}).Extract()
	if err != nil {
		t.Fatalf("parallel extract: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2: %v", len(records), records)
	}
	data := envelopeData(t, records[0])
	if got := data["record_id"]; got != "R-0001" {
		t.Errorf("record_id = %v, want R-0001; data=%v record=%v", got, data, records[0])
	}
	if got := data["root_uri"]; got != testCoreURI {
		t.Errorf("root_uri = %v, want %s; data=%v record=%v", got, testCoreURI, data, records[0])
	}
	if got := data["label"]; got != "Alpha" {
		t.Errorf("label = %v, want Alpha; data=%v record=%v", got, data, records[0])
	}
}

func TestIndexedNamespaceContextPreservesPrefixShadowing(t *testing.T) {
	xmlPath := namespaceFixturePath(t, filepath.Join("adversarial", "prefix-shadowing.xml"))
	indexPath := buildNamespaceTestIndex(t, xmlPath)
	cfg := loadNamespaceExtractConfig(t, map[string]string{"n": testCoreURI}, []extract.FieldMapping{
		{OutputField: "outer", XPath: "n:Label", Type: "string"},
		{OutputField: "shadowed", XPath: "n:Inner/n:Label", Type: "string"},
	})

	records, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    xmlPath,
		Workers:       1,
		ExtractConfig: cfg,
		VerifyIndex:   false,
	}).Extract()
	if err != nil {
		t.Fatalf("parallel extract: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1: %v", len(records), records)
	}
	data := envelopeData(t, records[0])
	if got := data["outer"]; got != "outer-core" {
		t.Errorf("outer = %v, want outer-core", got)
	}
	if _, ok := data["shadowed"]; ok {
		t.Fatalf("shadowed core-bound field should not match extension-rebound label: %v", data)
	}
}

func TestIndexedNamespaceContextRejectsStaleBoundIndex(t *testing.T) {
	cfg := loadNamespaceExtractConfig(t, map[string]string{"n": testCoreURI}, []extract.FieldMapping{
		{OutputField: "record_id", XPath: "@id", Type: "string"},
	})
	oldHeader := &index.RecordIndex{
		Version: index.LegacySchemaVersion,
		Source:  index.SourceInfo{OffsetKind: index.OffsetKindSourceBytes},
		Summary: index.SummaryStats{TotalRecords: 1},
	}

	_, err := NewParallelExtractor(ExtractionOptions{
		SourcePath:    namespaceFixturePath(t, "default-ns.xml"),
		Workers:       1,
		IndexStore:    &namespaceTestStore{header: oldHeader},
		ExtractConfig: cfg,
		VerifyIndex:   false,
		ReorderWindow: 1,
	}).Extract()
	if err == nil {
		t.Fatal("expected stale index error, got nil")
	}
	if !strings.Contains(err.Error(), "lacks namespace context") || !strings.Contains(err.Error(), "rebuild") {
		t.Fatalf("stale index error should give rebuild guidance, got: %v", err)
	}
}

func TestIndexedNamespaceFreeRecipeAllowsLegacyIndex(t *testing.T) {
	xmlPath := namespaceFixturePath(t, "default-ns.xml")
	cfg := loadNamespaceExtractConfig(t, nil, []extract.FieldMapping{
		{OutputField: "record_id", XPath: "@id", Type: "string"},
	})
	header := &index.RecordIndex{
		Version: index.LegacySchemaVersion,
		Source:  index.SourceInfo{OffsetKind: index.OffsetKindSourceBytes},
		Summary: index.SummaryStats{TotalRecords: 1},
	}
	records := []index.RecordMetadata{{
		RecordNum:   1,
		StartOffset: recordStartOffset(t, xmlPath),
		EndOffset:   recordEndOffset(t, xmlPath),
		SizeBytes:   recordEndOffset(t, xmlPath) - recordStartOffset(t, xmlPath),
	}}

	got, err := NewParallelExtractor(ExtractionOptions{
		SourcePath:    xmlPath,
		Workers:       1,
		IndexStore:    &namespaceTestStore{header: header, records: records},
		ExtractConfig: cfg,
		VerifyIndex:   false,
	}).Extract()
	if err != nil {
		t.Fatalf("legacy namespace-free extract should still work: %v", err)
	}
	if len(got) != 1 || envelopeData(t, got[0])["record_id"] != "R-0001" {
		t.Fatalf("legacy namespace-free extract got %v, want first record id", got)
	}
}

func TestIndexedNamespaceContextInjectionEscapesURI(t *testing.T) {
	xmlPath := namespaceFixturePath(t, filepath.Join("adversarial", "entity-escaped-uri.xml"))
	indexPath := buildNamespaceTestIndex(t, xmlPath)
	breakoutURI := `a"/><x>`
	cfg := loadNamespaceExtractConfig(t, map[string]string{"n": testCoreURI, "v": breakoutURI}, []extract.FieldMapping{
		{OutputField: "record_id", XPath: "@id", Type: "string"},
		{OutputField: "tag", XPath: "v:Tag", Type: "string"},
		{OutputField: "injected", XPath: "x", Type: "string"},
	})

	records, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    xmlPath,
		Workers:       1,
		ExtractConfig: cfg,
		VerifyIndex:   false,
	}).Extract()
	if err != nil {
		t.Fatalf("parallel extract with escaped URI: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("got %d records, want 1: %v", len(records), records)
	}
	data := envelopeData(t, records[0])
	if got := data["tag"]; got != "value-under-a-breakout-shaped-namespace-uri" {
		t.Errorf("tag = %v, want value-under-a-breakout-shaped-namespace-uri", got)
	}
	if _, ok := data["injected"]; ok {
		t.Fatalf("escaped namespace URI must not inject an x element: %v", data)
	}
}

func envelopeData(t *testing.T, record map[string]interface{}) map[string]interface{} {
	t.Helper()
	extractSection, ok := record["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("record missing extract section: %v", record)
	}
	data, ok := extractSection["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("record missing extract.data section: %v", record)
	}
	return data
}

func namespaceFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "tests", "fixtures", "namespace-conformance", name)
}

func buildNamespaceTestIndex(t *testing.T, xmlPath string) string {
	t.Helper()
	indexPath := filepath.Join(t.TempDir(), "records.recordindex.json")
	builder := index.NewBuilder(index.BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "test",
	})
	if _, err := builder.BuildTo(index.NewJSONIndexWriter(indexPath)); err != nil {
		t.Fatalf("build index: %v", err)
	}
	return indexPath
}

func loadNamespaceExtractConfig(t *testing.T, namespaces map[string]string, fields []extract.FieldMapping) *extract.ExtractRecordMatch {
	t.Helper()
	var b strings.Builder
	b.WriteString("record_type: ledger_record\n")
	if len(namespaces) > 0 {
		b.WriteString("namespaces:\n")
		for prefix, uri := range namespaces {
			b.WriteString("  ")
			b.WriteString(prefix)
			b.WriteString(": ")
			b.WriteString(strconvQuoteYAML(uri))
			b.WriteByte('\n')
		}
	}
	b.WriteString("match_selectors:\n  - xpath: \"//")
	if len(namespaces) > 0 {
		b.WriteString("n:")
	}
	b.WriteString("Record\"\n")
	b.WriteString("field_mappings:\n")
	for _, field := range fields {
		b.WriteString("  - output_field: ")
		b.WriteString(field.OutputField)
		b.WriteString("\n    xpath: ")
		b.WriteString(strconvQuoteYAML(field.XPath))
		b.WriteString("\n    type: ")
		b.WriteString(field.Type)
		b.WriteByte('\n')
	}
	b.WriteString("output_schema:\n  type: object\n  properties: {}\n")

	path := filepath.Join(t.TempDir(), "extract.yaml")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := extract.LoadExtractConfig(path)
	if err != nil {
		t.Fatalf("load config:\n%s\nerr: %v", b.String(), err)
	}
	return cfg
}

func strconvQuoteYAML(s string) string {
	quoted := strings.ReplaceAll(s, `\`, `\\`)
	quoted = strings.ReplaceAll(quoted, `"`, `\"`)
	return `"` + quoted + `"`
}

type namespaceTestStore struct {
	header  *index.RecordIndex
	records []index.RecordMetadata
}

func (s *namespaceTestStore) Header() (*index.RecordIndex, error) {
	header := *s.header
	return &header, nil
}

func (s *namespaceTestStore) Records(context.Context) (store.RecordIterator, error) {
	return &namespaceTestIterator{records: s.records}, nil
}

func (s *namespaceTestStore) Close() error { return nil }

type namespaceTestIterator struct {
	records []index.RecordMetadata
	next    int
}

func (it *namespaceTestIterator) Next() (*index.RecordMetadata, error) {
	if it.next >= len(it.records) {
		return nil, io.EOF
	}
	rec := it.records[it.next]
	it.next++
	return &rec, nil
}

func (it *namespaceTestIterator) Close() error { return nil }

func recordStartOffset(t *testing.T, path string) int64 {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	idx := strings.Index(string(data), "<Record")
	if idx < 0 {
		t.Fatalf("fixture has no <Record")
	}
	return int64(idx)
}

func recordEndOffset(t *testing.T, path string) int64 {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	idx := strings.Index(string(data), "</Record>")
	if idx < 0 {
		t.Fatalf("fixture has no </Record>")
	}
	return int64(idx + len("</Record>"))
}
