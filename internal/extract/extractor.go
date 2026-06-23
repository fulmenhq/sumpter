package extract

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/xmlquery"
	xpath "github.com/antchfx/xpath"
	"github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
	"github.com/fulmenhq/sumpter/internal/extract/transforms"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/validation"
	"github.com/fulmenhq/sumpter/internal/validation/dsl"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

var (
	extractValidatorOnce sync.Once
	extractValidator     *validation.SchemaValidator
	extractValidatorErr  error
	transformRegistry    = transforms.NewTransformRegistry()
)

func getExtractSchemaValidator() (*validation.SchemaValidator, error) {
	extractValidatorOnce.Do(func() {
		schemaFS, err := assets.GetSchemasFS()
		if err != nil {
			extractValidatorErr = fmt.Errorf("failed to access embedded schemas: %w", err)
			return
		}
		extractValidator = validation.NewSchemaValidatorFromFS(schemaFS)
	})
	return extractValidator, extractValidatorErr
}

// LoadSignatureConfig loads a signature configuration from YAML file
func LoadSignatureConfig(path string) (*FileSignature, error) {
	data, err := os.ReadFile(path) // #nosec G304 - path comes from user-provided config file path
	if err != nil {
		return nil, fmt.Errorf("failed to read signature config: %w", err)
	}

	var cfg FileSignature
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse signature config: %w", err)
	}

	return &cfg, nil
}

// LoadApplicabilityConfig loads a recipe applicability predicate from YAML.
func LoadApplicabilityConfig(path string) (*ApplicabilityConfig, error) {
	data, err := os.ReadFile(path) // #nosec G304 - path comes from validated recipe asset path
	if err != nil {
		return nil, fmt.Errorf("failed to read applicability config: %w", err)
	}

	var cfg ApplicabilityConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse applicability config: %w", err)
	}
	if err := validateApplicabilityConfig(&cfg); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func validateApplicabilityConfig(cfg *ApplicabilityConfig) error {
	if cfg == nil {
		return fmt.Errorf("applicability config cannot be nil")
	}
	predicate := cfg.Applicability
	if strings.TrimSpace(predicate.Type) == "" {
		return fmt.Errorf("applicability.type is required")
	}
	if strings.ToLower(strings.TrimSpace(predicate.Type)) != "xpath" {
		return fmt.Errorf("unsupported applicability.type %q", predicate.Type)
	}
	if strings.TrimSpace(predicate.Expression) == "" {
		return fmt.Errorf("applicability.expression is required")
	}
	return nil
}

// LoadExtractConfig loads an extract configuration from YAML file
func LoadExtractConfig(path string) (*ExtractRecordMatch, error) {
	data, err := os.ReadFile(path) // #nosec G304 - path comes from user-provided config file path
	if err != nil {
		return nil, fmt.Errorf("failed to read extract config: %w", err)
	}

	validator, err := getExtractSchemaValidator()
	if err != nil {
		return nil, fmt.Errorf("failed to load extract schema validator: %w", err)
	}

	validationResult, err := validator.ValidateExtractConfig(data, path)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed for %s: %w", path, err)
	}

	if !validationResult.IsValid() {
		return nil, fmt.Errorf("extract config validation failed for %s:\n%s", path, validationResult.ErrorSummary())
	}

	var cfg ExtractRecordMatch
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse extract config: %w", err)
	}

	if err := prepareExtractConfig(&cfg); err != nil {
		return nil, fmt.Errorf("failed to prepare extract config: %w", err)
	}

	return &cfg, nil
}

func prepareExtractConfig(cfg *ExtractRecordMatch) error {
	if cfg == nil {
		return fmt.Errorf("extract config is nil")
	}

	cfg.prepareOnce.Do(func() {
		if len(cfg.OutputSchema) > 0 && cfg.OutputValidator == nil {
			validator, err := buildOutputValidator(cfg.OutputSchema, cfg.UniformSchema)
			if err != nil {
				cfg.prepareErr = fmt.Errorf("failed to prepare output validator: %w", err)
				return
			}
			cfg.OutputValidator = validator
		}

		for i := range cfg.MatchSelectors {
			selector := &cfg.MatchSelectors[i]
			if strings.TrimSpace(selector.XPath) == "" {
				continue
			}
			if selector.CompiledXPath == nil {
				compiled, err := xpath.Compile(selector.XPath)
				if err != nil {
					cfg.prepareErr = fmt.Errorf("failed to compile match selector %q: %w", selector.XPath, err)
					return
				}
				selector.CompiledXPath = compiled
			}
		}

		for i := range cfg.FieldMappings {
			if err := compileFieldMapping(&cfg.FieldMappings[i]); err != nil {
				cfg.prepareErr = err
				return
			}
		}
	})

	return cfg.prepareErr
}

// SetUniformSchema enables recipe-level uniform record shape on a prepared
// extract config and rebuilds output validation to admit explicit null fields.
func SetUniformSchema(cfg *ExtractRecordMatch, enabled bool) error {
	if cfg == nil {
		return fmt.Errorf("extract config is nil")
	}
	cfg.UniformSchema = enabled
	if len(cfg.OutputSchema) == 0 {
		return nil
	}
	validator, err := buildOutputValidator(cfg.OutputSchema, enabled)
	if err != nil {
		return fmt.Errorf("failed to prepare output validator: %w", err)
	}
	cfg.OutputValidator = validator
	return nil
}

func buildOutputValidator(outputSchema map[string]interface{}, uniform bool) (*schema.Validator, error) {
	schemaForValidation := outputSchema
	if uniform {
		var err error
		schemaForValidation, err = nullableOutputSchema(outputSchema)
		if err != nil {
			return nil, err
		}
	}
	schemaBytes, err := json.Marshal(schemaForValidation)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal output schema: %w", err)
	}
	return schema.NewValidatorFromBytes(schemaBytes)
}

func nullableOutputSchema(outputSchema map[string]interface{}) (map[string]interface{}, error) {
	if len(outputSchema) == 0 {
		return outputSchema, nil
	}
	schemaBytes, err := json.Marshal(outputSchema)
	if err != nil {
		return nil, fmt.Errorf("failed to copy output schema: %w", err)
	}
	var copied map[string]interface{}
	if err := json.Unmarshal(schemaBytes, &copied); err != nil {
		return nil, fmt.Errorf("failed to copy output schema: %w", err)
	}
	properties, ok := copied["properties"].(map[string]interface{})
	if !ok {
		return copied, nil
	}
	for name, raw := range properties {
		properties[name] = map[string]interface{}{
			"anyOf": []interface{}{
				map[string]interface{}{"type": "null"},
				raw,
			},
		}
	}
	return copied, nil
}

func compileFieldMapping(mapping *FieldMapping) error {
	if mapping == nil {
		return nil
	}

	if strings.TrimSpace(mapping.XPath) != "" && mapping.CompiledXPath == nil {
		compiled, err := xpath.Compile(mapping.XPath)
		if err != nil {
			return fmt.Errorf("failed to compile XPath %q for field %q: %w", mapping.XPath, mapping.OutputField, err)
		}
		mapping.CompiledXPath = compiled
	}

	for i := range mapping.ItemMapping {
		if err := compileFieldMapping(&mapping.ItemMapping[i]); err != nil {
			return err
		}
	}

	for i := range mapping.Polymorphic {
		pm := &mapping.Polymorphic[i]
		if strings.TrimSpace(pm.MatchXPath) != "" && pm.CompiledMatchXPath == nil {
			compiled, err := xpath.Compile(pm.MatchXPath)
			if err != nil {
				return fmt.Errorf("failed to compile polymorphic match XPath %q: %w", pm.MatchXPath, err)
			}
			pm.CompiledMatchXPath = compiled
		}
		for j := range pm.FieldMappings {
			if err := compileFieldMapping(&pm.FieldMappings[j]); err != nil {
				return err
			}
		}
	}

	return nil
}

