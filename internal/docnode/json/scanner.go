package json

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// recordScanner streams one JSON document and yields the elements the record
// selector picks as records, in document order: "//Name" picks every element
// named Name, and "Name" only those directly under the document node, as the
// same XPath does on the whole-document route. It applies the same guards as
// Parse and reports the same first fault. A record's range is the value's own
// bytes on the raw input; records may nest, and an outer record is yielded
// before the records inside it.
type recordScanner struct {
	vr       *validatingReader
	w        *walker
	name     string
	anywhere bool // "//Name": match at any depth, not only directly under the root
	sizeOnly bool

	started bool
	stack   []frame
	open    []*openRecord // records in start order, not yet yielded
	count   int
	err     error

	// outer is the raw copy of the last yielded record; a record nested in it
	// is yielded as a subslice rather than copied again.
	outer      []byte
	outerStart int64
}

// frame is one open container.
type frame struct {
	obj  bool
	name string // element name of an array's items
	seen map[string]struct{}
	rec  *openRecord // the record this container is the value of, if any
	// depth is the node-model depth of the elements this container holds: a
	// member's array adds no element of its own, so its items sit at the
	// depth of the member.
	depth int
}

type openRecord struct {
	num        int
	start, end int64
	depth      int
	done       bool
}

// discard is the Handler for a walk that only validates.
type discard struct{}

func (discard) StartElement(string, Kind) error { return nil }
func (discard) Text(string) error               { return nil }
func (discard) EndElement() error               { return nil }

func newRecordScanner(r io.Reader, selector string, sizeOnly bool) (*recordScanner, error) {
	parsed, err := streaming.ParseRecordSelector(selector)
	if err != nil {
		return nil, err
	}
	vr := newValidatingReader(r)
	vr.capture = &captureBuf{}
	return &recordScanner{vr: vr, name: parsed.ElementName, anywhere: strings.HasPrefix(parsed.Raw, "//"), sizeOnly: sizeOnly}, nil
}

// Next returns the next record, or io.EOF after the last one. An error is
// sticky.
func (s *recordScanner) Next() (*docnode.Record, error) {
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

func (s *recordScanner) next() (*docnode.Record, error) {
	if !s.started {
		if err := s.begin(); err != nil {
			return nil, err
		}
	}
	for {
		if rec := s.ready(); rec != nil {
			return rec, nil
		}
		if len(s.stack) == 0 {
			if err := s.finish(); err != nil {
				return nil, err
			}
			return nil, io.EOF
		}
		if err := s.step(); err != nil {
			return nil, err
		}
	}
}

// begin reads the top-level value's opening token.
func (s *recordScanner) begin() error {
	s.started = true
	if err := s.vr.start(); err != nil {
		return err
	}
	dec := stdjson.NewDecoder(s.vr)
	dec.UseNumber()
	s.w = &walker{dec: dec, vr: s.vr, h: discard{}, base: s.vr.off}
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return errEmptyInput
		}
		return s.w.wrap(err)
	}
	delim, ok := tok.(stdjson.Delim)
	if !ok {
		return errTopLevel
	}
	if err := s.w.enter(); err != nil {
		return err
	}
	s.stack = append(s.stack, frame{obj: delim == '{', name: ItemName, seen: seenFor(delim == '{'), depth: 1})
	return nil
}

// step consumes one member or array item, or one closing delimiter.
func (s *recordScanner) step() error {
	top := &s.stack[len(s.stack)-1]
	pre := s.w.offset()
	s.release(pre)
	tok, err := s.w.token()
	if err != nil {
		return err
	}
	name := top.name
	inArray := !top.obj
	if top.obj {
		if tok == stdjson.Delim('}') {
			s.pop()
			return nil
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, s.w.offset())
		}
		if _, dup := top.seen[key]; dup {
			start, err := s.w.keyStart()
			if err != nil {
				return err
			}
			return fmt.Errorf("json: duplicate key %s at byte offset %d", keyText(key), start)
		}
		top.seen[key] = struct{}{}
		name = key
	} else if tok == stdjson.Delim(']') {
		s.pop()
		return nil
	}
	if top.obj {
		pre = s.w.offset()
		if tok, err = s.w.token(); err != nil {
			return err
		}
	}
	return s.value(name, tok, inArray, pre)
}

