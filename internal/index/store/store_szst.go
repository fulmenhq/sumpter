//go:build cgo && seekablezstd

package store

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fulmenhq/sumpter/internal/index"

	seekable "github.com/3leaps/seekable-zstd/bindings/go"
)

// SeekableZstdAvailable returns true when built with seekable-zstd support.
func SeekableZstdAvailable() bool {
	return true
}

// SeekableZstdVersion returns the seekable-zstd library version.
func SeekableZstdVersion() string {
	return seekable.Version()
}

// SzstHeader represents the header schema for seekable-zstd record index stores.
//
// This is distinct from index.RecordIndex to support the MVP binary format
// which includes record encoding metadata not present in the JSON schema.
type SzstHeader struct {
	// Version identifies the store format version.
	Version string `json:"version"`

	// Source describes the original XML file.
	Source index.SourceInfo `json:"source"`

	// Selector describes how records were identified.
	Selector index.SelectorInfo `json:"selector"`

	// Summary contains aggregate statistics.
	Summary index.SummaryStats `json:"summary"`

	// Records contains encoding metadata for the binary record table.
	Records SzstRecordsMetadata `json:"records"`
}

// SzstRecordsMetadata describes the binary encoding of the records table.
type SzstRecordsMetadata struct {
	// RecordCount is the number of records in the .records.szst file.
	RecordCount int64 `json:"record_count"`

	// RecordWidthBytes is the fixed width of each record in bytes.
	RecordWidthBytes int `json:"record_width_bytes"`

	// SHAEncoding describes how SHA256 is stored: "raw32" (32 bytes) or "hex" (64 bytes).
	SHAEncoding string `json:"sha_encoding"`

	// Endianness describes byte order: "little" or "big".
	Endianness string `json:"endianness"`

	// ElementNameStrategy describes how element names are stored:
	// "from_selector" (constant, not stored per-record) or "per_record" (stored in table).
	ElementNameStrategy string `json:"element_name_strategy,omitempty"`
}

// szstStore implements IndexStore for seekable-zstd binary format.
//
// The format consists of two files:
//   - *.recordindex.header.json: Header metadata (source, selector, summary, records encoding)
//   - *.recordindex.records.szst: Fixed-width binary record table
type szstStore struct {
	headerPath  string
	recordsPath string
	szstHeader  *SzstHeader
	reader      *seekable.Reader
}

// openSeekableZstdStore opens a seekable-zstd record index store.
func openSeekableZstdStore(headerPath string) (IndexStore, error) {
	// Derive records path from header path
	recordsPath := strings.TrimSuffix(headerPath, ".header.json") + ".records.szst"

	// Load header JSON
	headerData, err := os.ReadFile(headerPath) // #nosec G304 - path is user-provided
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	var szstHeader SzstHeader
	if err := json.Unmarshal(headerData, &szstHeader); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}

	// Validate required fields
	if szstHeader.Records.RecordWidthBytes <= 0 {
		return nil, fmt.Errorf("invalid header: record_width_bytes must be positive, got %d", szstHeader.Records.RecordWidthBytes)
	}
	if szstHeader.Records.RecordCount < 0 {
		return nil, fmt.Errorf("invalid header: record_count must be non-negative, got %d", szstHeader.Records.RecordCount)
	}

	// Open seekable-zstd reader for records
	reader, err := seekable.Open(recordsPath)
	if err != nil {
		return nil, fmt.Errorf("open records store: %w", err)
	}

	// Validate file size matches expected record count
	expectedSize := uint64(szstHeader.Records.RecordCount) * uint64(szstHeader.Records.RecordWidthBytes)
	actualSize := reader.Size()

	if actualSize != expectedSize {
		_ = reader.Close()
		return nil, fmt.Errorf("records file size mismatch: expected %d bytes (%d records * %d width), got %d bytes",
			expectedSize, szstHeader.Records.RecordCount, szstHeader.Records.RecordWidthBytes, actualSize)
	}

	// Validate no trailing bytes (size % width == 0)
	if actualSize%uint64(szstHeader.Records.RecordWidthBytes) != 0 {
		_ = reader.Close()
		return nil, fmt.Errorf("records file size %d is not evenly divisible by record width %d",
			actualSize, szstHeader.Records.RecordWidthBytes)
	}

	return &szstStore{
		headerPath:  headerPath,
		recordsPath: recordsPath,
		szstHeader:  &szstHeader,
		reader:      reader,
	}, nil
}