// readFileContent reads file content with transparent .gz decompression support.
// The path is an already-resolved local source produced by the uriio read
// boundary (see the extract command's input-reference resolution), so this
// local-core consumer never sees a cloud or file:// URI.
func readFileContent(filePath string, allowLargeFiles bool) ([]byte, error) {
	const maxFileSizeWithoutFlag = 1 * 1024 * 1024 * 1024 // 1GB

	// Get file size
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}

	isCompressed := strings.HasSuffix(strings.ToLower(filepath.Ext(filePath)), ".gz")
	fileSize := fileInfo.Size()

	// For compressed files, estimate decompressed size (conservative 10x ratio)
	// For XML, typical compression is 5-20x, so 10x is a reasonable middle ground
	estimatedSize := fileSize
	if isCompressed {
		estimatedSize = fileSize * 10
	}

	// Check size limit if flag not set
	if !allowLargeFiles && estimatedSize > maxFileSizeWithoutFlag {
		sizeGB := float64(estimatedSize) / (1024 * 1024 * 1024)
		return nil, fmt.Errorf(
			"file exceeds 1GB limit (estimated %.2f GB uncompressed); use --allow-large-files flag to process anyway (warning: may cause out-of-memory errors on large files)",
			sizeGB,
		)
	}

	// Note: No path validation here because users explicitly specify which XML file to process
	// Users should be able to process any XML file they have OS permissions to access
	data, err := os.ReadFile(filePath) // #nosec G304 - User-specified XML file (top-level input)
	if err != nil {
		return nil, err
	}

	// Check if file is gzip-compressed by extension
	if isCompressed {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			// If gzip decompression fails, return the original data
			// (file might have .gz extension but not be compressed)
			return data, nil
		}
		defer func() {
			_ = reader.Close() // Ignore close error
		}()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress gzip file: %w", err)
		}

		// Verify actual decompressed size if we're checking limits
		if !allowLargeFiles && int64(len(decompressed)) > maxFileSizeWithoutFlag {
			sizeGB := float64(len(decompressed)) / (1024 * 1024 * 1024)
			return nil, fmt.Errorf(
				"decompressed file exceeds 1GB limit (%.2f GB); use --allow-large-files flag to process anyway (warning: may cause out-of-memory errors on large files)",
				sizeGB,
			)
		}

		return decompressed, nil
	}

	return data, nil
}

// openFileStream opens a file for streaming with transparent .gz decompression.
// filePath is an already-resolved local source produced by the uriio read
// boundary, so this local-core consumer never sees a cloud or file:// URI.
func openFileStream(filePath string) (io.ReadCloser, error) {
	file, err := os.Open(filePath) // #nosec G304 - path comes from user-provided file list
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	// Check if file is gzip-compressed
	if strings.HasSuffix(strings.ToLower(filepath.Ext(filePath)), ".gz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			// If gzip decompression fails, reset and return the file reader
			_ = file.Close() // #nosec G104 - best effort close on error path
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		// Return a multi-closer that closes both gzip reader and file
		return &multiCloser{readers: []io.Closer{gzReader, file}}, nil
	}

	return file, nil
}

// multiCloser wraps multiple closers
type multiCloser struct {
	readers []io.Closer
}

func (mc *multiCloser) Read(p []byte) (n int, err error) {
	// Read from the first reader (the gzip reader)
	if len(mc.readers) > 0 {
		if r, ok := mc.readers[0].(io.Reader); ok {
			return r.Read(p)
		}
	}
	return 0, io.EOF
}

func (mc *multiCloser) Close() error {
	var errs []error
	for _, r := range mc.readers {
		if err := r.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors closing resources: %v", errs)
	}
	return nil
}

// ProcessFileStreaming processes a large file using streaming to minimize memory usage
func ProcessFileStreaming(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}) ExtractResult {
	return ProcessFileStreamingWithProvenance(filePath, sigCfg, extCfg, externalFields, provenance.RuntimeOptions{})
}

// ProcessFileStreamingWithProvenance processes a large file using streaming to
// minimize memory usage and enriches each record with runtime provenance.
func ProcessFileStreamingWithProvenance(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}, runtimeProvenance provenance.RuntimeOptions) ExtractResult {
	collector := &recordCollectingSink{}
	result := ProcessFileStreamingToSink(context.Background(), filePath, sigCfg, extCfg, externalFields, runtimeProvenance, collector)
	if result.Error == nil {
		result.Records = collector.Records()
	}
	return result
}

// ProcessFileStreamingToSink processes a large file using streaming and emits
// already-enriched records to sink without accumulating them in ExtractResult.
// The caller owns the sink lifecycle and must call Close after all intended
// file emissions are complete.
func ProcessFileStreamingToSink(ctx context.Context, filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}, runtimeProvenance provenance.RuntimeOptions, sink RecordSink) ExtractResult {
	if ctx == nil {
		ctx = context.Background()
	}

	logger := logging.GetLogger()
	if logger == nil {
		logger = zap.NewNop()
	}

	result := ExtractResult{File: filePath, LogicalURI: runtimeProvenance.SourceIdentity(filePath), SignatureMatchStatus: SignatureMatchUnknown}
	if sink == nil {
		result.Error = fmt.Errorf("record sink is nil")
		return result
	}

	logger.Info("Starting streaming extraction",
		zap.String("file", filePath),
		zap.String("mode", "streaming"))

	if extCfg == nil {
		result.Error = fmt.Errorf("extract config is nil")
		return result
	}

	emittedRecords := 0
	perSelectorCounts := make(map[int]int)
	boundaryDisposition := DispositionApplied
	boundaryReason := DispositionReason("")
	boundaryDetail := ""
	var scanner *streaming.RecordScanner

	markBoundaryFailure := func(reason DispositionReason) {
		boundaryDisposition = DispositionFailed
		boundaryReason = reason
		if result.Error != nil {
			boundaryDetail = result.Error.Error()
		}
	}

	finish := func() ExtractResult {
		if scanner != nil {
			perSelectorCounts[0] = scanner.RecordCount()
			result.PerSelectorCounts = perSelectorCounts
			result.PerSelectorCountsComplete = len(extCfg.MatchSelectors) == 1
		}

		summary := FileEmissionSummary{
			SourceFile:        runtimeProvenance.SourceIdentity(filePath),
			RecordType:        extCfg.RecordType,
			RecordCount:       emittedRecords,
			Disposition:       boundaryDisposition,
			DispositionReason: boundaryReason,
			DispositionDetail: boundaryDetail,
		}
		if err := sink.OnFileBoundary(ctx, summary); err != nil {
			logger.Error("Failed to emit file boundary", zap.Error(err))
			if result.Error == nil {
				result.Error = fmt.Errorf("failed to emit file boundary: %w", err)
			}
		}
		return result
	}

	// Get the record selector from the first match selector
	if len(extCfg.MatchSelectors) == 0 {
		result.Error = fmt.Errorf("no match selectors defined in extract config")
		markBoundaryFailure(DispositionReasonInternalError)
		return finish()
	}
	recordSelector := extCfg.MatchSelectors[0].XPath
	parsedSelector, err := streaming.ParseRecordSelector(recordSelector)
	if err != nil {
		result.Error = err
		markBoundaryFailure(DispositionReasonInternalError)
		return finish()
	}

	logger.Info("Initializing record scanner",
		zap.String("record_selector", recordSelector))

	streamingCfg := cloneExtractConfigForStreaming(extCfg)
	if err := prepareExtractConfig(streamingCfg); err != nil {
		logger.Error("Failed to prepare streaming extract config", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to prepare streaming extract config: %w", err)
		markBoundaryFailure(DispositionReasonInternalError)
		return finish()
	}

	// Open file stream
	stream, err := openFileStream(filePath)
	if err != nil {
		logger.Error("Failed to open file stream", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to open file stream: %w", err)
		markBoundaryFailure(DispositionReasonInternalError)
		return finish()
	}
	defer func() {
		if closeErr := stream.Close(); closeErr != nil {
			logger.Warn("Failed to close file stream", zap.Error(closeErr))
		}
	}()

	// Create record scanner
	scanner = streaming.NewRecordScanner(stream, recordSelector)
	defer func() {
		_ = scanner.Close() // Scanner close is best-effort, errors are not critical
	}()

	// Note: In streaming mode, signature checking is skipped because we don't have access
	// to the full document structure. Signature checking should be done before calling
	// ProcessFileStreaming, or the caller should ensure the file matches the expected format.
	logger.Debug("Streaming mode: signature checking skipped (checking against individual records)")

	// Process records one at a time
	for {
		recordBuffer, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Error("Failed to scan record", zap.Error(err))
			result.Error = fmt.Errorf("failed to scan record: %w", err)
			markBoundaryFailure(DispositionReasonParseError)
			return finish()
		}

		// Log progress every 100 records
		if recordBuffer.RecordNum%100 == 0 {
			logger.Info("Progress",
				zap.Int("records_scanned", recordBuffer.RecordNum),
				zap.String("file", filePath))
		}

		// Parse this record as a mini-DOM
		recordDoc, err := xmlquery.Parse(strings.NewReader(recordBuffer.XML))
		if err != nil {
			logger.Error("Failed to parse record XML",
				zap.Int("record_num", recordBuffer.RecordNum),
				zap.Error(err))
			result.Error = fmt.Errorf("failed to parse record %d: %w", recordBuffer.RecordNum, err)
			markBoundaryFailure(DispositionReasonParseError)
			return finish()
		}

		// Extract records from this mini-DOM using a cloned config whose match
		// selector targets the record root.
		records, err := extractRecords(recordDoc, streamingCfg, externalFields)
		if err != nil {
			logger.Error("Failed to extract from record",
				zap.Int("record_num", recordBuffer.RecordNum),
				zap.Error(err))
			result.Error = fmt.Errorf("failed to extract from record %d: %w", recordBuffer.RecordNum, err)
			markBoundaryFailure(DispositionReasonInternalError)
			return finish()
		}

		recordNums := make([]int, len(records))
		for i := range records {
			recordNums[i] = recordBuffer.RecordNum
		}
		if err := enrichRecordsWithRecordNums(records, recordNums, filePath, sigCfg, streamingCfg, runtimeProvenance); err != nil {
			logger.Error("Failed to enrich records",
				zap.Int("record_num", recordBuffer.RecordNum),
				zap.Error(err))
			result.Error = err
			markBoundaryFailure(DispositionReasonInternalError)
			return finish()
		}
		for _, record := range records {
			if err := sink.OnRecord(ctx, NewEmittedRecord(record)); err != nil {
				logger.Error("Failed to emit record",
					zap.Int("record_num", recordBuffer.RecordNum),
					zap.Error(err))
				result.Error = fmt.Errorf("failed to emit record %d: %w", recordBuffer.RecordNum, err)
				markBoundaryFailure(DispositionReasonInternalError)
				return finish()
			}
			emittedRecords++
		}
	}

	logger.Info("Streaming extraction complete",
		zap.String("file", filePath),
		zap.Int("total_records_scanned", scanner.RecordCount()),
		zap.Int("total_records_extracted", emittedRecords),
		zap.String("record_element", parsedSelector.ElementName))

	return finish()
}

// ProcessFile processes a single file for extraction
func ProcessFile(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}, allowLargeFiles bool) ExtractResult {
	return ProcessFileWithProvenance(filePath, sigCfg, extCfg, externalFields, allowLargeFiles, provenance.RuntimeOptions{})
}

