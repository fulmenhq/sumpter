package index

import (
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuilder_Build_SmallXML(t *testing.T) {
	// Create temporary XML file for testing
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<root>
	<Record id="1">
		<Name>First</Name>
		<Value>100</Value>
	</Record>
	<Record id="2">
		<Name>Second</Name>
		<Value>200</Value>
	</Record>
	<Record id="3">
		<Name>Third</Name>
		<Value>300</Value>
	</Record>
</root>`

	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	// Build index
	opts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		IncludeP50:     true,
		IncludeP95:     true,
		IncludeP99:     true,
		SumpterVersion: "0.1.2-test",
	}

	builder := NewBuilder(opts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Verify basic structure
	if index.Version != SchemaVersion {
		t.Errorf("Expected version %s, got %s", SchemaVersion, index.Version)
	}

	if index.Summary.TotalRecords != 3 {
		t.Errorf("Expected 3 records, got %d", index.Summary.TotalRecords)
	}

	if index.Selector.XPath != "//Record" {
		t.Errorf("Expected selector '//Record', got %s", index.Selector.XPath)
	}

	if index.Selector.ElementName != "Record" {
		t.Errorf("Expected element name 'Record', got %s", index.Selector.ElementName)
	}

	// Verify records are sequential
	for i, rec := range index.Records {
		expectedNum := i + 1
		if rec.RecordNum != expectedNum {
			t.Errorf("Record %d: expected RecordNum %d, got %d", i, expectedNum, rec.RecordNum)
		}

		if rec.SizeBytes <= 0 {
			t.Errorf("Record %d: expected positive size, got %d", i, rec.SizeBytes)
		}

		if rec.SHA256 == "" {
			t.Errorf("Record %d: missing SHA256 hash", i)
		}

		if rec.ElementName != "Record" {
			t.Errorf("Record %d: expected element name 'Record', got %s", i, rec.ElementName)
		}
	}

	// Verify statistics
	if index.Summary.MinRecordSizeBytes <= 0 {
		t.Errorf("Expected positive min record size, got %d", index.Summary.MinRecordSizeBytes)
	}

	if index.Summary.MaxRecordSizeBytes <= 0 {
		t.Errorf("Expected positive max record size, got %d", index.Summary.MaxRecordSizeBytes)
	}

	if index.Summary.AvgRecordSizeBytes <= 0 {
		t.Errorf("Expected positive avg record size, got %f", index.Summary.AvgRecordSizeBytes)
	}

	// Verify percentiles were calculated
	if index.Summary.P50RecordSizeBytes == 0 {
		t.Error("Expected p50 to be calculated")
	}

	if index.Summary.P95RecordSizeBytes == 0 {
		t.Error("Expected p95 to be calculated")
	}

	if index.Summary.P99RecordSizeBytes == 0 {
		t.Error("Expected p99 to be calculated")
	}

	// Verify source metadata
	fileInfo, _ := os.Stat(xmlPath)
	if index.Source.SizeBytes != fileInfo.Size() {
		t.Errorf("Expected source size %d, got %d", fileInfo.Size(), index.Source.SizeBytes)
	}

	if index.Source.SHA256 == "" {
		t.Error("Expected source SHA256 to be computed")
	}

	if index.Source.Compressed {
		t.Error("Expected uncompressed file to be marked as not compressed")
	}

	if index.Source.CompressionFormat != "none" {
		t.Errorf("Expected compression format 'none', got %s", index.Source.CompressionFormat)
	}

	if index.Source.OffsetKind != OffsetKindSourceBytes {
		t.Errorf("Expected offset kind %q, got %q", OffsetKindSourceBytes, index.Source.OffsetKind)
	}
}

func TestBuilder_Build_RejectsCompressedInputBeforeOutput(t *testing.T) {
	tmpDir := t.TempDir()
	gzipPath := filepath.Join(tmpDir, "test.xml.gz")
	outputPath := filepath.Join(tmpDir, "test.recordindex.json")

	file, err := os.Create(gzipPath)
	if err != nil {
		t.Fatalf("Failed to create gzip fixture: %v", err)
	}
	gz := gzip.NewWriter(file)
	if _, err := gz.Write([]byte(`<root><Record>data</Record></root>`)); err != nil {
		t.Fatalf("Failed to write gzip fixture: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("Failed to close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Failed to close gzip fixture: %v", err)
	}

	builder := NewBuilder(BuildOptions{
		InputPath:  gzipPath,
		OutputPath: outputPath,
		Selector:   "//Record",
	})

	_, err = builder.Build()
	if err == nil {
		t.Fatal("Expected compressed input to be rejected")
	}
	if !strings.Contains(err.Error(), "gunzip -c input.xml.gz > input.xml") {
		t.Fatalf("Expected actionable decompression hint, got: %v", err)
	}
	if _, statErr := os.Stat(outputPath); !os.IsNotExist(statErr) {
		t.Fatalf("Expected no output file, stat error: %v", statErr)
	}
}

func TestBuilder_WriteToFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a simple index
	index := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              "/test/file.xml",
			SizeBytes:         1024,
			SHA256:            "abc123",
			Compressed:        false,
			CompressionFormat: "none",
		},
		Selector: SelectorInfo{
			XPath:       "//Record",
			ElementName: "Record",
		},
		Records: []RecordMetadata{
			{
				RecordNum:   1,
				StartOffset: 0,
				EndOffset:   100,
				SizeBytes:   100,
				SHA256:      "def456",
				ElementName: "Record",
				Depth:       1,
			},
		},
		Summary: SummaryStats{
			TotalRecords:       1,
			TotalBytes:         100,
			AvgRecordSizeBytes: 100.0,
			MinRecordSizeBytes: 100,
			MaxRecordSizeBytes: 100,
		},
		Metadata: IndexMetadata{
			Generator: "test",
		},
	}

	// Write to file
	outputPath := filepath.Join(tmpDir, "test-index.json")
	builder := NewBuilder(BuildOptions{})
	if err := builder.WriteToFile(index, outputPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Verify file exists and is valid JSON
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	var loaded RecordIndex
	if err := json.Unmarshal(data, &loaded); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	// Verify content matches
	if loaded.Version != index.Version {
		t.Errorf("Version mismatch: expected %s, got %s", index.Version, loaded.Version)
	}

	if loaded.Summary.TotalRecords != index.Summary.TotalRecords {
		t.Errorf("TotalRecords mismatch: expected %d, got %d",
			index.Summary.TotalRecords, loaded.Summary.TotalRecords)
	}

	if loaded.Source.OffsetKind != OffsetKindSourceBytes {
		t.Errorf("Expected written offset kind %q, got %q", OffsetKindSourceBytes, loaded.Source.OffsetKind)
	}
}

func TestBuilder_WriteToFile_EmitsCurrentVersion(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "legacy-input-index.json")

	index := &RecordIndex{
		Version: LegacySchemaVersion,
		Source: SourceInfo{
			Path:              "/test/file.xml",
			SizeBytes:         1024,
			SHA256:            "abc123",
			Compressed:        false,
			CompressionFormat: "none",
		},
		Selector: SelectorInfo{
			XPath:       "//Record",
			ElementName: "Record",
		},
		Records: []RecordMetadata{},
		Summary: SummaryStats{},
		Metadata: IndexMetadata{
			Generator: "test",
		},
	}

	builder := NewBuilder(BuildOptions{})
	if err := builder.WriteToFile(index, outputPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	loaded, err := LoadIndex(outputPath)
	if err != nil {
		t.Fatalf("Failed to load written index: %v", err)
	}
	if loaded.Version != SchemaVersion {
		t.Fatalf("Expected written version %q, got %q", SchemaVersion, loaded.Version)
	}
	if loaded.Source.OffsetKind != OffsetKindSourceBytes {
		t.Fatalf("Expected written offset kind %q, got %q", OffsetKindSourceBytes, loaded.Source.OffsetKind)
	}
}

func TestDetectCompression(t *testing.T) {
	tests := []struct {
		path           string
		wantCompressed bool
		wantFormat     string
	}{
		{"file.xml", false, "none"},
		{"file.xml.gz", true, "gzip"},
		{"file.xml.GZ", true, "gzip"},
		{"file.xml.bz2", true, "bzip2"},
		{"file.xml.xz", true, "xz"},
		{"file.txt", false, "none"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			gotCompressed, gotFormat := detectCompression(tt.path)
			if gotCompressed != tt.wantCompressed {
				t.Errorf("detectCompression(%q) compressed = %v, want %v",
					tt.path, gotCompressed, tt.wantCompressed)
			}
			if gotFormat != tt.wantFormat {
				t.Errorf("detectCompression(%q) format = %v, want %v",
					tt.path, gotFormat, tt.wantFormat)
			}
		})
	}
}

func TestPercentile(t *testing.T) {
	tests := []struct {
		name  string
		sizes []int64
		p     float64
		want  int64
	}{
		{"empty", []int64{}, 0.5, 0},
		{"single", []int64{100}, 0.5, 100},
		{"two_p50", []int64{100, 200}, 0.5, 150},
		{"three_p50", []int64{100, 200, 300}, 0.5, 200},
		{"five_p95", []int64{100, 200, 300, 400, 500}, 0.95, 480}, // Linear interpolation: 400 + 0.8*(500-400)
		{"five_p99", []int64{100, 200, 300, 400, 500}, 0.99, 496}, // Linear interpolation: 400 + 0.96*(500-400)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := percentile(tt.sizes, tt.p)
			if got != tt.want {
				t.Errorf("percentile(%v, %.2f) = %d, want %d",
					tt.sizes, tt.p, got, tt.want)
			}
		})
	}
}

func TestComputeFileSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("Hello, World!")

	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	hash1, err := computeFileSHA256(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash: %v", err)
	}

	if hash1 == "" {
		t.Error("Expected non-empty hash")
	}

	// Verify hash is deterministic
	hash2, err := computeFileSHA256(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash second time: %v", err)
	}

	if hash1 != hash2 {
		t.Errorf("Hash not deterministic: %s != %s", hash1, hash2)
	}

	// Verify hash changes when content changes
	if err := os.WriteFile(testFile, []byte("Different content"), 0644); err != nil {
		t.Fatalf("Failed to modify test file: %v", err)
	}

	hash3, err := computeFileSHA256(testFile)
	if err != nil {
		t.Fatalf("Failed to compute hash third time: %v", err)
	}

	if hash1 == hash3 {
		t.Error("Hash did not change when content changed")
	}
}

func TestComputeRangeHashSHA256(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	content := []byte("0123456789ABCDEFGHIJ")

	if err := os.WriteFile(testFile, content, 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test reading a range
	hash1, err := computeRangeHashSHA256(testFile, 0, 10)
	if err != nil {
		t.Fatalf("Failed to compute range hash: %v", err)
	}

	if hash1 == "" {
		t.Error("Expected non-empty hash")
	}

	// Different range should produce different hash
	hash2, err := computeRangeHashSHA256(testFile, 10, 20)
	if err != nil {
		t.Fatalf("Failed to compute second range hash: %v", err)
	}

	if hash1 == hash2 {
		t.Error("Different ranges produced same hash")
	}

	// Same range should produce same hash
	hash3, err := computeRangeHashSHA256(testFile, 0, 10)
	if err != nil {
		t.Fatalf("Failed to compute third range hash: %v", err)
	}

	if hash1 != hash3 {
		t.Error("Same range produced different hash")
	}
}

// Edge case tests for malformed XML and error conditions
func TestBuilder_Build_MalformedXML(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		xmlContent  string
		expectError bool
		errorMsg    string
	}{
		{
			name:        "unclosed_tag",
			xmlContent:  `<?xml version="1.0"?><root><Record>Data</root>`,
			expectError: true,
			errorMsg:    "unclosed tag",
		},
		{
			name:        "invalid_xml_syntax",
			xmlContent:  `<?xml version="1.0"?><root><Record attr="unclosed>Data</Record></root>`,
			expectError: true,
			errorMsg:    "invalid syntax",
		},
		{
			name:        "empty_file",
			xmlContent:  ``,
			expectError: false, // Empty file is valid, just no records
			errorMsg:    "",
		},
		{
			name:        "xml_without_records",
			xmlContent:  `<?xml version="1.0"?><root><NoRecords>Data</NoRecords></root>`,
			expectError: false, // Valid XML, just no matching records
			errorMsg:    "",
		},
		{
			name:        "nested_unclosed_tag",
			xmlContent:  `<?xml version="1.0"?><root><Record><Nested>Data</Record></root>`,
			expectError: true,
			errorMsg:    "unclosed nested tag",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xmlPath := filepath.Join(tmpDir, tt.name+".xml")
			if err := os.WriteFile(xmlPath, []byte(tt.xmlContent), 0644); err != nil {
				t.Fatalf("Failed to create test XML: %v", err)
			}

			opts := BuildOptions{
				InputPath:      xmlPath,
				Selector:       "//Record",
				SumpterVersion: "0.1.2-test",
			}

			builder := NewBuilder(opts)
			_, err := builder.Build()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error for %s, but got none", tt.errorMsg)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func TestBuilder_Build_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "empty.xml")

	// Create empty file
	if err := os.WriteFile(xmlPath, []byte(""), 0644); err != nil {
		t.Fatalf("Failed to create empty file: %v", err)
	}

	opts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}

	builder := NewBuilder(opts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Expected empty file to build successfully: %v", err)
	}

	// Verify index reflects empty file
	if index.Summary.TotalRecords != 0 {
		t.Errorf("Expected 0 records for empty file, got %d", index.Summary.TotalRecords)
	}

	if index.Summary.TotalBytes != 0 {
		t.Errorf("Expected 0 total bytes, got %d", index.Summary.TotalBytes)
	}
}

