package json

import (
	"bytes"
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io"
)

// parseRecordValue builds the tree for one scanned record: a document node
// holding a single element named name whose value is raw. raw is the value's
// own bytes, which start at file offset at; errors report file-relative
// offsets. The element is built exactly as the whole-document route builds an
// array item named name, so the record's subtree equals the DOM's.
func parseRecordValue(raw []byte, name string, at int64) (*jnode, error) {
	vr := newValidatingReaderAt(bytes.NewReader(raw), at)
	dec := stdjson.NewDecoder(vr)
	dec.UseNumber()
	b := &treeBuilder{root: &jnode{typ: rootNode}}
	b.cur = b.root
	w := &walker{dec: dec, vr: vr, h: b, base: at}
	tok, err := dec.Token()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, errEmptyInput
		}
		return nil, w.wrap(err)
	}
	if err := w.value(name, tok, true); err != nil {
		return nil, err
	}
	end := w.offset()
	switch _, err := dec.Token(); {
	case err == io.EOF:
		return b.root, nil
	case isEncodingError(err):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("json: unexpected data after record value: %w", err)
	default:
		return nil, fmt.Errorf("json: unexpected data after record value at byte offset %d", w.skipSpace(end))
	}
}