// ProcessFileWithApplicability processes a file with an optional recipe applicability gate.
func ProcessFileWithApplicability(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, appCfg *ApplicabilityConfig, externalFields map[string]interface{}, allowLargeFiles bool, runtimeProvenance provenance.RuntimeOptions) ExtractResult {
	return processFileWithProvenance(context.Background(), filePath, sigCfg, extCfg, appCfg, externalFields, allowLargeFiles, runtimeProvenance, nil)
}

// ProcessFileWithApplicabilityToSink processes a file and emits already-enriched
// records to sink without populating ExtractResult.Records. The caller owns the
// sink lifecycle and must call Close after all intended emissions complete.
func ProcessFileWithApplicabilityToSink(ctx context.Context, filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, appCfg *ApplicabilityConfig, externalFields map[string]interface{}, allowLargeFiles bool, runtimeProvenance provenance.RuntimeOptions, sink RecordSink) ExtractResult {
	return processFileWithProvenance(ctx, filePath, sigCfg, extCfg, appCfg, externalFields, allowLargeFiles, runtimeProvenance, sink)
}

// ProcessFileWithProvenance processes a single file for extraction and enriches
// each record with runtime provenance.
func ProcessFileWithProvenance(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}, allowLargeFiles bool, runtimeProvenance provenance.RuntimeOptions) ExtractResult {
	return processFileWithProvenance(context.Background(), filePath, sigCfg, extCfg, nil, externalFields, allowLargeFiles, runtimeProvenance, nil)
}

func processFileWithProvenance(ctx context.Context, filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, appCfg *ApplicabilityConfig, externalFields map[string]interface{}, allowLargeFiles bool, runtimeProvenance provenance.RuntimeOptions, sink RecordSink) ExtractResult {
	if ctx == nil {
		ctx = context.Background()
	}
	logger := logging.GetLogger()
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Debug("Starting file processing", zap.String("file", filePath))

	t := newBoundaryTracker(ctx, filePath, extCfg, appCfg, runtimeProvenance, sink)

	// Check if we should use streaming mode for large files
	// Streaming mode threshold: 100MB uncompressed (or 10MB compressed with 10x ratio estimate)
	const streamingThreshold = 100 * 1024 * 1024 // 100MB

	shouldStream, estimatedSize, streamCompressed := shouldUseLargeFileStreaming(filePath, allowLargeFiles, sink, appCfg, streamingThreshold)
	if shouldStream {
		if appCfg != nil {
			t.result.Error = fmt.Errorf("applicability declared but not supported in streaming mode")
			t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
			t.markBoundaryFailure(DispositionReasonInternalError, t.result.Error.Error())
			t.emitBoundary()
			return t.result
		}
		logger.Info("Using streaming mode for large file",
			zap.String("file", filePath),
			zap.Int64("estimated_size_mb", estimatedSize/(1024*1024)),
			zap.Bool("compressed", streamCompressed),
			zap.Bool("allow_large_files", allowLargeFiles),
			zap.Bool("sink", sink != nil))
		if sink != nil {
			return ProcessFileStreamingToSink(ctx, filePath, sigCfg, extCfg, externalFields, runtimeProvenance, sink)
		}
		return ProcessFileStreamingWithProvenance(filePath, sigCfg, extCfg, externalFields, runtimeProvenance)
	}

	// Read file content (with transparent .gz decompression if needed)
	logger.Debug("Reading file content", zap.String("file", filePath))
	content, err := readFileContent(filePath, allowLargeFiles) // #nosec G304 - filePath comes from user-provided file list or directory scan
	if err != nil {
		logger.Error("Failed to read file", zap.String("file", filePath), zap.Error(err))
		t.result.Error = fmt.Errorf("failed to read file: %w", err)
		t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
		t.markBoundaryFailure(DispositionReasonInternalError, t.result.Error.Error())
		t.emitBoundary()
		return t.result
	}
	isCompressed := strings.HasSuffix(strings.ToLower(filepath.Ext(filePath)), ".gz")
	if isCompressed {
		logger.Debug("File decompressed successfully", zap.String("file", filePath), zap.Int("decompressed_size", len(content)))
	} else {
		logger.Debug("File read successfully", zap.String("file", filePath), zap.Int("size", len(content)))
	}

	// Parse XML document
	logger.Debug("Parsing XML document", zap.String("file", filePath))
	doc, err := xmlquery.Parse(strings.NewReader(string(content)))
	if err != nil {
		logger.Error("Failed to parse XML", zap.String("file", filePath), zap.Error(err))
		t.result.Error = fmt.Errorf("failed to parse XML: %w", err)
		t.markFailed(DispositionReasonParseError, t.result.Error.Error())
		t.markBoundaryFailure(DispositionReasonParseError, t.result.Error.Error())
		t.emitBoundary()
		return t.result
	}
	logger.Debug("XML parsed successfully", zap.String("file", filePath))

	return ProcessParsedDocument(ctx, doc, filePath, sigCfg, extCfg, appCfg, externalFields, runtimeProvenance, sink)
}