func TestBuilder_Build_NonExistentFile(t *testing.T) {
	opts := BuildOptions{
		InputPath:      "/nonexistent/file.xml",
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}

	builder := NewBuilder(opts)
	_, err := builder.Build()

	if err == nil {
		t.Error("Expected error for non-existent file")
	}
}

func TestBuilder_Build_InvalidSelector(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?><root><Record>Data</Record></root>`

	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	tests := []struct {
		name     string
		selector string
	}{
		{name: "empty", selector: ""},
		{name: "predicate", selector: "//Record[@type='sale']"},
		{name: "path", selector: "/root/Record"},
		{name: "namespace prefix", selector: "//ns:Record"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := BuildOptions{
				InputPath:      xmlPath,
				Selector:       tt.selector,
				SumpterVersion: "0.1.2-test",
			}

			builder := NewBuilder(opts)
			_, err := builder.Build()
			if err == nil {
				t.Fatal("expected unsupported selector error")
			}
			if !strings.Contains(err.Error(), "not yet supported for streaming/index mode") {
				t.Fatalf("error = %q, want streaming/index mode wording", err.Error())
			}
		})
	}
}

func TestBuilder_Build_BareElementSelector(t *testing.T) {
	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?><root><Record>Data</Record></root>`

	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	builder := NewBuilder(BuildOptions{
		InputPath:      xmlPath,
		Selector:       "Record",
		SumpterVersion: "0.1.2-test",
	})
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Build() failed: %v", err)
	}
	if index.Selector.XPath != "Record" {
		t.Fatalf("selector xpath = %q, want Record", index.Selector.XPath)
	}
	if index.Selector.ElementName != "Record" {
		t.Fatalf("selector element = %q, want Record", index.Selector.ElementName)
	}
	if index.Summary.TotalRecords != 1 {
		t.Fatalf("records = %d, want 1", index.Summary.TotalRecords)
	}
}
