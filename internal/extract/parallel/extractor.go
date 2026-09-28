package parallel

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"go.uber.org/zap"
)

// SeekableExtractor extracts records from specific byte ranges in a file
type SeekableExtractor struct {
	filePath       string
	extCfg         *extract.ExtractRecordMatch
	sigCfg         *extract.FileSignature
	externalFields map[string]interface{}
	provenance     provenance.RuntimeOptions
	namespaces     map[int][]index.NamespaceDeclaration
	format         docnode.Format
	formatErr      error
	logger         *logging.ComponentLogger
}

// NewSeekableExtractor creates a new seekable extractor.
func NewSeekableExtractor(filePath string, extCfg *extract.ExtractRecordMatch, sigCfg *extract.FileSignature, externalFields map[string]interface{}, runtimeProvenance ...provenance.RuntimeOptions) *SeekableExtractor {
	var runtimeFields provenance.RuntimeOptions
	if len(runtimeProvenance) > 0 {
		runtimeFields = runtimeProvenance[0]
	}

	se := &SeekableExtractor{
		filePath:       filePath,
		extCfg:         extCfg,
		sigCfg:         sigCfg,
		externalFields: externalFields,
		provenance:     runtimeFields,
		logger:         logging.Component("parallel-extractor"),
	}
	format, ok := docnode.Lookup(docnode.Default)
	if !ok {
		se.formatErr = fmt.Errorf("input format %q is not registered", docnode.Default)
	}
	se.format = format
	return se
}

// SetNamespaceContexts installs the index-level namespace context table used to
// reconstruct standalone record fragments before parsing.
func (se *SeekableExtractor) SetNamespaceContexts(contexts []index.NamespaceContext) {
	se.namespaces = index.NamespaceContextByID(contexts)
}

// ExtractRecord extracts a single record from a specific byte range
func (se *SeekableExtractor) ExtractRecord(item WorkItem) WorkResult {
	result := WorkResult{
		RecordNum: item.RecordNum,
	}

	se.logger.Debug("Extracting record",
		zap.Int("record_num", item.RecordNum),
		zap.Int64("start_offset", item.StartOffset),
		zap.Int64("end_offset", item.EndOffset),
		zap.Int64("size_bytes", item.SizeBytes))

	// Read the specific byte range
	xmlData, err := se.readByteRange(item.StartOffset, item.EndOffset)
	if err != nil {
		err = se.provenance.DiagnosticError(err, se.filePath)
		result.Error = fmt.Errorf("failed to read byte range for record %d: %w", item.RecordNum, err)
		return result
	}
	record := &docnode.Record{Raw: xmlData, Num: item.RecordNum}
	if len(se.namespaces) > 0 {
		record.Context = se.namespaces[item.NamespaceContextRef]
	}
	if se.formatErr != nil {
		result.Error = fmt.Errorf("failed to parse XML for record %d: %w", item.RecordNum, se.formatErr)
		return result
	}

	// Parse XML into mini-DOM, applying the record's namespace context
	doc, err := se.format.ParseRecord(record)
	if err != nil {
		var contextErr *docnode.ContextError
		if errors.As(err, &contextErr) {
			err = se.provenance.DiagnosticError(contextErr.Err, se.filePath)
			result.Error = fmt.Errorf("failed to apply namespace context for record %d: %w", item.RecordNum, err)
			return result
		}
		err = se.provenance.DiagnosticError(err, se.filePath)
		result.Error = fmt.Errorf("failed to parse XML for record %d: %w", item.RecordNum, err)
		return result
	}

	// Extract fields using existing logic
	recordData, err := se.extractFields(doc)
	if err != nil {
		err = se.provenance.DiagnosticError(err, se.filePath)
		result.Error = fmt.Errorf("failed to extract fields for record %d: %w", item.RecordNum, err)
		return result
	}

	if err := extract.EnrichRecordWithRecordNum(recordData, se.filePath, se.sigCfg, se.extCfg, se.provenance, item.RecordNum); err != nil {
		err = se.provenance.DiagnosticError(err, se.filePath)
		result.Error = fmt.Errorf("failed to enrich record %d: %w", item.RecordNum, err)
		return result
	}

	result.Data = recordData
	return result
}

// readByteRange reads a specific byte range from the file
// This method is thread-safe as each worker gets independent file access
func (se *SeekableExtractor) readByteRange(start, end int64) ([]byte, error) {
	// Each worker opens its own file handle for independent seeking
	file, err := os.Open(se.filePath) // #nosec G304 - path comes from user-provided CLI argument
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Seek to start position
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek to offset %d: %w", start, err)
	}

	// Read exact byte range
	size := end - start
	buf := make([]byte, size)
	n, err := io.ReadFull(file, buf)
	if err != nil {
		return nil, fmt.Errorf("failed to read %d bytes at offset %d: %w", size, start, err)
	}
	if int64(n) != size {
		return nil, fmt.Errorf("read %d bytes, expected %d", n, size)
	}

	return buf, nil
}

// extractFields extracts field mappings from an XML document
func (se *SeekableExtractor) extractFields(doc docnode.Document) (map[string]interface{}, error) {
	// Use existing extract package logic
	return extract.ExtractFieldsWithExternal(doc, se.extCfg, se.externalFields)
}

// Worker is a goroutine that processes work items from the work channel
func Worker(id int, workChan <-chan WorkItem, resultChan chan<- WorkResult, extractor *SeekableExtractor, stats *ExtractionStats) {
	logger := logging.Component("parallel-worker")
	logger.Debug("Worker started", zap.Int("worker_id", id))

	for item := range workChan {
		logger.Debug("Worker processing item",
			zap.Int("worker_id", id),
			zap.Int("record_num", item.RecordNum))

		result := extractor.ExtractRecord(item)

		if result.Error != nil {
			logger.Error("Worker extraction failed",
				zap.Int("worker_id", id),
				zap.Int("record_num", item.RecordNum),
				zap.Error(result.Error))
			stats.IncrementFailed()
		} else {
			logger.Debug("Worker extraction succeeded",
				zap.Int("worker_id", id),
				zap.Int("record_num", item.RecordNum))
			stats.IncrementProcessed()
		}

		resultChan <- result
	}

	logger.Debug("Worker finished", zap.Int("worker_id", id))
}