// value handles the value starting at tok, named name. pre is the decoder
// offset before the token was read.
func (s *recordScanner) value(name string, tok stdjson.Token, inArray bool, pre int64) error {
	delim, isDelim := tok.(stdjson.Delim)
	if isDelim && delim != '{' && delim != '[' {
		return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, s.w.offset())
	}
	// A member whose value is an array is not itself an element: each of its
	// items is one element named by the key.
	memberArray := isDelim && delim == '[' && !inArray
	depth := s.stack[len(s.stack)-1].depth
	var rec *openRecord
	if name == s.name && !memberArray && (s.anywhere || depth == 1) {
		start, err := s.valueStart(pre)
		if err != nil {
			return err
		}
		s.count++
		rec = &openRecord{num: s.count, start: start, depth: depth}
		s.open = append(s.open, rec)
	}
	if !isDelim {
		if rec != nil {
			rec.end, rec.done = s.w.offset(), true
		}
		return nil
	}
	if err := s.w.enter(); err != nil {
		return err
	}
	childDepth := depth + 1
	if memberArray {
		childDepth = depth
	}
	s.stack = append(s.stack, frame{obj: delim == '{', name: name, seen: seenFor(delim == '{'), rec: rec, depth: childDepth})
	return nil
}

func (s *recordScanner) pop() {
	f := s.stack[len(s.stack)-1]
	s.stack = s.stack[:len(s.stack)-1]
	s.w.depth--
	if f.rec != nil {
		f.rec.end, f.rec.done = s.w.offset(), true
	}
}

// ready returns the first record in start order once it has closed.
func (s *recordScanner) ready() *docnode.Record {
	if len(s.open) == 0 || !s.open[0].done {
		return nil
	}
	o := s.open[0]
	s.open[0] = nil
	s.open = s.open[1:]
	rec := &docnode.Record{
		Num:         o.num,
		StartOffset: o.start,
		EndOffset:   o.end,
		SizeBytes:   o.end - o.start,
		Name:        s.name,
		Depth:       o.depth,
	}
	if !s.sizeOnly {
		if o.start >= s.outerStart && o.end <= s.outerStart+int64(len(s.outer)) && s.outer != nil {
			i, j := o.start-s.outerStart, o.end-s.outerStart
			rec.Raw = s.outer[i:j:j]
		} else {
			rec.Raw = s.vr.capture.slice(o.start, o.end)
			s.outer, s.outerStart = rec.Raw, o.start
		}
	}
	return rec
}

// release lets the capture drop bytes no open record needs. off is the
// decoder offset before the next token.
func (s *recordScanner) release(off int64) {
	if len(s.open) > 0 && s.open[0].start < off {
		off = s.open[0].start
	}
	s.vr.capture.discardBefore(off)
}

// valueStart returns the offset of the first byte of the value whose token
// was read from pre: only whitespace and separators lie between.
func (s *recordScanner) valueStart(pre int64) (int64, error) {
	for off := pre; ; off++ {
		b, ok := s.vr.capture.byteAt(off)
		if !ok {
			return 0, fmt.Errorf("json: internal error: no value start at or after byte offset %d", pre)
		}
		if !isSpace(b) && b != ',' && b != ':' {
			return off, nil
		}
	}
}

// finish checks that nothing but whitespace follows the top-level value.
func (s *recordScanner) finish() error {
	end := s.w.offset()
	switch _, err := s.w.dec.Token(); {
	case err == io.EOF:
		return nil
	case isEncodingError(err):
		return err
	case err != nil:
		return fmt.Errorf("json: unexpected data after top-level value: %w", err)
	default:
		return fmt.Errorf("json: unexpected data after top-level value at byte offset %d", s.w.skipSpace(end))
	}
}

func (s *recordScanner) RecordCount() int {
	return s.count - len(s.open)
}

// BytesRead returns the raw bytes consumed from the input so far, byte order
// mark included.
func (s *recordScanner) BytesRead() int64 {
	return s.vr.off
}

func (s *recordScanner) Close() error { return nil }

func seenFor(obj bool) map[string]struct{} {
	if obj {
		return make(map[string]struct{})
	}
	return nil
}

// captureBuf holds the bytes handed to the decoder from base on, so a
// record's raw bytes can be sliced out once it closes.
type captureBuf struct {
	buf  []byte
	base int64
}

func (c *captureBuf) write(p []byte, at int64) {
	if len(c.buf) == 0 {
		c.base = at
	}
	c.buf = append(c.buf, p...)
}

func (c *captureBuf) discardBefore(off int64) {
	n := off - c.base
	switch {
	case n <= 0:
	case n >= int64(len(c.buf)):
		c.buf = c.buf[:0]
		c.base = off
	default:
		c.buf = c.buf[n:]
		c.base = off
	}
}

func (c *captureBuf) byteAt(off int64) (byte, bool) {
	i := off - c.base
	if i < 0 || i >= int64(len(c.buf)) {
		return 0, false
	}
	return c.buf[i], true
}

// slice returns a copy of the bytes in [start, end).
func (c *captureBuf) slice(start, end int64) []byte {
	out := make([]byte, end-start)
	copy(out, c.buf[start-c.base:end-c.base])
	return out
}
