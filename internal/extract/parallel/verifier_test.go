package parallel

import (
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"
)

func TestSafetyVerifier_CompressedSourceDetection(t *testing.T) {
	idx := &index.RecordIndex{
		Version: "1.0.0",
		Source: index.SourceInfo{
			Path:              "/tmp/test.xml.gz",
			Compressed:        true,
			CompressionFormat: "gzip",
		},
		Selector: index.SelectorInfo{
			XPath: "//record",
		},
	}

	verifier := NewSafetyVerifier(idx, "/tmp/test.xml.gz", "/tmp/test.index.json")

	err := verifier.VerifyIntegrity()
	if err == nil {
		t.Error("Expected error for compressed source, got nil")
	}

	if err != nil && len(err.Error()) == 0 {
		t.Error("Error message should not be empty")
	}

	if err != nil && !strings.Contains(err.Error(), "gunzip -c input.xml.gz > input.xml") {
		t.Fatalf("Expected safe decompression hint, got: %v", err)
	}
}

func TestSafetyVerifier_DecompressedOffsetKindDetection(t *testing.T) {
	idx := &index.RecordIndex{
		Version: "record-index-szst/v0.1.0",
		Source: index.SourceInfo{
			Path:       "/tmp/test.xml",
			Compressed: false,
			OffsetKind: index.OffsetKindDecompressedBytes,
		},
		Selector: index.SelectorInfo{
			XPath: "//record",
		},
	}

	verifier := NewSafetyVerifierFromHeader(idx, "/tmp/test.xml", "/tmp/test.recordindex.header.json")

	err := verifier.VerifyIntegrity()
	if err == nil {
		t.Error("Expected error for decompressed offset kind, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), index.OffsetKindDecompressedBytes) {
		t.Fatalf("Expected offset kind in error, got: %v", err)
	}
}

func TestSafetyVerifier_UncompressedSource(t *testing.T) {
	idx := &index.RecordIndex{
		Version: "1.0.0",
		Source: index.SourceInfo{
			Path:       "/tmp/test.xml",
			Compressed: false,
		},
	}

	verifier := NewSafetyVerifier(idx, "/tmp/test.xml", "/tmp/test.index.json")

	// Should not fail compression check (though file may not exist for SHA verification)
	_ = verifier.VerifyIntegrity()
	// We expect this might fail on SHA, but it shouldn't fail on compression check
}
