// Package reftable loads external reference tables once per extract run and serves
// two bounded, recipe-callable query patterns against them:
//
//   - membership (Pattern A): is a record field a member of the distinct set of one
//     named column? — backs the in_reference DSL function.
//   - key→value lookup (Pattern B): map a record field (key) to a value-column
//     entry — backs the lookup_reference DSL function.
//
// A table is loaded from a bounded source stream (csv/tsv/ndjson), projected down to
// only the declared column(s) — raw rows and unused columns are never retained —
// hashed, capped (max_rows fail-loud, max_bytes pre-read), and frozen. After the
// registry is built nothing reachable from it is mutated, so concurrent reads from
// the parallel extractor's workers are safe without locking (Go maps are safe for
// concurrent reads once all writes have completed). The package does no network or
// expression evaluation: it owns load/parse/hash/containment and exposes a small
// read-only lookup surface; the command layer feeds it source bytes and the DSL
// evaluator calls its lookups.
package reftable

import (
	"fmt"
	"sort"
)

// Mode is a reference table's query mode, fixed at load from the declared columns.
type Mode string

const (
	// ModeMembership is Pattern A: a distinct set of one named column.
	ModeMembership Mode = "membership"
	// ModeLookup is Pattern B: a key column mapped to a value column.
	ModeLookup Mode = "lookup"
)

// Format is a supported reference-table source format.
type Format string

const (
	FormatCSV    Format = "csv"
	FormatTSV    Format = "tsv"
	FormatNDJSON Format = "ndjson"
)

// SourceMetadata is the logical identity of a table's source recorded in
// provenance. It carries identity, never row values, signed URLs, or credentials.
type SourceMetadata struct {
	// LogicalURI is the workspace-relative local path or the logical s3:// URI.
	LogicalURI string
	// CredentialsHandle is the resolved logical handle name for a cloud source
	// (empty for local). Same name-only posture as FU-2 input/output handles.
	CredentialsHandle string
	// SizeBytes is the source byte size that was read (post-cap).
	SizeBytes int64
}

// Table is an immutable, loaded reference table. Construct only via Load; after
// that no field (including the maps) is mutated.
type Table struct {
	name          string
	mode          Mode
	format        Format
	membership    map[string]struct{} // Pattern A — projected single column
	lookup        map[string]string   // Pattern B — projected key→value
	source        SourceMetadata
	rowCount      int // physical source data rows read (the max_rows basis)
	contentSHA256 string
}

// Name returns the declared table name.
func (t *Table) Name() string { return t.name }

// Mode returns the table's query mode.
func (t *Table) Mode() Mode { return t.mode }

// Format returns the source format.
func (t *Table) Format() Format { return t.format }

// RowCount returns the number of physical source data rows read — the quantity
// max_rows caps. For a membership table this can exceed the distinct retained
// entries (duplicate source rows collapse in the set but each still counts toward
// max_rows); for a lookup table duplicate keys fail loud, so it equals the retained
// pairs. This is the count recorded in provenance.
func (t *Table) RowCount() int { return t.rowCount }

// ContentSHA256 returns the "sha256:"-prefixed hash of the raw source bytes.
func (t *Table) ContentSHA256() string { return t.contentSHA256 }

// Source returns the table's logical source metadata.
func (t *Table) Source() SourceMetadata { return t.source }

// Registry is the run-scoped, immutable set of loaded reference tables. It is built
// once before extraction begins and then read concurrently by every worker.
type Registry struct {
	tables map[string]*Table
}

// NewRegistry builds a registry from already-loaded tables, rejecting a duplicate
// table name. The caller must not mutate the tables afterward.
func NewRegistry(tables []*Table) (*Registry, error) {
	m := make(map[string]*Table, len(tables))
	for _, t := range tables {
		if t == nil {
			continue
		}
		if _, dup := m[t.name]; dup {
			return nil, fmt.Errorf("reference table %q is declared more than once", t.name)
		}
		m[t.name] = t
	}
	return &Registry{tables: m}, nil
}

// Declared reports whether a table name is present in the registry. Used by the
// pre-flight validation that rejects an unknown literal table name before any file
// is extracted.
func (r *Registry) Declared(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.tables[name]
	return ok
}

// Len reports the number of loaded tables.
func (r *Registry) Len() int {
	if r == nil {
		return 0
	}
	return len(r.tables)
}

// Tables returns the loaded tables sorted by name, for deterministic provenance.
func (r *Registry) Tables() []*Table {
	if r == nil {
		return nil
	}
	out := make([]*Table, 0, len(r.tables))
	for _, t := range r.tables {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// Contains implements the in_reference query (Pattern A). It reports whether key is
// a member of the table's membership column. A miss is (false, nil) — never an
// error. An unknown table, or a table that is not a membership table, is a loud
// error (caught at pre-flight in normal use, but defended here too).
func (r *Registry) Contains(table, key string) (bool, error) {
	t, ok := r.tables[table]
	if !ok {
		return false, fmt.Errorf("in_reference: reference table %q is not declared", table)
	}
	if t.mode != ModeMembership {
		return false, fmt.Errorf("in_reference: reference table %q is a key→value lookup table, not a membership table (declare a single membership column)", table)
	}
	_, found := t.membership[key]
	return found, nil
}

// Lookup implements the lookup_reference query (Pattern B). It returns the
// value-column entry for key and whether the key was present. A missing key is
// ("", false, nil) — the caller substitutes the declared default. An unknown table,
// or a table that is not a lookup table, is a loud error.
func (r *Registry) Lookup(table, key string) (string, bool, error) {
	t, ok := r.tables[table]
	if !ok {
		return "", false, fmt.Errorf("lookup_reference: reference table %q is not declared", table)
	}
	if t.mode != ModeLookup {
		return "", false, fmt.Errorf("lookup_reference: reference table %q is a membership table, not a key→value lookup table (declare key_column + value_column)", table)
	}
	v, found := t.lookup[key]
	return v, found, nil
}