// Header returns the index header information.
//
// Note: This converts from SzstHeader to index.RecordIndex for compatibility
// with the existing extraction pipeline.
func (s *szstStore) Header() (*index.RecordIndex, error) {
	return &index.RecordIndex{
		Version:  s.szstHeader.Version,
		Source:   s.szstHeader.Source,
		Selector: s.szstHeader.Selector,
		Summary:  s.szstHeader.Summary,
		// Records slice is intentionally empty - use Records() iterator instead
	}, nil
}

// Records returns an iterator over all record metadata entries.
func (s *szstStore) Records(ctx context.Context) (RecordIterator, error) {
	// Default to Background context if nil to prevent panic
	if ctx == nil {
		ctx = context.Background()
	}

	return &szstRecordIterator{
		reader:      s.reader,
		recordWidth: s.szstHeader.Records.RecordWidthBytes,
		recordCount: int(s.szstHeader.Records.RecordCount),
		shaEncoding: s.szstHeader.Records.SHAEncoding,
		endianness:  s.szstHeader.Records.Endianness,
		current:     0,
		ctx:         ctx,
	}, nil
}

// Close releases resources.
func (s *szstStore) Close() error {
	if s.reader != nil {
		return s.reader.Close()
	}
	return nil
}

// szstRecordIterator iterates over binary records in the .szst file.
type szstRecordIterator struct {
	reader      *seekable.Reader
	recordWidth int
	recordCount int
	shaEncoding string
	endianness  string
	current     int
	ctx         context.Context
}

// Next returns the next record metadata entry.
func (it *szstRecordIterator) Next() (*index.RecordMetadata, error) {
	// Check context cancellation
	select {
	case <-it.ctx.Done():
		return nil, it.ctx.Err()
	default:
	}

	if it.current >= it.recordCount {
		return nil, io.EOF
	}

	// Read one record worth of bytes
	offset := int64(it.current * it.recordWidth)
	buf := make([]byte, it.recordWidth)

	n, err := it.reader.ReadAt(buf, offset)
	if err != nil && err != io.EOF {
		return nil, fmt.Errorf("read record %d: %w", it.current, err)
	}
	if n < it.recordWidth {
		return nil, fmt.Errorf("short read for record %d: got %d, want %d", it.current, n, it.recordWidth)
	}

	// Decode binary record based on endianness
	var byteOrder binary.ByteOrder = binary.LittleEndian
	if it.endianness == "big" {
		byteOrder = binary.BigEndian
	}

	// Layout (64 bytes default): start(8) + end(8) + size(8) + depth(4) + sha256(32) + padding(4)
	rec := &index.RecordMetadata{
		StartOffset: int64(byteOrder.Uint64(buf[0:8])),
		EndOffset:   int64(byteOrder.Uint64(buf[8:16])),
		SizeBytes:   int64(byteOrder.Uint64(buf[16:24])),
		Depth:       int(byteOrder.Uint32(buf[24:28])),
	}

	// Decode SHA256 based on encoding
	switch it.shaEncoding {
	case "raw32":
		// 32 raw bytes at offset 28, convert to hex string
		rec.SHA256 = fmt.Sprintf("%x", buf[28:60])
	case "hex":
		// 64 hex characters stored directly (less efficient but human-readable)
		rec.SHA256 = string(buf[28:92])
	default:
		// Default to raw32 for backward compatibility
		rec.SHA256 = fmt.Sprintf("%x", buf[28:60])
	}

	it.current++
	return rec, nil
}

// Close is a no-op for szst iterator (reader is owned by store).
func (it *szstRecordIterator) Close() error {
	return nil
}
