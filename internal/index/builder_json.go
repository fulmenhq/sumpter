package index

import (
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// buildJSONTo indexes an uncompressed JSON source. The source is opened once:
// its hash, the record scan and every record hash read that one open file,
// and the source is hashed again at the end so a change during the build
// fails it. Records are the selector's elements in document order, with
// ranges over the raw source bytes; JSON records have no namespace context.
func (b *Builder) buildJSONTo(startTime time.Time, started *[]IndexWriter, writers []IndexWriter) (*RecordIndex, error) {
	selector, err := jsonElementSelector(b.opts.Selector)
	if err != nil {
		return nil, err
	}
	src, err := openJSONSource(b.opts.InputPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()

	sourceHash, err := src.hash()
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}
	sourceIdentity := b.opts.SourceIdentity
	if sourceIdentity == "" {
		sourceIdentity = b.opts.InputPath
	}
	header := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              sourceIdentity,
			SizeBytes:         src.size,
			SHA256:            sourceHash,
			Compressed:        false,
			CompressionFormat: "none",
			OffsetKind:        OffsetKindSourceBytes,
			Format:            SourceFormatJSON,
			CreatedAt:         time.Now().UTC(),
		},
		Selector:          SelectorInfo{XPath: selector.Raw, ElementName: selector.ElementName},
		NamespaceContexts: []NamespaceContext{{ID: 0, Declarations: []NamespaceDeclaration{}}},
		Metadata: IndexMetadata{
			Generator:      fmt.Sprintf("sumpter index build %s", b.opts.SumpterVersion),
			SumpterVersion: b.opts.SumpterVersion,
		},
	}

	for _, writer := range writers {
		if writer == nil {
			return nil, fmt.Errorf("index writer is required")
		}
		if err := writer.Start(header); err != nil {
			return nil, fmt.Errorf("failed to start index writer: %w", err)
		}
		*started = append(*started, writer)
	}

	scanner, err := src.records(selector.Raw)
	if err != nil {
		return nil, err
	}
	defer func() { _ = scanner.Close() }()

	var (
		totalBytes int64
		minSize    int64 = -1
		maxSize    int64
		sizes      []int64
		count      int
	)
	wantSizes := b.opts.IncludeP50 || b.opts.IncludeP95 || b.opts.IncludeP99
	for {
		rec, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("failed to scan record %d: %w", count+1, err)
		}
		count++
		recordHash, err := src.rangeHash(rec.StartOffset, rec.EndOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash for record %d: %w", rec.Num, err)
		}
		metadata := RecordMetadata{
			RecordNum:   rec.Num,
			StartOffset: rec.StartOffset,
			EndOffset:   rec.EndOffset,
			SizeBytes:   rec.SizeBytes,
			SHA256:      recordHash,
			ElementName: rec.Name,
			Depth:       rec.Depth,
		}
		for _, writer := range writers {
			if err := writer.AppendRecord(metadata); err != nil {
				return nil, fmt.Errorf("failed to append record %d: %w", rec.Num, err)
			}
		}
		totalBytes += rec.SizeBytes
		if minSize == -1 || rec.SizeBytes < minSize {
			minSize = rec.SizeBytes
		}
		if rec.SizeBytes > maxSize {
			maxSize = rec.SizeBytes
		}
		if wantSizes {
			sizes = append(sizes, rec.SizeBytes)
		}
	}

	// The whole-source hash was taken before the scan; take it again so a
	// source changed during the build cannot leave the header and the record
	// hashes describing different bytes.
	again, err := src.hash()
	if err != nil {
		return nil, fmt.Errorf("failed to recompute source file hash: %w", err)
	}
	if again != sourceHash {
		return nil, fmt.Errorf("source changed while the index was built; rebuild it from a stable file")
	}

	if count == 0 {
		minSize = 0
	}
	summary := SummaryStats{
		TotalRecords:       count,
		TotalBytes:         totalBytes,
		MinRecordSizeBytes: minSize,
		MaxRecordSizeBytes: maxSize,
	}
	if count > 0 {
		summary.AvgRecordSizeBytes = float64(totalBytes) / float64(count)
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

// jsonElementSelector parses a JSON record selector: Name or //Name.
func jsonElementSelector(selector string) (streaming.RecordSelector, error) {
	return streaming.ParseRecordSelector(selector)
}
