package streaming

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"
)

func TestExtractElementName(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		want     string
	}{
		{
			name:     "simple double slash",
			selector: "//VariationArchive",
			want:     "VariationArchive",
		},
		{
			name:     "absolute path",
			selector: "/root/child/Record",
			want:     "Record",
		},
		{
			name:     "with predicate",
			selector: "//Transaction[@type='sale']",
			want:     "Transaction",
		},
		{
			name:     "just element name",
			selector: "Element",
			want:     "Element",
		},
		{
			name:     "nested path with predicate",
			selector: "/root/items/Item[1]",
			want:     "Item",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractElementName(tt.selector)
			if got != tt.want {
				t.Errorf("extractElementName(%q) = %q, want %q", tt.selector, got, tt.want)
			}
		})
	}
}

func TestRecordScanner_BasicScan(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record id="1">
    <name>First</name>
    <value>100</value>
  </Record>
  <Record id="2">
    <name>Second</name>
    <value>200</value>
  </Record>
  <Record id="3">
    <name>Third</name>
    <value>300</value>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	// Scan first record
	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on first record: %v", err)
	}
	if record == nil {
		t.Fatal("expected record, got nil")
	}
	if record.RecordNum != 1 {
		t.Errorf("first record number = %d, want 1", record.RecordNum)
	}
	if !strings.Contains(record.XML, "First") {
		t.Errorf("first record doesn't contain expected content: %s", record.XML)
	}

	// Scan second record
	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on second record: %v", err)
	}
	if record.RecordNum != 2 {
		t.Errorf("second record number = %d, want 2", record.RecordNum)
	}
	if !strings.Contains(record.XML, "Second") {
		t.Errorf("second record doesn't contain expected content: %s", record.XML)
	}

	// Scan third record
	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on third record: %v", err)
	}
	if record.RecordNum != 3 {
		t.Errorf("third record number = %d, want 3", record.RecordNum)
	}
	if !strings.Contains(record.XML, "Third") {
		t.Errorf("third record doesn't contain expected content: %s", record.XML)
	}

	// Should get EOF on next call
	record, err = scanner.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
	if record != nil {
		t.Errorf("expected nil record at EOF, got %v", record)
	}
}

func TestRecordScanner_NestedElements(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record>
    <metadata>
      <Record><!-- This is NOT a top-level record -->
        <nested>data</nested>
      </Record>
    </metadata>
    <data>First record</data>
  </Record>
  <Record>
    <data>Second record</data>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	// Should get 2 records (only top-level Record elements)
	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on first record: %v", err)
	}
	if !strings.Contains(record.XML, "First record") {
		t.Errorf("first record missing expected content: %s", record.XML)
	}
	// The nested <Record> should be included in the first record's XML
	if !strings.Contains(record.XML, "<nested>data</nested>") {
		t.Errorf("first record should contain nested Record element: %s", record.XML)
	}

	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on second record: %v", err)
	}
	if !strings.Contains(record.XML, "Second record") {
		t.Errorf("second record missing expected content: %s", record.XML)
	}

	// Should get EOF
	_, err = scanner.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestRecordScanner_EmptyRecords(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record/>
  <Record></Record>
  <Record>
    <data>Has content</data>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	// First empty record (self-closing)
	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on first record: %v", err)
	}
	if record.RecordNum != 1 {
		t.Errorf("first record number = %d, want 1", record.RecordNum)
	}

	// Second empty record (with separate closing tag)
	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on second record: %v", err)
	}
	if record.RecordNum != 2 {
		t.Errorf("second record number = %d, want 2", record.RecordNum)
	}

	// Third record with content
	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on third record: %v", err)
	}
	if record.RecordNum != 3 {
		t.Errorf("third record number = %d, want 3", record.RecordNum)
	}
	if !strings.Contains(record.XML, "Has content") {
		t.Errorf("third record missing expected content: %s", record.XML)
	}

	// EOF
	_, err = scanner.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestRecordScanner_CompressedStream(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record id="1">
    <data>Compressed</data>
  </Record>
  <Record id="2">
    <data>Records</data>
  </Record>
</root>`

	// Compress the XML data
	var compressed bytes.Buffer
	gzWriter := gzip.NewWriter(&compressed)
	if _, err := gzWriter.Write([]byte(xmlData)); err != nil {
		t.Fatalf("failed to compress test data: %v", err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatalf("failed to close gzip writer: %v", err)
	}

	// Create scanner with gzip reader
	gzReader, err := gzip.NewReader(&compressed)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer func() {
		_ = gzReader.Close() // Best effort close in test
	}()

	scanner := NewRecordScanner(gzReader, "//Record")

	// Scan first record
	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on first record: %v", err)
	}
	if !strings.Contains(record.XML, "Compressed") {
		t.Errorf("first record missing expected content: %s", record.XML)
	}

	// Scan second record
	record, err = scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on second record: %v", err)
	}
	if !strings.Contains(record.XML, "Records") {
		t.Errorf("second record missing expected content: %s", record.XML)
	}

	// EOF
	_, err = scanner.Next()
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}
}

func TestRecordScanner_AttributesPreserved(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record id="test-123" type="sale" xmlns:custom="http://example.com">
    <data attr="value">Content</data>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that attributes are preserved
	if !strings.Contains(record.XML, `id="test-123"`) {
		t.Errorf("record XML missing id attribute: %s", record.XML)
	}
	if !strings.Contains(record.XML, `type="sale"`) {
		t.Errorf("record XML missing type attribute: %s", record.XML)
	}
	if !strings.Contains(record.XML, `attr="value"`) {
		t.Errorf("record XML missing nested attribute: %s", record.XML)
	}
}

func TestRecordScanner_MalformedXML(t *testing.T) {
	tests := []struct {
		name    string
		xmlData string
	}{
		{
			name:    "unclosed tag",
			xmlData: `<root><Record><data>content</Record></root>`,
		},
		{
			name:    "mismatched tags",
			xmlData: `<root><Record><data>content</wrong></Record></root>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scanner := NewRecordScanner(strings.NewReader(tt.xmlData), "//Record")

			_, err := scanner.Next()
			if err == nil {
				t.Error("expected error for malformed XML, got nil")
			}
			if err == io.EOF {
				t.Error("expected parse error, got EOF")
			}
		})
	}
}

