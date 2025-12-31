//go:build !cgo || !seekablezstd

package store

import (
	"github.com/fulmenhq/sumpter/internal/index"
)

// writeBinaryRecordsImpl is the stub implementation when CGO/seekablezstd is not available.
func writeBinaryRecordsImpl(_ string, _ []index.RecordMetadata) error {
	return ErrSeekableZstdNotAvailable
}
