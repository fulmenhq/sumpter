package index

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRecordIndexStream_HeaderAndIterate(t *testing.T) {
	tmpDir := t.TempDir()

	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<root>
	<Record id="1"><Value>100</Value></Record>
	<Record id="2"><Value>200</Value></Record>
	<Record id="3"><Value>300</Value></Record>
</root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		IncludeP50:     true,
		IncludeP95:     true,
		IncludeP99:     true,
		SumpterVersion: "0.1.2-test",
	}

	builder := NewBuilder(buildOpts)
	idx, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "test.recordindex.json")
	if err := builder.WriteToFile(idx, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	stream, err := OpenRecordIndexStream(indexPath)
	if err != nil {
		t.Fatalf("OpenRecordIndexStream failed: %v", err)
	}
	defer func() { _ = stream.Close() }()

	header, err := stream.Header()
	if err != nil {
		t.Fatalf("Header failed: %v", err)
	}

	if header.Version != SchemaVersion {
		t.Fatalf("Expected version %q, got %q", SchemaVersion, header.Version)
	}
	if header.Source.Path != xmlPath {
		t.Fatalf("Expected source path %q, got %q", xmlPath, header.Source.Path)
	}
	if header.Selector.XPath != "//Record" {
		t.Fatalf("Expected selector %q, got %q", "//Record", header.Selector.XPath)
	}

	var records []RecordMetadata
	for {
		rec, err := stream.NextRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRecord failed: %v", err)
		}
		records = append(records, *rec)
	}

	if len(records) != 3 {
		t.Fatalf("Expected 3 records, got %d", len(records))
	}
	for i, rec := range records {
		expected := i + 1
		if rec.RecordNum != expected {
			t.Fatalf("Expected record_num %d, got %d", expected, rec.RecordNum)
		}
		if rec.SizeBytes <= 0 {
			t.Fatalf("Expected positive size_bytes for record %d", rec.RecordNum)
		}
		if rec.SHA256 == "" {
			t.Fatalf("Expected sha256 for record %d", rec.RecordNum)
		}
	}

	finalHeader, err := stream.Header()
	if err != nil {
		t.Fatalf("Header after iteration failed: %v", err)
	}

	if finalHeader.Summary.TotalRecords != 3 {
		t.Fatalf("Expected summary total_records 3, got %d", finalHeader.Summary.TotalRecords)
	}
	if finalHeader.Summary.MinRecordSizeBytes <= 0 || finalHeader.Summary.MaxRecordSizeBytes <= 0 {
		t.Fatalf("Expected min/max sizes to be populated")
	}
}

func TestRecordIndexStream_IterateWithoutHeader(t *testing.T) {
	tmpDir := t.TempDir()

	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<root>
	<Record>One</Record>
	<Record>Two</Record>
</root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}

	builder := NewBuilder(buildOpts)
	idx, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	indexPath := filepath.Join(tmpDir, "test.recordindex.json")
	if err := builder.WriteToFile(idx, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	stream, err := OpenRecordIndexStream(indexPath)
	if err != nil {
		t.Fatalf("OpenRecordIndexStream failed: %v", err)
	}
	defer func() { _ = stream.Close() }()

	count := 0
	for {
		rec, err := stream.NextRecord()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("NextRecord failed: %v", err)
		}
		if rec.RecordNum <= 0 {
			t.Fatalf("Expected positive record_num")
		}
		count++
	}

	if count != 2 {
		t.Fatalf("Expected 2 records, got %d", count)
	}
}
