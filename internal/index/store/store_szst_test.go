//go:build cgo && seekablezstd

package store

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"

	seekable "github.com/3leaps/seekable-zstd/bindings/go"
)

// TestSeekableZstdLinkage verifies the seekable-zstd library links correctly.
func TestSeekableZstdLinkage(t *testing.T) {
	t.Logf("seekable-zstd version: %s", seekable.Version())
	t.Logf("GOOS: %s, GOARCH: %s", runtime.GOOS, runtime.GOARCH)

	if !SeekableZstdAvailable() {
		t.Fatal("SeekableZstdAvailable() returned false, but test is running with seekablezstd tag")
	}
}

// TestSeekableZstdReadRange verifies ReadRange works on the hello.szst fixture.
func TestSeekableZstdReadRange(t *testing.T) {
	fixturePath := filepath.Join("testdata", "hello.szst")

	reader, err := seekable.Open(fixturePath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", fixturePath, err)
	}
	defer reader.Close()

	// Verify metadata
	t.Logf("Size: %d bytes (decompressed)", reader.Size())
	t.Logf("FrameCount: %d", reader.FrameCount())

	if reader.Size() != 11 {
		t.Errorf("Expected size 11 (Hello World), got %d", reader.Size())
	}

	// Test ReadRange: read "Hello"
	data, err := reader.ReadRange(0, 5)
	if err != nil {
		t.Fatalf("ReadRange(0, 5) failed: %v", err)
	}
	if string(data) != "Hello" {
		t.Errorf("Expected 'Hello', got %q", string(data))
	}

	// Test ReadRange: read "World"
	data, err = reader.ReadRange(6, 11)
	if err != nil {
		t.Fatalf("ReadRange(6, 11) failed: %v", err)
	}
	if string(data) != "World" {
		t.Errorf("Expected 'World', got %q", string(data))
	}

	// Test ReadAt (io.ReaderAt interface)
	buf := make([]byte, 5)
	n, err := reader.ReadAt(buf, 0)
	if err != nil {
		t.Fatalf("ReadAt(buf, 0) failed: %v", err)
	}
	if n != 5 {
		t.Errorf("Expected n=5, got %d", n)
	}
	if string(buf) != "Hello" {
		t.Errorf("Expected 'Hello', got %q", string(buf))
	}

	t.Log("ReadRange and ReadAt work correctly")
}

// TestSeekableZstdFullContent reads the entire content.
func TestSeekableZstdFullContent(t *testing.T) {
	fixturePath := filepath.Join("testdata", "hello.szst")

	reader, err := seekable.Open(fixturePath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", fixturePath, err)
	}
	defer reader.Close()

	data, err := reader.ReadRange(0, reader.Size())
	if err != nil {
		t.Fatalf("ReadRange(0, %d) failed: %v", reader.Size(), err)
	}

	expected := "Hello World"
	if string(data) != expected {
		t.Errorf("Expected %q, got %q", expected, string(data))
	}

	t.Logf("Full content: %q", string(data))
}

