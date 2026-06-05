package parallel

import (
	"context"
	"fmt"
	"sync"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

// ParallelExtractor orchestrates parallel extraction using a record index
type ParallelExtractor struct {
	opts   ExtractionOptions
	logger *logging.ComponentLogger
}

// NewParallelExtractor creates a new parallel extractor
func NewParallelExtractor(opts ExtractionOptions) *ParallelExtractor {
	return &ParallelExtractor{
		opts:   opts,
		logger: logging.Component("parallel-extractor"),
	}
}

// Extract performs parallel extraction and returns ordered results.
//
// Uses streaming record access to avoid loading full []RecordMetadata into memory.
func (pe *ParallelExtractor) Extract() ([]map[string]interface{}, error) {
	pe.logger.Info("Starting parallel extraction",
		zap.String("index", pe.opts.IndexPath),
		zap.String("source", pe.opts.SourcePath),
		zap.Int("workers", pe.opts.Workers))

	// Use provided IndexStore or open a new one
	var indexStore store.IndexStore
	var err error
	ownsStore := false

	if pe.opts.IndexStore != nil {
		// Use pre-opened store (caller retains ownership)
		indexStore = pe.opts.IndexStore
		pe.logger.Debug("Using pre-opened index store")
	} else {
		// Open index store for streaming access
		indexStore, err = store.Open(pe.opts.IndexPath)
		if err != nil {
			return nil, fmt.Errorf("failed to open record index: %w", err)
		}
		ownsStore = true
	}

	// Only close if we own the store
	if ownsStore {
		defer func() { _ = indexStore.Close() }()
	}

	// Get header for logging and verification
	header, err := indexStore.Header()
	if err != nil {
		return nil, fmt.Errorf("failed to read index header: %w", err)
	}

	pe.logger.Info("Record index opened (streaming mode)",
		zap.String("version", header.Version),
		zap.Int("total_records", header.Summary.TotalRecords),
		zap.String("selector", header.Selector.XPath))

	if err := index.ValidateRecordIndexHeaderVersion(header.Version); err != nil {
		return nil, fmt.Errorf("unsupported record index header: %w", err)
	}

	if err := index.ValidateSourceByteOffsets(header, pe.opts.SourcePath); err != nil {
		return nil, fmt.Errorf("record index is not safe for parallel extraction: %w", err)
	}

	// Safety verification (if enabled)
	if pe.opts.VerifyIndex {
		verifier := NewSafetyVerifierFromHeader(header, pe.opts.SourcePath, pe.opts.IndexPath)
		if err := verifier.VerifyIntegrity(); err != nil {
			return nil, fmt.Errorf("safety verification failed: %w", err)
		}
		pe.logger.Info("Safety verification passed")
	}

	// Create seekable extractor
	extCfg, ok := pe.opts.ExtractConfig.(*extract.ExtractRecordMatch)
	if !ok {
		return nil, fmt.Errorf("extract config must be *extract.ExtractRecordMatch")
	}

	var sigCfg *extract.FileSignature
	if pe.opts.SignatureConfig != nil {
		sigCfg, ok = pe.opts.SignatureConfig.(*extract.FileSignature)
		if !ok {
			return nil, fmt.Errorf("signature config must be *extract.FileSignature")
		}
	}

	// Create work scheduler using streaming store
	ctx := context.Background()
	scheduler, err := NewWorkSchedulerFromStore(ctx, indexStore, pe.opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create work scheduler: %w", err)
	}

	// Schedule work (starts goroutine that streams records)
	if err := scheduler.ScheduleWork(); err != nil {
		return nil, fmt.Errorf("failed to schedule work: %w", err)
	}

	extractor := NewSeekableExtractor(pe.opts.SourcePath, extCfg, sigCfg, pe.opts.ExternalFields, pe.opts.RuntimeProvenance)

	// Start worker pool
	var wg sync.WaitGroup
	stats := scheduler.GetStats()

	pe.logger.Info("Starting worker pool",
		zap.Int("workers", pe.opts.Workers))

	for i := 0; i < pe.opts.Workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			Worker(workerID, scheduler.GetWorkChannel(), scheduler.GetResultChannel(), extractor, stats)
		}(i)
	}

	// Wait for all workers to finish, then close result channel
	go func() {
		wg.Wait()
		scheduler.CloseResultChannel()
		pe.logger.Info("All workers finished")
	}()

	// Create result aggregator
	aggregator := NewResultAggregatorWithRelease(header.Summary.TotalRecords, scheduler.releaseWindowSlot)
	skippedRecords := scheduler.GetSkippedRecords()
	aggregator.Collect(scheduler.GetResultChannelForRead(), skippedRecords, header.Summary.TotalRecords)

	// Collect ordered results
	var results []map[string]interface{}
	for result := range aggregator.GetOutputChannel() {
		if result.Skipped {
			continue
		}
		if result.Error != nil {
			pe.logger.Error("Record extraction failed",
				zap.Int("record_num", result.RecordNum),
				zap.Error(result.Error))
			continue
		}
		results = append(results, result.Data)
	}

	// Wait for aggregator to finish
	aggregator.Wait()

	// Log final statistics
	total, processed, skipped, failed := stats.GetStats()
	pe.logger.Info("Parallel extraction complete",
		zap.Int("total_records", total),
		zap.Int("processed", processed),
		zap.Int("skipped", skipped),
		zap.Int("failed", failed),
		zap.Int("extracted", len(results)))

	if failed > 0 {
		return results, fmt.Errorf("%d records failed to extract", failed)
	}

	return results, nil
}