// ProcessParsedDocument runs applicability, signature matching, and record
// extraction against an ALREADY-PARSED document. It is the shared seam used by
// both single-recipe processing (via processFileWithProvenance, which reads and
// parses the file first) and the extract-multi dispatcher, which parses each
// input file ONCE and dispatches the parsed document to multiple recipes.
//
// The doc MUST be treated as strictly read-only by callers and by extraction:
// the multi-recipe pass shares a single *xmlquery.Node across recipes, so any
// node mutation would be a cross-recipe shared-state hazard. Each recipe must
// pass its own extCfg (use CloneRecordMatch per concurrent holder).
func ProcessParsedDocument(ctx context.Context, doc *xmlquery.Node, filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, appCfg *ApplicabilityConfig, externalFields map[string]interface{}, runtimeProvenance provenance.RuntimeOptions, sink RecordSink) ExtractResult {
	if ctx == nil {
		ctx = context.Background()
	}
	logger := logging.GetLogger()
	if logger == nil {
		logger = zap.NewNop()
	}
	t := newBoundaryTracker(ctx, filePath, extCfg, appCfg, runtimeProvenance, sink)

	if appCfg != nil {
		applies, err := evaluateApplicability(doc, appCfg)
		if err != nil {
			logger.Error("Failed to evaluate applicability", zap.String("file", filePath), zap.Error(err))
			t.result.Error = fmt.Errorf("failed to evaluate applicability: %w", err)
			t.markFailed(DispositionReasonValidationError, t.result.Error.Error())
			t.markBoundaryFailure(DispositionReasonValidationError, t.result.Error.Error())
			t.emitBoundary()
			return t.result
		}
		if !applies {
			logger.Debug("File is not applicable to recipe", zap.String("file", filePath))
			t.result.Disposition = DispositionNotApplicable
			t.result.DispositionReason = DispositionReasonApplicabilityPredicateFalse
			t.result.DispositionDetail = "applicability predicate evaluated false"
			t.result.PerSelectorCounts = zeroSelectorCounts(extCfg)
			t.result.PerSelectorCountsComplete = true
			t.emitBoundary()
			return t.result
		}
	}

	// Check if file matches signature
	logger.Debug("Checking signature match", zap.String("file", filePath), zap.String("signature", sigCfg.SignatureID))
	matches, confidence, err := matchesSignature(doc, sigCfg)
	t.result.SignatureConfidence = confidence
	if err != nil {
		logger.Error("Failed to check signature", zap.String("file", filePath), zap.Error(err))
		t.result.Error = fmt.Errorf("failed to check signature: %w", err)
		t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
		t.markBoundaryFailure(DispositionReasonInternalError, t.result.Error.Error())
		t.emitBoundary()
		return t.result
	}
	logger.Debug("Signature check complete", zap.String("file", filePath), zap.Bool("matches", matches), zap.Float64("confidence", confidence))

	if !matches {
		// File doesn't match signature, return empty result unless applicability made the mismatch a failed disposition.
		logger.Debug("File does not match signature", zap.String("file", filePath))
		t.result.SignatureMatchStatus = SignatureMatchMismatched
		t.result.PerSelectorCounts = zeroSelectorCounts(extCfg)
		t.result.PerSelectorCountsComplete = true
		if appCfg != nil {
			t.result.Disposition = DispositionFailed
			t.result.DispositionReason = DispositionReasonSignatureMismatch
			t.result.DispositionDetail = "signature predicate did not match"
		}
		t.emitBoundary()
		return t.result
	}

	t.result.SignatureMatchStatus = SignatureMatchMatched

	if err := prepareExtractConfig(extCfg); err != nil {
		logger.Error("Failed to prepare extract config", zap.String("file", filePath), zap.Error(err))
		t.result.Error = fmt.Errorf("failed to prepare extract config: %w", err)
		t.markBoundaryFailure(DispositionReasonInternalError, t.result.Error.Error())
		t.emitBoundary()
		return t.result
	}

	// Extract records
	logger.Debug("Starting record extraction", zap.String("file", filePath), zap.String("record_type", extCfg.RecordType))
	if sink != nil {
		perSelectorCounts, count, err := extractRecordsWithCountsAndRecordNumsToSink(ctx, doc, extCfg, externalFields, filePath, sigCfg, runtimeProvenance, sink)
		if err != nil {
			logger.Error("Failed to extract records to sink", zap.String("file", filePath), zap.Error(err))
			t.result.Error = fmt.Errorf("failed to extract records: %w", err)
			t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
			t.markBoundaryFailure(DispositionReasonInternalError, t.result.Error.Error())
			t.emitBoundary()
			return t.result
		}
		if appCfg != nil {
			t.result.Disposition = DispositionApplied
		}
		t.emittedRecords = count
		t.result.PerSelectorCounts = perSelectorCounts
		t.result.PerSelectorCountsComplete = true
		t.emitBoundary()
		logger.Debug("Record sink extraction complete", zap.String("file", filePath), zap.Int("record_count", t.emittedRecords))
		return t.result
	}

	extractedRecords, perSelectorCounts, err := extractRecordsWithCountsAndRecordNums(doc, extCfg, externalFields)
	if err != nil {
		logger.Error("Failed to extract records", zap.String("file", filePath), zap.Error(err))
		t.result.Error = fmt.Errorf("failed to extract records: %w", err)
		t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
		return t.result
	}
	records, recordNums := splitExtractedRecords(extractedRecords)
	logger.Debug("Record extraction complete", zap.String("file", filePath), zap.Int("record_count", len(records)))

	if err := enrichRecordsWithRecordNums(records, recordNums, filePath, sigCfg, extCfg, runtimeProvenance); err != nil {
		logger.Error("Failed to apply metadata", zap.String("file", filePath), zap.Error(err))
		t.result.Error = err
		t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
		return t.result
	}

	if appCfg != nil {
		t.result.Disposition = DispositionApplied
	}
	t.result.Records = records
	t.result.PerSelectorCounts = perSelectorCounts
	t.result.PerSelectorCountsComplete = true
	return t.result
}

// boundaryTracker centralizes per-file disposition state and file-boundary
// emission so the read/parse step (processFileWithProvenance) and the
// parsed-document step (ProcessParsedDocument) share identical semantics. It
// owns the in-flight ExtractResult for one file.
type boundaryTracker struct {
	ctx                 context.Context
	sink                RecordSink
	filePath            string
	extCfg              *ExtractRecordMatch
	appCfg              *ApplicabilityConfig
	rp                  provenance.RuntimeOptions
	result              ExtractResult
	emittedRecords      int
	boundaryDisposition Disposition
	boundaryReason      DispositionReason
	boundaryDetail      string
}

func newBoundaryTracker(ctx context.Context, filePath string, extCfg *ExtractRecordMatch, appCfg *ApplicabilityConfig, rp provenance.RuntimeOptions, sink RecordSink) *boundaryTracker {
	return &boundaryTracker{
		ctx:                 ctx,
		sink:                sink,
		filePath:            filePath,
		extCfg:              extCfg,
		appCfg:              appCfg,
		rp:                  rp,
		result:              ExtractResult{File: filePath, LogicalURI: rp.SourceIdentity(filePath), SignatureMatchStatus: SignatureMatchUnknown},
		boundaryDisposition: DispositionApplied,
	}
}

// markFailed records a failed disposition on the result, but only when an
// applicability gate is configured (matching the original single-recipe
// semantics where disposition is meaningful only for recipe-applicability runs).
func (t *boundaryTracker) markFailed(reason DispositionReason, detail string) {
	if t.appCfg == nil {
		return
	}
	t.result.Disposition = DispositionFailed
	t.result.DispositionReason = reason
	t.result.DispositionDetail = detail
}

func (t *boundaryTracker) markBoundaryFailure(reason DispositionReason, detail string) {
	t.boundaryDisposition = DispositionFailed
	t.boundaryReason = reason
	t.boundaryDetail = detail
}

func (t *boundaryTracker) emitBoundary() {
	if t.sink == nil {
		return
	}
	switch t.result.Disposition {
	case DispositionFailed:
		t.boundaryDisposition = DispositionFailed
		t.boundaryReason = t.result.DispositionReason
		t.boundaryDetail = t.result.DispositionDetail
	case DispositionNotApplicable:
		t.boundaryDisposition = DispositionNotApplicable
		t.boundaryReason = t.result.DispositionReason
		t.boundaryDetail = t.result.DispositionDetail
	}
	summary := FileEmissionSummary{
		SourceFile:        t.rp.SourceIdentity(t.filePath),
		RecordType:        t.extCfg.RecordType,
		RecordCount:       t.emittedRecords,
		Disposition:       t.boundaryDisposition,
		DispositionReason: t.boundaryReason,
		DispositionDetail: t.boundaryDetail,
	}
	if err := t.sink.OnFileBoundary(t.ctx, summary); err != nil && t.result.Error == nil {
		t.result.Error = fmt.Errorf("failed to emit file boundary: %w", err)
		t.markFailed(DispositionReasonInternalError, t.result.Error.Error())
	}
}

func shouldUseLargeFileStreaming(filePath string, allowLargeFiles bool, sink RecordSink, appCfg *ApplicabilityConfig, streamingThreshold int64) (bool, int64, bool) {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return false, 0, false
	}
	isCompressed := strings.HasSuffix(strings.ToLower(filepath.Ext(filePath)), ".gz")
	estimatedSize := fileInfo.Size()
	if isCompressed {
		estimatedSize = fileInfo.Size() * 10 // Conservative 10x decompression ratio
	}
	return estimatedSize > streamingThreshold && (allowLargeFiles || (sink != nil && appCfg == nil)), estimatedSize, isCompressed
}

