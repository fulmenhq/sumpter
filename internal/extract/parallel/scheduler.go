package parallel

import (
	"context"
	"fmt"
	"io"
	"runtime"

	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

const (
	// MaxWorkerMultiplier limits worker count to NumCPU * multiplier
	MaxWorkerMultiplier = 2
)

// NewWorkScheduler creates a new work scheduler from a record index.
//
// Deprecated: Use NewWorkSchedulerFromStore for streaming access.
func NewWorkScheduler(idx *index.RecordIndex, opts ExtractionOptions) (*WorkScheduler, error) {
	if idx == nil {
		return nil, fmt.Errorf("record index cannot be nil")
	}

	workers := capWorkerCount(opts.Workers)
	opts.Workers = workers
	reorderWindow := capReorderWindow(opts.ReorderWindow, workers)

	stats := &ExtractionStats{
		TotalRecords: len(idx.Records),
		WorkersUsed:  workers,
	}

	return &WorkScheduler{
		index:          idx,
		ctx:            context.Background(),
		opts:           opts,
		workChan:       make(chan WorkItem, workers*2),
		resultChan:     make(chan WorkResult, workers*2),
		windowSlots:    make(chan struct{}, reorderWindow),
		stats:          stats,
		skippedRecords: make([]int, 0),
	}, nil
}

// NewWorkSchedulerFromStore creates a new work scheduler that streams records
// from an IndexStore, avoiding loading the full records array into memory.
func NewWorkSchedulerFromStore(ctx context.Context, indexStore store.IndexStore, opts ExtractionOptions) (*WorkScheduler, error) {
	if indexStore == nil {
		return nil, fmt.Errorf("index store cannot be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	// Get header for record count
	header, err := indexStore.Header()
	if err != nil {
		return nil, fmt.Errorf("failed to get index header: %w", err)
	}

	workers := capWorkerCount(opts.Workers)
	opts.Workers = workers
	reorderWindow := capReorderWindow(opts.ReorderWindow, workers)

	stats := &ExtractionStats{
		TotalRecords: header.Summary.TotalRecords,
		WorkersUsed:  workers,
	}

	return &WorkScheduler{
		indexStore:     indexStore,
		ctx:            ctx,
		opts:           opts,
		workChan:       make(chan WorkItem, workers*2),
		resultChan:     make(chan WorkResult, workers*2),
		windowSlots:    make(chan struct{}, reorderWindow),
		stats:          stats,
		skippedRecords: make([]int, 0),
	}, nil
}

// capWorkerCount validates and caps worker count based on CPU.
func capWorkerCount(requested int) int {
	workers := requested
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	maxWorkers := runtime.NumCPU() * MaxWorkerMultiplier
	if workers > maxWorkers {
		logging.Component("parallel-scheduler").Info("Capping worker count",
			zap.Int("requested", requested),
			zap.Int("capped_to", maxWorkers),
			zap.Int("max_allowed", maxWorkers))
		workers = maxWorkers
	}
	return workers
}

func capReorderWindow(requested, workers int) int {
	if requested > 0 {
		return requested
	}
	if workers <= 0 {
		workers = 1
	}
	return workers * 2
}

func (ws *WorkScheduler) acquireWindowSlot() bool {
	if ws.windowSlots == nil {
		return true
	}
	select {
	case ws.windowSlots <- struct{}{}:
		return true
	case <-ws.ctx.Done():
		return false
	}
}

func (ws *WorkScheduler) releaseWindowSlot() {
	if ws.windowSlots == nil {
		return
	}
	select {
	case <-ws.windowSlots:
	default:
	}
}

// ScheduleWork distributes record extraction tasks across workers.
// Returns immediately after starting the scheduling goroutine.
func (ws *WorkScheduler) ScheduleWork() error {
	// Use streaming path if indexStore is available
	if ws.indexStore != nil {
		return ws.scheduleWorkStreaming()
	}

	// Legacy path: iterate over full records slice
	return ws.scheduleWorkLegacy()
}

// scheduleWorkStreaming uses the IndexStore iterator to stream records.
func (ws *WorkScheduler) scheduleWorkStreaming() error {
	logger := logging.Component("parallel-scheduler")
	logger.Info("Scheduling work (streaming mode)",
		zap.Int("total_records", ws.stats.TotalRecords),
		zap.Int("workers", ws.opts.Workers),
		zap.Int("max_record_size_mb", ws.opts.MaxRecordSizeMB))

	maxRecordBytes := int64(ws.opts.MaxRecordSizeMB) * 1024 * 1024

	go func() {
		defer close(ws.workChan)

		iter, err := ws.indexStore.Records(ws.ctx)
		if err != nil {
			logger.Error("Failed to get records iterator", zap.Error(err))
			return
		}
		defer func() { _ = iter.Close() }()

		localCounter := 0
		scheduled := 0

		for {
			// Check for context cancellation
			select {
			case <-ws.ctx.Done():
				logger.Info("Scheduling cancelled", zap.Error(ws.ctx.Err()))
				return
			default:
			}

			record, err := iter.Next()
			if err == io.EOF {
				break
			}
			localCounter++
			if err != nil {
				readErr := fmt.Errorf("failed to read record %d from index: %w", localCounter, err)
				logger.Error("Failed to read record", zap.Int("local_counter", localCounter), zap.Error(err))
				if !ws.acquireWindowSlot() {
					logger.Info("Scheduling cancelled before read-error result send", zap.Error(ws.ctx.Err()))
					return
				}
				select {
				case ws.resultChan <- WorkResult{RecordNum: localCounter, Error: readErr}:
				case <-ws.ctx.Done():
					ws.releaseWindowSlot()
					logger.Info("Scheduling cancelled during read-error result send", zap.Error(ws.ctx.Err()))
					return
				}
				ws.stats.IncrementFailed()
				continue
			}

			// Use record's RecordNum only if explicitly set (> 0).
			// RecordNum == 0 is ambiguous (could be unset or 0-based index),
			// so we always use localCounter for safety. Future stores that use
			// 0-based numbering should declare record_num_base in the header.
			recordNum := record.RecordNum
			if recordNum <= 0 {
				recordNum = localCounter
			}

			// Apply max record size filter if specified
			if ws.opts.MaxRecordSizeMB > 0 && record.SizeBytes > maxRecordBytes {
				if !ws.acquireWindowSlot() {
					logger.Info("Scheduling cancelled before oversized record handling", zap.Error(ws.ctx.Err()))
					return
				}
				if ws.opts.SkipLargeRecords {
					logger.Debug("Skipping oversized record",
						zap.Int("record_num", recordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))

					ws.mu.Lock()
					ws.skippedRecords = append(ws.skippedRecords, recordNum)
					ws.mu.Unlock()
					ws.stats.IncrementSkipped()
					select {
					case ws.resultChan <- WorkResult{RecordNum: recordNum, Skipped: true}:
					case <-ws.ctx.Done():
						ws.releaseWindowSlot()
						logger.Info("Scheduling cancelled during skipped result send", zap.Error(ws.ctx.Err()))
						return
					}
					continue
				} else {
					logger.Error("Record exceeds size limit",
						zap.Int("record_num", recordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))
					select {
					case ws.resultChan <- WorkResult{
						RecordNum: recordNum,
						Error: fmt.Errorf("record %d size %d bytes exceeds limit %d MB",
							recordNum, record.SizeBytes, ws.opts.MaxRecordSizeMB),
					}:
					case <-ws.ctx.Done():
						ws.releaseWindowSlot()
						logger.Info("Scheduling cancelled during oversized failure send", zap.Error(ws.ctx.Err()))
						return
					}
					ws.stats.IncrementFailed()
					continue
				}
			}

			// Create work item
			workItem := WorkItem{
				RecordNum:           recordNum,
				StartOffset:         record.StartOffset,
				EndOffset:           record.EndOffset,
				SizeBytes:           record.SizeBytes,
				NamespaceContextRef: record.NamespaceContextRef,
			}

			// Send to work channel (blocks if workers are busy)
			if !ws.acquireWindowSlot() {
				logger.Info("Scheduling cancelled before work send", zap.Error(ws.ctx.Err()))
				return
			}
			select {
			case ws.workChan <- workItem:
				scheduled++
			case <-ws.ctx.Done():
				ws.releaseWindowSlot()
				logger.Info("Scheduling cancelled during send", zap.Error(ws.ctx.Err()))
				return
			}
		}

		ws.mu.Lock()
		skippedCount := len(ws.skippedRecords)
		ws.mu.Unlock()

		logger.Info("Work scheduling complete (streaming)",
			zap.Int("scheduled", scheduled),
			zap.Int("skipped", skippedCount))
	}()

	return nil
}

// scheduleWorkLegacy uses the full records slice (deprecated path).
func (ws *WorkScheduler) scheduleWorkLegacy() error {
	logger := logging.Component("parallel-scheduler")
	logger.Info("Scheduling work (legacy mode)",
		zap.Int("total_records", len(ws.index.Records)),
		zap.Int("workers", ws.opts.Workers),
		zap.Int("max_record_size_mb", ws.opts.MaxRecordSizeMB))

	maxRecordBytes := int64(ws.opts.MaxRecordSizeMB) * 1024 * 1024

	go func() {
		defer close(ws.workChan)

		for _, record := range ws.index.Records {
			// Apply max record size filter if specified
			if ws.opts.MaxRecordSizeMB > 0 && record.SizeBytes > maxRecordBytes {
				if !ws.acquireWindowSlot() {
					logger.Info("Scheduling cancelled before oversized record handling", zap.Error(ws.ctx.Err()))
					return
				}
				if ws.opts.SkipLargeRecords {
					logger.Debug("Skipping oversized record",
						zap.Int("record_num", record.RecordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))

					ws.mu.Lock()
					ws.skippedRecords = append(ws.skippedRecords, record.RecordNum)
					ws.mu.Unlock()
					ws.stats.IncrementSkipped()
					ws.resultChan <- WorkResult{RecordNum: record.RecordNum, Skipped: true}
					continue
				} else {
					logger.Error("Record exceeds size limit",
						zap.Int("record_num", record.RecordNum),
						zap.Int64("size_bytes", record.SizeBytes),
						zap.Int("max_allowed_mb", ws.opts.MaxRecordSizeMB))
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
				RecordNum:           record.RecordNum,
				StartOffset:         record.StartOffset,
				EndOffset:           record.EndOffset,
				SizeBytes:           record.SizeBytes,
				NamespaceContextRef: record.NamespaceContextRef,
			}

			// Send to work channel (blocks if workers are busy)
			if !ws.acquireWindowSlot() {
				logger.Info("Scheduling cancelled before work send", zap.Error(ws.ctx.Err()))
				return
			}
			ws.workChan <- workItem
		}

		logger.Info("Work scheduling complete (legacy)",
			zap.Int("scheduled", len(ws.index.Records)-len(ws.skippedRecords)),
			zap.Int("skipped", len(ws.skippedRecords)))
	}()

	return nil
}

// GetWorkChannel returns the channel for workers to receive work items.
func (ws *WorkScheduler) GetWorkChannel() <-chan WorkItem {
	return ws.workChan
}

// GetResultChannel returns the channel for workers to send results.
func (ws *WorkScheduler) GetResultChannel() chan<- WorkResult {
	return ws.resultChan
}

// GetResultChannelForRead returns the result channel for reading (aggregator).
func (ws *WorkScheduler) GetResultChannelForRead() <-chan WorkResult {
	return ws.resultChan
}

// GetStats returns current extraction statistics.
func (ws *WorkScheduler) GetStats() *ExtractionStats {
	return ws.stats
}

// GetSkippedRecords returns the list of skipped record numbers.
func (ws *WorkScheduler) GetSkippedRecords() []int {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	result := make([]int, len(ws.skippedRecords))
	copy(result, ws.skippedRecords)
	return result
}

// CloseResultChannel closes the result channel (called after all workers finish).
func (ws *WorkScheduler) CloseResultChannel() {
	close(ws.resultChan)
}
