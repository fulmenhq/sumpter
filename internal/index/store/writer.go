// Package store provides index storage abstractions for record indexes.
//
// This file implements the binary writer for seekable-zstd record indexes.

package store

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

	writer := NewSeekableIndexWriter(basePath)
	if err := writer.Start(&normalized); err != nil {
		return err
	}
	defer func() { _ = writer.Close() }()

	for i := range normalized.Records {
		if err := writer.AppendRecord(normalized.Records[i]); err != nil {
			return err
		}
	}

	if err := writer.Finalize(&normalized); err != nil {
		return err
	}

	return nil
}

// SeekableIndexWriter writes seekable-zstd record indexes progressively.
type SeekableIndexWriter struct {
	basePath      string
	headerPath    string
	recordsPath   string
	headerTmp     string
	recordsTmp    string
	headerBackup  string
	recordsBackup string
	stream        binaryRecordStream
	recordCount   int
	started       bool
	prepared      bool
	committed     bool
	completed     bool
	finalized     bool
	closed        bool
}

// NewSeekableIndexWriter returns a progressive seekable-zstd index writer.
func NewSeekableIndexWriter(basePath string) *SeekableIndexWriter {
	return &SeekableIndexWriter{basePath: basePath}
}

func (w *SeekableIndexWriter) Start(_ *index.RecordIndex) error {
	if w.started {
		return fmt.Errorf("seekable index writer already started")
	}

	w.headerPath, w.recordsPath = DeriveSeekablePaths(w.basePath)
	if err := os.MkdirAll(filepath.Dir(w.recordsPath), 0o750); err != nil {
		return fmt.Errorf("failed to create records directory: %w", err)
	}
	tmpPath, err := tempSiblingPath(w.recordsPath)
	if err != nil {
		return fmt.Errorf("failed to create temporary records path: %w", err)
	}
	w.recordsTmp = tmpPath

	stream, err := newBinaryRecordStream(w.recordsTmp)
	if err != nil {
		_ = os.Remove(w.recordsTmp)
		w.recordsTmp = ""
		return fmt.Errorf("failed to create binary record stream: %w", err)
	}

	w.stream = stream
	w.started = true
	return nil
}

func (w *SeekableIndexWriter) AppendRecord(record index.RecordMetadata) error {
	if !w.started {
		return fmt.Errorf("seekable index writer not started")
	}
	if w.finalized {
		return fmt.Errorf("seekable index writer already finalized")
	}
	if err := w.stream.AppendRecord(&record); err != nil {
		return fmt.Errorf("failed to write binary record %d: %w", record.RecordNum, err)
	}
	w.recordCount++
	return nil
}

func (w *SeekableIndexWriter) Prepare(idx *index.RecordIndex) error {
	if idx == nil {
		return fmt.Errorf("record index summary is required")
	}
	if !w.started {
		return fmt.Errorf("seekable index writer not started")
	}
	if w.prepared {
		return nil
	}

	if w.stream != nil {
		if err := w.stream.Close(); err != nil {
			return fmt.Errorf("failed to finish binary records: %w", err)
		}
		w.stream = nil
	}

	normalized := *idx
	index.NormalizeRecordIndex(&normalized)

	header := SzstIndexHeader{
		Version:  SzstStoreVersion,
		Source:   normalized.Source,
		Selector: normalized.Selector,
		Summary:  normalized.Summary,
		Metadata: normalized.Metadata,
		Records: SzstRecordsMetadata{
			RecordCount:      w.recordCount,
			RecordWidthBytes: BinaryRecordWidth,
			SHAEncoding:      "raw32",
			Endianness:       "little",
			RecordsFile:      filepath.Base(w.recordsPath),
		},
	}

	headerTmp, err := tempSiblingPath(w.headerPath)
	if err != nil {
		_ = os.Remove(w.recordsTmp)
		w.recordsTmp = ""
		return fmt.Errorf("failed to create temporary header path: %w", err)
	}
	w.headerTmp = headerTmp

	if err := writeHeaderJSON(w.headerTmp, &header); err != nil {
		_ = os.Remove(w.headerTmp)
		_ = os.Remove(w.recordsTmp)
		w.headerTmp = ""
		w.recordsTmp = ""
		return fmt.Errorf("failed to write header: %w", err)
	}

	w.prepared = true
	return nil
}

func (w *SeekableIndexWriter) Commit() error {
	if !w.prepared {
		return fmt.Errorf("seekable index writer not prepared")
	}
	if w.committed {
		return nil
	}

	recordsBackup, headerBackup, err := publishSeekablePair(w.recordsTmp, w.recordsPath, w.headerTmp, w.headerPath)
	if err != nil {
		return err
	}
	w.recordsBackup = recordsBackup
	w.headerBackup = headerBackup
	w.recordsTmp = ""
	w.headerTmp = ""
	w.committed = true
	return nil
}