// ParseFileForDOMDispatch reads and DOM-parses a file once for multi-recipe
// dispatch, where the resulting (read-only) document is shared across recipes.
//
// It enforces the DOM-only contract of the multi-recipe pass: a file large
// enough that the single-recipe engine would route it to the streaming path is
// rejected with an error rather than silently DOM-parsed, because multi-recipe
// extraction does not support the streaming/indexed path. The returned document
// MUST be treated as strictly read-only by callers (it is shared across
// recipes via ProcessParsedDocument).
func ParseFileForDOMDispatch(filePath string, allowLargeFiles bool) (*xmlquery.Node, error) {
	const streamingThreshold = 100 * 1024 * 1024 // 100MB, matching the single-recipe streaming threshold
	if info, err := os.Stat(filePath); err == nil {
		estimated := info.Size()
		if strings.HasSuffix(strings.ToLower(filepath.Ext(filePath)), ".gz") {
			estimated = info.Size() * 10 // conservative decompression estimate, matching shouldUseLargeFileStreaming
		}
		if estimated > streamingThreshold {
			return nil, fmt.Errorf(
				"file %s is too large for extract-multi (estimated %d bytes exceeds the %d DOM-parse threshold); "+
					"extract-multi parses each file once into memory and does not support the large-file streaming path",
				filePath, estimated, int64(streamingThreshold))
		}
	}
	content, err := readFileContent(filePath, allowLargeFiles) // #nosec G304 - filePath comes from a user-provided file list or directory scan
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	doc, err := xmlquery.Parse(strings.NewReader(string(content)))
	if err != nil {
		return nil, fmt.Errorf("failed to parse XML: %w", err)
	}
	return doc, nil
}

func evaluateApplicability(doc *xmlquery.Node, cfg *ApplicabilityConfig) (bool, error) {
	if cfg == nil {
		return true, nil
	}
	predicate := cfg.Applicability
	if strings.ToLower(strings.TrimSpace(predicate.Type)) != "xpath" {
		return false, fmt.Errorf("unsupported applicability.type %q", predicate.Type)
	}
	return evaluateXPathBoolean(doc, predicate.Expression)
}

// matchesSignature checks if the document matches the signature
func matchesSignature(doc *xmlquery.Node, cfg *FileSignature) (bool, float64, error) {
	score := 0.0
	totalWeight := 0.0

	for _, pattern := range cfg.MatchPatterns {
		totalWeight += pattern.Weight

		if matchesPattern(doc, pattern) {
			score += pattern.Weight
		}
	}

	if totalWeight == 0 {
		return false, 0, fmt.Errorf("no patterns with weight > 0")
	}

	confidence := score / totalWeight
	return confidence >= cfg.ConfidenceThreshold, confidence, nil
}

// matchesPattern checks if a pattern matches the document.
//
// The selector is treated as a full XPath expression and evaluated via
// xpath.Compile/Evaluate. Result-type coercion follows XPath 1.0's
// boolean() conversion rules:
//
//   - bool         → returned as-is
//   - float64      → true iff non-zero AND not NaN (per XPath 1.0 §4.3:
//     "a number is true if and only if it is neither positive zero, negative
//     zero, nor NaN" — e.g. number() over non-numeric text returns NaN)
//   - string       → true iff non-empty (covers name(), local-name(), etc.)
//   - NodeIterator → true iff at least one node matches
//
// This makes the matcher accept any well-formed XPath, including:
//
//	count(//Record) > 0          (legacy form, still works)
//	count(//Record) > 1          (was returning false; now correct)
//	count(//Record) = 1          (was returning false; now correct)
//	count(//A) > 0 and count(//B) > 0
//	boolean(//Record)
//	//Record                     (plain node-set)
//	/Envelope                    (absolute path)
//
// A compile error or evaluation error is treated as a non-match.
func matchesPattern(doc *xmlquery.Node, pattern MatchPattern) bool {
	matched, err := evaluateXPathBoolean(doc, pattern.Selector)
	return err == nil && matched
}

func evaluateXPathBoolean(doc *xmlquery.Node, selector string) (bool, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return false, fmt.Errorf("empty XPath expression")
	}

	expr, err := xpath.Compile(selector)
	if err != nil {
		return false, fmt.Errorf("compile XPath %q: %w", selector, err)
	}

	switch v := expr.Evaluate(xmlquery.CreateXPathNavigator(doc)).(type) {
	case bool:
		return v, nil
	case float64:
		// Per XPath 1.0 §4.3 boolean(): a number is true iff non-zero AND not NaN.
		return v != 0 && !math.IsNaN(v), nil
	case string:
		return v != "", nil
	case *xpath.NodeIterator:
		// Truthy iff at least one node is yielded.
		return v != nil && v.MoveNext(), nil
	default:
		return false, fmt.Errorf("unsupported XPath result type %T", v)
	}
}

// extractRecords extracts records from document using the extract config
func extractRecords(doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}) ([]map[string]interface{}, error) {
	records, _, err := extractRecordsWithCounts(doc, cfg, externalFields)
	return records, err
}

type extractedRecord struct {
	data      map[string]interface{}
	recordNum int
}

func extractRecordsWithCounts(doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}) ([]map[string]interface{}, map[int]int, error) {
	extractedRecords, perSelectorCounts, err := extractRecordsWithCountsAndRecordNums(doc, cfg, externalFields)
	if err != nil {
		return nil, nil, err
	}
	records, _ := splitExtractedRecords(extractedRecords)
	return records, perSelectorCounts, nil
}

func extractRecordsWithCountsAndRecordNums(doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}) ([]extractedRecord, map[int]int, error) {
	var extractedRecords []extractedRecord
	perSelectorCounts := make(map[int]int, len(cfg.MatchSelectors))
	recordNum := 0

	for i := range cfg.MatchSelectors {
		selector := &cfg.MatchSelectors[i]
		nodes, err := evaluateNodeSet(doc, selector.CompiledXPath, selector.XPath)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to evaluate XPath %s: %w", selector.XPath, err)
		}
		perSelectorCounts[i] = len(nodes)
		if len(nodes) == 0 {
			continue
		}

		for _, node := range nodes {
			recordNum++
			record := make(map[string]interface{})

			// Apply field mappings
			for j := range cfg.FieldMappings {
				mapping := &cfg.FieldMappings[j]
				if strings.TrimSpace(mapping.Expression) != "" {
					continue
				}
				value, err := extractValue(node, mapping)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to extract value for field %s: %w", mapping.OutputField, err)
				}
				if value != nil {
					record[mapping.OutputField] = value
				}
			}

			for j := range cfg.FieldMappings {
				mapping := &cfg.FieldMappings[j]
				if strings.TrimSpace(mapping.Expression) == "" {
					continue
				}
				value, err := extractExpressionValue(mapping, record, externalFields, cfg.ReferenceTables)
				if err != nil {
					return nil, nil, err
				}
				if value != nil {
					record[mapping.OutputField] = value
				}
			}

			// Add external fields
			for key, value := range externalFields {
				if _, exists := record[key]; exists {
					return nil, nil, fmt.Errorf("external field key %q collides with extracted record field; rename one of them to keep injection vs content-extraction fidelity explicit", key)
				}
				// Internal (derive-only) captures stay out of the emitted record:
				// they were already in expression scope above.
				if isInternalField(value) {
					continue
				}
				record[key] = value
			}
			applyUniformSchema(record, cfg)

			// Apply filters
			if passesFilters(record, cfg.Filters) {
				if cfg.OutputValidator != nil {
					res, err := cfg.OutputValidator.Validate(record)
					if err != nil {
						return nil, nil, fmt.Errorf("output schema validation failed: %w", err)
					}
					if !res.Valid {
						return nil, nil, fmt.Errorf("output schema validation failed:\n%s", formatValidationErrors(res.Errors))
					}
				}
				extractedRecords = append(extractedRecords, extractedRecord{
					data:      record,
					recordNum: recordNum,
				})
			}
		}
	}

	return extractedRecords, perSelectorCounts, nil
}

