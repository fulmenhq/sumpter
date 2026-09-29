//go:build cgo && seekablezstd

package parallel

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestJSONIndexedRouteSeekableStore runs the indexed route from a seekable
// store, whose rows carry no names, and requires the streaming route's
// records; a changed record byte fails the input.
func TestJSONIndexedRouteSeekableStore(t *testing.T) {
	f := newJSONFixture(t, tenRows, "//rows")
	base := filepath.Join(filepath.Dir(f.src), "szst")
	b := index.NewBuilder(index.BuildOptions{InputPath: f.src, Selector: "//rows", InputFormat: index.SourceFormatJSON})
	if _, err := b.BuildTo(store.NewSeekableIndexWriter(base)); err != nil {
		t.Fatal(err)
	}
	f.idx = base + ".recordindex.header.json"
	streamSink := &captureSink{}
	if res := extract.ProcessFileStreamingToSink(context.Background(), f.src, f.sig, extract.CloneRecordMatch(f.ext), nil, provenance.RuntimeOptions{}, streamSink); res.Error != nil {
		t.Fatal(res.Error)
	}
	sink, summary, err := f.run(t, f.opts(4))
	if err != nil || projection(sink.records) != projection(streamSink.records) || summary.RecordCount != 10 {
		t.Fatalf("szst indexed: %v, %d records\n%s", err, summary.RecordCount, projection(sink.records))
	}
	if err := os.WriteFile(f.src, []byte(strings.Replace(tenRows, `"id":4`, `"id":8`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.run(t, f.opts(4)); err == nil || !strings.Contains(err.Error(), "record 4 bytes do not match the index hash") {
		t.Fatalf("changed record byte: %v", err)
	}
}

// TestJSONIndexedRouteSeekableStoreRefusesBadNamespaceTables requires a
// seekable JSON header to hold exactly the empty context 0; any other table
// fails the input before a row is published.
func TestJSONIndexedRouteSeekableStoreRefusesBadNamespaceTables(t *testing.T) {
	empty := func(id int) map[string]any { return map[string]any{"id": id, "declarations": []any{}} }
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"missing table", func(h map[string]any) { delete(h, "namespace_contexts") }, "requires a namespace_contexts table"},
		{"empty table", func(h map[string]any) { h["namespace_contexts"] = []any{} }, "requires a namespace_contexts table"},
		{"wrong id", func(h map[string]any) { h["namespace_contexts"] = []any{empty(7)} }, "found context 7"},
		{"extra context", func(h map[string]any) { h["namespace_contexts"] = []any{empty(0), empty(1)} }, "exactly one namespace context"},
		{"duplicate context", func(h map[string]any) { h["namespace_contexts"] = []any{empty(0), empty(0)} }, "more than once"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJSONFixture(t, tenRows, "//rows")
			base := filepath.Join(filepath.Dir(f.src), "szst")
			b := index.NewBuilder(index.BuildOptions{InputPath: f.src, Selector: "//rows", InputFormat: index.SourceFormatJSON})
			if _, err := b.BuildTo(store.NewSeekableIndexWriter(base)); err != nil {
				t.Fatal(err)
			}
			f.idx = base + ".recordindex.header.json"
			raw, err := os.ReadFile(f.idx)
			if err != nil {
				t.Fatal(err)
			}
			var h map[string]any
			if err := json.Unmarshal(raw, &h); err != nil {
				t.Fatal(err)
			}
			tc.edit(h)
			if raw, err = json.Marshal(h); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.idx, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			assertFailedInput(t, f, f.opts(4), tc.want)
		})
	}
}

// TestJSONIndexedRouteSeekableStoreRefusesRangeSequences writes each
// out-of-preorder record sequence to a seekable store, whose rows carry no
// names, and requires a failed input with and without full verification.
func TestJSONIndexedRouteSeekableStoreRefusesRangeSequences(t *testing.T) {
	for _, tc := range rangeSequenceEdits {
		for _, verify := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s verify=%v", tc.name, verify), func(t *testing.T) {
				f := newJSONFixture(t, nestedRows, "//rows")
				built, err := index.LoadIndex(f.idx)
				if err != nil {
					t.Fatal(err)
				}
				built.Records = renumber(tc.edit(built.Records))
				base := filepath.Join(filepath.Dir(f.src), "szst")
				if err := store.WriteSeekableIndex(base, built); err != nil {
					t.Fatal(err)
				}
				f.idx = base + ".recordindex.header.json"
				o := f.opts(4)
				o.VerifyIndex = verify
				assertFailedInput(t, f, o, tc.want)
			})
		}
	}
}
