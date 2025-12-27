package store

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// TestSeekableZstdStub verifies the stub returns the expected error.
func TestSeekableZstdStub(t *testing.T) {
	// This test runs when built without seekablezstd tag
	if SeekableZstdAvailable() {
		t.Skip("Skipping stub test - seekable-zstd is available")
	}

	_, err := Open("test.recordindex.header.json")
	if err != ErrSeekableZstdNotAvailable {
		t.Errorf("Expected ErrSeekableZstdNotAvailable, got: %v", err)
	}
}

// TestJSONStoreOpen verifies the JSON store opens correctly.
func TestJSONStoreOpen(t *testing.T) {
	// Create a minimal test index
	testDir := t.TempDir()
	indexPath := filepath.Join(testDir, "test.recordindex.json")

	testIndex := `{
  "version": "record-index/v0.1.0",
  "source": {
    "path": "/test/data.xml",
    "size_bytes": 1000,
    "sha256": "abc123"
  },
  "selector": {
    "xpath": "//Record",
    "element": "Record"
  },
  "records": [
    {
      "start_offset": 0,
      "end_offset": 100,
      "size_bytes": 100,
      "sha256": "def456",
      "element_name": "Record",
      "depth": 1
    },
    {
      "start_offset": 100,
      "end_offset": 200,
      "size_bytes": 100,
      "sha256": "ghi789",
      "element_name": "Record",
      "depth": 1
    }
  ],
  "summary": {
    "total_records": 2,
    "total_size_bytes": 200
  }
}`

	if err := os.WriteFile(indexPath, []byte(testIndex), 0644); err != nil {
		t.Fatalf("Failed to write test index: %v", err)
	}

	store, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", indexPath, err)
	}
	defer func() { _ = store.Close() }()

	// Verify header
	header, err := store.Header()
	if err != nil {
		t.Fatalf("Header() failed: %v", err)
	}

	if header.Version != "record-index/v0.1.0" {
		t.Errorf("Expected version 'record-index/v0.1.0', got %q", header.Version)
	}

	if header.Source.Path != "/test/data.xml" {
		t.Errorf("Expected source path '/test/data.xml', got %q", header.Source.Path)
	}

	// Verify records iteration
	ctx := context.Background()
	iter, err := store.Records(ctx)
	if err != nil {
		t.Fatalf("Records() failed: %v", err)
	}
	defer func() { _ = iter.Close() }()

	var records []int64
	for {
		rec, err := iter.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() failed: %v", err)
		}
		records = append(records, rec.StartOffset)
	}

	if len(records) != 2 {
		t.Errorf("Expected 2 records, got %d", len(records))
	}

	if len(records) >= 2 {
		if records[0] != 0 {
			t.Errorf("Expected first record at offset 0, got %d", records[0])
		}
		if records[1] != 100 {
			t.Errorf("Expected second record at offset 100, got %d", records[1])
		}
	}
}

// TestJSONStoreNilContext verifies Records() handles nil context gracefully.
func TestJSONStoreNilContext(t *testing.T) {
	testDir := t.TempDir()
	indexPath := filepath.Join(testDir, "test.recordindex.json")

	testIndex := `{
  "version": "record-index/v0.1.0",
  "source": {"path": "/test/data.xml", "size_bytes": 1000, "sha256": "abc123"},
  "selector": {"xpath": "//Record", "element": "Record"},
  "records": [
    {"start_offset": 0, "end_offset": 100, "size_bytes": 100, "sha256": "def456", "element_name": "Record", "depth": 1}
  ],
  "summary": {"total_records": 1, "total_size_bytes": 100}
}`

	if err := os.WriteFile(indexPath, []byte(testIndex), 0644); err != nil {
		t.Fatalf("Failed to write test index: %v", err)
	}

	store, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", indexPath, err)
	}
	defer func() { _ = store.Close() }()

	// Pass nil context - should not panic
	//nolint:staticcheck // SA1012: intentionally passing nil context to test defensive handling
	iter, err := store.Records(nil)
	if err != nil {
		t.Fatalf("Records(nil) failed: %v", err)
	}
	defer func() { _ = iter.Close() }()

	// Should be able to iterate without panic
	rec, err := iter.Next()
	if err != nil {
		t.Fatalf("Next() failed: %v", err)
	}
	if rec.StartOffset != 0 {
		t.Errorf("Expected start offset 0, got %d", rec.StartOffset)
	}
}

// TestJSONStoreContextCancellation verifies iteration respects context.
func TestJSONStoreContextCancellation(t *testing.T) {
	testDir := t.TempDir()
	indexPath := filepath.Join(testDir, "test.recordindex.json")

	testIndex := `{
  "version": "record-index/v0.1.0",
  "source": {"path": "/test/data.xml", "size_bytes": 1000, "sha256": "abc123"},
  "selector": {"xpath": "//Record", "element": "Record"},
  "records": [
    {"start_offset": 0, "end_offset": 100, "size_bytes": 100, "sha256": "def456", "element_name": "Record", "depth": 1}
  ],
  "summary": {"total_records": 1, "total_size_bytes": 100}
}`

	if err := os.WriteFile(indexPath, []byte(testIndex), 0644); err != nil {
		t.Fatalf("Failed to write test index: %v", err)
	}

	store, err := Open(indexPath)
	if err != nil {
		t.Fatalf("Open(%q) failed: %v", indexPath, err)
	}
	defer func() { _ = store.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	iter, err := store.Records(ctx)
	if err != nil {
		t.Fatalf("Records() failed: %v", err)
	}
	defer func() { _ = iter.Close() }()

	_, err = iter.Next()
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got: %v", err)
	}
}
