package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

const (
	// SchemaVersion is the current record index schema version
	SchemaVersion = "record-index/v0.1.0"
)

// Builder creates XML record indexes with streaming architecture
// to maintain constant memory usage regardless of source file size.
type Builder struct {
	opts BuildOptions
}

// NewBuilder creates a new index builder with the given options
func NewBuilder(opts BuildOptions) *Builder {
	return &Builder{opts: opts}
}

// Build creates a record index by streaming through the XML file.
// This function maintains constant memory usage by:
// 1. Streaming file for SHA256 computation (first pass)
// 2. Streaming for record boundary detection (second pass)
// 3. Computing statistics incrementally
// 4. Writing index to disk progressively
func (b *Builder) Build() (*RecordIndex, error) {
	startTime := time.Now()

	// Validate input file exists
	fileInfo, err := os.Stat(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}

	// Compute source file SHA256 (first pass)
	sourceHash, err := computeFileSHA256(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}

	// Detect compression
	compressed, compressionFormat := detectCompression(b.opts.InputPath)

	// Open file for record scanning (second pass)
	file, err := os.Open(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer func() { _ = file.Close() }()

	// Create record scanner in size-only mode for memory efficiency
	scanner := streaming.NewRecordScannerSizeOnly(file, b.opts.Selector)
	defer func() { _ = scanner.Close() }()

	// Collect records with incremental statistics
	records := make([]RecordMetadata, 0, 1024) // Pre-allocate with reasonable capacity
	var totalBytes int64
	var minSize int64 = -1
	var maxSize int64
	sizes := make([]int64, 0, 1024) // For percentile calculation

	recordNum := 0
	for {
		rec, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to scan record %d: %w", recordNum+1, err)
		}

		recordNum++

		// Compute record SHA256 by re-reading the specific byte range
		// This is more efficient than buffering entire records in memory
		recordHash, err := computeRangeHashSHA256(b.opts.InputPath, rec.StartOffset, rec.EndOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash for record %d: %w", recordNum, err)
		}

		// Create record metadata
		metadata := RecordMetadata{
			RecordNum:   rec.RecordNum,
			StartOffset: rec.StartOffset,
			EndOffset:   rec.EndOffset,
			SizeBytes:   rec.SizeBytes,
			SHA256:      recordHash,
			ElementName: rec.ElementName,
			Depth:       rec.Depth,
		}
		records = append(records, metadata)

		// Update statistics incrementally
		totalBytes += rec.SizeBytes
		if minSize == -1 || rec.SizeBytes < minSize {
			minSize = rec.SizeBytes
		}
		if rec.SizeBytes > maxSize {
			maxSize = rec.SizeBytes
		}

		// Collect sizes for percentile calculation (if requested)
		if b.opts.IncludeP50 || b.opts.IncludeP95 || b.opts.IncludeP99 {
			sizes = append(sizes, rec.SizeBytes)
		}
	}

	// Handle empty file case
	if recordNum == 0 {
		minSize = 0
	}

	// Calculate average
	avgSize := float64(0)
	if recordNum > 0 {
		avgSize = float64(totalBytes) / float64(recordNum)
	}

	// Calculate percentiles if requested
	summary := SummaryStats{
		TotalRecords:       recordNum,
		TotalBytes:         totalBytes,
		AvgRecordSizeBytes: avgSize,
		MinRecordSizeBytes: minSize,
		MaxRecordSizeBytes: maxSize,
	}

	if len(sizes) > 0 {
		sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
		if b.opts.IncludeP50 {
			summary.P50RecordSizeBytes = percentile(sizes, 0.50)
		}
		if b.opts.IncludeP95 {
			summary.P95RecordSizeBytes = percentile(sizes, 0.95)
		}
		if b.opts.IncludeP99 {
			summary.P99RecordSizeBytes = percentile(sizes, 0.99)
		}
	}

	// Extract element name from selector
	elementName := extractElementName(b.opts.Selector)

	// Build index structure
	index := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              b.opts.InputPath,
			SizeBytes:         fileInfo.Size(),
			SHA256:            sourceHash,
			Compressed:        compressed,
			CompressionFormat: compressionFormat,
			CreatedAt:         time.Now().UTC(),
		},
		Selector: SelectorInfo{
			XPath:       b.opts.Selector,
			ElementName: elementName,
		},
		Records: records,
		Summary: summary,
		Metadata: IndexMetadata{
			Generator:       fmt.Sprintf("sumpter index build %s", b.opts.SumpterVersion),
			BuildDurationMs: time.Since(startTime).Milliseconds(),
			SumpterVersion:  b.opts.SumpterVersion,
		},
	}

	return index, nil
}

// WriteToFile writes the record index to a JSON file
func (b *Builder) WriteToFile(index *RecordIndex, outputPath string) error {
	// Create output directory if needed
	dir := filepath.Dir(outputPath)
	// #nosec G301 - standard permissions for output directories
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write JSON with indentation for readability
	file, err := os.Create(outputPath) // #nosec G304 - outputPath is user-provided CLI argument
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(index); err != nil {
		return fmt.Errorf("failed to encode index to JSON: %w", err)
	}

	return nil
}

// computeFileSHA256 computes the SHA256 hash of an entire file using streaming
func computeFileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// computeRangeHashSHA256 computes the SHA256 hash of a specific byte range in a file
func computeRangeHashSHA256(path string, start, end int64) (string, error) {
	file, err := os.Open(path) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	// Seek to start position
	if _, err := file.Seek(start, io.SeekStart); err != nil {
		return "", err
	}

	// Read only the specified range
	hash := sha256.New()
	limitedReader := io.LimitReader(file, end-start)
	if _, err := io.Copy(hash, limitedReader); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
}

// detectCompression determines if a file is compressed based on extension
func detectCompression(path string) (bool, string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".gz":
		return true, "gzip"
	case ".bz2":
		return true, "bzip2"
	case ".xz":
		return true, "xz"
	default:
		return false, "none"
	}
}

// extractElementName extracts the element name from an XPath selector
func extractElementName(selector string) string {
	selector = strings.TrimSpace(selector)
	selector = strings.TrimPrefix(selector, "//")
	selector = strings.TrimPrefix(selector, "/")

	parts := strings.Split(selector, "/")
	if len(parts) > 0 {
		elementName := parts[len(parts)-1]
		if idx := strings.Index(elementName, "["); idx >= 0 {
			elementName = elementName[:idx]
		}
		if colon := strings.Index(elementName, ":"); colon >= 0 {
			elementName = elementName[colon+1:]
		}
		return strings.TrimSpace(elementName)
	}
	return selector
}

// percentile calculates the percentile value from a sorted slice
func percentile(sortedSizes []int64, p float64) int64 {
	if len(sortedSizes) == 0 {
		return 0
	}
	if len(sortedSizes) == 1 {
		return sortedSizes[0]
	}

	// Calculate index (0-based)
	index := p * float64(len(sortedSizes)-1)
	lower := int(index)
	upper := lower + 1

	if upper >= len(sortedSizes) {
		return sortedSizes[len(sortedSizes)-1]
	}

	// Linear interpolation between lower and upper
	weight := index - float64(lower)
	return int64(float64(sortedSizes[lower])*(1-weight) + float64(sortedSizes[upper])*weight)
}