// TestWriteReadRoundtrip verifies that WriteSeekableIndex produces files
// that Open can read back with matching SHA256 values.
// This is the critical integration test for the binary format audit fix.
func TestWriteReadRoundtrip(t *testing.T) {
	// Create temp directory for test files
	tmpDir, err := os.MkdirTemp("", "szst-roundtrip-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test index with known SHA256 values
	testIndex := &index.RecordIndex{
		Version: "1.0.0",
		Source: index.SourceInfo{
			Path:      "/test/data.xml",
			SizeBytes: 1000000,
		},
		Selector: index.SelectorInfo{
			ElementName: "Record",
		},
		Summary: index.SummaryStats{
			TotalRecords: 3,
		},
		Records: []index.RecordMetadata{
			{
				RecordNum:   1,
				StartOffset: 100,
				EndOffset:   200,
				SizeBytes:   100,
				Depth:       2,
				SHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", // Empty string hash
			},
			{
				RecordNum:   2,
				StartOffset: 200,
				EndOffset:   500,
				SizeBytes:   300,
				Depth:       2,
				SHA256:      "a665a45920422f9d417e4867efdc4fb8a04a1f3fff1fa07e998e86f7f7a27ae3", // "123" hash
			},
			{
				RecordNum:   3,
				StartOffset: 500,
				EndOffset:   1000,
				SizeBytes:   500,
				Depth:       3,
				SHA256:      "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9", // "hello world" hash
			},
		},
	}

	// Write seekable index
	basePath := filepath.Join(tmpDir, "test")
	if err := WriteSeekableIndex(basePath, testIndex); err != nil {
		t.Fatalf("WriteSeekableIndex failed: %v", err)
	}

	// Verify files were created
	headerPath := basePath + ".recordindex.header.json"
	recordsPath := basePath + ".recordindex.records.szst"

	if _, err := os.Stat(headerPath); os.IsNotExist(err) {
		t.Fatalf("Header file not created: %s", headerPath)
	}
	if _, err := os.Stat(recordsPath); os.IsNotExist(err) {
		t.Fatalf("Records file not created: %s", recordsPath)
	}

	t.Logf("Created header: %s", headerPath)
	t.Logf("Created records: %s", recordsPath)

	// Open the store and read back records
	store, err := Open(headerPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer store.Close()

	// Verify header
	header, err := store.Header()
	if err != nil {
		t.Fatalf("Header() failed: %v", err)
	}
	// Version should be the store format version, not the input index version
	if header.Version != SzstStoreVersion {
		t.Errorf("Version mismatch: got %s, want %s", header.Version, SzstStoreVersion)
	}
	if header.Summary.TotalRecords != testIndex.Summary.TotalRecords {
		t.Errorf("TotalRecords mismatch: got %d, want %d", header.Summary.TotalRecords, testIndex.Summary.TotalRecords)
	}
	if header.Source.OffsetKind != index.OffsetKindSourceBytes {
		t.Errorf("OffsetKind mismatch: got %q, want %q", header.Source.OffsetKind, index.OffsetKindSourceBytes)
	}

	// Read all records and verify SHA256 values match
	iter, err := store.Records(context.Background())
	if err != nil {
		t.Fatalf("Records() failed: %v", err)
	}
	defer iter.Close()

	recordsRead := 0
	for {
		rec, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() failed: %v", err)
		}

		// Find matching original record
		if recordsRead >= len(testIndex.Records) {
			t.Fatalf("Read more records than expected: %d", recordsRead+1)
		}
		orig := testIndex.Records[recordsRead]

		// Verify all fields match, especially SHA256
		if rec.RecordNum != orig.RecordNum {
			t.Errorf("Record %d: RecordNum mismatch: got %d, want %d", recordsRead, rec.RecordNum, orig.RecordNum)
		}
		if rec.StartOffset != orig.StartOffset {
			t.Errorf("Record %d: StartOffset mismatch: got %d, want %d", recordsRead, rec.StartOffset, orig.StartOffset)
		}
		if rec.EndOffset != orig.EndOffset {
			t.Errorf("Record %d: EndOffset mismatch: got %d, want %d", recordsRead, rec.EndOffset, orig.EndOffset)
		}
		if rec.SizeBytes != orig.SizeBytes {
			t.Errorf("Record %d: SizeBytes mismatch: got %d, want %d", recordsRead, rec.SizeBytes, orig.SizeBytes)
		}
		if rec.Depth != orig.Depth {
			t.Errorf("Record %d: Depth mismatch: got %d, want %d", recordsRead, rec.Depth, orig.Depth)
		}
		if rec.SHA256 != orig.SHA256 {
			t.Errorf("Record %d: SHA256 MISMATCH (critical bug!):\n  got:  %s\n  want: %s",
				recordsRead, rec.SHA256, orig.SHA256)
		}

		t.Logf("Record %d: SHA256 verified: %s", recordsRead, rec.SHA256)
		recordsRead++
	}

	if recordsRead != len(testIndex.Records) {
		t.Errorf("Record count mismatch: read %d, want %d", recordsRead, len(testIndex.Records))
	}

	t.Logf("Roundtrip test passed: %d records with correct SHA256 values", recordsRead)
}

// TestRecordsFileOverride verifies that the reader honors the records_file field
// in the header when present, rather than always deriving from the header filename.
func TestRecordsFileOverride(t *testing.T) {
	// Create temp directory for test files
	tmpDir, err := os.MkdirTemp("", "szst-override-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test index
	testIndex := &index.RecordIndex{
		Version: "1.0.0",
		Source: index.SourceInfo{
			Path:      "/test/data.xml",
			SizeBytes: 1000,
		},
		Selector: index.SelectorInfo{
			ElementName: "Record",
		},
		Summary: index.SummaryStats{
			TotalRecords: 1,
		},
		Records: []index.RecordMetadata{
			{
				RecordNum:   1,
				StartOffset: 100,
				EndOffset:   200,
				SizeBytes:   100,
				Depth:       2,
				SHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			},
		},
	}

	// Write seekable index with standard naming
	basePath := filepath.Join(tmpDir, "test")
	if err := WriteSeekableIndex(basePath, testIndex); err != nil {
		t.Fatalf("WriteSeekableIndex failed: %v", err)
	}

	// Rename the records file to a non-standard name
	origRecordsPath := basePath + ".recordindex.records.szst"
	newRecordsPath := filepath.Join(tmpDir, "custom-records.szst")
	if err := os.Rename(origRecordsPath, newRecordsPath); err != nil {
		t.Fatalf("Failed to rename records file: %v", err)
	}

	// Modify the header to point to the new records file
	headerPath := basePath + ".recordindex.header.json"
	headerData, err := os.ReadFile(headerPath)
	if err != nil {
		t.Fatalf("Failed to read header: %v", err)
	}

	var header SzstIndexHeader
	if err := json.Unmarshal(headerData, &header); err != nil {
		t.Fatalf("Failed to parse header: %v", err)
	}

	// Update records_file to point to the renamed file
	header.Records.RecordsFile = "custom-records.szst"

	updatedHeader, err := json.MarshalIndent(&header, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal header: %v", err)
	}

	if err := os.WriteFile(headerPath, updatedHeader, 0644); err != nil {
		t.Fatalf("Failed to write header: %v", err)
	}

	// Open the store - should honor records_file override
	store, err := Open(headerPath)
	if err != nil {
		t.Fatalf("Open failed (should have honored records_file): %v", err)
	}
	defer store.Close()

	// Read records to verify it worked
	iter, err := store.Records(context.Background())
	if err != nil {
		t.Fatalf("Records() failed: %v", err)
	}
	defer iter.Close()

	rec, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() failed: %v", err)
	}

	if rec.SHA256 != testIndex.Records[0].SHA256 {
		t.Errorf("SHA256 mismatch: got %s, want %s", rec.SHA256, testIndex.Records[0].SHA256)
	}

	t.Log("records_file override honored correctly")
}
