package reftable

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func membershipSpec(maxRows int) Spec {
	return Spec{Name: "curated", Format: FormatCSV, Header: true, Column: "accession", MaxRows: maxRows}
}

func lookupSpec(maxRows int) Spec {
	return Spec{Name: "mol", Format: FormatCSV, Header: true, KeyColumn: "accession", ValueColumn: "molecule_type", MaxRows: maxRows}
}

func mustLoad(t *testing.T, spec Spec, body string) *Table {
	t.Helper()
	tbl, err := Load(spec, strings.NewReader(body))
	if err != nil {
		t.Fatalf("Load(%s): %v", spec.Name, err)
	}
	return tbl
}

func TestLoadMembershipCSV(t *testing.T) {
	tbl := mustLoad(t, membershipSpec(100),
		"accession,release_date\nNM_000001,2026-01-15\nNR_000002,2026-01-15\nNM_000001,2026-02-01\n")
	if tbl.Mode() != ModeMembership {
		t.Fatalf("mode = %s, want membership", tbl.Mode())
	}
	// RowCount is physical source rows (3), the max_rows basis; the duplicate
	// NM_000001 still collapses in the distinct membership set queried below.
	if tbl.RowCount() != 3 {
		t.Errorf("row count = %d, want 3 (physical source rows)", tbl.RowCount())
	}
	reg, _ := NewRegistry([]*Table{tbl})
	for _, tc := range []struct {
		key  string
		want bool
	}{{"NM_000001", true}, {"NR_000002", true}, {"XR_999", false}} {
		got, err := reg.Contains("curated", tc.key)
		if err != nil {
			t.Fatalf("Contains(%q): %v", tc.key, err)
		}
		if got != tc.want {
			t.Errorf("Contains(%q) = %v, want %v", tc.key, got, tc.want)
		}
	}
	if !strings.HasPrefix(tbl.ContentSHA256(), "sha256:") {
		t.Errorf("content hash = %q, want sha256: prefix", tbl.ContentSHA256())
	}
}

func TestLoadTSV(t *testing.T) {
	spec := membershipSpec(100)
	spec.Format = FormatTSV
	tbl := mustLoad(t, spec, "accession\trelease_date\nNM_1\t2026-01-15\n")
	reg, _ := NewRegistry([]*Table{tbl})
	if ok, _ := reg.Contains("curated", "NM_1"); !ok {
		t.Error("tsv membership did not load NM_1")
	}
}

func TestLoadLookupCSV(t *testing.T) {
	tbl := mustLoad(t, lookupSpec(100),
		"accession,molecule_type\nNM_000001,mRNA\nNR_000002,ncRNA\n")
	reg, _ := NewRegistry([]*Table{tbl})
	if v, ok, _ := reg.Lookup("mol", "NM_000001"); !ok || v != "mRNA" {
		t.Errorf("Lookup(NM_000001) = %q,%v, want mRNA,true", v, ok)
	}
	if _, ok, _ := reg.Lookup("mol", "ABSENT"); ok {
		t.Error("Lookup(ABSENT) reported present")
	}
}

func TestLoadNDJSONLookupMatchesCSV(t *testing.T) {
	spec := lookupSpec(100)
	spec.Format = FormatNDJSON
	tbl := mustLoad(t, spec,
		`{"accession":"NM_000001","molecule_type":"mRNA"}`+"\n"+`{"accession":"NR_000002","molecule_type":"ncRNA"}`+"\n")
	reg, _ := NewRegistry([]*Table{tbl})
	if v, ok, _ := reg.Lookup("mol", "NR_000002"); !ok || v != "ncRNA" {
		t.Errorf("ndjson Lookup = %q,%v, want ncRNA,true", v, ok)
	}
}