func extractRecordsWithCountsAndRecordNumsToSink(ctx context.Context, doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}, sourceFile string, sigCfg *FileSignature, runtimeProvenance provenance.RuntimeOptions, sink RecordSink) (map[int]int, int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return nil, 0, fmt.Errorf("record sink is nil")
	}

	perSelectorCounts := make(map[int]int, len(cfg.MatchSelectors))
	recordNum := 0
	emittedRecords := 0

	for i := range cfg.MatchSelectors {
		selector := &cfg.MatchSelectors[i]
		nodes, err := evaluateNodeSet(doc, selector.CompiledXPath, selector.XPath)
		if err != nil {
			return nil, emittedRecords, fmt.Errorf("failed to evaluate XPath %s: %w", selector.XPath, err)
		}
		perSelectorCounts[i] = len(nodes)
		if len(nodes) == 0 {
			continue
		}

		for _, node := range nodes {
			if err := ctx.Err(); err != nil {
				return nil, emittedRecords, err
			}
			recordNum++
			record := make(map[string]interface{})

			for j := range cfg.FieldMappings {
				mapping := &cfg.FieldMappings[j]
				if strings.TrimSpace(mapping.Expression) != "" {
					continue
				}
				value, err := extractValue(node, mapping)
				if err != nil {
					return nil, emittedRecords, fmt.Errorf("failed to extract value for field %s: %w", mapping.OutputField, err)
				}
				if value != nil {
					record[mapping.OutputField] = value
				}
			}

			for j := range cfg.FieldMappings {
				mapping := &cfg.FieldMappings[j]
				if strings.TrimSpace(mapping.Expression) == "" {
					continue
				}
				value, err := extractExpressionValue(mapping, record, externalFields, cfg.ReferenceTables)
				if err != nil {
					return nil, emittedRecords, err
				}
				if value != nil {
					record[mapping.OutputField] = value
				}
			}

			for key, value := range externalFields {
				if _, exists := record[key]; exists {
					return nil, emittedRecords, fmt.Errorf("external field key %q collides with extracted record field; rename one of them to keep injection vs content-extraction fidelity explicit", key)
				}
				// Internal (derive-only) captures stay out of the emitted record:
				// they were already in expression scope above.
				if isInternalField(value) {
					continue
				}
				record[key] = value
			}
			applyUniformSchema(record, cfg)

			if !passesFilters(record, cfg.Filters) {
				continue
			}
			if cfg.OutputValidator != nil {
				res, err := cfg.OutputValidator.Validate(record)
				if err != nil {
					return nil, emittedRecords, fmt.Errorf("output schema validation failed: %w", err)
				}
				if !res.Valid {
					return nil, emittedRecords, fmt.Errorf("output schema validation failed:\n%s", formatValidationErrors(res.Errors))
				}
			}
			if err := EnrichRecordWithRecordNum(record, sourceFile, sigCfg, cfg, runtimeProvenance, recordNum); err != nil {
				return nil, emittedRecords, err
			}
			if err := sink.OnRecord(ctx, NewEmittedRecord(record)); err != nil {
				return nil, emittedRecords, fmt.Errorf("failed to emit record %d: %w", recordNum, err)
			}
			emittedRecords++
		}
	}

	return perSelectorCounts, emittedRecords, nil
}

func splitExtractedRecords(extractedRecords []extractedRecord) ([]map[string]interface{}, []int) {
	records := make([]map[string]interface{}, 0, len(extractedRecords))
	recordNums := make([]int, 0, len(extractedRecords))
	for _, extracted := range extractedRecords {
		records = append(records, extracted.data)
		recordNums = append(recordNums, extracted.recordNum)
	}
	return records, recordNums
}

func applyUniformSchema(record map[string]interface{}, cfg *ExtractRecordMatch) {
	if record == nil || cfg == nil || !cfg.UniformSchema {
		return
	}
	properties, ok := cfg.OutputSchema["properties"].(map[string]interface{})
	if !ok {
		return
	}
	for name := range properties {
		if _, exists := record[name]; !exists {
			record[name] = nil
		}
	}
}

func zeroSelectorCounts(cfg *ExtractRecordMatch) map[int]int {
	if cfg == nil {
		return nil
	}
	counts := make(map[int]int, len(cfg.MatchSelectors))
	for i := range cfg.MatchSelectors {
		counts[i] = 0
	}
	return counts
}

func enrichRecordsWithRecordNums(records []map[string]interface{}, recordNums []int, sourceFile string, sigCfg *FileSignature, cfg *ExtractRecordMatch, runtimeProvenance provenance.RuntimeOptions) error {
	if len(records) == 0 {
		return nil
	}
	if recordNums != nil && len(recordNums) != len(records) {
		return fmt.Errorf("record number count %d does not match record count %d", len(recordNums), len(records))
	}

	for i, record := range records {
		recordNum := 0
		if recordNums != nil {
			recordNum = recordNums[i]
		}
		if err := EnrichRecordWithRecordNum(record, sourceFile, sigCfg, cfg, runtimeProvenance, recordNum); err != nil {
			return err
		}
	}

	return nil
}

// EnrichRecord wraps a raw extracted record in Sumpter's standard output
// envelope and attaches safe runtime provenance fields.
func EnrichRecord(record map[string]interface{}, sourceFile string, sigCfg *FileSignature, cfg *ExtractRecordMatch, runtimeProvenance provenance.RuntimeOptions) error {
	return EnrichRecordWithRecordNum(record, sourceFile, sigCfg, cfg, runtimeProvenance, 0)
}

// EnrichRecordWithRecordNum wraps a raw extracted record and, when provided,
// records the 1-based pre-filter source-document position in _runtime.
func EnrichRecordWithRecordNum(record map[string]interface{}, sourceFile string, sigCfg *FileSignature, cfg *ExtractRecordMatch, runtimeProvenance provenance.RuntimeOptions, recordNum int) error {
	showSummaries := true
	showValidation := true
	if cfg.OutputOptions != nil {
		if cfg.OutputOptions.ShowSummaries != nil {
			showSummaries = *cfg.OutputOptions.ShowSummaries
		}
		if cfg.OutputOptions.ShowValidationMetadata != nil {
			showValidation = *cfg.OutputOptions.ShowValidationMetadata
		}
	}

	dataCopy := make(map[string]interface{}, len(record))
	for k, v := range record {
		dataCopy[k] = v
	}

	var (
		runtime          *dsl.ValidationRuntime
		validationReport map[string]interface{}
		err              error
	)

	if cfg.ValidationMetadata != nil && cfg.ValidationMetadata.Enable {
		runtime, err = dsl.RunValidation(cfg.ValidationMetadata, record)
		if err != nil {
			return fmt.Errorf("validation execution failed: %w", err)
		}

		validationReport, err = dsl.BuildValidationReport(cfg.ValidationMetadata, runtime)
		if err != nil {
			return fmt.Errorf("failed to build validation metadata: %w", err)
		}
	}

	var summary map[string]interface{}
	if showSummaries {
		summary, err = buildSummaries(cfg.Summaries, runtime, record)
		if err != nil {
			return fmt.Errorf("failed to build summaries: %w", err)
		}
	}

	runtimeMetadata := buildRuntimeMetadata(sourceFile, sigCfg, cfg, runtime, showSummaries && len(summary) > 0, showValidation && validationReport != nil, runtimeProvenance, recordNum)

	final := make(map[string]interface{}, 3)
	final["_runtime"] = runtimeMetadata

	if showValidation && validationReport != nil {
		final["_validation"] = validationReport
	}

	extractBlock := map[string]interface{}{
		"data": dataCopy,
	}
	if showSummaries && len(summary) > 0 {
		extractBlock["summary"] = summary
	}
	final["extract"] = extractBlock

	for k := range record {
		delete(record, k)
	}
	for k, v := range final {
		record[k] = v
	}

	if runtime != nil && cfg.ValidationMetadata != nil {
		shouldFail, failureErr := runtime.ShouldFailExtraction(cfg.ValidationMetadata.FailurePolicy)
		if shouldFail {
			if failureErr != nil {
				return failureErr
			}
			return fmt.Errorf("validation failed")
		}
	}

	return nil
}