// ExtractToSink performs parallel extraction and emits ordered records directly
// to sink. Scheduling is bounded by a sliding reorder window: workers can only
// run a limited number of records ahead of the ordered sink emission point.
func (pe *ParallelExtractor) ExtractToSink(ctx context.Context, sink extract.RecordSink) (extract.FileEmissionSummary, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return extract.FileEmissionSummary{}, fmt.Errorf("record sink cannot be nil")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	pe.logger.Info("Starting parallel extraction to sink",
		zap.String("index", pe.opts.IndexPath),
		zap.String("source", pe.opts.SourcePath),
		zap.Int("workers", pe.opts.Workers))

	var indexStore store.IndexStore
	var err error
	ownsStore := false

	if pe.opts.IndexStore != nil {
		indexStore = pe.opts.IndexStore
		pe.logger.Debug("Using pre-opened index store")
	} else {
		indexStore, err = store.Open(pe.opts.IndexPath)
		if err != nil {
			return extract.FileEmissionSummary{}, fmt.Errorf("failed to open record index: %w", err)
		}
		ownsStore = true
	}

	if ownsStore {
		defer func() { _ = indexStore.Close() }()
	}

	header, err := indexStore.Header()
	if err != nil {
		return extract.FileEmissionSummary{}, fmt.Errorf("failed to read index header: %w", err)
	}

	summary := extract.FileEmissionSummary{
		SourceFile:  header.Source.Path,
		RecordCount: 0,
		Disposition: extract.DispositionApplied,
	}
	if extCfg, ok := pe.opts.ExtractConfig.(*extract.ExtractRecordMatch); ok && extCfg != nil {
		summary.RecordType = extCfg.RecordType
	}

	if err := index.ValidateRecordIndexHeaderVersion(header.Version); err != nil {
		return summary, fmt.Errorf("unsupported record index header: %w", err)
	}

	if err := index.ValidateSourceByteOffsets(header, pe.opts.SourcePath); err != nil {
		return summary, fmt.Errorf("record index is not safe for parallel extraction: %w", err)
	}

	if pe.opts.VerifyIndex {
		verifier := NewSafetyVerifierFromHeader(header, pe.opts.SourcePath, pe.opts.IndexPath)
		if err := verifier.VerifyIntegrity(); err != nil {
			return summary, fmt.Errorf("safety verification failed: %w", err)
		}
		pe.logger.Info("Safety verification passed")
	}

	extCfg, ok := pe.opts.ExtractConfig.(*extract.ExtractRecordMatch)
	if !ok {
		return summary, fmt.Errorf("extract config must be *extract.ExtractRecordMatch")
	}

	var sigCfg *extract.FileSignature
	if pe.opts.SignatureConfig != nil {
		sigCfg, ok = pe.opts.SignatureConfig.(*extract.FileSignature)
		if !ok {
			return summary, fmt.Errorf("signature config must be *extract.FileSignature")
		}
	}

	scheduler, err := NewWorkSchedulerFromStore(ctx, indexStore, pe.opts)
	if err != nil {
		return summary, fmt.Errorf("failed to create work scheduler: %w", err)
	}

	if err := scheduler.ScheduleWork(); err != nil {
		return summary, fmt.Errorf("failed to schedule work: %w", err)
	}

	extractor := NewSeekableExtractor(pe.opts.SourcePath, extCfg, sigCfg, pe.opts.ExternalFields, pe.opts.RuntimeProvenance)

	var wg sync.WaitGroup
	stats := scheduler.GetStats()

	pe.logger.Info("Starting worker pool",
		zap.Int("workers", pe.opts.Workers))

	for i := 0; i < pe.opts.Workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			Worker(workerID, scheduler.GetWorkChannel(), scheduler.GetResultChannel(), extractor, stats)
		}(i)
	}

	go func() {
		wg.Wait()
		scheduler.CloseResultChannel()
		pe.logger.Info("All workers finished")
	}()

	aggregator := NewResultAggregatorWithRelease(header.Summary.TotalRecords, scheduler.releaseWindowSlot)
	skippedRecords := scheduler.GetSkippedRecords()
	aggregator.Collect(scheduler.GetResultChannelForRead(), skippedRecords, header.Summary.TotalRecords)

	var firstExtractionErr error
	var sinkErr error
	for result := range aggregator.GetOutputChannel() {
		if result.Skipped {
			continue
		}
		if result.Error != nil {
			pe.logger.Error("Record extraction failed",
				zap.Int("record_num", result.RecordNum),
				zap.Error(result.Error))
			if firstExtractionErr == nil {
				firstExtractionErr = result.Error
			}
			continue
		}
		if sinkErr != nil {
			continue
		}
		if err := sink.OnRecord(ctx, extract.NewEmittedRecord(result.Data)); err != nil {
			sinkErr = fmt.Errorf("failed to emit parallel record %d: %w", result.RecordNum, err)
			cancel()
			continue
		}
		summary.RecordCount++
	}

	aggregator.Wait()

	total, processed, skipped, failed := stats.GetStats()
	pe.logger.Info("Parallel extraction to sink complete",
		zap.Int("total_records", total),
		zap.Int("processed", processed),
		zap.Int("skipped", skipped),
		zap.Int("failed", failed),
		zap.Int("emitted", summary.RecordCount))

	if sinkErr != nil {
		summary.Disposition = extract.DispositionFailed
		summary.DispositionReason = extract.DispositionReasonInternalError
		summary.DispositionDetail = sinkErr.Error()
		if err := sink.OnFileBoundary(context.Background(), summary); err != nil {
			return summary, fmt.Errorf("%w; failed to emit parallel file boundary: %v", sinkErr, err)
		}
		return summary, sinkErr
	}

	if failed > 0 {
		summary.Disposition = extract.DispositionFailed
		summary.DispositionReason = extract.DispositionReasonInternalError
		if firstExtractionErr != nil {
			summary.DispositionDetail = firstExtractionErr.Error()
		}
		if err := sink.OnFileBoundary(ctx, summary); err != nil {
			return summary, fmt.Errorf("failed to emit parallel file boundary: %w", err)
		}
		return summary, fmt.Errorf("%d records failed to extract", failed)
	}

	if err := sink.OnFileBoundary(ctx, summary); err != nil {
		return summary, fmt.Errorf("failed to emit parallel file boundary: %w", err)
	}

	return summary, nil
}