func TestLoadDuplicateLookupKeyFailsLoud(t *testing.T) {
	_, err := Load(lookupSpec(100), strings.NewReader("accession,molecule_type\nNM_1,mRNA\nNM_1,ncRNA\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate key") {
		t.Fatalf("err = %v, want duplicate-key fail-loud", err)
	}
}

func TestLoadMaxRowsFailLoud(t *testing.T) {
	_, err := Load(membershipSpec(2), strings.NewReader("accession\nA\nB\nC\n"))
	if err == nil || !strings.Contains(err.Error(), "max_rows") {
		t.Fatalf("err = %v, want max_rows abort", err)
	}
}

// TestLoadMaxRowsCountsPhysicalRows guards the cap against a many-duplicate
// membership source: distinct entries stay at 1, but max_rows is enforced on the
// physical source rows so the source cannot bypass the cap and stall until max_bytes.
func TestLoadMaxRowsCountsPhysicalRows(t *testing.T) {
	// 3 physical data rows, all the same value (1 distinct), cap of 2.
	_, err := Load(membershipSpec(2), strings.NewReader("accession\nDUP\nDUP\nDUP\n"))
	if err == nil || !strings.Contains(err.Error(), "max_rows") {
		t.Fatalf("err = %v, want max_rows abort on physical rows despite a single distinct value", err)
	}
	// At the cap (2 physical rows of the same value) the load succeeds and reports
	// physical RowCount, not distinct cardinality.
	tbl := mustLoad(t, membershipSpec(2), "accession\nDUP\nDUP\n")
	if tbl.RowCount() != 2 {
		t.Errorf("row count = %d, want 2 physical rows", tbl.RowCount())
	}
}

func TestLoadMaxBytesFailLoudStreaming(t *testing.T) {
	spec := membershipSpec(1_000_000)
	spec.MaxBytes = 32 // tiny cap
	big := "accession\n" + strings.Repeat("NM_0000001\n", 100)
	_, err := Load(spec, strings.NewReader(big))
	if err == nil || !strings.Contains(err.Error(), "max_bytes") {
		t.Fatalf("err = %v, want max_bytes abort", err)
	}
}

func TestLoadWrongColumnCountFailsLoud(t *testing.T) {
	_, err := Load(membershipSpec(100), strings.NewReader("accession,release_date\nNM_1\n"))
	if err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Fatalf("err = %v, want wrong-column-count at row 2", err)
	}
}

func TestLoadBadJSONFailsLoud(t *testing.T) {
	spec := lookupSpec(100)
	spec.Format = FormatNDJSON
	_, err := Load(spec, strings.NewReader(`{"accession":"NM_1","molecule_type":"mRNA"}`+"\n{bad json\n"))
	if err == nil || !strings.Contains(err.Error(), "line 2") {
		t.Fatalf("err = %v, want malformed JSON at line 2", err)
	}
}

func TestLoadMissingColumnFailsLoud(t *testing.T) {
	_, err := Load(membershipSpec(100), strings.NewReader("not_accession\nX\n"))
	if err == nil || !strings.Contains(err.Error(), "accession") {
		t.Fatalf("err = %v, want missing declared column", err)
	}
}

// TestLoadErrorsCarryNoCellContents asserts the redaction discipline: load/parse
// errors reference the table name + row number, never the cell values.
func TestLoadErrorsCarryNoCellContents(t *testing.T) {
	secret := "TOPSECRETVALUE"
	_, err := Load(lookupSpec(100), strings.NewReader("accession,molecule_type\n"+secret+",mRNA\n"+secret+",ncRNA\n"))
	if err == nil {
		t.Fatal("expected duplicate-key error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error leaked a cell value: %v", err)
	}
}

func TestLoadEmptyTableValid(t *testing.T) {
	tbl := mustLoad(t, membershipSpec(100), "accession\n")
	reg, _ := NewRegistry([]*Table{tbl})
	if ok, _ := reg.Contains("curated", "anything"); ok {
		t.Error("empty membership table matched")
	}
	lt := mustLoad(t, lookupSpec(100), "accession,molecule_type\n")
	lreg, _ := NewRegistry([]*Table{lt})
	if _, ok, _ := lreg.Lookup("mol", "anything"); ok {
		t.Error("empty lookup table matched")
	}
}

