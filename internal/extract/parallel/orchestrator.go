package parallel

import (
	"context"
	"fmt"
	"sync"

	"github.com/fulmenhq/sumpter/internal/extract"
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

	// Safety verification (if enabled)
	if pe.opts.VerifyIndex {
		verifier := NewSafetyVerifierFromHeader(header, pe.opts.SourcePath, pe.opts.IndexPath)
		if err := verifier.VerifyIntegrity(); err != nil {
			return nil, fmt.Errorf("safety verification failed: %w", err)
		}
		pe.logger.Info("Safety verification passed")
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

	extractor := NewSeekableExtractor(pe.opts.SourcePath, extCfg, sigCfg, pe.opts.ExternalFields)

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
	aggregator := NewResultAggregator(header.Summary.TotalRecords)
	skippedRecords := scheduler.GetSkippedRecords()
	aggregator.Collect(scheduler.GetResultChannelForRead(), skippedRecords, header.Summary.TotalRecords)

	// Collect ordered results
	var results []map[string]interface{}
	for result := range aggregator.GetOutputChannel() {
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
