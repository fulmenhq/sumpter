package parallel

import (
	"fmt"
	"runtime"

	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

const (
	// MaxWorkerMultiplier limits worker count to NumCPU * multiplier
	MaxWorkerMultiplier = 2
)

// NewWorkScheduler creates a new work scheduler from a record index
func NewWorkScheduler(idx *index.RecordIndex, opts ExtractionOptions) (*WorkScheduler, error) {
	if idx == nil {
		return nil, fmt.Errorf("record index cannot be nil")
	}

	// Validate and cap worker count
	workers := opts.Workers
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	maxWorkers := runtime.NumCPU() * MaxWorkerMultiplier
	if workers > maxWorkers {
		workers = maxWorkers
		logging.Component("parallel-scheduler").Info("Capping worker count",
			zap.Int("requested", opts.Workers),
			zap.Int("capped_to", workers),
			zap.Int("max_allowed", maxWorkers))
	}
	opts.Workers = workers

	stats := &ExtractionStats{
		TotalRecords: len(idx.Records),
		WorkersUsed:  workers,
	}

	return &WorkScheduler{
		index:          idx,
		opts:           opts,
		workChan:       make(chan WorkItem, workers*2), // Buffer 2x workers
		resultChan:     make(chan WorkResult, workers*2),
		stats:          stats,
		skippedRecords: make([]int, 0),
	}, nil
}

// ScheduleWork distributes record extraction tasks across workers
// Returns immediately after populating work channel
func (ws *WorkScheduler) ScheduleWork() error {
	logger := logging.Component("parallel-scheduler")
	logger.Info("Scheduling work",
		zap.Int("total_records", len(ws.index.Records)),
		zap.Int("workers", ws.opts.Workers),
		zap.Int("max_record_size_mb", ws.opts.MaxRecordSizeMB))

	maxRecordBytes := int64(ws.opts.MaxRecordSizeMB) * 1024 * 1024

	go func() {
		defer close(ws.workChan)

		for _, record := range ws.index.Records {
			// Apply max record size filter if specified
			if ws.opts.MaxRecordSizeMB > 0 && record.SizeBytes > maxRecordBytes {
				if ws.opts.SkipLargeRecords {
					logger.Debug("Skipping oversized record",
						zap.Int("record_num", record.RecordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))

					ws.mu.Lock()
					ws.skippedRecords = append(ws.skippedRecords, record.RecordNum)
					ws.mu.Unlock()
					ws.stats.IncrementSkipped()
					continue
				} else {
					// Fail fast if not skipping
					logger.Error("Record exceeds size limit",
						zap.Int("record_num", record.RecordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))
					// Send error result
					ws.resultChan <- WorkResult{
						RecordNum: record.RecordNum,
						Error: fmt.Errorf("record %d size %d bytes exceeds limit %d MB",
							record.RecordNum, record.SizeBytes, ws.opts.MaxRecordSizeMB),
					}
					ws.stats.IncrementFailed()
					continue
				}
			}

			// Create work item
			workItem := WorkItem{
				RecordNum:   record.RecordNum,
				StartOffset: record.StartOffset,
				EndOffset:   record.EndOffset,
				SizeBytes:   record.SizeBytes,
			}

			// Send to work channel (blocks if workers are busy)
			ws.workChan <- workItem
		}

		logger.Info("Work scheduling complete",
			zap.Int("scheduled", len(ws.index.Records)-len(ws.skippedRecords)),
			zap.Int("skipped", len(ws.skippedRecords)))
	}()

	return nil
}

// GetWorkChannel returns the channel for workers to receive work items
func (ws *WorkScheduler) GetWorkChannel() <-chan WorkItem {
	return ws.workChan
}

// GetResultChannel returns the channel for workers to send results
func (ws *WorkScheduler) GetResultChannel() chan<- WorkResult {
	return ws.resultChan
}

// GetResultChannelForRead returns the result channel for reading (aggregator)
func (ws *WorkScheduler) GetResultChannelForRead() <-chan WorkResult {
	return ws.resultChan
}

// GetStats returns current extraction statistics
func (ws *WorkScheduler) GetStats() *ExtractionStats {
	return ws.stats
}

// GetSkippedRecords returns the list of skipped record numbers
func (ws *WorkScheduler) GetSkippedRecords() []int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	// Return a copy to prevent external modification
	result := make([]int, len(ws.skippedRecords))
	copy(result, ws.skippedRecords)
	return result
}

// CloseResultChannel closes the result channel (called after all workers finish)
func (ws *WorkScheduler) CloseResultChannel() {
	close(ws.resultChan)
}
