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
	"time"

	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// Builder creates XML record indexes with a streaming scanner. The BuildTo
// writer path keeps default memory bounded by parser state and writer buffering.
type Builder struct {
	opts BuildOptions
}

// NewBuilder creates a new index builder with the given options
func NewBuilder(opts BuildOptions) *Builder {
	return &Builder{opts: opts}
}

// Build creates a record index by streaming through the XML file and collecting
// records in memory for compatibility with callers that need a complete
// RecordIndex value. Use BuildTo for bounded-memory artifact writing.
func (b *Builder) Build() (*RecordIndex, error) {
	collector := &collectingIndexWriter{}
	index, err := b.BuildTo(collector)
	if err != nil {
		return nil, err
	}
	index.Records = collector.records
	return index, nil
}

// BuildTo creates a record index by streaming through the XML file and appends
// each discovered record to the supplied writers. Default memory usage is
// bounded by parser state, writer buffering, and optional exact-percentile
// retention; it does not retain RecordMetadata for the full file.
func (b *Builder) BuildTo(writers ...IndexWriter) (result *RecordIndex, err error) {
	startTime := time.Now()
	startedWriters := make([]IndexWriter, 0, len(writers))
	defer func() {
		for i := len(startedWriters) - 1; i >= 0; i-- {
			if closeErr := startedWriters[i].Close(); closeErr != nil && err == nil {
				err = fmt.Errorf("failed to close index writer: %w", closeErr)
			}
		}
	}()

	// Validate input file exists
	fileInfo, err := os.Stat(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}

	// Detect and reject compressed inputs before any hashing, scanning, or output.
	compressed, compressionFormat := DetectCompression(b.opts.InputPath)
	if compressed {
		return nil, CompressedSourceIndexBuildError(b.opts.InputPath, compressionFormat)
	}

	recordSelector, err := streaming.ParseRecordSelector(b.opts.Selector)
	if err != nil {
		return nil, err
	}

	// Compute source file SHA256 (first pass)
	sourceHash, err := computeFileSHA256(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}

	// The header records the logical source identity (the s3:// URI for cloud
	// sources), not the staged local read path under $SUMPTER_HOME/work, which is
	// internal and must never leak into the index. Local builds leave
	// SourceIdentity empty and fall back to InputPath (byte-for-byte unchanged).
	sourceIdentity := b.opts.SourceIdentity
	if sourceIdentity == "" {
		sourceIdentity = b.opts.InputPath
	}

	header := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              sourceIdentity,
			SizeBytes:         fileInfo.Size(),
			SHA256:            sourceHash,
			Compressed:        compressed,
			CompressionFormat: compressionFormat,
			OffsetKind:        OffsetKindSourceBytes,
			CreatedAt:         time.Now().UTC(),
		},
		Selector: SelectorInfo{
			XPath:       recordSelector.Raw,
			ElementName: recordSelector.ElementName,
		},
		Metadata: IndexMetadata{
			Generator:      fmt.Sprintf("sumpter index build %s", b.opts.SumpterVersion),
			SumpterVersion: b.opts.SumpterVersion,
		},
	}
	NormalizeRecordIndex(header)

	for _, writer := range writers {
		if writer == nil {
			return nil, fmt.Errorf("index writer is required")
		}
		if err := writer.Start(header); err != nil {
			return nil, fmt.Errorf("failed to start index writer: %w", err)
		}
		startedWriters = append(startedWriters, writer)
	}

	// Open file for record scanning and range hashing (second pass)
	file, err := os.Open(b.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var reader io.Reader = file

	// Create record scanner in size-only mode for memory efficiency
	scanner := streaming.NewRecordScannerSizeOnly(reader, recordSelector.Raw)
	defer func() { _ = scanner.Close() }()

	// Emit records while updating statistics incrementally.
	var totalBytes int64
	var minSize int64 = -1
	var maxSize int64
	var sizes []int64
	if b.opts.IncludeP50 || b.opts.IncludeP95 || b.opts.IncludeP99 {
		sizes = make([]int64, 0, 1024)
	}

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

		// Compute record SHA256 over the original source bytes. ReadAt avoids
		// disturbing the scanner's sequential file offset and avoids an
		// open/seek/close cycle per record.
		recordHash, err := computeRangeHashSHA256FromReader(file, rec.StartOffset, rec.EndOffset)
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

		for _, writer := range writers {
			if err := writer.AppendRecord(metadata); err != nil {
				return nil, fmt.Errorf("failed to append record %d: %w", recordNum, err)
			}
		}

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

	index := *header
	index.Summary = summary
	index.Metadata.BuildDurationMs = time.Since(startTime).Milliseconds()
	NormalizeRecordIndex(&index)

	for _, writer := range writers {
		if err := writer.Prepare(&index); err != nil {
			return nil, fmt.Errorf("failed to prepare index writer: %w", err)
		}
	}
	for _, writer := range writers {
		if err := writer.Commit(); err != nil {
			return nil, fmt.Errorf("failed to commit index writer: %w", err)
		}
	}
	for _, writer := range writers {
		if err := writer.Complete(); err != nil {
			return nil, fmt.Errorf("failed to complete index writer: %w", err)
		}
	}

	return &index, nil
}

// WriteToFile writes the record index to a JSON file
func (b *Builder) WriteToFile(index *RecordIndex, outputPath string) error {
	if index == nil {
		return fmt.Errorf("record index is required")
	}
	normalized := *index
	normalized.Version = SchemaVersion
	NormalizeRecordIndex(&normalized)

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
	if err := encoder.Encode(&normalized); err != nil {
		return fmt.Errorf("failed to encode index to JSON: %w", err)
	}

	return nil
}

// VerifySourceIntegrity checks that the file at localPath matches the size and
// SHA-256 recorded in the index header's SourceInfo. It is the cloud read-
// boundary guard for parallel/seekable extraction: a remote source object is
// mutable, so if it changed since the index was built the recorded byte offsets
// are no longer valid and extraction must fail rather than read garbage ranges.
func VerifySourceIntegrity(localPath string, source SourceInfo) error {
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("stat source for integrity check: %w", err)
	}
	if info.Size() != source.SizeBytes {
		return fmt.Errorf("source size mismatch: index recorded %d bytes but the source is %d bytes (the source changed since the index was built; rebuild the index)", source.SizeBytes, info.Size())
	}
	sum, err := computeFileSHA256(localPath)
	if err != nil {
		return fmt.Errorf("hash source for integrity check: %w", err)
	}
	if sum != source.SHA256 {
		return fmt.Errorf("source SHA-256 mismatch: index recorded %s but the source hashes to %s (the source changed since the index was built; rebuild the index)", source.SHA256, sum)
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

	return computeRangeHashSHA256FromReader(file, start, end)
}

func computeRangeHashSHA256FromReader(reader io.ReaderAt, start, end int64) (string, error) {
	if end < start {
		return "", fmt.Errorf("invalid byte range: start %d after end %d", start, end)
	}

	hash := sha256.New()
	section := io.NewSectionReader(reader, start, end-start)
	if _, err := io.Copy(hash, section); err != nil {
		return "", err
	}

	return hex.EncodeToString(hash.Sum(nil)), nil
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
	result := float64(sortedSizes[lower])*(1-weight) + float64(sortedSizes[upper])*weight
	return int64(result + 0.5) // Round to nearest integer
}
