package parallel

import (
	"sync"

	"github.com/fulmenhq/sumpter/internal/index"
)

// WorkItem represents a single record extraction task
type WorkItem struct {
	RecordNum   int
	StartOffset int64
	EndOffset   int64
	SizeBytes   int64
}

// WorkResult represents the result of extracting a single record
type WorkResult struct {
	RecordNum int
	Data      map[string]interface{}
	Error     error
}

// ExtractionOptions configures parallel extraction behavior
type ExtractionOptions struct {
	// Index path and source file
	IndexPath  string
	SourcePath string

	// Worker pool configuration
	Workers int // Number of parallel workers

	// Safety constraints
	MaxRecordSizeMB  int  // Maximum record size in MB (0 = no limit)
	SkipLargeRecords bool // If true, skip oversized records; if false, fail
	VerifyIndex      bool // Run SHA verification before extraction

	// Progress reporting
	ShowProgress bool

	// Extract configuration (from existing extractor)
	ExtractConfig   interface{} // Will be *extract.ExtractRecordMatch
	SignatureConfig interface{} // Will be *extract.FileSignature
	ExternalFields  map[string]interface{}
}

// ExtractionStats tracks extraction metrics
type ExtractionStats struct {
	TotalRecords     int
	ProcessedRecords int
	SkippedRecords   int
	FailedRecords    int
	WorkersUsed      int
	mu               sync.RWMutex
}

// IncrementProcessed atomically increments the processed record count
func (s *ExtractionStats) IncrementProcessed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ProcessedRecords++
}

// IncrementSkipped atomically increments the skipped record count
func (s *ExtractionStats) IncrementSkipped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.SkippedRecords++
}

// IncrementFailed atomically increments the failed record count
func (s *ExtractionStats) IncrementFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.FailedRecords++
}

// GetStats returns a snapshot of current statistics
func (s *ExtractionStats) GetStats() (total, processed, skipped, failed int) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.TotalRecords, s.ProcessedRecords, s.SkippedRecords, s.FailedRecords
}

// WorkScheduler distributes extraction work across workers
type WorkScheduler struct {
	index          *index.RecordIndex
	opts           ExtractionOptions
	workChan       chan WorkItem
	resultChan     chan WorkResult
	stats          *ExtractionStats
	skippedRecords []int // Record numbers of skipped records
	mu             sync.Mutex
}

// ResultAggregator collects results and emits them in order
type ResultAggregator struct {
	results      map[int]WorkResult
	nextExpected int
	outputChan   chan WorkResult
	doneChan     chan struct{}
	mu           sync.Mutex
}