func TestRecordScanner_LargeRecord(t *testing.T) {
	// Simulate a large record with many child elements
	var xmlBuilder strings.Builder
	xmlBuilder.WriteString(`<?xml version="1.0" encoding="UTF-8"?><root><Record>`)

	// Add 1000 child elements
	for i := 0; i < 1000; i++ {
		xmlBuilder.WriteString(`<item>`)
		xmlBuilder.WriteString(strings.Repeat("x", 100)) // 100 chars per item
		xmlBuilder.WriteString(`</item>`)
	}

	xmlBuilder.WriteString(`</Record></root>`)

	scanner := NewRecordScanner(strings.NewReader(xmlBuilder.String()), "//Record")

	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the record contains all items
	expectedSize := 100 * 1000 // approximately (ignoring XML tags)
	if len(record.XML) < expectedSize {
		t.Errorf("record XML size = %d, expected at least %d", len(record.XML), expectedSize)
	}

	// Verify we can parse the extracted XML
	if !strings.Contains(record.XML, "<item>") {
		t.Error("record XML doesn't contain expected item elements")
	}
}

func TestRecordScanner_RecordCount(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record><data>1</data></Record>
  <Record><data>2</data></Record>
  <Record><data>3</data></Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	if scanner.RecordCount() != 0 {
		t.Errorf("initial count = %d, want 0", scanner.RecordCount())
	}

	_, _ = scanner.Next() // Ignore error/result, just counting
	if scanner.RecordCount() != 1 {
		t.Errorf("count after first = %d, want 1", scanner.RecordCount())
	}

	_, _ = scanner.Next() // Ignore error/result, just counting
	if scanner.RecordCount() != 2 {
		t.Errorf("count after second = %d, want 2", scanner.RecordCount())
	}

	_, _ = scanner.Next() // Ignore error/result, just counting
	if scanner.RecordCount() != 3 {
		t.Errorf("count after third = %d, want 3", scanner.RecordCount())
	}
}

func TestRecordScanner_CommentsAndProcessingInstructions(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record>
    <!-- This is a comment -->
    <data>Content</data>
    <?processing instruction?>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Comments and PIs should be preserved in the record XML
	if !strings.Contains(record.XML, "<!-- This is a comment -->") {
		t.Errorf("record XML missing comment: %s", record.XML)
	}
	if !strings.Contains(record.XML, "<?processing instruction?>") {
		t.Errorf("record XML missing processing instruction: %s", record.XML)
	}
}

