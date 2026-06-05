package parallel

import (
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

// NewResultAggregator creates a new result aggregator
// totalRecords is the expected number of results (used for completion detection)
func NewResultAggregator(totalRecords int) *ResultAggregator {
	return NewResultAggregatorWithRelease(totalRecords, nil)
}

// NewResultAggregatorWithRelease creates a result aggregator that calls
// releaseSlot after each record number advances through ordered output.
func NewResultAggregatorWithRelease(totalRecords int, releaseSlot func()) *ResultAggregator {
	return &ResultAggregator{
		results:      make(map[int]WorkResult),
		nextExpected: 1, // Record numbers start at 1
		outputChan:   make(chan WorkResult),
		doneChan:     make(chan struct{}),
		releaseSlot:  releaseSlot,
	}
}

// Add receives a result from a worker (potentially out of order)
// and emits results to output channel in strict sequential order
func (ra *ResultAggregator) Add(result WorkResult) {
	ra.mu.Lock()
	defer ra.mu.Unlock()

	logger := logging.Component("parallel-aggregator")
	logger.Debug("Aggregator received result",
		zap.Int("record_num", result.RecordNum),
		zap.Int("next_expected", ra.nextExpected),
		zap.Int("buffered_count", len(ra.results)))

	// Store result in buffer
	ra.results[result.RecordNum] = result

	// Emit all consecutive results starting from nextExpected
	for {
		if res, exists := ra.results[ra.nextExpected]; exists {
			logger.Debug("Aggregator emitting result",
				zap.Int("record_num", ra.nextExpected))

			// This send intentionally happens while holding ra.mu. Collect is
			// the single writer, and blocking here is the backpressure point
			// that prevents later results from advancing ahead of ordered output.
			ra.outputChan <- res
			ra.release()
			delete(ra.results, ra.nextExpected)
			ra.nextExpected++
		} else {
			// Wait for missing record
			logger.Debug("Aggregator waiting for record",
				zap.Int("waiting_for", ra.nextExpected),
				zap.Int("buffered_results", len(ra.results)))
			break
		}
	}
}

// Collect starts the aggregation process
// Reads from resultChan and maintains ordering
func (ra *ResultAggregator) Collect(resultChan <-chan WorkResult, skippedRecords []int, totalExpected int) {
	logger := logging.Component("parallel-aggregator")
	logger.Info("Aggregator starting",
		zap.Int("total_expected", totalExpected),
		zap.Int("skipped_count", len(skippedRecords)))

	// Build set of skipped record numbers for O(1) lookup
	skipped := make(map[int]bool)
	for _, recordNum := range skippedRecords {
		skipped[recordNum] = true
	}

	go func() {
		defer close(ra.outputChan)
		defer close(ra.doneChan)

		for result := range resultChan {
			ra.Add(result)
		}

		// After all results received, emit any remaining buffered results
		// and advance past skipped records
		ra.mu.Lock()
		for ra.nextExpected <= totalExpected {
			if skipped[ra.nextExpected] {
				// Skip this record number
				logger.Debug("Aggregator skipping record",
					zap.Int("record_num", ra.nextExpected))
				ra.release()
				ra.nextExpected++
				continue
			}

			if res, exists := ra.results[ra.nextExpected]; exists {
				logger.Debug("Aggregator emitting final buffered result",
					zap.Int("record_num", ra.nextExpected))
				// Keep final-drain emission under the same single-writer
				// backpressure invariant as Add.
				ra.outputChan <- res
				ra.release()
				delete(ra.results, ra.nextExpected)
				ra.nextExpected++
			} else {
				// Missing result (likely failed extraction)
				logger.Warn("Aggregator missing expected result",
					zap.Int("record_num", ra.nextExpected))
				ra.nextExpected++
			}
		}

		// Warn about any unexpected buffered results
		if len(ra.results) > 0 {
			logger.Warn("Aggregator has unexpected buffered results",
				zap.Int("count", len(ra.results)))
		}

		ra.mu.Unlock()

		logger.Info("Aggregator complete",
			zap.Int("last_emitted", ra.nextExpected-1))
	}()
}

func (ra *ResultAggregator) release() {
	if ra.releaseSlot != nil {
		ra.releaseSlot()
	}
}

// GetOutputChannel returns the channel that emits results in order
func (ra *ResultAggregator) GetOutputChannel() <-chan WorkResult {
	return ra.outputChan
}

// Wait blocks until aggregation is complete
func (ra *ResultAggregator) Wait() {
	<-ra.doneChan
}
