//go:build cgo && seekablezstd

package store

import (
	"fmt"

	seekable "github.com/3leaps/seekable-zstd/bindings/go"
	"github.com/fulmenhq/sumpter/internal/index"
)

type seekableBinaryRecordStream struct {
	encoder *seekable.Encoder
	closed  bool
}

func newBinaryRecordStream(path string) (binaryRecordStream, error) {
	encoder, err := seekable.NewEncoder(path, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to create seekable encoder: %w", err)
	}
	return &seekableBinaryRecordStream{encoder: encoder}, nil
}

func (s *seekableBinaryRecordStream) AppendRecord(record *index.RecordMetadata) error {
	if s.closed {
		return fmt.Errorf("seekable encoder already closed")
	}
	buf := make([]byte, BinaryRecordWidth)
	if err := EncodeBinaryRecord(buf, record); err != nil {
		return err
	}
	if _, err := s.encoder.Write(buf); err != nil {
		return err
	}
	return nil
}

func (s *seekableBinaryRecordStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	compressedSize, err := s.encoder.Finish()
	if err != nil {
		return fmt.Errorf("failed to finish encoder: %w", err)
	}

	_ = compressedSize // Could log this if desired

	return nil
}