func buildSummaries(configs []SummaryConfig, runtime *dsl.ValidationRuntime, record map[string]interface{}) (map[string]interface{}, error) {
	if len(configs) == 0 {
		return nil, nil
	}

	variables, err := buildVariableMap(runtime, record)
	if err != nil {
		return nil, err
	}

	summaries := make(map[string]interface{}, len(configs))

	for _, cfg := range configs {
		total, err := evaluateNumericExpression(cfg.Total.Expression, variables)
		if err != nil {
			return nil, fmt.Errorf("summary %s total: %w", cfg.Name, err)
		}

		components := make([]map[string]interface{}, 0, len(cfg.Components))
		componentSum := 0.0
		var remainderCfg *SummaryComponentConfig

		for i := range cfg.Components {
			componentCfg := cfg.Components[i]
			if componentCfg.Remainder {
				temp := componentCfg
				remainderCfg = &temp
				continue
			}

			value, err := evaluateNumericExpression(componentCfg.Expression, variables)
			if err != nil {
				return nil, fmt.Errorf("summary %s component %s: %w", cfg.Name, componentCfg.Name, err)
			}

			componentSum += value

			component := map[string]interface{}{
				"name":  componentCfg.Name,
				"label": componentCfg.Label,
				"value": normalizeNumber(value),
			}

			if componentCfg.Format != "" {
				component["format"] = componentCfg.Format
			}

			if total != 0 {
				component["share"] = normalizeNumber(value / total)
			}

			components = append(components, component)
		}

		if remainderCfg != nil {
			remainderValue := total - componentSum
			if math.Abs(remainderValue) < 1e-9 {
				remainderValue = 0
			}

			component := map[string]interface{}{
				"name":  remainderCfg.Name,
				"label": remainderCfg.Label,
				"value": normalizeNumber(remainderValue),
			}

			if remainderCfg.Format != "" {
				component["format"] = remainderCfg.Format
			}

			if total != 0 {
				component["share"] = normalizeNumber(remainderValue / total)
			}

			components = append(components, component)
			componentSum += remainderValue
		}

		summary := map[string]interface{}{
			"label":      cfg.Label,
			"total":      normalizeNumber(total),
			"components": components,
		}

		if cfg.Format != "" {
			summary["format"] = cfg.Format
		}

		if math.Abs(total-componentSum) > 1e-6 {
			summary["unreconciled"] = normalizeNumber(total - componentSum)
		}

		summaries[cfg.Name] = summary
	}

	return summaries, nil
}

func buildVariableMap(runtime *dsl.ValidationRuntime, record map[string]interface{}) (map[string]interface{}, error) {
	variables := make(map[string]interface{})

	for key, value := range record {
		variables[key] = value
	}

	if runtime == nil {
		return variables, nil
	}

	for name, acc := range runtime.Accumulators {
		result, err := acc.GetResult()
		if err != nil {
			return nil, fmt.Errorf("failed to get accumulator %s result: %w", name, err)
		}
		variables[name] = result
	}

	for name, value := range runtime.AggregationResults {
		variables[name] = value
	}

	if runtime.ReconciliationScalars != nil {
		for name, value := range runtime.ReconciliationScalars {
			variables[name] = value
		}
	}

	return variables, nil
}

func evaluateNumericExpression(expression string, variables map[string]interface{}) (float64, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return 0, nil
	}

	expr, err := dsl.ParseExpression(expression)
	if err != nil {
		return 0, err
	}

	evaluator := dsl.NewEvaluator(variables)
	value, err := evaluator.EvaluateExpression(expr)
	if err != nil {
		return 0, err
	}

	return coerceToFloat(value)
}

func normalizeNumber(value float64) interface{} {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}

	rounded := math.Round(value*1e6) / 1e6
	if math.Abs(rounded-math.Round(rounded)) < 1e-6 {
		return int64(math.Round(rounded))
	}

	return rounded
}

func coerceToFloat(value interface{}) (float64, error) {
	switch v := value.(type) {
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return 0, nil
		}
		num, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, fmt.Errorf("failed to parse numeric string %q: %w", s, err)
		}
		return num, nil
	default:
		return 0, fmt.Errorf("unsupported numeric expression result type %T", value)
	}
}

func buildRuntimeMetadata(sourceFile string, sigCfg *FileSignature, cfg *ExtractRecordMatch, runtime *dsl.ValidationRuntime, summariesIncluded, validationIncluded bool, runtimeProvenance provenance.RuntimeOptions, recordNum int) map[string]interface{} {
	metadata := map[string]interface{}{
		"generated_at":        time.Now().UTC().Format(time.RFC3339),
		"source_file":         runtimeProvenance.SourceIdentity(sourceFile),
		"record_type":         cfg.RecordType,
		"summaries_included":  summariesIncluded,
		"validation_included": validationIncluded,
	}
	if recordNum > 0 {
		metadata["record_num"] = recordNum
	}

	if sigCfg != nil {
		if sigCfg.SignatureID != "" {
			metadata["signature_id"] = sigCfg.SignatureID
		}
		if sigCfg.Name != "" {
			metadata["signature_name"] = sigCfg.Name
		}
	}

	if cfg.ValidationMetadata != nil {
		metadata["validation_enabled"] = cfg.ValidationMetadata.Enable
		if cfg.ValidationMetadata.ArrayPath != "" {
			metadata["validation_array_path"] = cfg.ValidationMetadata.ArrayPath
		}
	}

	if runtime != nil {
		metadata["validation_record_count"] = runtime.RecordCount
	}

	for key, value := range runtimeProvenance.RuntimeFields() {
		metadata[key] = value
	}

	return metadata
}

// extractValue extracts a value using XPath
func extractValue(node *xmlquery.Node, mapping *FieldMapping) (interface{}, error) {
	if mapping == nil {
		return nil, nil
	}

	typeName := strings.ToLower(mapping.Type)

	if typeName == "array" {
		return extractArrayValue(node, mapping)
	}

	if mapping.XPath == "" {
		return nil, nil
	}

	value, err := evaluateXPathValue(node, mapping.XPath, mapping.CompiledXPath)
	if err != nil {
		return nil, err
	}

	if mapping.Transform == "exists" {
		exists, err := evaluateExists(node, mapping.XPath, mapping.CompiledXPath)
		if err != nil {
			return nil, err
		}
		return exists, nil
	}

	if mapping.Transform != "" {
		transformed, err := transformRegistry.Apply(mapping.Transform, fmt.Sprintf("%v", value), mapping.TransformParams)
		if err != nil {
			return nil, err
		}
		return transformed, nil
	}

	switch typeName {
	case "number":
		return coerceNumber(value)
	case "integer":
		return coerceInteger(value)
	case "boolean":
		return coerceBoolean(value)
	case "string", "":
		return coerceString(value)
	default:
		return value, nil
	}
}

func extractExpressionValue(mapping *FieldMapping, record map[string]interface{}, externalFields map[string]interface{}, references dsl.ReferenceLookup) (interface{}, error) {
	if mapping == nil {
		return nil, nil
	}

	expression := strings.TrimSpace(mapping.Expression)
	if expression == "" {
		return nil, nil
	}
	if strings.TrimSpace(mapping.Transform) != "" {
		return nil, fmt.Errorf("transform %q is not supported for expression field %s", mapping.Transform, mapping.OutputField)
	}
	if len(mapping.TransformParams) > 0 {
		return nil, fmt.Errorf("transform_params are not supported for expression field %s", mapping.OutputField)
	}

	expr, err := dsl.ParseExpression(expression)
	if err != nil {
		return nil, fmt.Errorf("failed to parse expression for field %s (%q): %w", mapping.OutputField, expression, err)
	}

	scope, err := buildExpressionScope(record, externalFields)
	if err != nil {
		return nil, err
	}
	evaluator := dsl.NewEvaluator(scope, dsl.WithReferenceLookup(references))
	value, err := evaluator.EvaluateExpression(expr)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate expression for field %s (%q): %w", mapping.OutputField, expression, err)
	}

	switch strings.ToLower(mapping.Type) {
	case "number":
		return coerceNumber(value)
	case "integer":
		return coerceInteger(value)
	case "boolean":
		return coerceBoolean(value)
	case "string", "":
		return coerceString(value)
	default:
		return value, nil
	}
}

func buildExpressionScope(record map[string]interface{}, externalFields map[string]interface{}) (map[string]interface{}, error) {
	scope := make(map[string]interface{}, len(record)+len(externalFields))
	for key, value := range record {
		scope[key] = value
	}
	for key, value := range externalFields {
		if _, exists := scope[key]; exists {
			return nil, fmt.Errorf("external field key %q collides with extracted record field; rename one of them to keep injection vs content-extraction fidelity explicit", key)
		}
		// Internal (derive-only) captures are visible in expression scope with
		// their real value; the record-emission merge is what skips them.
		scope[key] = unwrapExternalFieldValue(value)
	}
	return scope, nil
}

