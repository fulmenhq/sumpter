package parallel

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"
)

func TestParallelExtractor_RejectsUnsafeOffsetSemanticsWithoutVerification(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.recordindex.json")
	sourcePath := filepath.Join(tmpDir, "test.xml")

	idx := &index.RecordIndex{
		Version: index.SchemaVersion,
		Source: index.SourceInfo{
			Path:              sourcePath,
			Compressed:        false,
			CompressionFormat: "none",
			OffsetKind:        index.OffsetKindDecompressedBytes,
		},
		Selector: index.SelectorInfo{XPath: "//Record", ElementName: "Record"},
		Records:  []index.RecordMetadata{},
		Summary:  index.SummaryStats{TotalRecords: 0},
	}
	if err := index.NewBuilder(index.BuildOptions{}).WriteToFile(idx, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	_, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    sourcePath,
		Workers:       1,
		VerifyIndex:   false,
		ExtractConfig: nil,
	}).Extract()
	if err == nil {
		t.Fatal("Expected unsafe offset semantics to be rejected")
	}
	if !strings.Contains(err.Error(), index.OffsetKindDecompressedBytes) {
		t.Fatalf("Expected offset kind in error, got: %v", err)
	}
}

func TestParallelExtractor_RejectsCompressedLiveSourcePathWithoutVerification(t *testing.T) {
	tmpDir := t.TempDir()
	indexPath := filepath.Join(tmpDir, "test.recordindex.json")
	sourcePath := filepath.Join(tmpDir, "test.xml.gz")

	idx := &index.RecordIndex{
		Version: index.SchemaVersion,
		Source: index.SourceInfo{
			Path:              sourcePath,
			Compressed:        false,
			CompressionFormat: "none",
			OffsetKind:        index.OffsetKindSourceBytes,
		},
		Selector: index.SelectorInfo{XPath: "//Record", ElementName: "Record"},
		Records:  []index.RecordMetadata{},
		Summary:  index.SummaryStats{TotalRecords: 0},
	}
	if err := index.NewBuilder(index.BuildOptions{}).WriteToFile(idx, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	_, err := NewParallelExtractor(ExtractionOptions{
		IndexPath:     indexPath,
		SourcePath:    sourcePath,
		Workers:       1,
		VerifyIndex:   false,
		ExtractConfig: nil,
	}).Extract()
	if err == nil {
		t.Fatal("Expected compressed live source path to be rejected")
	}
	if !strings.Contains(err.Error(), "appears gzip compressed") {
		t.Fatalf("Expected compressed source path error, got: %v", err)
	}
}
