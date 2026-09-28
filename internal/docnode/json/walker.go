package json

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
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
	errTruncated   = errors.New("json: unexpected end of JSON input")
	errInvalidUTF8 = errors.New("json: invalid UTF-8 in input")
	errEmptyInput  = errors.New("json: empty input")
	errTopLevel    = errors.New("json: top-level value must be an object or array")
)

// byteSource gives the walker read-back access to input bytes for error
// offsets. It may not hold every byte.
type byteSource interface {
	byteAt(off int64) (byte, bool)
}

// keySpanSource is implemented by sources that track string delimiters as
// bytes stream through, so a key's opening quote is known exactly however
// long the key is.
type keySpanSource interface {
	// keyOpen returns the opening-quote offset of the string whose closing
	// quote is at close.
	keyOpen(close int64) (int64, bool)
	// prune drops tracking for strings that closed before off.
	prune(off int64)
}

type sliceSource []byte

func (s sliceSource) byteAt(off int64) (byte, bool) {
	if off < 0 || off >= int64(len(s)) {
		return 0, false
	}
	return s[off], true
}

// walker enforces every structural guard and reports elements to a Handler.
type walker struct {
	dec   *stdjson.Decoder
	src   byteSource
	h     Handler
	depth int
}

// Walk streams one JSON document from r, applies the same guards as Parse,
// and reports its elements to h. It holds at most a bounded window of the
// input in memory.
func Walk(r io.Reader, h Handler) error {
	vr := newValidatingReader(r)
	return walk(vr, vr, h)
}

func walk(r io.Reader, src byteSource, h Handler) error {
	dec := stdjson.NewDecoder(r)
	dec.UseNumber()
	w := &walker{dec: dec, src: src, h: h}

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

	end := dec.InputOffset()
	switch _, err := dec.Token(); {
	case err == io.EOF:
		return nil
	case errors.Is(err, errInvalidUTF8):
		return err
	case err != nil:
		return fmt.Errorf("json: unexpected data after top-level value: %w", err)
	default:
		return fmt.Errorf("json: unexpected data after top-level value at byte offset %d", w.skipSpace(end))
	}
}

// token reads the next token inside an open container.
func (w *walker) token() (stdjson.Token, error) {
	if ks, ok := w.src.(keySpanSource); ok {
		ks.prune(w.dec.InputOffset())
	}
	tok, err := w.dec.Token()
	if err != nil {
		return nil, w.wrap(err)
	}
	return tok, nil
}

func (w *walker) wrap(err error) error {
	if errors.Is(err, errInvalidUTF8) {
		return errInvalidUTF8
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errTruncated
	}
	return fmt.Errorf("json: %w", err)
}

func (w *walker) enter() error {
	w.depth++
	if w.depth > MaxDepth {
		return fmt.Errorf("json: nesting depth exceeds %d at byte offset %d", MaxDepth, w.dec.InputOffset()-1)
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
			return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, w.dec.InputOffset())
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("json: duplicate key %q at byte offset %d", key, w.keyStart())
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
		return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, w.dec.InputOffset())
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
// read.
func (w *walker) keyStart() int64 {
	end := w.dec.InputOffset() - 1 // closing quote
	if ks, ok := w.src.(keySpanSource); ok {
		if open, found := ks.keyOpen(end); found {
			return open
		}
	}
	for i := end - 1; i >= 0; i-- {
		b, ok := w.src.byteAt(i)
		if !ok {
			return end
		}
		if b != '"' {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0; j-- {
			c, ok := w.src.byteAt(j)
			if !ok || c != '\\' {
				break
			}
			bs++
		}
		if bs%2 == 0 {
			return i
		}
	}
	return end
}

// skipSpace returns the offset of the first non-whitespace byte at or after
// off that the source holds.
func (w *walker) skipSpace(off int64) int64 {
	for {
		b, ok := w.src.byteAt(off)
		if !ok || !isSpace(b) {
			return off
		}
		off++
	}
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// windowSize bounds the read-back window a streaming walk keeps for error
// offsets.
const windowSize = 64 << 10

// validatingReader rejects invalid UTF-8 as bytes stream through and keeps a
// bounded window of recent input for error offsets.
type validatingReader struct {
	r       io.Reader
	carry   []byte // incomplete rune from the previous read
	window  []byte
	winBase int64 // offset of window[0]
	err     error

	// String-delimiter tracking over the bytes handed to the decoder: spans
	// holds strings the decoder may not have consumed yet, in order.
	pos     int64 // offset of the next byte handed out
	inStr   bool
	escaped bool
	strOpen int64
	spans   []strSpan
}

type strSpan struct{ open, close int64 }

func newValidatingReader(r io.Reader) *validatingReader {
	return &validatingReader{r: r}
}

func (v *validatingReader) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.r.Read(p)
	if n > 0 {
		v.remember(p[:n])
		if verr := v.check(p[:n], err == io.EOF); verr != nil {
			v.err = verr
			return 0, verr
		}
		v.track(p[:n])
	}
	if err == io.EOF && len(v.carry) > 0 {
		v.err = errInvalidUTF8
		return n, v.err
	}
	return n, err
}

// check validates chunk, carrying an incomplete trailing rune to the next
// read.
func (v *validatingReader) check(chunk []byte, final bool) error {
	buf := chunk
	if len(v.carry) > 0 {
		buf = append(append([]byte{}, v.carry...), chunk...)
		v.carry = v.carry[:0]
	}
	for len(buf) > 0 {
		if buf[0] < utf8.RuneSelf {
			buf = buf[1:]
			continue
		}
		if !utf8.FullRune(buf) {
			if final {
				return errInvalidUTF8
			}
			v.carry = append(v.carry, buf...)
			return nil
		}
		r, size := utf8.DecodeRune(buf)
		if r == utf8.RuneError && size <= 1 {
			return errInvalidUTF8
		}
		buf = buf[size:]
	}
	return nil
}

func (v *validatingReader) remember(chunk []byte) {
	v.window = append(v.window, chunk...)
	if over := len(v.window) - windowSize; over > 0 {
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

// track records the offsets of string delimiters in chunk, the next bytes the
// decoder will see.
func (v *validatingReader) track(chunk []byte) {
	for i, b := range chunk {
		switch {
		case !v.inStr:
			if b == '"' {
				v.inStr = true
				v.strOpen = v.pos + int64(i)
			}
		case v.escaped:
			v.escaped = false
		case b == '\\':
			v.escaped = true
		case b == '"':
			v.inStr = false
			v.spans = append(v.spans, strSpan{open: v.strOpen, close: v.pos + int64(i)})
		}
	}
	v.pos += int64(len(chunk))
}

func (v *validatingReader) keyOpen(close int64) (int64, bool) {
	i := sort.Search(len(v.spans), func(i int) bool { return v.spans[i].close >= close })
	if i < len(v.spans) && v.spans[i].close == close {
		return v.spans[i].open, true
	}
	return 0, false
}

func (v *validatingReader) prune(off int64) {
	i := sort.Search(len(v.spans), func(i int) bool { return v.spans[i].close >= off })
	if i > 0 {
		v.spans = append(v.spans[:0], v.spans[i:]...)
	}
}
