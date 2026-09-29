package json

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"
)

// Kind is the kind of value an element holds.
type Kind uint8

// Element kinds. KindArray marks an array item that is itself an array.
const (
	KindObject Kind = iota
	KindArray
	KindString
	KindNumber
	KindBool
	KindNull
)

// Handler receives a document's elements in document order, as the node model
// defines them: a member whose value is an array produces one element per
// item, and an empty array produces none.
type Handler interface {
	// StartElement opens an element with the given local name and kind.
	StartElement(name string, kind Kind) error
	// Text carries a scalar element's string-value. Null elements get none.
	Text(value string) error
	EndElement() error
}

var (
	errTruncated  = errors.New("json: unexpected end of JSON input")
	errNotUTF8    = errors.New("json: JSON input must be UTF-8")
	errEmptyInput = errors.New("json: empty input")
	errTopLevel   = errors.New("json: top-level value must be an object or array")
)

// invalidUTF8Error reports the file-relative offset of the first byte of an
// invalid or incomplete UTF-8 sequence.
type invalidUTF8Error struct {
	offset int64
}

func (e *invalidUTF8Error) Error() string {
	return fmt.Sprintf("json: invalid UTF-8 at byte offset %d", e.offset)
}

// isEncodingError reports whether err came from the validating reader.
func isEncodingError(err error) bool {
	var u *invalidUTF8Error
	return errors.As(err, &u) || errors.Is(err, errNotUTF8)
}

// maxKeyText bounds how many bytes of a key an error message shows.
const maxKeyText = 64

// keyText quotes key for an error message. A key longer than maxKeyText
// bytes is cut on a rune boundary and followed by its length in bytes.
func keyText(key string) string {
	if len(key) <= maxKeyText {
		return strconv.Quote(key)
	}
	cut := maxKeyText
	for cut > 0 && !utf8.RuneStart(key[cut]) {
		cut--
	}
	return fmt.Sprintf("%s…(%d bytes)", strconv.Quote(key[:cut]), len(key))
}

// walker enforces every structural guard and reports elements to a Handler.
type walker struct {
	dec   *stdjson.Decoder
	vr    *validatingReader
	h     Handler
	depth int
	// base is added to decoder offsets to make them file-relative (a stripped
	// byte order mark shifts the decoder's stream).
	base int64
}

// Walk streams one JSON document from r, applies the same guards as Parse,
// and reports its elements to h. It holds at most a bounded window of the
// input in memory.
func Walk(r io.Reader, h Handler) error {
	return walk(newValidatingReader(r), h)
}

// walk reads through vr to EOF: a fault the reader defers past the last
// token is still reported.
func walk(vr *validatingReader, h Handler) error {
	if err := vr.start(); err != nil {
		return err
	}
	dec := stdjson.NewDecoder(vr)
	dec.UseNumber()
	w := &walker{dec: dec, vr: vr, h: h, base: vr.off}

	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return errEmptyInput
		}
		return w.wrap(err)
	}
	delim, ok := tok.(stdjson.Delim)
	if !ok {
		return errTopLevel
	}
	if err := w.enter(); err != nil {
		return err
	}
	if delim == '{' {
		err = w.object()
	} else {
		err = w.array(ItemName)
	}
	if err != nil {
		return err
	}
	w.depth--

	end := w.offset()
	switch _, err := dec.Token(); {
	case err == io.EOF:
		return nil
	case isEncodingError(err):
		return err
	case err != nil:
		return fmt.Errorf("json: unexpected data after top-level value: %w", err)
	default:
		return fmt.Errorf("json: unexpected data after top-level value at byte offset %d", w.skipSpace(end))
	}
}

// token reads the next token inside an open container.
func (w *walker) token() (stdjson.Token, error) {
	w.vr.prune(w.offset())
	tok, err := w.dec.Token()
	if err != nil {
		return nil, w.wrap(err)
	}
	return tok, nil
}

// offset is the decoder's position as a file-relative byte offset.
func (w *walker) offset() int64 {
	return w.dec.InputOffset() + w.base
}

func (w *walker) wrap(err error) error {
	if isEncodingError(err) {
		return err
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errTruncated
	}
	return fmt.Errorf("json: %w", err)
}

func (w *walker) enter() error {
	w.depth++
	if w.depth > MaxDepth {
		return fmt.Errorf("json: nesting depth exceeds %d at byte offset %d", MaxDepth, w.offset()-1)
	}
	return nil
}

