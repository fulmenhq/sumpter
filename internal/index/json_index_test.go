package index

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"

	"github.com/fulmenhq/sumpter/internal/docnode"
	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
)

// buildJSONIndex writes src to a file, builds a JSON index for selector and
// returns the source path, the index path and the built index.
func buildJSONIndex(t *testing.T, src, selector string) (string, string, *RecordIndex) {
	t.Helper()
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	if err := os.WriteFile(in, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "in.recordindex.json")
	b := NewBuilder(BuildOptions{InputPath: in, Selector: selector, InputFormat: SourceFormatJSON})
	idx, err := b.Build()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := b.WriteToFile(idx, out); err != nil {
		t.Fatal(err)
	}
	return in, out, idx
}

// TestJSONIndexMatchesStreamScanner requires the index to hold exactly the
// records the J2 streaming scanner yields, with hashes over the raw ranges.
func TestJSONIndexMatchesStreamScanner(t *testing.T) {
	for _, tc := range []struct{ name, src, sel string }{
		{"keyed array", `{"d":{"rows":[{"id":1},{"id":2},{"id":3}]}}`, "//rows"},
		{"top-level array", "\xef\xbb\xbf[ {\"a\":1}, 2 ,null,\"s\"]", "item"},
		{"nested same-name", `{"m":[[1,2],[3]]}`, "//m"},
		{"anchored bare name", `{"a":{"r":1},"r":2}`, "r"},
		{"no match", `{"a":1}`, "zzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, idx := buildJSONIndex(t, tc.src, tc.sel)
			if idx.Version != SchemaVersion || idx.Source.Format != SourceFormatJSON {
				t.Fatalf("header %s %s", idx.Version, idx.Source.Format)
			}
			if len(idx.NamespaceContexts) != 1 || idx.NamespaceContexts[0].ID != 0 || idx.NamespaceContexts[0].Declarations == nil || len(idx.NamespaceContexts[0].Declarations) != 0 {
				t.Fatalf("namespace contexts %+v", idx.NamespaceContexts)
			}
			sc, err := docjson.Format{}.NewScanner(strings.NewReader(tc.src), tc.sel, false)
			if err != nil {
				t.Fatal(err)
			}
			var want []*docnode.Record
			for {
				r, err := sc.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				want = append(want, r)
			}
			if len(idx.Records) != len(want) || idx.Summary.TotalRecords != len(want) {
				t.Fatalf("%d records (summary %d), scanner %d", len(idx.Records), idx.Summary.TotalRecords, len(want))
			}
			for i, r := range idx.Records {
				w := want[i]
				if r.RecordNum != w.Num || r.StartOffset != w.StartOffset || r.EndOffset != w.EndOffset || r.SizeBytes != w.SizeBytes || r.ElementName != w.Name || r.Depth != w.Depth || r.NamespaceContextRef != 0 {
					t.Fatalf("record %d: index %+v, scanner %+v", i+1, r, w)
				}
				h, err := computeRangeHashSHA256FromReader(bytes.NewReader([]byte(tc.src)), w.StartOffset, w.EndOffset)
				if err != nil || h != r.SHA256 {
					t.Fatalf("record %d hash %s, want %s (%v)", i+1, r.SHA256, h, err)
				}
			}
		})
	}
}

func TestJSONIndexMatchesV013Schema(t *testing.T) {
	_, out, _ := buildJSONIndex(t, `{"d":{"rows":[{"id":1},{"id":2}]}}`, "//rows")
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	ctx, _ := json.Marshal(doc["namespace_contexts"])
	if string(ctx) != `[{"declarations":[],"id":0}]` {
		t.Fatalf("namespace contexts %s", ctx)
	}
	schema, err := os.ReadFile("../../schemas/index/v0.1.3/record-index.schema.json")
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
		t.Fatalf("JSON index does not match v0.1.3 schema: %+v", res.Errors)
	}
}

func TestJSONIndexBuildRefusals(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.json")
	if err := os.WriteFile(in, []byte(`{"r":[1,{"a":1,"a":2}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	gz := filepath.Join(dir, "in.json.gz")
	if err := os.WriteFile(gz, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, format, want string
		route                    bool
	}{
		{"ndjson", in, "ndjson", "not supported for ndjson input", true},
		{"unknown format", in, "yaml", `unknown input format "yaml"`, false},
		{"compressed source", gz, SourceFormatJSON, "requires an uncompressed source", false},
		{"malformed source", in, SourceFormatJSON, `duplicate key "a"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewBuilder(BuildOptions{InputPath: tc.path, Selector: "//r", InputFormat: tc.format}).Build()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
			if tc.route && !errors.Is(err, docnode.ErrRouteUnsupported) {
				t.Fatalf("error %v is not route_unsupported", err)
			}
		})
	}
}

