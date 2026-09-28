package extract_test

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

// Benchmarks for the document-access path on the streaming and DOM routes.
// They use only the file-level entry points so the same file measures any
// implementation of the node layer. The input is generated, not a fixture.

const seamBenchRecords = 20000

func writeSeamBenchXML(b *testing.B) string {
	b.Helper()
	path := filepath.Join(b.TempDir(), "seam-bench.xml")
	file, err := os.Create(path) // #nosec G304 -- generated file under b.TempDir
	if err != nil {
		b.Fatalf("create: %v", err)
	}
	w := bufio.NewWriterSize(file, 64*1024)
	if _, err := fmt.Fprint(w, `<?xml version="1.0" encoding="UTF-8"?><root>`); err != nil {
		b.Fatalf("write header: %v", err)
	}
	for i := 1; i <= seamBenchRecords; i++ {
		if _, err := fmt.Fprintf(w, `<record id="%06d"><name>item-%06d</name><value>%d</value>`+
			`<tags><tag>a%d</tag><tag>b%d</tag><tag>c%d</tag></tags>`+
			`<lines><line sku="s%d"><qty>%d</qty></line><line sku="t%d"><qty>%d</qty></line></lines></record>`,
			i, i, i, i, i, i, i, i, i, i+1); err != nil {
			b.Fatalf("write record %d: %v", i, err)
		}
	}
	if _, err := fmt.Fprint(w, `</root>`); err != nil {
		b.Fatalf("write footer: %v", err)
	}
	if err := w.Flush(); err != nil {
		b.Fatalf("flush: %v", err)
	}
	if err := file.Close(); err != nil {
		b.Fatalf("close: %v", err)
	}
	return path
}

func seamBenchSignature() *extract.FileSignature {
	return &extract.FileSignature{
		SignatureID:         "seam-bench",
		ConfidenceThreshold: 1.0,
		MatchPatterns:       []extract.MatchPattern{{PatternID: "root", Selector: "/root", Weight: 1.0}},
	}
}

func seamBenchExtractConfig(b *testing.B) *extract.ExtractRecordMatch {
	b.Helper()
	cfg := &extract.ExtractRecordMatch{
		RecordType:     "bench_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//record"}},
		FieldMappings: []extract.FieldMapping{
			{OutputField: "id", XPath: "@id", Type: "string"},
			{OutputField: "name", XPath: "name", Type: "string"},
			{OutputField: "value", XPath: "value", Type: "integer"},
			{OutputField: "tags", XPath: "tags/tag", Type: "array"},
			{OutputField: "lines", XPath: "lines/line", Type: "array", ItemMapping: []extract.FieldMapping{
				{OutputField: "sku", XPath: "@sku", Type: "string"},
				{OutputField: "qty", XPath: "qty", Type: "integer"},
			}},
		},
	}
	if err := extract.PrepareRecordMatch(cfg); err != nil {
		b.Fatalf("PrepareRecordMatch: %v", err)
	}
	return cfg
}

func BenchmarkExtractStreaming_20k(b *testing.B) {
	path := writeSeamBenchXML(b)
	sig := seamBenchSignature()
	cfg := seamBenchExtractConfig(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := extract.ProcessFileStreaming(path, sig, cfg, nil)
		if res.Error != nil || len(res.Records) != seamBenchRecords {
			b.Fatalf("streaming: err=%v records=%d", res.Error, len(res.Records))
		}
	}
}

func BenchmarkExtractDOM_20k(b *testing.B) {
	path := writeSeamBenchXML(b)
	sig := seamBenchSignature()
	cfg := seamBenchExtractConfig(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		res := extract.ProcessFile(path, sig, cfg, nil, false)
		if res.Error != nil || len(res.Records) != seamBenchRecords {
			b.Fatalf("DOM: err=%v records=%d", res.Error, len(res.Records))
		}
	}
}
