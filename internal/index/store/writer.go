// Package store provides index storage abstractions for record indexes.
//
// This file implements the binary writer for seekable-zstd record indexes.

package store

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/fulmenhq/sumpter/internal/index"
)

// BinaryRecordWidth is the fixed width of each record in the binary format.
// Layout (64 bytes total):
//   - start_offset: int64 (8 bytes)
//   - end_offset: int64 (8 bytes)
//   - size_bytes: int64 (8 bytes)
//   - depth: int32 (4 bytes)
//   - record_num: int32 (4 bytes)
//   - sha256: [32]byte (32 bytes, raw binary)
const BinaryRecordWidth = 64

const (
	maxInt64AsUint64 = uint64(1<<63 - 1)
	maxUint32AsInt64 = int64(1<<32 - 1)
)

// SzstStoreVersion is the version identifier for the seekable-zstd store format.
// This is distinct from the JSON schema version (record-index/v0.1.1) to allow
// independent evolution of the binary store format.
const SzstStoreVersion = "record-index-szst/v0.1.0"

// SzstIndexHeader is the header structure for seekable-zstd index stores.
// This is written to *.recordindex.header.json alongside the binary records.
type SzstIndexHeader struct {
	Version  string              `json:"version"`
	Source   index.SourceInfo    `json:"source"`
	Selector index.SelectorInfo  `json:"selector"`
	Summary  index.SummaryStats  `json:"summary"`
	Metadata index.IndexMetadata `json:"metadata"`
	Records  SzstRecordsMetadata `json:"records"`
}

// SzstRecordsMetadata describes the binary records file format.
type SzstRecordsMetadata struct {
	RecordCount      int    `json:"record_count"`
	RecordWidthBytes int    `json:"record_width_bytes"`
	SHAEncoding      string `json:"sha_encoding"` // "raw32" for binary SHA256
	Endianness       string `json:"endianness"`   // "little" for little-endian
	RecordsFile      string `json:"records_file"` // Relative path to .records.szst
}

// WriteSeekableIndex writes a record index in seekable-zstd format.
// This produces two files:
//   - <basePath>.recordindex.header.json: JSON header with metadata
//   - <basePath>.recordindex.records.szst: Binary records compressed with seekable-zstd
//
// The basePath should be without extension, e.g., "/path/to/clinvar" produces:
//   - /path/to/clinvar.recordindex.header.json
//   - /path/to/clinvar.recordindex.records.szst
func WriteSeekableIndex(basePath string, idx *index.RecordIndex) error {
	if idx == nil {
		return fmt.Errorf("record index is required")
	}
	normalized := *idx
	index.NormalizeRecordIndex(&normalized)

	headerPath := basePath + ".recordindex.header.json"
	recordsPath := basePath + ".recordindex.records.szst"

	// Write binary records first (so we can fail early if encoder unavailable)
	if err := writeBinaryRecords(recordsPath, normalized.Records); err != nil {
		return fmt.Errorf("failed to write binary records: %w", err)
	}

	// Create header
	header := SzstIndexHeader{
		Version:  SzstStoreVersion,
		Source:   normalized.Source,
		Selector: normalized.Selector,
		Summary:  normalized.Summary,
		Metadata: normalized.Metadata,
		Records: SzstRecordsMetadata{
			RecordCount:      len(normalized.Records),
			RecordWidthBytes: BinaryRecordWidth,
			SHAEncoding:      "raw32",
			Endianness:       "little",
			RecordsFile:      filepath.Base(recordsPath),
		},
	}

	// Write header JSON
	if err := writeHeaderJSON(headerPath, &header); err != nil {
		// Clean up records file on header write failure
		_ = os.Remove(recordsPath)
		return fmt.Errorf("failed to write header: %w", err)
	}

	return nil
}

// writeHeaderJSON writes the header to a JSON file.
func writeHeaderJSON(path string, header *SzstIndexHeader) error {
	// Create output directory if needed
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	file, err := os.Create(path) // #nosec G304 - path is constructed from user input
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	return encoder.Encode(header)
}

// writeBinaryRecords writes records to a seekable-zstd compressed file.
// This requires CGO and the seekablezstd build tag.
func writeBinaryRecords(path string, records []index.RecordMetadata) error {
	return writeBinaryRecordsImpl(path, records)
}

// EncodeBinaryRecord encodes a single record to a fixed-width binary buffer.
// The buffer must be at least BinaryRecordWidth bytes.
func EncodeBinaryRecord(buf []byte, rec *index.RecordMetadata) error {
	if len(buf) < BinaryRecordWidth {
		return fmt.Errorf("buffer too small: need %d, got %d", BinaryRecordWidth, len(buf))
	}
	if rec == nil {
		return fmt.Errorf("record is required")
	}

	startOffset, err := checkedUint64FromInt64("start_offset", rec.StartOffset)
	if err != nil {
		return err
	}
	endOffset, err := checkedUint64FromInt64("end_offset", rec.EndOffset)
	if err != nil {
		return err
	}
	sizeBytes, err := checkedUint64FromInt64("size_bytes", rec.SizeBytes)
	if err != nil {
		return err
	}
	depth, err := checkedUint32FromInt("depth", rec.Depth)
	if err != nil {
		return err
	}
	recordNum, err := checkedUint32FromInt("record_num", rec.RecordNum)
	if err != nil {
		return err
	}

	// Encode fields in little-endian order
	binary.LittleEndian.PutUint64(buf[0:8], startOffset)
	binary.LittleEndian.PutUint64(buf[8:16], endOffset)
	binary.LittleEndian.PutUint64(buf[16:24], sizeBytes)
	binary.LittleEndian.PutUint32(buf[24:28], depth)
	binary.LittleEndian.PutUint32(buf[28:32], recordNum)

	// Decode hex SHA256 to raw bytes
	sha256Bytes, err := hexToBytes32(rec.SHA256)
	if err != nil {
		return fmt.Errorf("invalid SHA256 for record %d: %w", rec.RecordNum, err)
	}
	copy(buf[32:64], sha256Bytes[:])

	return nil
}

