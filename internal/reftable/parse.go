package reftable

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// cappedReader bounds the cumulative bytes read from a source so an oversized or
// unbounded stream aborts during the read, before the whole table is resident. The
// error fires as soon as the limit is crossed; it is the streaming half of the
// max_bytes pre-read cap (the command layer also prechecks a known size).
type cappedReader struct {
	r     io.Reader
	limit int64
	read  int64
	name  string
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.read > c.limit {
		return 0, overBytesError{name: c.name, limit: c.limit}
	}
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.read > c.limit {
		return n, overBytesError{name: c.name, limit: c.limit}
	}
	return n, err
}

type overBytesError struct {
	name  string
	limit int64
}

func (e overBytesError) Error() string {
	return fmt.Sprintf("reference table %q: source exceeds max_bytes (%d) — no partial load; raise max_bytes or shrink the source", e.name, e.limit)
}

// asOverBytes returns the byte-cap error if err wraps one, so loaders surface it
// directly rather than as a generic parse error.
func asOverBytes(err error) error {
	var obe overBytesError
	if errors.As(err, &obe) {
		return obe
	}
	return nil
}

// columnPositions holds resolved header positions for the declared columns.
type columnPositions struct {
	col   int // membership
	key   int // lookup key
	value int // lookup value
}

// resolveColumns maps the declared column name(s) to header positions, failing loud
// if any is absent.
func resolveColumns(spec Spec, header []string) (columnPositions, error) {
	index := make(map[string]int, len(header))
	for i, name := range header {
		if _, dup := index[name]; !dup { // first occurrence wins for lookup
			index[name] = i
		}
	}
	pos := columnPositions{col: -1, key: -1, value: -1}
	need := func(name string) (int, error) {
		i, ok := index[name]
		if !ok {
			return -1, fmt.Errorf("reference table %q: declared column %q is not present in the source header", spec.Name, name)
		}
		return i, nil
	}
	var err error
	if spec.Mode() == ModeMembership {
		if pos.col, err = need(spec.Column); err != nil {
			return pos, err
		}
	} else {
		if pos.key, err = need(spec.KeyColumn); err != nil {
			return pos, err
		}
		if pos.value, err = need(spec.ValueColumn); err != nil {
			return pos, err
		}
	}
	return pos, nil
}

// loadDelimited parses csv/tsv via encoding/csv. header: true is required (columns
// are referenced by name). csv.Reader enforces a uniform field count, so a wrong
// column count fails loud — and its error carries the row, never the cell contents.
func loadDelimited(t *Table, spec Spec, r io.Reader, comma rune) error {
	if !spec.Header {
		return fmt.Errorf("reference table %q: csv/tsv sources require header: true to reference columns by name", spec.Name)
	}
	cr := csv.NewReader(r)
	cr.Comma = comma
	cr.FieldsPerRecord = 0 // set from the header row, then enforced for every record

	header, err := cr.Read()
	if err != nil {
		if err == io.EOF {
			return nil // header-less empty stream → empty table (valid)
		}
		return parseError(spec.Name, 0, err)
	}
	pos, err := resolveColumns(spec, header)
	if err != nil {
		return err
	}

	row := 1 // header is row 1
	for {
		rec, rerr := cr.Read()
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return parseError(spec.Name, row+1, rerr)
		}
		row++
		if cerr := t.countPhysicalRow(spec.MaxRows); cerr != nil {
			return cerr
		}
		if t.mode == ModeMembership {
			t.addMembership(rec[pos.col])
			continue
		}
		if aerr := t.addLookup(rec[pos.key], rec[pos.value], row); aerr != nil {
			return aerr
		}
	}
	return nil
}

// loadNDJSON parses one JSON object per line. Columns name object fields; a declared
// field must be present and a JSON string. A bounded scanner buffer plus the byte
// cap stop a pathological no-newline line from forcing a huge allocation. Errors
// carry the line number, never the cell/line contents.
func loadNDJSON(t *Table, spec Spec, r io.Reader) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxNDJSONLine)

	row := 0
	for sc.Scan() {
		row++
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		if cerr := t.countPhysicalRow(spec.MaxRows); cerr != nil {
			return cerr
		}
		var obj map[string]json.RawMessage
		if jerr := json.Unmarshal(line, &obj); jerr != nil {
			return fmt.Errorf("reference table %q: malformed JSON object at line %d", spec.Name, row)
		}
		if t.mode == ModeMembership {
			v, ferr := ndjsonString(spec, obj, spec.Column, row)
			if ferr != nil {
				return ferr
			}
			t.addMembership(v)
			continue
		}
		k, ferr := ndjsonString(spec, obj, spec.KeyColumn, row)
		if ferr != nil {
			return ferr
		}
		v, ferr := ndjsonString(spec, obj, spec.ValueColumn, row)
		if ferr != nil {
			return ferr
		}
		if aerr := t.addLookup(k, v, row); aerr != nil {
			return aerr
		}
	}
	if serr := sc.Err(); serr != nil {
		if obe := asOverBytes(serr); obe != nil {
			return obe
		}
		if errors.Is(serr, bufio.ErrTooLong) {
			return fmt.Errorf("reference table %q: a line near %d exceeds the maximum line size (%d bytes)", spec.Name, row+1, maxNDJSONLine)
		}
		return parseError(spec.Name, row, serr)
	}
	return nil
}

// ndjsonString extracts a declared field as a JSON string, failing loud (no
// contents) if the field is absent or not a string.
func ndjsonString(spec Spec, obj map[string]json.RawMessage, field string, row int) (string, error) {
	raw, ok := obj[field]
	if !ok {
		return "", fmt.Errorf("reference table %q: line %d is missing the declared field %q", spec.Name, row, field)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("reference table %q: line %d field %q is not a JSON string", spec.Name, row, field)
	}
	return s, nil
}

// parseError wraps a loader error with table + row context, passing the byte-cap
// error through unchanged and never including cell contents.
func parseError(name string, row int, err error) error {
	if obe := asOverBytes(err); obe != nil {
		return obe
	}
	return fmt.Errorf("reference table %q: malformed source at row %d (wrong column count or unreadable record)", name, row)
}