func extractArrayValue(node *xmlquery.Node, mapping *FieldMapping) (interface{}, error) {
	if mapping == nil {
		return nil, nil
	}

	nodes, err := evaluateNodeSet(node, mapping.CompiledXPath, mapping.XPath)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
	}

	if len(nodes) == 0 {
		return nil, nil
	}

	if len(mapping.Polymorphic) > 0 {
		var items []map[string]interface{}
		for _, sourceNode := range nodes {
			for i := range mapping.Polymorphic {
				pm := &mapping.Polymorphic[i]
				targetNodes, err := resolvePolymorphicTargets(sourceNode, pm)
				if err != nil {
					return nil, err
				}
				for _, targetNode := range targetNodes {
					record := make(map[string]interface{})
					for j := range pm.FieldMappings {
						fieldMap := &pm.FieldMappings[j]
						value, err := extractValue(targetNode, fieldMap)
						if err != nil {
							return nil, fmt.Errorf("failed to extract polymorphic field %s: %w", fieldMap.OutputField, err)
						}
						if value != nil {
							record[fieldMap.OutputField] = value
						}
					}

					if pm.ItemType != "" {
						if _, exists := record["item_type"]; !exists {
							record["item_type"] = pm.ItemType
						}
					}

					if len(record) > 0 {
						items = append(items, record)
					}
				}
			}
		}

		if len(items) == 0 {
			return nil, nil
		}
		return items, nil
	}

	if len(mapping.ItemMapping) > 0 {
		var items []map[string]interface{}
		for _, itemNode := range nodes {
			item := make(map[string]interface{})
			for j := range mapping.ItemMapping {
				itemMap := &mapping.ItemMapping[j]
				value, err := extractValue(itemNode, itemMap)
				if err != nil {
					return nil, err
				}
				item[itemMap.OutputField] = value
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			return nil, nil
		}
		return items, nil
	}

	var values []interface{}
	for _, itemNode := range nodes {
		val := strings.TrimSpace(itemNode.InnerText())
		if val != "" {
			values = append(values, val)
		}
	}

	if len(values) == 0 {
		return nil, nil
	}

	return values, nil
}

func evaluateExists(node *xmlquery.Node, expr string, compiled *xpath.Expr) (bool, error) {
	if node == nil {
		return false, nil
	}
	if strings.TrimSpace(expr) == "" && compiled == nil {
		return false, nil
	}

	nodes, err := evaluateNodeSet(node, compiled, expr)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate XPath %s: %w", expr, err)
	}
	return len(nodes) > 0, nil
}

func evaluateXPathValue(node *xmlquery.Node, expr string, compiled *xpath.Expr) (interface{}, error) {
	if node == nil {
		return nil, nil
	}
	if strings.TrimSpace(expr) == "" && compiled == nil {
		return nil, nil
	}

	xp := compiled
	var err error
	if xp == nil {
		xp, err = xpath.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile XPath %s: %w", expr, err)
		}
	}

	value := xp.Evaluate(xmlquery.CreateXPathNavigator(node))
	switch v := value.(type) {
	case nil:
		return nil, nil
	case bool:
		return v, nil
	case float64:
		return v, nil
	case string:
		return strings.TrimSpace(v), nil
	case *xpath.NodeIterator:
		if v.MoveNext() {
			return strings.TrimSpace(v.Current().Value()), nil
		}
		return nil, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func evaluateNodeSet(node *xmlquery.Node, compiled *xpath.Expr, expr string) ([]*xmlquery.Node, error) {
	if node == nil {
		return nil, nil
	}

	xp := compiled
	var err error
	if xp == nil {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			return nil, nil
		}
		xp, err = xpath.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile XPath %q: %w", expr, err)
		}
	}

	value := xp.Evaluate(xmlquery.CreateXPathNavigator(node))
	switch v := value.(type) {
	case *xpath.NodeIterator:
		return collectNodes(v), nil
	case xpath.NodeIterator:
		return collectNodes(&v), nil
	case bool:
		if v {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	case float64:
		if v != 0 {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	case string:
		if strings.TrimSpace(v) != "" {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func collectNodes(iter *xpath.NodeIterator) []*xmlquery.Node {
	var nodes []*xmlquery.Node
	if iter == nil {
		return nodes
	}

	for iter.MoveNext() {
		if nav, ok := iter.Current().(*xmlquery.NodeNavigator); ok && nav != nil {
			if current := nav.Current(); current != nil {
				nodes = append(nodes, current)
			}
		}
	}
	return nodes
}

func resolvePolymorphicTargets(node *xmlquery.Node, mapping *PolymorphicMapping) ([]*xmlquery.Node, error) {
	if mapping == nil {
		return nil, nil
	}

	if strings.TrimSpace(mapping.MatchXPath) != "" || mapping.CompiledMatchXPath != nil {
		return evaluateNodeSet(node, mapping.CompiledMatchXPath, mapping.MatchXPath)
	}

	if strings.TrimSpace(mapping.ElementType) != "" {
		matches := findChildrenByNameAll(node, mapping.ElementType)
		if strings.EqualFold(node.Data, mapping.ElementType) {
			matches = append([]*xmlquery.Node{node}, matches...)
		}
		return matches, nil
	}

	return []*xmlquery.Node{node}, nil
}

func findChildrenByNameAll(node *xmlquery.Node, name string) []*xmlquery.Node {
	var result []*xmlquery.Node
	if node == nil {
		return result
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xmlquery.ElementNode && strings.EqualFold(child.Data, name) {
			result = append(result, child)
		}
	}

	return result
}

func coerceString(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		// Absent: no selected node / no expression result. Stays absent (omitted from
		// the record → undefined in DSL scope). The ternary guard remains the pattern
		// for maybe-absent fields.
		return nil, nil
	case string:
		// Present-but-empty (and whitespace-only) string values bind as a first-class
		// "" — the typed-string coercion contract: trim, then preserve the empty string
		// as a DEFINED value rather than collapsing it to nil. This makes XPath
		// boolean(node) presence and the string field agree for an empty element, and
		// string_length("")/starts_with_any("", ...) follow their contracts. Shared by
		// XPath string mappings and expression mappings with type: string. Only a nil
		// value remains absent/undefined. (empty-element-bind)
		return strings.TrimSpace(v), nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func coerceNumber(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case float64:
		return v, nil
	case float32:
		return float64(v), nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case int32:
		return float64(v), nil
	case uint:
		return float64(v), nil
	case uint32:
		return float64(v), nil
	case uint64:
		return float64(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		num, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse number %q: %w", s, err)
		}
		return num, nil
	case bool:
		if v {
			return float64(1), nil
		}
		return float64(0), nil
	default:
		return nil, fmt.Errorf("unsupported numeric value type %T", value)
	}
}

func coerceInteger(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case float64:
		return int64(v), nil
	case float32:
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case int32:
		return int64(v), nil
	case uint:
		return uint64ToInt64(uint64(v))
	case uint32:
		return int64(v), nil
	case uint64:
		return uint64ToInt64(v)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		if strings.Contains(s, ".") {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse float %q for integer conversion: %w", s, err)
			}
			return int64(f), nil
		}
		num, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse integer %q: %w", s, err)
		}
		return num, nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	default:
		return nil, fmt.Errorf("unsupported integer value type %T", value)
	}
}

func uint64ToInt64(value uint64) (int64, error) {
	if value > math.MaxInt64 {
		return 0, fmt.Errorf("uint64 value %d overflows int64", value)
	}
	return int64(value), nil // #nosec G115 -- guarded by the MaxInt64 bound check above.
}

func coerceBoolean(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case float32:
		return v != 0, nil
	case int:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case int32:
		return v != 0, nil
	case uint:
		return v != 0, nil
	case uint32:
		return v != 0, nil
	case uint64:
		return v != 0, nil
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		switch s {
		case "", "0", "false", "no", "off":
			return false, nil
		case "1", "true", "yes", "on":
			return true, nil
		default:
			return s != "", nil
		}
	default:
		return nil, fmt.Errorf("unsupported boolean value type %T", value)
	}
}

// passesFilters checks if a record passes the filters
func passesFilters(record map[string]interface{}, filters map[string]interface{}) bool {
	// Simple filter implementation
	for key, condition := range filters {
		if value, exists := record[key]; exists {
			// Simple condition check (e.g., "> 0")
			if condStr, ok := condition.(string); ok {
				if strings.HasPrefix(condStr, "> ") {
					threshold := strings.TrimPrefix(condStr, "> ")
					// Basic comparison
					if valueStr, ok := value.(string); ok {
						if valueStr <= threshold {
							return false
						}
					}
				}
			}
		}
	}
	return true
}

func formatValidationErrors(errors []schema.ValidationError) string {
	if len(errors) == 0 {
		return ""
	}
	var b strings.Builder
	for i, err := range errors {
		path := err.Path
		if path == "" {
			path = "(root)"
		}
		fmt.Fprintf(&b, "  %d. %s: %s\n", i+1, path, err.Message)
	}
	return b.String()
}