// memProvider serves an index from memory, as a store would.
type memProvider struct{ idx *RecordIndex }

func (p memProvider) Header() (*RecordIndex, error) { h := *p.idx; h.Records = nil; return &h, nil }
func (p memProvider) Records(context.Context) (RecordIterator, error) {
	return &memIter{recs: p.idx.Records}, nil
}
func (p memProvider) Close() error { return nil }

type memIter struct {
	recs []RecordMetadata
	i    int
}

func (it *memIter) Next() (*RecordMetadata, error) {
	if it.i >= len(it.recs) {
		return nil, io.EOF
	}
	r := it.recs[it.i]
	it.i++
	return &r, nil
}
func (it *memIter) Close() error { return nil }

func TestJSONIndexSemanticVerify(t *testing.T) {
	src := `{"d":{"rows":[{"id":1},{"id":2},{"id":3}]},"x":{"rows":[{"id":4}]}}`
	verify := func(in string, idx *RecordIndex, format string) *VerifyResult {
		t.Helper()
		res, err := NewVerifier(VerifyOptions{InputPath: in, InputFormat: format}).VerifyWithProvider(memProvider{idx})
		if err != nil {
			t.Fatalf("verify error: %v", err)
		}
		return res
	}
	in, _, idx := buildJSONIndex(t, src, "//rows")
	if res := verify(in, idx, SourceFormatJSON); !res.Valid || res.RecordsVerified != 4 {
		t.Fatalf("clean index: %+v", res)
	}
	clone := func() *RecordIndex {
		c := *idx
		c.Records = append([]RecordMetadata(nil), idx.Records...)
		return &c
	}
	for _, tc := range []struct {
		name string
		edit func(*RecordIndex)
		want string
	}{
		{"dropped record, count kept", func(c *RecordIndex) { c.Records = c.Records[:3] }, "source holds record 4"},
		{"dropped record, count adjusted", func(c *RecordIndex) { c.Records = c.Records[:3]; c.Summary.TotalRecords = 3 }, "source holds record 4"},
		{"extra record", func(c *RecordIndex) { c.Records = append(c.Records, c.Records[3]); c.Summary.TotalRecords = 5 }, "the source holds only 4"},
		{"declared count", func(c *RecordIndex) { c.Summary.TotalRecords = 1 << 40 }, "index declares 1099511627776 records"},
		{"negative count", func(c *RecordIndex) { c.Summary.TotalRecords = -1 }, "negative record count"},
		{"shifted range", func(c *RecordIndex) { c.Records[1].StartOffset++; c.Records[1].SizeBytes-- }, "record 2 does not match the source"},
		{"forged name", func(c *RecordIndex) { c.Records[0].ElementName = "other" }, "record 1 does not match the source"},
		{"forged depth", func(c *RecordIndex) { c.Records[0].Depth = 9 }, "record 1 does not match the source"},
		{"forged ordinal", func(c *RecordIndex) { c.Records[2].RecordNum = 7 }, "record 3 does not match the source"},
		{"namespace ref", func(c *RecordIndex) { c.Records[0].NamespaceContextRef = 1 }, "record 1 does not match the source"},
		{"record hash", func(c *RecordIndex) { c.Records[3].SHA256 = strings.Repeat("0", 64) }, "record 4 hash mismatch"},
		{"forged selector", func(c *RecordIndex) { c.Selector = SelectorInfo{XPath: "//d", ElementName: "d"} }, "record 1 does not match the source"},
		{"selector and name disagree", func(c *RecordIndex) { c.Selector.ElementName = "d" }, "but the header records element_name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := clone()
			tc.edit(c)
			res := verify(in, c, SourceFormatJSON)
			if res.Valid || !strings.Contains(res.ErrorMessage, tc.want) {
				t.Fatalf("result %+v, want %q", res, tc.want)
			}
		})
	}
	t.Run("changed source", func(t *testing.T) {
		if err := os.WriteFile(in, []byte(strings.Replace(src, `"id":4`, `"id":5`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if res := verify(in, idx, SourceFormatJSON); res.Valid || !strings.Contains(res.ErrorMessage, "source file hash mismatch") {
			t.Fatalf("result %+v", res)
		}
	})
	t.Run("format mismatch", func(t *testing.T) {
		if _, err := NewVerifier(VerifyOptions{InputPath: in, InputFormat: SourceFormatXML}).VerifyWithProvider(memProvider{idx}); err == nil || !strings.Contains(err.Error(), "built from json input") {
			t.Fatalf("error %v", err)
		}
	})
}
