package parallel

import (
	"fmt"
	"sync"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
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

// Extract performs parallel extraction and returns ordered results
func (pe *ParallelExtractor) Extract() ([]map[string]interface{}, error) {
	pe.logger.Info("Starting parallel extraction",
		zap.String("index", pe.opts.IndexPath),
		zap.String("source", pe.opts.SourcePath),
		zap.Int("workers", pe.opts.Workers))

	// Load record index
	idx, err := index.LoadIndex(pe.opts.IndexPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load record index: %w", err)
	}

	pe.logger.Info("Record index loaded",
		zap.String("version", idx.Version),
		zap.Int("total_records", len(idx.Records)),
		zap.String("selector", idx.Selector.XPath))

	// Safety verification (if enabled)
	if pe.opts.VerifyIndex {
		verifier := NewSafetyVerifier(idx, pe.opts.SourcePath, pe.opts.IndexPath)
		if err := verifier.VerifyIntegrity(); err != nil {
			return nil, fmt.Errorf("safety verification failed: %w", err)
		}
		pe.logger.Info("Safety verification passed")
	}

	// Create work scheduler
	scheduler, err := NewWorkScheduler(idx, pe.opts)
	if err != nil {
		return nil, fmt.Errorf("failed to create work scheduler: %w", err)
	}

	// Schedule work (starts goroutine that populates work channel)
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
	aggregator := NewResultAggregator(len(idx.Records))
	skippedRecords := scheduler.GetSkippedRecords()
	aggregator.Collect(scheduler.GetResultChannelForRead(), skippedRecords, len(idx.Records))

	// Collect ordered results
	var results []map[string]interface{}
	for result := range aggregator.GetOutputChannel() {
		if result.Error != nil {
			pe.logger.Error("Record extraction failed",
				zap.Int("record_num", result.RecordNum),
				zap.Error(result.Error))
			// Continue processing other records
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
