//go:build cgo && seekablezstd

package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"

	"github.com/fulmenhq/sumpter/internal/index"
)

// writeContractStore writes a one-record seekable store in dir and returns
// the header path.
func writeContractStore(t *testing.T, dir, format string) string {
	t.Helper()
	idx := &index.RecordIndex{
		Source: index.SourceInfo{
			Path: "/src/in." + format, SizeBytes: 10, Format: format,
			SHA256:     "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			OffsetKind: index.OffsetKindSourceBytes, CreatedAt: time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC),
		},
		Selector: index.SelectorInfo{XPath: "//r", ElementName: "r"},
		Summary:  index.SummaryStats{TotalRecords: 1},
		Records: []index.RecordMetadata{{
			RecordNum: 1, StartOffset: 1, EndOffset: 9, SizeBytes: 8, Depth: 1,
			SHA256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
		}},
	}
	base := filepath.Join(dir, "idx")
	if err := WriteSeekableIndex(base, idx); err != nil {
		t.Fatal(err)
	}
	return base + ".recordindex.header.json"
}

// editHeader rewrites the header's JSON through fn.
func editHeader(t *testing.T, path string, fn func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(raw, &h); err != nil {
		t.Fatal(err)
	}
	fn(h)
	out, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

func records(h map[string]any) map[string]any { return h["records"].(map[string]any) }
func source(h map[string]any) map[string]any  { return h["source"].(map[string]any) }

func TestSzstV012HeaderShape(t *testing.T) {
	for _, format := range []string{index.SourceFormatXML, index.SourceFormatJSON} {
		path := writeContractStore(t, t.TempDir(), format)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var h map[string]any
		if err := json.Unmarshal(raw, &h); err != nil {
			t.Fatal(err)
		}
		r := records(h)
		if h["version"] != index.SzstStoreVersion || source(h)["format"] != format {
			t.Fatalf("%s: version %v format %v", format, h["version"], source(h)["format"])
		}
		for _, flat := range []string{"record_width_bytes", "sha_encoding", "endianness"} {
			if _, ok := r[flat]; ok {
				t.Fatalf("%s: v0.1.2 header carries flat %s, which an earlier reader would accept", format, flat)
			}
		}
		layout, ok := r["layout"].(map[string]any)
		if !ok || layout["width_bytes"] != float64(BinaryRecordWidth) {
			t.Fatalf("%s: layout %v", format, r["layout"])
		}
		s, err := Open(path)
		if err != nil {
			t.Fatalf("%s: open: %v", format, err)
		}
		it, err := s.Records(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		rec, err := it.Next()
		if err != nil || rec.RecordNum != 1 || rec.EndOffset != 9 {
			t.Fatalf("%s: record %+v, %v", format, rec, err)
		}
		_ = it.Close()
		_ = s.Close()
	}
}

func TestSzstHeaderRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"unknown version", func(h map[string]any) { h["version"] = "record-index-szst/v9.9.9" }, "unsupported seekable-zstd index version"},
		{"v0.1.2 without layout", func(h map[string]any) { delete(records(h), "layout") }, "requires records.layout"},
		{"v0.1.2 with flat width too", func(h map[string]any) { records(h)["record_width_bytes"] = BinaryRecordWidth }, "must not carry flat"},
		{"v0.1.2 wrong width", func(h map[string]any) { records(h)["layout"].(map[string]any)["width_bytes"] = 64 }, "records.layout must be"},
		{"v0.1.2 missing format", func(h map[string]any) { delete(source(h), "format") }, "requires source.format"},
		{"v0.1.2 unknown format", func(h map[string]any) { source(h)["format"] = "yaml" }, `source.format "yaml" is not supported`},
		{"v0.1.1 with layout", func(h map[string]any) { h["version"] = index.LegacySzstStoreVersion }, "predates records.layout"},
		{"v0.1.1 forging json", func(h map[string]any) {
			h["version"] = index.LegacySzstStoreVersion
			r := records(h)
			delete(r, "layout")
			r["record_width_bytes"], r["sha_encoding"], r["endianness"] = BinaryRecordWidth, "raw32", "little"
			source(h)["format"] = "json"
		}, "predates source.format and is XML only"},
		{"records_file absolute", func(h map[string]any) { records(h)["records_file"] = "/etc/passwd" }, "must be a file name beside the header"},
		{"records_file traversal", func(h map[string]any) { records(h)["records_file"] = "../idx.recordindex.records.szst" }, "must be a file name beside the header"},
		{"records_file parent", func(h map[string]any) { records(h)["records_file"] = ".." }, "is not a file name"},
		{"records_file empty", func(h map[string]any) { records(h)["records_file"] = "" }, "records_file is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeContractStore(t, t.TempDir(), index.SourceFormatJSON)
			editHeader(t, path, tc.edit)
			s, err := Open(path)
			if err == nil {
				_ = s.Close()
				t.Fatal("header accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
}

func TestSzstLegacyXMLHeaderStillReads(t *testing.T) {
	path := writeContractStore(t, t.TempDir(), index.SourceFormatXML)
	editHeader(t, path, func(h map[string]any) {
		h["version"] = index.LegacySzstStoreVersion
		r := records(h)
		delete(r, "layout")
		r["record_width_bytes"], r["sha_encoding"], r["endianness"] = BinaryRecordWidth, "raw32", "little"
		delete(source(h), "format")
	})
	s, err := Open(path)
	if err != nil {
		t.Fatalf("legacy XML header refused: %v", err)
	}
	defer func() { _ = s.Close() }()
	h, err := s.Header()
	if err != nil {
		t.Fatal(err)
	}
	if format, err := index.SourceFormat(h); err != nil || format != index.SourceFormatXML {
		t.Fatalf("legacy format %q, %v", format, err)
	}
}

func TestSzstRecordsFileSymlinkRefused(t *testing.T) {
	dir := t.TempDir()
	path := writeContractStore(t, dir, index.SourceFormatJSON)
	outside := filepath.Join(t.TempDir(), "elsewhere.szst")
	real := filepath.Join(dir, "idx.recordindex.records.szst")
	if err := os.Rename(real, outside); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, real); err != nil {
		t.Fatal(err)
	}
	if s, err := Open(path); err == nil {
		_ = s.Close()
		t.Fatal("records_file symlink followed")
	}
}

// TestSzstParentDirectorySwapIsAnchored replaces the header's directory
// between reading the header and opening the records file; the records must
// come from the directory the header was read from, or the open must fail.
func TestSzstParentDirectorySwapIsAnchored(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "index")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	path := writeContractStore(t, dir, index.SourceFormatJSON)
	// A second store whose one record has different offsets.
	other := filepath.Join(root, "other")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	otherHeader := writeContractStore(t, other, index.SourceFormatJSON)
	editHeader(t, otherHeader, func(map[string]any) {})
	moved := filepath.Join(root, "moved")
	testHookAfterHeaderRead = func() {
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(other, dir); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { testHookAfterHeaderRead = nil }()
	s, err := Open(path)
	if err != nil {
		t.Logf("open refused after the swap: %v", err)
		return // refusing is also acceptable
	}
	t.Log("open succeeded after the swap; checking which records file it holds")
	defer func() { _ = s.Close() }()
	st := s.(*szstStore)
	info, err := st.records.Stat()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.Stat(filepath.Join(moved, "idx.recordindex.records.szst"))
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, want) {
		t.Fatal("records file came from the swapped-in directory")
	}
}

func TestSzstV012HeaderMatchesSchema(t *testing.T) {
	for _, format := range []string{index.SourceFormatXML, index.SourceFormatJSON} {
		path := writeContractStore(t, t.TempDir(), format)
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		schema, err := os.ReadFile("../../../schemas/index/v0.1.3/szst-header.schema.json")
		if err != nil {
			t.Fatal(err)
		}
		var v any
		_ = json.Unmarshal(raw, &v)
		res, err := goneatschema.ValidateFromBytes(schema, v)
		if err != nil {
			t.Fatal(err)
		}
		if !res.Valid {
			t.Fatalf("%s: header does not match szst-header schema: %+v", format, res.Errors)
		}
	}
}

func TestSzstV012NullContextRefused(t *testing.T) {
	path := writeContractStore(t, t.TempDir(), index.SourceFormatJSON)
	editHeader(t, path, func(h map[string]any) {
		h["namespace_contexts"] = []any{map[string]any{"id": 0, "declarations": nil}}
	})
	if s, err := Open(path); err == nil {
		_ = s.Close()
		t.Fatal("null context accepted in a v0.1.2 header")
	} else if !strings.Contains(err.Error(), "has no declarations array") {
		t.Fatalf("error %v", err)
	}
}

// TestSzstJSONIndexBuildAndSemanticVerify builds a JSON index into the
// seekable store and verifies it semantically through the store, where the
// record name comes from the header selector.
func TestSzstJSONIndexBuildAndSemanticVerify(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	src := `{"d":{"rows":[{"id":1},{"id":2}]},"x":{"rows":{"id":3}}}`
	if err := os.WriteFile(in, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "in")
	b := index.NewBuilder(index.BuildOptions{InputPath: in, Selector: "//rows", InputFormat: index.SourceFormatJSON})
	if _, err := b.BuildTo(NewSeekableIndexWriter(base)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(base + ".recordindex.header.json")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	h, err := s.Header()
	if err != nil {
		t.Fatal(err)
	}
	if format, err := index.SourceFormat(h); err != nil || format != index.SourceFormatJSON || h.Summary.TotalRecords != 3 {
		t.Fatalf("header format %q total %d, %v", format, h.Summary.TotalRecords, err)
	}
	res, err := index.NewVerifier(index.VerifyOptions{InputPath: in, InputFormat: index.SourceFormatJSON}).VerifyWithProvider(providerOf{s})
	if err != nil || !res.Valid || res.RecordsVerified != 3 {
		t.Fatalf("verify %+v, %v", res, err)
	}
	if err := os.WriteFile(in, []byte(strings.Replace(src, `"id":3`, `"id":4`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	res, err = index.NewVerifier(index.VerifyOptions{InputPath: in, InputFormat: index.SourceFormatJSON}).VerifyWithProvider(providerOf{s})
	if err != nil || res.Valid {
		t.Fatalf("changed source verified: %+v, %v", res, err)
	}
}

// providerOf adapts an IndexStore to the verifier's record provider.
type providerOf struct{ s IndexStore }

func (p providerOf) Header() (*index.RecordIndex, error) { return p.s.Header() }
func (p providerOf) Records(ctx context.Context) (index.RecordIterator, error) {
	return p.s.Records(ctx)
}
func (p providerOf) Close() error { return p.s.Close() }
