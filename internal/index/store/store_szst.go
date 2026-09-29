//go:build cgo && seekablezstd

package store

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

// Note: SzstIndexHeader and SzstRecordsMetadata types are defined in writer.go
// to avoid redeclaration when both files are compiled together.

// szstStore implements IndexStore for seekable-zstd binary format.
//
// The format consists of two files:
//   - *.recordindex.header.json: Header metadata (source, selector, summary, records encoding)
//   - *.recordindex.records.szst: Fixed-width binary record table
type szstStore struct {
	headerPath  string
	recordsPath string
	szstHeader  *SzstIndexHeader
	layout      SzstRecordLayout
	reader      *seekable.Reader
	records     *os.File // held open for a v0.1.2 store; the reader reads it by descriptor
}

// testHookAfterHeaderRead, when a test sets it, runs between reading the
// header and opening the records file.
var testHookAfterHeaderRead func()

// openSeekableZstdStore opens a seekable-zstd record index store.
func openSeekableZstdStore(headerPath string) (IndexStore, error) {
	// The header's directory is the trust root: it is opened once, and the
	// header and a v0.1.2 records file are both opened relative to it.
	headerDir := filepath.Dir(headerPath)
	dir, err := openAnchoredDir(headerDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	headerData, err := dir.readFile(filepath.Base(headerPath))
	if err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	var szstHeader SzstIndexHeader
	if err := json.Unmarshal(headerData, &szstHeader); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if szstHeader.Records.RecordCount < 0 {
		return nil, fmt.Errorf("invalid header: record_count must be non-negative, got %d", szstHeader.Records.RecordCount)
	}
	layout, err := szstLayout(&szstHeader)
	if err != nil {
		return nil, err
	}
	if _, err := index.SourceFormat(&index.RecordIndex{Version: szstHeader.Version, Source: szstHeader.Source}); err != nil {
		return nil, fmt.Errorf("invalid header: %w", err)
	}

	if testHookAfterHeaderRead != nil {
		testHookAfterHeaderRead()
	}
	var (
		reader  *seekable.Reader
		records *os.File
	)
	if szstHeader.Version == index.SzstStoreVersion {
		// The header names one file beside it; open that file without
		// following a link and hand the reader the open file itself.
		if err := recordsFileName(szstHeader.Records.RecordsFile); err != nil {
			return nil, err
		}
		records, err = dir.openRegular(szstHeader.Records.RecordsFile)
		if err != nil {
			return nil, err
		}
		fdPath, err := descriptorPath(records)
		if err != nil {
			_ = records.Close()
			return nil, err
		}
		if reader, err = seekable.Open(fdPath); err != nil {
			_ = records.Close()
			return nil, fmt.Errorf("open records store: %w", err)
		}
	} else {
		recordsPath := strings.TrimSuffix(headerPath, ".header.json") + ".records.szst"
		if szstHeader.Records.RecordsFile != "" {
			recordsPath = filepath.Join(headerDir, szstHeader.Records.RecordsFile)
		}
		if reader, err = seekable.Open(recordsPath); err != nil {
			return nil, fmt.Errorf("open records store: %w", err)
		}
	}
	closeAll := func() {
		_ = reader.Close()
		if records != nil {
			_ = records.Close()
		}
	}

	// Validate file size matches expected record count
	expectedSize := uint64(szstHeader.Records.RecordCount) * uint64(layout.WidthBytes)
	actualSize := reader.Size()
	if actualSize != expectedSize {
		closeAll()
		return nil, fmt.Errorf("records file size mismatch: expected %d bytes (%d records * %d width), got %d bytes",
			expectedSize, szstHeader.Records.RecordCount, layout.WidthBytes, actualSize)
	}

	return &szstStore{
		headerPath:  headerPath,
		recordsPath: filepath.Join(headerDir, szstHeader.Records.RecordsFile),
		szstHeader:  &szstHeader,
		layout:      layout,
		reader:      reader,
		records:     records,
	}, nil
}

// szstLayout returns the record layout a header declares. A v0.1.2 header
// must describe it in records.layout and carry none of the flat fields; an
// earlier header must use the flat fields and no layout object. Any other
// version is refused.
func szstLayout(h *SzstIndexHeader) (SzstRecordLayout, error) {
	r := h.Records
	switch h.Version {
	case index.SzstStoreVersion:
		if r.Layout == nil {
			return SzstRecordLayout{}, fmt.Errorf("invalid header: %s requires records.layout", h.Version)
		}
		if r.RecordWidthBytes != 0 || r.SHAEncoding != "" || r.Endianness != "" {
			return SzstRecordLayout{}, fmt.Errorf("invalid header: %s must not carry flat record_width_bytes, sha_encoding, or endianness beside records.layout", h.Version)
		}
		l := *r.Layout
		if l.WidthBytes != BinaryRecordWidth || l.SHAEncoding != "raw32" || l.Endianness != "little" {
			return SzstRecordLayout{}, fmt.Errorf("invalid header: records.layout must be %d-byte raw32 little-endian records, got %d-byte %q %q", BinaryRecordWidth, l.WidthBytes, l.SHAEncoding, l.Endianness)
		}
		return l, nil
	case index.LegacySzstStoreVersion, index.LegacySzstStoreVersionV010:
		if r.Layout != nil {
			return SzstRecordLayout{}, fmt.Errorf("invalid header: %s predates records.layout", h.Version)
		}
		if r.RecordWidthBytes <= 0 {
			return SzstRecordLayout{}, fmt.Errorf("invalid header: record_width_bytes must be positive, got %d", r.RecordWidthBytes)
		}
		return SzstRecordLayout{WidthBytes: r.RecordWidthBytes, SHAEncoding: r.SHAEncoding, Endianness: r.Endianness}, nil
	default:
		return SzstRecordLayout{}, fmt.Errorf("unsupported seekable-zstd index version: %q (expected %s, %s, or %s)", h.Version, index.SzstStoreVersion, index.LegacySzstStoreVersion, index.LegacySzstStoreVersionV010)
	}
}

// Header returns the index header information.
//
// Note: This converts from SzstHeader to index.RecordIndex for compatibility
// with the existing extraction pipeline.
func (s *szstStore) Header() (*index.RecordIndex, error) {
	header := &index.RecordIndex{
		Version:           s.szstHeader.Version,
		Source:            s.szstHeader.Source,
		Selector:          s.szstHeader.Selector,
		NamespaceContexts: s.szstHeader.NamespaceContexts,
		Summary:           s.szstHeader.Summary,
		// Records slice is intentionally empty - use Records() iterator instead
	}
	index.NormalizeRecordIndex(header)
	return header, nil
}

// Records returns an iterator over all record metadata entries.
func (s *szstStore) Records(ctx context.Context) (RecordIterator, error) {
	// Default to Background context if nil to prevent panic
	if ctx == nil {
		ctx = context.Background()
	}

	return &szstRecordIterator{
		reader:      s.reader,
		recordWidth: s.layout.WidthBytes,
		recordCount: int(s.szstHeader.Records.RecordCount),
		shaEncoding: s.layout.SHAEncoding,
		endianness:  s.layout.Endianness,
		current:     0,
		ctx:         ctx,
	}, nil
}

// Close releases resources.
func (s *szstStore) Close() error {
	var err error
	if s.reader != nil {
		err = s.reader.Close()
	}
	if s.records != nil {
		err = errors.Join(err, s.records.Close())
	}
	return err
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

	// Validate buffer size for chosen encoding
	minSize := 32 // Fixed fields: start(8) + end(8) + size(8) + depth(4) + record_num(4)
	switch it.shaEncoding {
	case "raw32":
		minSize += 32 // 32 bytes for raw SHA256
	case "hex":
		minSize += 64 // 64 bytes for hex-encoded SHA256
	default:
		minSize += 32 // Default to raw32
	}
	if it.recordWidth < minSize {
		return nil, fmt.Errorf("record_width_bytes %d is too small for sha_encoding %q (need at least %d)",
			it.recordWidth, it.shaEncoding, minSize)
	}

	// Layout (68 bytes for raw32 in v0.1.1; 64-byte legacy rows omit namespace_context_ref):
	//   start_offset: int64  (bytes 0-8)
	//   end_offset:   int64  (bytes 8-16)
	//   size_bytes:   int64  (bytes 16-24)
	//   depth:        int32  (bytes 24-28)
	//   record_num:   int32  (bytes 28-32)
	//   sha256:       [32]b  (bytes 32-64) for raw32, or [64]b for hex
	//   namespace_context_ref: int32 (bytes 64-68, when present)
	rec := &index.RecordMetadata{
		StartOffset: int64(byteOrder.Uint64(buf[0:8])),
		EndOffset:   int64(byteOrder.Uint64(buf[8:16])),
		SizeBytes:   int64(byteOrder.Uint64(buf[16:24])),
		Depth:       int(byteOrder.Uint32(buf[24:28])),
		RecordNum:   int(byteOrder.Uint32(buf[28:32])),
	}

	// Decode SHA256 based on encoding (starts at byte 32)
	switch it.shaEncoding {
	case "raw32":
		// 32 raw bytes at offset 32, convert to hex string
		rec.SHA256 = fmt.Sprintf("%x", buf[32:64])
	case "hex":
		// 64 hex characters stored directly at offset 32
		rec.SHA256 = string(buf[32:96])
	default:
		// Default to raw32 for backward compatibility
		rec.SHA256 = fmt.Sprintf("%x", buf[32:64])
	}
	if it.recordWidth >= BinaryRecordWidth {
		rec.NamespaceContextRef = int(byteOrder.Uint32(buf[64:68]))
	}

	it.current++
	return rec, nil
}

// Close is a no-op for szst iterator (reader is owned by store).
func (it *szstRecordIterator) Close() error {
	return nil
}
