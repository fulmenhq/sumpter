package store

import (
	"context"
	"io"

	"github.com/fulmenhq/sumpter/internal/index"
)

// jsonStore wraps RecordIndexStream to implement IndexStore.
type jsonStore struct {
	stream *index.RecordIndexStream
	header *index.RecordIndex
}

// openJSONStore opens a JSON record index file for streaming access.
func openJSONStore(path string) (IndexStore, error) {
	stream, err := index.OpenRecordIndexStream(path)
	if err != nil {
		return nil, err
	}

	return &jsonStore{stream: stream}, nil
}

// Header returns the index header information.
func (s *jsonStore) Header() (*index.RecordIndex, error) {
	if s.header != nil {
		return s.header, nil
	}

	header, err := s.stream.Header()
	if err != nil {
		return nil, err
	}

	s.header = header
	return s.header, nil
}

// Records returns an iterator over all record metadata entries.
func (s *jsonStore) Records(ctx context.Context) (RecordIterator, error) {
	// Default to Background context if nil to prevent panic
	if ctx == nil {
		ctx = context.Background()
	}

	// Ensure header is loaded first (positions stream at records array)
	if _, err := s.Header(); err != nil {
		return nil, err
	}

	return &jsonRecordIterator{
		stream: s.stream,
		ctx:    ctx,
	}, nil
}

// Close releases resources.
func (s *jsonStore) Close() error {
	return s.stream.Close()
}

// jsonRecordIterator wraps RecordIndexStream for record iteration.
type jsonRecordIterator struct {
	stream *index.RecordIndexStream
	ctx    context.Context
}

// Next returns the next record metadata entry.
func (it *jsonRecordIterator) Next() (*index.RecordMetadata, error) {
	// Check context cancellation
	select {
	case <-it.ctx.Done():
		return nil, it.ctx.Err()
	default:
	}

	rec, err := it.stream.NextRecord()
	if err == io.EOF {
		return nil, io.EOF
	}
	if err != nil {
		return nil, err
	}

	return rec, nil
}

// Close is a no-op for JSON iterator (stream is owned by store).
func (it *jsonRecordIterator) Close() error {
	return nil
}
