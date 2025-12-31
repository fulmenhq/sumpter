//go:build cgo && seekablezstd

package store

import (
	"fmt"

	seekable "github.com/3leaps/seekable-zstd/bindings/go"
	"github.com/fulmenhq/sumpter/internal/index"
)

// writeBinaryRecordsImpl writes records to a seekable-zstd compressed file.
// This implementation uses CGO and requires the seekablezstd build tag.
func writeBinaryRecordsImpl(path string, records []index.RecordMetadata) error {
	// Create encoder with default frame size (256KB)
	encoder, err := seekable.NewEncoder(path, 0)
	if err != nil {
		return fmt.Errorf("failed to create seekable encoder: %w", err)
	}

	// Write each record as fixed-width binary
	buf := make([]byte, BinaryRecordWidth)
	for i := range records {
		if err := EncodeBinaryRecord(buf, &records[i]); err != nil {
			_ = encoder.Close() // Abort on error
			return fmt.Errorf("failed to encode record %d: %w", i, err)
		}

		if _, err := encoder.Write(buf); err != nil {
			_ = encoder.Close()
			return fmt.Errorf("failed to write record %d: %w", i, err)
		}
	}

	// Finish and write seek table
	compressedSize, err := encoder.Finish()
	if err != nil {
		return fmt.Errorf("failed to finish encoder: %w", err)
	}

	_ = compressedSize // Could log this if desired

	return nil
}
