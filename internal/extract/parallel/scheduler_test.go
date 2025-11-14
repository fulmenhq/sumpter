package parallel

import (
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"
)

func TestNewWorkScheduler(t *testing.T) {
	idx := &index.RecordIndex{
		Version: "1.0.0",
		Records: []index.RecordMetadata{
			{RecordNum: 0, StartOffset: 0, EndOffset: 100, SizeBytes: 100},
			{RecordNum: 1, StartOffset: 100, EndOffset: 200, SizeBytes: 100},
		},
	}

	opts := ExtractionOptions{
		Workers:          2,
		MaxRecordSizeMB:  1,
		SkipLargeRecords: false,
	}

	scheduler, err := NewWorkScheduler(idx, opts)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	if scheduler == nil {
		t.Fatal("Scheduler is nil")
	}

	stats := scheduler.GetStats()
	if stats.TotalRecords != 2 {
		t.Errorf("Expected 2 total records, got %d", stats.TotalRecords)
	}
}

func TestWorkScheduler_SkipLargeRecords(t *testing.T) {
	idx := &index.RecordIndex{
		Version: "1.0.0",
		Records: []index.RecordMetadata{
			{RecordNum: 0, StartOffset: 0, EndOffset: 100, SizeBytes: 100},
			{RecordNum: 1, StartOffset: 100, EndOffset: 300000, SizeBytes: 199900}, // ~200KB (large)
			{RecordNum: 2, StartOffset: 300000, EndOffset: 300100, SizeBytes: 100},
		},
	}

	opts := ExtractionOptions{
		Workers:          2,
		MaxRecordSizeMB:  0, // Use default 10MB
		SkipLargeRecords: true,
	}

	scheduler, err := NewWorkScheduler(idx, opts)
	if err != nil {
		t.Fatalf("Failed to create scheduler: %v", err)
	}

	// All records should fit under 10MB default limit
	stats := scheduler.GetStats()
	if stats.TotalRecords != 3 {
		t.Errorf("Expected 3 total records, got %d", stats.TotalRecords)
	}
}
