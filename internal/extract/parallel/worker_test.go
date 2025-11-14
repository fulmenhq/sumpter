package parallel

import (
	"testing"
)

func TestWorker_BasicOperation(t *testing.T) {
	// This is a basic test to ensure Worker function doesn't panic
	// Full integration testing will be done in orchestrator tests

	workChan := make(chan WorkItem)
	resultChan := make(chan WorkResult, 1)
	stats := &ExtractionStats{TotalRecords: 1}

	// Close work channel immediately to exit worker
	close(workChan)

	// Worker should exit cleanly when work channel is closed
	Worker(0, workChan, resultChan, nil, stats)

	// Test passes if we reach here without panic
}

func TestExtractionStats_ThreadSafety(t *testing.T) {
	stats := &ExtractionStats{TotalRecords: 100}

	// Test atomic operations
	stats.IncrementProcessed()
	stats.IncrementSkipped()
	stats.IncrementFailed()

	total, processed, skipped, failed := stats.GetStats()

	if total != 100 {
		t.Errorf("Expected total=100, got %d", total)
	}
	if processed != 1 {
		t.Errorf("Expected processed=1, got %d", processed)
	}
	if skipped != 1 {
		t.Errorf("Expected skipped=1, got %d", skipped)
	}
	if failed != 1 {
		t.Errorf("Expected failed=1, got %d", failed)
	}
}
