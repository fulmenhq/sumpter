package index

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// RecordIndexStream provides streaming access to a JSON record index.
//
// It avoids decoding the full "records" array into memory, which is critical
// for very large indexes (millions of records).
//
// Close must be called to release the underlying file descriptor.
//
// This stream currently supports only uncompressed JSON index files.
// Compressed/alternate encodings will be handled by a higher-level index store.
//
// NOTE: The JSON index schema requires a top-level "records" array.
// If it is missing, the stream returns an error.

type RecordIndexStream struct {
	file *os.File
	dec  *json.Decoder

	header RecordIndex

	startedObject   bool
	inRecords       bool
	recordsDone     bool
	objectDone      bool
	recordsFieldSet bool
}

// OpenRecordIndexStream opens a JSON record index for streaming reads.
func OpenRecordIndexStream(path string) (*RecordIndexStream, error) {
	file, err := os.Open(path) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return nil, fmt.Errorf("open record index: %w", err)
	}

	s := &RecordIndexStream{
		file: file,
		dec:  json.NewDecoder(file),
	}

	if err := s.consumeObjectStart(); err != nil {
		_ = file.Close()
		return nil, err
	}

	return s, nil
}

// Header returns the index header information.
//
// Header parses forward until the records array begins, capturing any header
// fields encountered before "records".
func (s *RecordIndexStream) Header() (*RecordIndex, error) {
	if s.objectDone || s.inRecords {
		NormalizeRecordIndex(&s.header)
		return &s.header, nil
	}

	if err := s.parseUntilRecords(); err != nil {
		return nil, err
	}

	NormalizeRecordIndex(&s.header)
	return &s.header, nil
}

// NextRecord returns the next RecordMetadata entry.
//
// Returns io.EOF once the records array is fully consumed.
func (s *RecordIndexStream) NextRecord() (*RecordMetadata, error) {
	if s.objectDone {
		return nil, io.EOF
	}

	if !s.inRecords {
		if err := s.parseUntilRecords(); err != nil {
			return nil, err
		}
	}

	if s.recordsDone {
		return nil, io.EOF
	}

	if !s.dec.More() {
		endTok, err := s.dec.Token()
		if err != nil {
			return nil, fmt.Errorf("read records array end: %w", err)
		}
		if delim, ok := endTok.(json.Delim); !ok || delim != ']' {
			return nil, fmt.Errorf("invalid records array end token: %v", endTok)
		}

		s.recordsDone = true
		s.inRecords = false

		if err := s.parseRemainingObjectFields(); err != nil {
			return nil, err
		}

		return nil, io.EOF
	}

	var rec RecordMetadata
	if err := s.dec.Decode(&rec); err != nil {
		return nil, fmt.Errorf("decode record: %w", err)
	}

	return &rec, nil
}

// Close releases underlying resources.
func (s *RecordIndexStream) Close() error {
	if s.file == nil {
		return nil
	}
	err := s.file.Close()
	s.file = nil
	return err
}

func (s *RecordIndexStream) consumeObjectStart() error {
	if s.startedObject {
		return nil
	}

	tok, err := s.dec.Token()
	if err != nil {
		return fmt.Errorf("read index JSON start: %w", err)
	}

	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return fmt.Errorf("invalid index JSON start token: %v", tok)
	}

	s.startedObject = true
	return nil
}

func (s *RecordIndexStream) parseUntilRecords() error {
	if s.objectDone || s.inRecords {
		return nil
	}

	for {
		if !s.dec.More() {
			endTok, err := s.dec.Token()
			if err != nil {
				return fmt.Errorf("read index JSON end: %w", err)
			}
			if delim, ok := endTok.(json.Delim); !ok || delim != '}' {
				return fmt.Errorf("invalid index JSON end token: %v", endTok)
			}
			s.objectDone = true

			if !s.recordsFieldSet {
				return fmt.Errorf("invalid record index: missing records field")
			}

			return nil
		}

		keyTok, err := s.dec.Token()
		if err != nil {
			return fmt.Errorf("read index field key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("invalid index field key token: %v", keyTok)
		}

		if key == "records" {
			arrTok, err := s.dec.Token()
			if err != nil {
				return fmt.Errorf("read records array start: %w", err)
			}
			if delim, ok := arrTok.(json.Delim); !ok || delim != '[' {
				return fmt.Errorf("invalid records array start token: %v", arrTok)
			}

			s.inRecords = true
			s.recordsFieldSet = true
			return nil
		}

		if err := s.decodeFieldIntoHeader(key); err != nil {
			return err
		}
	}
}

func (s *RecordIndexStream) parseRemainingObjectFields() error {
	if s.objectDone {
		return nil
	}

	for {
		if !s.dec.More() {
			endTok, err := s.dec.Token()
			if err != nil {
				return fmt.Errorf("read index JSON end: %w", err)
			}
			if delim, ok := endTok.(json.Delim); !ok || delim != '}' {
				return fmt.Errorf("invalid index JSON end token: %v", endTok)
			}

			s.objectDone = true
			return nil
		}

		keyTok, err := s.dec.Token()
		if err != nil {
			return fmt.Errorf("read index field key: %w", err)
		}
		key, ok := keyTok.(string)
		if !ok {
			return fmt.Errorf("invalid index field key token: %v", keyTok)
		}

		if key == "records" {
			return fmt.Errorf("invalid record index: duplicate records field")
		}

		if err := s.decodeFieldIntoHeader(key); err != nil {
			return err
		}
	}
}

func (s *RecordIndexStream) decodeFieldIntoHeader(key string) error {
	switch key {
	case "version":
		return s.dec.Decode(&s.header.Version)
	case "source":
		return s.dec.Decode(&s.header.Source)
	case "selector":
		return s.dec.Decode(&s.header.Selector)
	case "summary":
		return s.dec.Decode(&s.header.Summary)
	case "metadata":
		return s.dec.Decode(&s.header.Metadata)
	default:
		// Forward-compatible: ignore unknown fields.
		var discard json.RawMessage
		return s.dec.Decode(&discard)
	}
}
