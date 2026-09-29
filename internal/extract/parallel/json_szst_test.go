//go:build cgo && seekablezstd

package parallel

import (
	"context"
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
