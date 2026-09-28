package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// MaxDepth is the deepest container nesting Parse accepts.
const MaxDepth = 1024

var errTruncated = errors.New("json: unexpected end of JSON input")

type parser struct {
	data  []byte
	dec   *stdjson.Decoder
	depth int
}

// parse builds the tree for one JSON document. It never returns a partial
// tree: any error discards it.
func parse(data []byte) (*jnode, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("json: invalid UTF-8 in input")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("json: empty input")
	}
	dec := stdjson.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	p := &parser{data: data, dec: dec}

	tok, err := dec.Token()
	if err != nil {
		return nil, p.wrap(err)
	}
	root := &jnode{typ: rootNode}
	delim, ok := tok.(stdjson.Delim)
	if !ok {
		return nil, errors.New("json: top-level value must be an object or array")
	}
	if err := p.enter(); err != nil {
		return nil, err
	}
	if delim == '{' {
		err = p.parseObject(root)
	} else {
		err = p.parseArray(root, ItemName)
	}
	if err != nil {
		return nil, err
	}
	p.depth--

	end := p.dec.InputOffset()
	switch _, err := dec.Token(); {
	case err == io.EOF:
		return root, nil
	case err != nil:
		return nil, fmt.Errorf("json: unexpected data after top-level value: %w", err)
	default:
		return nil, fmt.Errorf("json: unexpected data after top-level value at byte offset %d", p.skipSpace(end))
	}
}

// token reads the next token inside an open container.
func (p *parser) token() (stdjson.Token, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return nil, p.wrap(err)
	}
	return tok, nil
}

func (p *parser) wrap(err error) error {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return errTruncated
	}
	return fmt.Errorf("json: %w", err)
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > MaxDepth {
		return fmt.Errorf("json: nesting depth exceeds %d at byte offset %d", MaxDepth, p.dec.InputOffset()-1)
	}
	return nil
}

// parseObject appends the members of the object just opened to target.
func (p *parser) parseObject(target *jnode) error {
	seen := make(map[string]struct{})
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}
		if tok == stdjson.Delim('}') {
			return nil
		}
		key, ok := tok.(string)
		if !ok {
			return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, p.dec.InputOffset())
		}
		if _, dup := seen[key]; dup {
			return fmt.Errorf("json: duplicate key %q at byte offset %d", key, p.keyStart())
		}
		seen[key] = struct{}{}
		tok, err = p.token()
		if err != nil {
			return err
		}
		if err := p.parseValue(target, key, tok, false); err != nil {
			return err
		}
	}
}

// parseArray appends one element named name per item of the array just
// opened to parent.
func (p *parser) parseArray(parent *jnode, name string) error {
	for {
		tok, err := p.token()
		if err != nil {
			return err
		}
		if tok == stdjson.Delim(']') {
			return nil
		}
		if err := p.parseValue(parent, name, tok, true); err != nil {
			return err
		}
	}
}

// parseValue adds the value starting at tok under parent as element name. A
// member's array value repeats name per item under parent; an array item that
// is itself an array becomes one element holding its own repeated items.
func (p *parser) parseValue(parent *jnode, name string, tok stdjson.Token, inArray bool) error {
	switch v := tok.(type) {
	case stdjson.Delim:
		if err := p.enter(); err != nil {
			return err
		}
		var err error
		switch {
		case v == '{':
			el := &jnode{typ: elementNode, name: name}
			parent.appendChild(el)
			err = p.parseObject(el)
		case inArray:
			el := &jnode{typ: elementNode, name: name}
			parent.appendChild(el)
			err = p.parseArray(el, name)
		default:
			err = p.parseArray(parent, name)
		}
		p.depth--
		return err
	case string:
		addScalar(parent, name, docnode.ScalarString, v)
	case stdjson.Number:
		addScalar(parent, name, docnode.ScalarNumber, v.String())
	case bool:
		s := "false"
		if v {
			s = "true"
		}
		addScalar(parent, name, docnode.ScalarBool, s)
	case nil:
		parent.appendChild(&jnode{typ: elementNode, name: name, scalar: true, kind: docnode.ScalarNull})
	default:
		return fmt.Errorf("json: unexpected token %v at byte offset %d", tok, p.dec.InputOffset())
	}
	return nil
}

func addScalar(parent *jnode, name string, kind docnode.ScalarKind, value string) {
	el := &jnode{typ: elementNode, name: name, scalar: true, kind: kind}
	el.appendChild(&jnode{typ: textNode, value: value})
	parent.appendChild(el)
}

// keyStart returns the byte offset of the opening quote of the key token just
// read.
func (p *parser) keyStart() int64 {
	end := p.dec.InputOffset() - 1 // closing quote
	for i := end - 1; i >= 0; i-- {
		if p.data[i] != '"' {
			continue
		}
		bs := 0
		for j := i - 1; j >= 0 && p.data[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return i
		}
	}
	return end
}

// skipSpace returns the offset of the first non-whitespace byte at or after
// off.
func (p *parser) skipSpace(off int64) int64 {
	for off < int64(len(p.data)) && isSpace(p.data[off]) {
		off++
	}
	return off
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