func TestSpecValidate(t *testing.T) {
	cases := []struct {
		name    string
		spec    Spec
		wantErr string
	}{
		{"membership ok", membershipSpec(10), ""},
		{"lookup ok", lookupSpec(10), ""},
		{"both shapes", Spec{Name: "x", Format: FormatCSV, Column: "a", KeyColumn: "b", ValueColumn: "c", MaxRows: 1}, "not both"},
		{"lookup missing value", Spec{Name: "x", Format: FormatCSV, KeyColumn: "k", MaxRows: 1}, "both key_column and value_column"},
		{"no columns", Spec{Name: "x", Format: FormatCSV, MaxRows: 1}, "membership column or key_column"},
		{"bad format", Spec{Name: "x", Format: "parquet", Column: "a", MaxRows: 1}, "unsupported format"},
		{"no max_rows", Spec{Name: "x", Format: FormatCSV, Column: "a"}, "max_rows is required"},
		{"key==value", Spec{Name: "x", Format: FormatCSV, KeyColumn: "a", ValueColumn: "a", MaxRows: 1}, "must differ"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.spec.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestRegistryWrongModeAndUnknown(t *testing.T) {
	m := mustLoad(t, membershipSpec(10), "accession\nNM_1\n")
	l := mustLoad(t, lookupSpec(10), "accession,molecule_type\nNM_1,mRNA\n")
	reg, _ := NewRegistry([]*Table{m, l})

	if _, err := reg.Contains("mol", "NM_1"); err == nil || !strings.Contains(err.Error(), "lookup table") {
		t.Errorf("Contains on a lookup table: err = %v, want wrong-mode", err)
	}
	if _, _, err := reg.Lookup("curated", "NM_1"); err == nil || !strings.Contains(err.Error(), "membership table") {
		t.Errorf("Lookup on a membership table: err = %v, want wrong-mode", err)
	}
	if _, err := reg.Contains("nope", "x"); err == nil || !strings.Contains(err.Error(), "not declared") {
		t.Errorf("Contains unknown table: err = %v, want not-declared", err)
	}
	if !reg.Declared("curated") || reg.Declared("nope") {
		t.Error("Declared mismatch")
	}
	if len(reg.Tables()) != 2 || reg.Tables()[0].Name() != "curated" {
		t.Errorf("Tables() not sorted: %v", reg.Tables())
	}
}

func TestNewRegistryDuplicateName(t *testing.T) {
	a := mustLoad(t, membershipSpec(10), "accession\nA\n")
	b := mustLoad(t, membershipSpec(10), "accession\nB\n")
	if _, err := NewRegistry([]*Table{a, b}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatalf("err = %v, want duplicate-name", err)
	}
}

// --- C1 containment ---

func TestResolveLocalSourceContained(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "ref")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "t.csv"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveLocalSource(root, "ref/t.csv")
	if err != nil {
		t.Fatalf("contained path rejected: %v", err)
	}
	if !strings.HasSuffix(got, filepath.Join("ref", "t.csv")) {
		t.Errorf("resolved = %q", got)
	}
}

func TestResolveLocalSourceRejectsAbsoluteAndDotDot(t *testing.T) {
	root := t.TempDir()
	for _, src := range []string{"/etc/passwd", "../../secrets.csv", "ref/../../escape.csv"} {
		if _, err := ResolveLocalSource(root, src); err == nil {
			t.Errorf("ResolveLocalSource(%q) accepted; want rejection", src)
		}
	}
}

func TestResolveLocalSourceRejectsSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.csv"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.csv"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// 1) Final component is a symlink escaping the workspace.
	escLink := filepath.Join(root, "esc.csv")
	if err := os.Symlink(filepath.Join(outside, "secret.csv"), escLink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := ResolveLocalSource(root, "esc.csv"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("escaping symlink: err = %v, want symlink rejection", err)
	}

	// 2) Final component is a symlink that stays INSIDE the workspace — still banned.
	inLink := filepath.Join(root, "in.csv")
	if err := os.Symlink(filepath.Join(root, "real.csv"), inLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLocalSource(root, "in.csv"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("in-tree symlink: err = %v, want symlink rejection (hard ban)", err)
	}

	// 3) Parent directory is a symlink escaping the workspace.
	if err := os.Symlink(outside, filepath.Join(root, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveLocalSource(root, "linkdir/secret.csv"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Errorf("symlinked parent: err = %v, want symlink rejection", err)
	}

	// 4) A normal contained relative file resolves fine.
	if _, err := ResolveLocalSource(root, "real.csv"); err != nil {
		t.Errorf("contained real file rejected: %v", err)
	}
}

func TestResolveLocalSourceMissingFile(t *testing.T) {
	root := t.TempDir()
	if _, err := ResolveLocalSource(root, "ref/nope.csv"); err == nil {
		t.Error("missing file accepted")
	}
}