// object reports the members of the object just opened.
func (w *walker) object() error {
	seen := make(map[string]struct{})
	for {
		tok, err := w.token()
		if err != nil {
			return err
		}
		if tok == stdjson.Delim('}') {
			return nil
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, w.offset())
		}
		if _, dup := seen[key]; dup {
			start, err := w.keyStart()
			if err != nil {
				return err
			}
			return fmt.Errorf("json: duplicate key %s at byte offset %d", keyText(key), start)
		}
		seen[key] = struct{}{}
		tok, err = w.token()
		if err != nil {
			return err
		}
		if err := w.value(key, tok, false); err != nil {
			return err
		}
	}
}

// array reports one element named name per item of the array just opened.
func (w *walker) array(name string) error {
	for {
		tok, err := w.token()
		if err != nil {
			return err
		}
		if tok == stdjson.Delim(']') {
			return nil
		}
		if err := w.value(name, tok, true); err != nil {
			return err
		}
	}
}

// value reports the value starting at tok as element name. A member's array
// value repeats name per item; an array item that is itself an array becomes
// one element holding its own repeated items.
func (w *walker) value(name string, tok stdjson.Token, inArray bool) error {
	switch v := tok.(type) {
	case stdjson.Delim:
		if err := w.enter(); err != nil {
			return err
		}
		var err error
		switch {
		case v == '{':
			if err = w.h.StartElement(name, KindObject); err == nil {
				if err = w.object(); err == nil {
					err = w.h.EndElement()
				}
			}
		case inArray:
			if err = w.h.StartElement(name, KindArray); err == nil {
				if err = w.array(name); err == nil {
					err = w.h.EndElement()
				}
			}
		default:
			err = w.array(name)
		}
		w.depth--
		return err
	case string:
		return w.scalar(name, KindString, v)
	case stdjson.Number:
		return w.scalar(name, KindNumber, v.String())
	case bool:
		s := "false"
		if v {
			s = "true"
		}
		return w.scalar(name, KindBool, s)
	case nil:
		if err := w.h.StartElement(name, KindNull); err != nil {
			return err
		}
		return w.h.EndElement()
	default:
		return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, w.offset())
	}
}

func (w *walker) scalar(name string, kind Kind, value string) error {
	if err := w.h.StartElement(name, kind); err != nil {
		return err
	}
	if err := w.h.Text(value); err != nil {
		return err
	}
	return w.h.EndElement()
}

// keyStart returns the byte offset of the opening quote of the key token just
// read. The reader records every string the decoder can have consumed, so a
// miss means the tracking is broken; it fails rather than guess an offset.
func (w *walker) keyStart() (int64, error) {
	end := w.offset() - 1 // closing quote
	if open, ok := w.vr.keyOpen(end); ok {
		return open, nil
	}
	return 0, fmt.Errorf("json: internal error: no string recorded ending at byte offset %d", end)
}

