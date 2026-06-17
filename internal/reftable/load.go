package reftable

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DefaultMaxBytes is the fail-loud source-size cap applied when a table declares no
// max_bytes. A reference table is an explicit run-resource choice; this bounds the
// staged/read bytes so an oversized or unbounded source aborts rather than
// exhausting memory or staging disk.
const DefaultMaxBytes int64 = 100 << 20 // 100 MiB

// maxNDJSONLine bounds a single ndjson line so a pathological no-newline multi-GB
// "line" cannot force an unbounded buffer allocation before the byte cap fires.
const maxNDJSONLine = 8 << 20 // 8 MiB

// Spec is a resolved reference-table declaration ready to load. Membership (Pattern
// A) sets Column; lookup (Pattern B) sets KeyColumn + ValueColumn; they are mutually
// exclusive. The command layer fills Source/MaxBytes after resolving the source.
type Spec struct {
	Name        string
	Format      Format
	Header      bool   // csv/tsv only
	Column      string // Pattern A membership column
	KeyColumn   string // Pattern B key column
	ValueColumn string // Pattern B value column
	MaxRows     int    // required, fail-loud
	MaxBytes    int64  // fail-loud pre-read cap (DefaultMaxBytes if 0)
	Source      SourceMetadata
}

// Mode returns the query mode implied by the declared columns.
func (s Spec) Mode() Mode {
	if s.Column != "" {
		return ModeMembership
	}
	return ModeLookup
}

// Validate checks the static shape of a spec (columns, caps, format) independent of
// the source bytes — used at config validation / pre-flight before any I/O.
func (s Spec) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("reference table name is required")
	}
	switch s.Format {
	case FormatCSV, FormatTSV, FormatNDJSON:
	default:
		return fmt.Errorf("reference table %q: unsupported format %q (use csv, tsv, or ndjson)", s.Name, s.Format)
	}
	hasMembership := s.Column != ""
	hasLookup := s.KeyColumn != "" || s.ValueColumn != ""
	switch {
	case hasMembership && hasLookup:
		return fmt.Errorf("reference table %q: declare either a membership column or key_column+value_column, not both", s.Name)
	case hasMembership:
		// ok
	case hasLookup:
		if s.KeyColumn == "" || s.ValueColumn == "" {
			return fmt.Errorf("reference table %q: a key→value lookup table needs both key_column and value_column", s.Name)
		}
		if s.KeyColumn == s.ValueColumn {
			return fmt.Errorf("reference table %q: key_column and value_column must differ", s.Name)
		}
	default:
		return fmt.Errorf("reference table %q: declare a membership column or key_column+value_column", s.Name)
	}
	if s.MaxRows <= 0 {
		return fmt.Errorf("reference table %q: max_rows is required and must be positive", s.Name)
	}
	return nil
}

// effectiveMaxBytes returns the configured cap or the default.
func (s Spec) effectiveMaxBytes() int64 {
	if s.MaxBytes > 0 {
		return s.MaxBytes
	}
	return DefaultMaxBytes
}

// Load parses a bounded source stream into an immutable Table per spec. It enforces
// max_bytes while reading (so an oversized source aborts before the whole table is
// resident), hashes the raw bytes, enforces max_rows fail-loud, and projects only
// the declared column(s) — raw rows and unused columns are never retained. Load and
// parse errors carry the table name + row number but never cell contents.
func Load(spec Spec, r io.Reader) (*Table, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}

	hasher := sha256.New()
	capped := &cappedReader{r: io.TeeReader(r, hasher), limit: spec.effectiveMaxBytes(), name: spec.Name}

	t := &Table{
		name:   spec.Name,
		mode:   spec.Mode(),
		format: spec.Format,
		source: spec.Source,
	}
	if t.mode == ModeMembership {
		t.membership = make(map[string]struct{})
	} else {
		t.lookup = make(map[string]string)
	}

	var err error
	switch spec.Format {
	case FormatCSV:
		err = loadDelimited(t, spec, capped, ',')
	case FormatTSV:
		err = loadDelimited(t, spec, capped, '\t')
	case FormatNDJSON:
		err = loadNDJSON(t, spec, capped)
	default:
		return nil, fmt.Errorf("reference table %q: unsupported format %q", spec.Name, spec.Format)
	}
	if err != nil {
		return nil, err
	}

	// rowCount is the physical source-row count accumulated by the loader (the
	// max_rows basis), not the distinct retained cardinality.
	t.contentSHA256 = "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	t.source.SizeBytes = capped.read
	return t, nil
}

// countPhysicalRow records one physical source data row and enforces max_rows on the
// physical count, before the row is retained. Duplicate membership rows collapse in
// the set but still count here, so a many-duplicate source cannot bypass max_rows and
// stall until the byte cap fires. Fails loud past the cap (no partial load).
func (t *Table) countPhysicalRow(maxRows int) error {
	t.rowCount++
	if t.rowCount > maxRows {
		return overRows(t.name)
	}
	return nil
}

// addMembership inserts a membership value (Pattern A); duplicate source values
// collapse into the distinct set. The max_rows cap is enforced on physical rows by
// countPhysicalRow before this is called.
func (t *Table) addMembership(value string) {
	t.membership[value] = struct{}{}
}

// addLookup inserts a key→value pair (Pattern B); a duplicate key fails loud. The
// max_rows cap is enforced on physical rows by countPhysicalRow before this is
// called.
func (t *Table) addLookup(key, value string, row int) error {
	if _, dup := t.lookup[key]; dup {
		return fmt.Errorf("reference table %q: duplicate key at row %d (key→value tables must have unique keys; resolve the source ambiguity)", t.name, row)
	}
	t.lookup[key] = value
	return nil
}

func overRows(name string) error {
	return fmt.Errorf("reference table %q: exceeds max_rows (no partial load — raise max_rows or shrink the source)", name)
}
