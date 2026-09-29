package json

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// NDJSONToken is the format token for line-delimited JSON input.
const NDJSONToken = "ndjson"

func init() {
	docnode.Register(NDJSONFormat{})
}

// NDJSONFormat is line-delimited JSON input: one JSON object per line, each
// line one record. It has no whole-document route; every input is scanned
// record by record. A record is the element named by the record selector,
// holding the line's object, exactly as one item of a keyed array.
type NDJSONFormat struct{}

// Token returns "ndjson".
func (NDJSONFormat) Token() string { return NDJSONToken }

// Parse reports that ndjson input has no whole-document route.
func (NDJSONFormat) Parse(io.Reader) (docnode.Document, error) {
	return nil, fmt.Errorf("ndjson: line-delimited input has no whole-document route; it is always read record by record: %w", docnode.ErrRouteUnsupported)
}

// ParseRecord parses one scanned record, as the json format does.
func (NDJSONFormat) ParseRecord(rec *docnode.Record) (docnode.Document, error) {
	return parseRecord(rec)
}

// NewScanner scans r line by line. Each line that is not whitespace-only is
// one record named by selector ("Name" or "//Name").
func (NDJSONFormat) NewScanner(r io.Reader, selector string, sizeOnly bool) (docnode.RecordScanner, error) {
	parsed, err := streaming.ParseRecordSelector(selector)
	if err != nil {
		return nil, err
	}
	return &lineScanner{vr: newValidatingReader(r), name: parsed.ElementName, sizeOnly: sizeOnly}, nil
}

// RecordDocument returns a document holding a copy of the element n, as the
// json format does.
func (NDJSONFormat) RecordDocument(n docnode.Node) (docnode.Document, error) {
	return recordDocument(n)
}

// NodeOf maps an iterator position to its node, as the json format does.
func (NDJSONFormat) NodeOf(nav xpath.NodeNavigator) (docnode.Node, bool) {
	return Format{}.NodeOf(nav)
}

var errNoValue = errors.New("ndjson: input contains no JSON value")

// lineScanner yields one record per non-blank line. Offsets are file-relative
// and count a leading byte order mark; a record's range is its value's bytes,
// never the surrounding whitespace or the line terminator.
type lineScanner struct {
	vr       *validatingReader
	br       *bufio.Reader
	name     string
	sizeOnly bool

	started bool
	off     int64 // file offset of the next line
	line    []byte
	count   int
	err     error
}

func (s *lineScanner) Next() (*docnode.Record, error) {
	if s.err != nil {
		return nil, s.err
	}
	rec, err := s.next()
	if err != nil {
		s.err = err
		return nil, err
	}
	return rec, nil
}

func (s *lineScanner) next() (*docnode.Record, error) {
	if !s.started {
		s.started = true
		if err := s.vr.start(); err != nil {
			return nil, err
		}
		s.off = s.vr.off
		s.br = bufio.NewReader(s.vr)
	}
	for {
		line, readErr := s.readLine()
		start := s.off
		s.off += int64(len(line))
		if readErr != nil && readErr != io.EOF {
			// The reader handed on every byte before the fault. A fault in
			// those bytes comes first in byte order; a line cut short by the
			// fault is not one.
			if err := s.checkPartial(line, start); err != nil {
				return nil, err
			}
			return nil, readErr
		}
		first, last := valueBounds(line)
		if first < last {
			return s.record(line[first:last], start+int64(first))
		}
		if readErr == io.EOF {
			if s.count == 0 {
				return nil, errNoValue
			}
			return nil, io.EOF
		}
	}
}

// record validates one line's value and returns it as the next record.
func (s *lineScanner) record(value []byte, at int64) (*docnode.Record, error) {
	num := s.count + 1
	if value[0] != '{' {
		return nil, fmt.Errorf("ndjson: record %d at byte offset %d is not a JSON object; each line must hold one object", num, at)
	}
	if err := walk(newValidatingReaderAt(bytes.NewReader(value), at), discard{}); err != nil {
		return nil, fmt.Errorf("ndjson: record %d: %w", num, err)
	}
	s.count = num
	rec := &docnode.Record{
		Num:         num,
		StartOffset: at,
		EndOffset:   at + int64(len(value)),
		SizeBytes:   int64(len(value)),
		Name:        s.name,
		Depth:       1,
	}
	if !s.sizeOnly {
		rec.Raw = append([]byte(nil), value...)
	}
	return rec, nil
}

// checkPartial reports a fault in the bytes of a line that precede a read
// fault. Running out of bytes is not one.
func (s *lineScanner) checkPartial(line []byte, start int64) error {
	first, last := valueBounds(line)
	if first >= last {
		return nil
	}
	value := line[first:last]
	if value[0] != '{' {
		return fmt.Errorf("ndjson: record %d at byte offset %d is not a JSON object; each line must hold one object", s.count+1, start+int64(first))
	}
	err := walk(newValidatingReaderAt(bytes.NewReader(value), start+int64(first)), discard{})
	if err == nil || errors.Is(err, errTruncated) {
		return nil
	}
	return fmt.Errorf("ndjson: record %d: %w", s.count+1, err)
}

// readLine returns the next line including its terminator, or what remains
// before EOF or a read fault.
func (s *lineScanner) readLine() ([]byte, error) {
	s.line = s.line[:0]
	for {
		chunk, err := s.br.ReadSlice('\n')
		s.line = append(s.line, chunk...)
		if err != bufio.ErrBufferFull {
			return s.line, err
		}
	}
}

// valueBounds returns the span of line between its leading and trailing JSON
// whitespace; first >= last for a whitespace-only line.
func valueBounds(line []byte) (int, int) {
	first, last := 0, len(line)
	for first < last && isSpace(line[first]) {
		first++
	}
	for last > first && isSpace(line[last-1]) {
		last--
	}
	return first, last
}

func (s *lineScanner) RecordCount() int { return s.count }

// BytesRead returns the raw bytes consumed from the input so far, byte order
// mark included.
func (s *lineScanner) BytesRead() int64 { return s.vr.off }

func (s *lineScanner) Close() error { return nil }