// skipSpace returns the offset of the first non-whitespace byte at or after
// off that the source holds.
func (w *walker) skipSpace(off int64) int64 {
	for {
		b, ok := w.vr.byteAt(off)
		if !ok || !isSpace(b) {
			return off
		}
		off++
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// windowSize is the read-back a walk keeps for error offsets; the window
// holds at least this much recent input and at most twice it.
const windowSize = 64 << 10

// fillSize is how much input the validating reader requests at a time.
const fillSize = 32 << 10

// validatingReader strips one leading UTF-8 byte order mark, refuses other
// encodings, and rejects invalid UTF-8 as bytes stream through. It hands the
// decoder only complete, valid UTF-8: bytes before a fault are handed on and
// the fault is returned on the next read, so the decoder meets faults in byte
// order. It keeps a bounded window of recent input for error offsets, and
// tracks the strings in the bytes it hands on so a key's opening quote is
// known exactly however long the key is.
type validatingReader struct {
	r       io.Reader
	started bool
	skipped int64 // bytes of a stripped byte order mark
	chunk   []byte

	buf     []byte // bytes read but not yet handed on
	off     int64  // file offset of buf[0]
	valid   int    // leading bytes of buf known to be valid UTF-8
	pending error  // fault to return once the bytes before it are handed on
	eof     bool
	err     error // sticky: a refused encoding or a read error

	window  []byte
	winBase int64 // file offset of window[0]

	// String tracking over the bytes handed on: spans holds, in order, the
	// strings the decoder may not have consumed yet.
	inStr   bool
	escaped bool
	strOpen int64
	spans   []strSpan

	// capture, when set, receives every byte handed on with its file offset.
	capture *captureBuf
}

type strSpan struct{ open, close int64 }

func newValidatingReader(r io.Reader) *validatingReader {
	return &validatingReader{r: r}
}

// newValidatingReaderAt reads bytes that start at file offset off in the
// middle of an input: no byte order mark is stripped or refused, and every
// offset it reports is file-relative.
func newValidatingReaderAt(r io.Reader, off int64) *validatingReader {
	return &validatingReader{r: r, started: true, off: off}
}

// start inspects the first bytes: it strips one UTF-8 byte order mark and
// refuses UTF-16 and UTF-32 input.
func (v *validatingReader) start() error {
	if v.started {
		return v.err
	}
	v.started = true
	for len(v.buf) < 4 && !v.eof && v.err == nil {
		v.fill()
	}
	if v.err != nil {
		return v.err
	}
	head := v.buf
	switch {
	case len(head) >= 3 && head[0] == 0xef && head[1] == 0xbb && head[2] == 0xbf:
		v.buf = v.buf[3:]
		v.off, v.skipped = 3, 3
	case len(head) >= 2 && (head[0] == 0xff && head[1] == 0xfe || head[0] == 0xfe && head[1] == 0xff):
		v.err = errNotUTF8 // UTF-16 or UTF-32LE byte order mark
	case len(head) >= 2 && (head[0] == 0 || head[1] == 0):
		v.err = errNotUTF8 // UTF-16 or UTF-32 without a byte order mark, or UTF-32BE's
	}
	return v.err
}

// fill reads more input into buf.
func (v *validatingReader) fill() {
	if v.chunk == nil {
		v.chunk = make([]byte, fillSize)
	}
	n, err := v.r.Read(v.chunk)
	if n > 0 {
		v.remember(v.chunk[:n], v.off+int64(len(v.buf)))
		v.buf = append(v.buf, v.chunk[:n]...)
	}
	switch {
	case err == io.EOF:
		v.eof = true
	case err != nil:
		v.err = err
	}
}

func (v *validatingReader) Read(p []byte) (int, error) {
	if err := v.start(); err != nil {
		return 0, err
	}
	for {
		v.scan()
		if v.valid > 0 {
			n := copy(p, v.buf[:v.valid])
			v.track(v.buf[:n], v.off)
			if v.capture != nil {
				v.capture.write(v.buf[:n], v.off)
			}
			v.buf = v.buf[n:]
			v.valid -= n
			v.off += int64(n)
			return n, nil
		}
		switch {
		case v.pending != nil:
			return 0, v.pending
		case v.err != nil:
			return 0, v.err
		case v.eof && len(v.buf) > 0:
			// Only an incomplete sequence remains.
			v.pending = &invalidUTF8Error{offset: v.off}
		case v.eof:
			return 0, io.EOF
		default:
			v.fill()
		}
	}
}

// scan extends valid over buf. It stops at an incomplete trailing sequence,
// which waits for more input, or at an invalid one, which becomes pending.
func (v *validatingReader) scan() {
	if v.pending != nil {
		return
	}
	b := v.buf
	i := v.valid
	for i < len(b) {
		if b[i] < utf8.RuneSelf {
			i++
			continue
		}
		if !utf8.FullRune(b[i:]) {
			break
		}
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			v.pending = &invalidUTF8Error{offset: v.off + int64(i)}
			break
		}
		i += size
	}
	v.valid = i
}

func (v *validatingReader) remember(chunk []byte, at int64) {
	if len(v.window) == 0 {
		v.winBase = at
	}
	v.window = append(v.window, chunk...)
	// Compact only at twice the window, so the copy is amortized over reads.
	if len(v.window) > 2*windowSize {
		over := len(v.window) - windowSize
		v.window = append(v.window[:0], v.window[over:]...)
		v.winBase += int64(over)
	}
}

func (v *validatingReader) byteAt(off int64) (byte, bool) {
	i := off - v.winBase
	if i < 0 || i >= int64(len(v.window)) {
		return 0, false
	}
	return v.window[i], true
}

// track records the strings in chunk, the next bytes the decoder will see,
// which start at file offset at.
func (v *validatingReader) track(chunk []byte, at int64) {
	for i, b := range chunk {
		switch {
		case !v.inStr:
			if b == '"' {
				v.inStr = true
				v.strOpen = at + int64(i)
			}
		case v.escaped:
			v.escaped = false
		case b == '\\':
			v.escaped = true
		case b == '"':
			v.inStr = false
			v.spans = append(v.spans, strSpan{open: v.strOpen, close: at + int64(i)})
		}
	}
}

// keyOpen returns the opening-quote offset of the string whose closing quote
// is at close.
func (v *validatingReader) keyOpen(close int64) (int64, bool) {
	i := sort.Search(len(v.spans), func(i int) bool { return v.spans[i].close >= close })
	if i < len(v.spans) && v.spans[i].close == close {
		return v.spans[i].open, true
	}
	return 0, false
}

// prune drops strings that closed before off, which the decoder has consumed.
func (v *validatingReader) prune(off int64) {
	i := sort.Search(len(v.spans), func(i int) bool { return v.spans[i].close >= off })
	if i > 0 {
		v.spans = append(v.spans[:0], v.spans[i:]...)
	}
}
