//go:build cgo && seekablezstd

package store

import (
	"path/filepath"
	"runtime"
	"testing"

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