func (w *SeekableIndexWriter) Complete() error {
	if !w.committed && w.prepared {
		return fmt.Errorf("seekable index writer not committed")
	}
	if w.completed {
		return nil
	}
	w.completed = true
	w.finalized = true
	if w.recordsBackup != "" {
		if err := os.Remove(w.recordsBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove seekable records backup: %w", err)
		}
		w.recordsBackup = ""
	}
	if w.headerBackup != "" {
		if err := os.Remove(w.headerBackup); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to remove seekable header backup: %w", err)
		}
		w.headerBackup = ""
	}
	return nil
}

func (w *SeekableIndexWriter) Finalize(idx *index.RecordIndex) error {
	if err := w.Prepare(idx); err != nil {
		return err
	}
	if err := w.Commit(); err != nil {
		return err
	}
	return w.Complete()
}

func (w *SeekableIndexWriter) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	var err error
	if w.stream != nil {
		err = w.stream.Close()
		w.stream = nil
	}
	if !w.completed {
		if w.committed {
			err = errors.Join(err,
				os.Remove(w.recordsPath),
				os.Remove(w.headerPath),
				restoreIfStaged(w.recordsBackup, w.recordsPath),
				restoreIfStaged(w.headerBackup, w.headerPath),
			)
			w.recordsBackup = ""
			w.headerBackup = ""
			w.committed = false
		}
		if w.headerTmp != "" {
			err = errors.Join(err, os.Remove(w.headerTmp))
			w.headerTmp = ""
		}
		if w.recordsTmp != "" {
			err = errors.Join(err, os.Remove(w.recordsTmp))
			w.recordsTmp = ""
		}
	}
	return err
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

func tempSiblingPath(finalPath string) (string, error) {
	dir := filepath.Dir(finalPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}
	file, err := os.CreateTemp(dir, filepath.Base(finalPath)+".tmp-*") // #nosec G304 - output directory is derived from user-provided path
	if err != nil {
		return "", err
	}
	path := file.Name()
	closeErr := file.Close()
	removeErr := os.Remove(path)
	return path, errors.Join(closeErr, removeErr)
}

func publishSeekablePair(recordsTmp, recordsFinal, headerTmp, headerFinal string) (string, string, error) {
	recordsBackup := backupPath(recordsFinal)
	headerBackup := backupPath(headerFinal)
	recordsBackedUp := false
	headerBackedUp := false

	if err := renameIfExists(recordsFinal, recordsBackup); err != nil {
		return "", "", fmt.Errorf("failed to stage existing records for replacement: %w", err)
	} else if err == nil && fileExists(recordsBackup) {
		recordsBackedUp = true
	}

	if err := renameIfExists(headerFinal, headerBackup); err != nil {
		if recordsBackedUp {
			_ = os.Rename(recordsBackup, recordsFinal)
		}
		return "", "", fmt.Errorf("failed to stage existing header for replacement: %w", err)
	} else if err == nil && fileExists(headerBackup) {
		headerBackedUp = true
	}

	if err := os.Rename(recordsTmp, recordsFinal); err != nil {
		restoreSeekableBackups(recordsBackedUp, recordsBackup, recordsFinal, headerBackedUp, headerBackup, headerFinal)
		return "", "", fmt.Errorf("failed to publish seekable records: %w", err)
	}
	if err := os.Rename(headerTmp, headerFinal); err != nil {
		_ = os.Remove(recordsFinal)
		restoreSeekableBackups(recordsBackedUp, recordsBackup, recordsFinal, headerBackedUp, headerBackup, headerFinal)
		return "", "", fmt.Errorf("failed to publish seekable header: %w", err)
	}

	if !recordsBackedUp {
		recordsBackup = ""
	}
	if !headerBackedUp {
		headerBackup = ""
	}
	return recordsBackup, headerBackup, nil
}

func backupPath(path string) string {
	return fmt.Sprintf("%s.bak-%d-%d", path, os.Getpid(), time.Now().UnixNano())
}

func renameIfExists(oldPath, newPath string) error {
	err := os.Rename(oldPath, newPath)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func restoreSeekableBackups(recordsBackedUp bool, recordsBackup, recordsFinal string, headerBackedUp bool, headerBackup, headerFinal string) {
	if recordsBackedUp {
		_ = os.Rename(recordsBackup, recordsFinal)
	}
	if headerBackedUp {
		_ = os.Rename(headerBackup, headerFinal)
	}
}

func restoreIfStaged(backupPath, finalPath string) error {
	if backupPath == "" {
		return nil
	}
	if err := os.Rename(backupPath, finalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type binaryRecordStream interface {
	AppendRecord(record *index.RecordMetadata) error
	Close() error
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