// DecodeBinaryRecord decodes a fixed-width binary buffer to a record.
// The buffer must be at least BinaryRecordWidth bytes.
func DecodeBinaryRecord(buf []byte) (*index.RecordMetadata, error) {
	if len(buf) < BinaryRecordWidth {
		return nil, fmt.Errorf("buffer too small: need %d, got %d", BinaryRecordWidth, len(buf))
	}

	startOffset, err := checkedInt64FromUint64("start_offset", binary.LittleEndian.Uint64(buf[0:8]))
	if err != nil {
		return nil, err
	}
	endOffset, err := checkedInt64FromUint64("end_offset", binary.LittleEndian.Uint64(buf[8:16]))
	if err != nil {
		return nil, err
	}
	sizeBytes, err := checkedInt64FromUint64("size_bytes", binary.LittleEndian.Uint64(buf[16:24]))
	if err != nil {
		return nil, err
	}
	depth, err := checkedIntFromUint32("depth", binary.LittleEndian.Uint32(buf[24:28]))
	if err != nil {
		return nil, err
	}
	recordNum, err := checkedIntFromUint32("record_num", binary.LittleEndian.Uint32(buf[28:32]))
	if err != nil {
		return nil, err
	}

	rec := &index.RecordMetadata{
		StartOffset: startOffset,
		EndOffset:   endOffset,
		SizeBytes:   sizeBytes,
		Depth:       depth,
		RecordNum:   recordNum,
		SHA256:      bytesToHex(buf[32:64]),
	}

	return rec, nil
}

func checkedUint64FromInt64(field string, value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d", field, value)
	}
	return uint64(value), nil // #nosec G115 - non-negative int64 always fits uint64
}

func checkedUint32FromInt(field string, value int) (uint32, error) {
	if value < 0 {
		return 0, fmt.Errorf("%s must be non-negative, got %d", field, value)
	}
	if int64(value) > maxUint32AsInt64 {
		return 0, fmt.Errorf("%s exceeds uint32 max: %d", field, value)
	}
	return uint32(value), nil // #nosec G115 - value is checked against uint32 max above
}

func checkedInt64FromUint64(field string, value uint64) (int64, error) {
	if value > maxInt64AsUint64 {
		return 0, fmt.Errorf("%s exceeds int64 max: %d", field, value)
	}
	return int64(value), nil // #nosec G115 - value is checked against int64 max above
}

func checkedIntFromUint32(field string, value uint32) (int, error) {
	if strconv.IntSize == 32 && value > uint32(1<<31-1) {
		return 0, fmt.Errorf("%s exceeds int max: %d", field, value)
	}
	return int(value), nil // #nosec G115 - value fits int on this architecture
}

// hexToBytes32 converts a 64-character hex string to a 32-byte array.
func hexToBytes32(hexStr string) ([32]byte, error) {
	var result [32]byte

	hexStr = strings.TrimSpace(hexStr)
	if len(hexStr) != 64 {
		return result, fmt.Errorf("expected 64 hex chars, got %d", len(hexStr))
	}

	for i := 0; i < 32; i++ {
		b, err := hexByteToByte(hexStr[i*2], hexStr[i*2+1])
		if err != nil {
			return result, err
		}
		result[i] = b
	}

	return result, nil
}

// hexByteToByte converts two hex characters to a byte.
func hexByteToByte(hi, lo byte) (byte, error) {
	h, err := hexCharToNibble(hi)
	if err != nil {
		return 0, err
	}
	l, err := hexCharToNibble(lo)
	if err != nil {
		return 0, err
	}
	return (h << 4) | l, nil
}

// hexCharToNibble converts a hex character to its 4-bit value.
func hexCharToNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	default:
		return 0, fmt.Errorf("invalid hex char: %c", c)
	}
}

// bytesToHex converts a byte slice to a hex string.
func bytesToHex(b []byte) string {
	const hexChars = "0123456789abcdef"
	result := make([]byte, len(b)*2)
	for i, v := range b {
		result[i*2] = hexChars[v>>4]
		result[i*2+1] = hexChars[v&0x0f]
	}
	return string(result)
}

// DeriveSeekablePaths derives header and records paths from a base path or JSON path.
// If the input ends with .recordindex.json, it derives the base path.
// Returns (headerPath, recordsPath).
func DeriveSeekablePaths(path string) (string, string) {
	var basePath string

	if strings.HasSuffix(path, ".recordindex.json") {
		// Input is a JSON path, derive base
		basePath = strings.TrimSuffix(path, ".recordindex.json")
	} else if strings.HasSuffix(path, ".recordindex.header.json") {
		// Input is already a header path
		basePath = strings.TrimSuffix(path, ".recordindex.header.json")
	} else {
		// Input is a base path
		basePath = path
	}

	return basePath + ".recordindex.header.json", basePath + ".recordindex.records.szst"
}