func TestRecordScanner_ByteTracking(t *testing.T) {
	xmlData := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <Record id="1">
    <name>First</name>
    <value>100</value>
  </Record>
  <Record id="2">
    <name>Second</name>
    <value>200</value>
  </Record>
</root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	// Initial state: no bytes read yet (decoder may buffer some)
	initialBytes := scanner.BytesRead()
	if initialBytes < 0 {
		t.Errorf("initial BytesRead() = %d, should be non-negative", initialBytes)
	}

	// Scan first record
	record1, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on first record: %v", err)
	}

	// Check that offsets are populated
	if record1.StartOffset <= 0 {
		t.Errorf("first record StartOffset = %d, want > 0", record1.StartOffset)
	}
	if record1.EndOffset <= record1.StartOffset {
		t.Errorf("first record EndOffset = %d, want > StartOffset (%d)", record1.EndOffset, record1.StartOffset)
	}
	if record1.SizeBytes <= 0 {
		t.Errorf("first record SizeBytes = %d, want > 0", record1.SizeBytes)
	}
	if record1.SizeBytes != (record1.EndOffset - record1.StartOffset) {
		t.Errorf("first record SizeBytes = %d, want %d (EndOffset - StartOffset)",
			record1.SizeBytes, record1.EndOffset-record1.StartOffset)
	}

	// Scan second record
	record2, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error on second record: %v", err)
	}

	// Second record should start after first record ends
	if record2.StartOffset <= record1.EndOffset {
		t.Errorf("second record StartOffset = %d, should be > first EndOffset (%d)",
			record2.StartOffset, record1.EndOffset)
	}
	if record2.EndOffset <= record2.StartOffset {
		t.Errorf("second record EndOffset = %d, want > StartOffset (%d)", record2.EndOffset, record2.StartOffset)
	}
	if record2.SizeBytes <= 0 {
		t.Errorf("second record SizeBytes = %d, want > 0", record2.SizeBytes)
	}

	// BytesRead should be at least as much as the second record's end
	finalBytes := scanner.BytesRead()
	if finalBytes < record2.EndOffset {
		t.Errorf("BytesRead() = %d, should be >= second record EndOffset (%d)",
			finalBytes, record2.EndOffset)
	}

	// Total file size should be approximately equal to BytesRead after EOF
	_, err = scanner.Next() // Should get EOF
	if err != io.EOF {
		t.Errorf("expected EOF, got %v", err)
	}

	totalBytes := scanner.BytesRead()
	expectedSize := int64(len(xmlData))
	if totalBytes != expectedSize {
		t.Errorf("total BytesRead() = %d, want %d (file size)", totalBytes, expectedSize)
	}
}

func TestRecordScanner_ByteTrackingSingleRecord(t *testing.T) {
	xmlData := `<root><Record>test</Record></root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	record, err := scanner.Next()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify byte tracking fields are populated
	if record.StartOffset == 0 {
		t.Error("StartOffset should be > 0 after XML prolog/root element")
	}
	if record.EndOffset <= record.StartOffset {
		t.Errorf("EndOffset (%d) should be > StartOffset (%d)", record.EndOffset, record.StartOffset)
	}
	if record.SizeBytes <= 0 {
		t.Errorf("SizeBytes = %d, want > 0", record.SizeBytes)
	}

	// Verify size calculation is correct
	calculatedSize := record.EndOffset - record.StartOffset
	if record.SizeBytes != calculatedSize {
		t.Errorf("SizeBytes = %d, want %d (EndOffset - StartOffset)",
			record.SizeBytes, calculatedSize)
	}
}

func TestRecordScanner_BytesReadMethod(t *testing.T) {
	xmlData := `<root><Record>A</Record><Record>B</Record><Record>C</Record></root>`

	scanner := NewRecordScanner(strings.NewReader(xmlData), "//Record")

	// Track bytes read as we scan
	bytesBeforeScan := scanner.BytesRead()
	if bytesBeforeScan < 0 {
		t.Errorf("BytesRead() before scan = %d, want >= 0", bytesBeforeScan)
	}

	// Scan all records
	recordCount := 0
	for {
		bytesBeforeNext := scanner.BytesRead()
		record, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		recordCount++
		bytesAfterNext := scanner.BytesRead()

		// BytesRead should be monotonically increasing (never decrease)
		if bytesAfterNext < bytesBeforeNext {
			t.Errorf("BytesRead decreased: %d -> %d", bytesBeforeNext, bytesAfterNext)
		}

		// Note: xml.Decoder buffers input, so BytesRead() may not increase
		// incrementally for every record. It typically reads the entire input
		// on the first Token() call, so BytesRead() will jump to the file size
		// immediately and remain constant thereafter.

		// Record offsets use InputOffset() which tracks XML stream position,
		// not underlying reader position. So we can't directly compare them.
		// Just verify offsets are reasonable.
		if record.StartOffset < 0 {
			t.Errorf("Record %d StartOffset (%d) is negative",
				recordCount, record.StartOffset)
		}
		if record.EndOffset < record.StartOffset {
			t.Errorf("Record %d EndOffset (%d) < StartOffset (%d)",
				recordCount, record.EndOffset, record.StartOffset)
		}
	}

	if recordCount != 3 {
		t.Errorf("scanned %d records, want 3", recordCount)
	}

	// After EOF, BytesRead should equal file size
	// (decoder should have read the entire input by now)
	finalBytes := scanner.BytesRead()
	expectedSize := int64(len(xmlData))
	if finalBytes != expectedSize {
		t.Errorf("final BytesRead() = %d, want %d", finalBytes, expectedSize)
	}
}
