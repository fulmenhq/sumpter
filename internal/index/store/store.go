// Package store provides index storage abstractions for record indexes.
//
// This package defines interfaces for reading record index metadata and records
// from various storage backends (JSON, seekable-zstd binary, etc.).
//
// The primary goal is to decouple "how index bytes are stored" from "how
// extraction consumes record boundaries", enabling:
//   - Streaming access to records without loading full arrays into memory
//   - Compressed storage with parallel random access (via seekable-zstd)
//   - Transparent format detection based on file extension
package store

import (
	"context"
	"errors"

	"github.com/fulmenhq/sumpter/internal/index"
)

// ErrSeekableZstdNotAvailable is returned when a .szst store is requested
// but the binary was built without seekable-zstd support.
var ErrSeekableZstdNotAvailable = errors.New("seekable-zstd support not available: rebuild with CGO_ENABLED=1 and -tags seekablezstd")

// IndexStore provides access to record index data.
//
// Implementations may read from JSON files, seekable-zstd compressed binary
// stores, or other formats.
type IndexStore interface {
	// Header returns the index header information (source, selector, summary).
	// This should be cheap to call and not require loading all records.
	Header() (*index.RecordIndex, error)

	// Records returns an iterator over all record metadata entries.
	// The caller must call Close on the returned iterator when done.
	Records(ctx context.Context) (RecordIterator, error)

	// Close releases resources associated with the store.
	Close() error
}

// RecordIterator provides streaming access to record metadata.
type RecordIterator interface {
	// Next returns the next record metadata entry.
	// Returns io.EOF when all records have been consumed.
	Next() (*index.RecordMetadata, error)

	// Close releases resources associated with the iterator.
	Close() error
}

// Open opens an index store from the given path.
//
// The format is detected by file extension:
//   - *.recordindex.json: JSON format (streaming reader)
//   - *.recordindex.header.json: Seekable-zstd binary format (header + .records.szst)
//
// For seekable-zstd format, the binary must be built with CGO_ENABLED=1
// and the "seekablezstd" build tag. Otherwise, ErrSeekableZstdNotAvailable
// is returned.
func Open(path string) (IndexStore, error) {
	return openStore(path)
}

// openStore is the internal implementation, allowing build-tag switching.
// See store_json.go and store_szst.go / store_szst_stub.go.
func openStore(path string) (IndexStore, error) {
	// Detect format by extension
	if isSeekableZstdStore(path) {
		return openSeekableZstdStore(path)
	}

	// Default: JSON format
	return openJSONStore(path)
}

// isSeekableZstdStore returns true if path indicates a seekable-zstd store.
func isSeekableZstdStore(path string) bool {
	// Convention: *.recordindex.header.json indicates the header of a
	// seekable-zstd store (with companion *.recordindex.records.szst)
	const headerSuffix = ".recordindex.header.json"
	if len(path) >= len(headerSuffix) {
		return path[len(path)-len(headerSuffix):] == headerSuffix
	}
	return false
}
