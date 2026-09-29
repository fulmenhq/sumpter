package parallel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// jsonFixture is a JSON source with its index and a record-scoped recipe.
type jsonFixture struct {
	src, idx string
	sig      *extract.FileSignature
	ext      *extract.ExtractRecordMatch
}

func newJSONFixture(t *testing.T, body, selector string) jsonFixture {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "in.json")
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	idx := filepath.Join(dir, "in.recordindex.json")
	b := index.NewBuilder(index.BuildOptions{InputPath: src, Selector: selector, InputFormat: index.SourceFormatJSON})
	built, err := b.Build()
	if err != nil {
		t.Fatal(err)
	}
	if err := b.WriteToFile(built, idx); err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(selector, "//")
	return jsonFixture{
		src: src, idx: idx,
		sig: &extract.FileSignature{
			SignatureID: "j", FormatType: extract.FormatJSON, MatchScope: extract.MatchScopeRecord,
			ConfidenceThreshold: 1, MatchPatterns: []extract.MatchPattern{{PatternID: "r", Selector: "/" + name, Weight: 1}},
		},
		ext: &extract.ExtractRecordMatch{
			RecordType:     "rec",
			MatchSelectors: []extract.MatchSelector{{XPath: selector}},
			FieldMappings:  []extract.FieldMapping{{OutputField: "v", XPath: ".", Type: "string"}},
		},
	}
}

type captureSink struct {
	records    []map[string]interface{}
	boundaries []extract.FileEmissionSummary
}

func (s *captureSink) OnRecord(_ context.Context, r extract.EmittedRecord) error {
	s.records = append(s.records, r.Envelope())
	return nil
}
func (s *captureSink) OnFileBoundary(_ context.Context, b extract.FileEmissionSummary) error {
	s.boundaries = append(s.boundaries, b)
	return nil
}
func (s *captureSink) Close(context.Context) error { return nil }

func (f jsonFixture) opts(workers int) ExtractionOptions {
	return ExtractionOptions{
		IndexPath: f.idx, SourcePath: f.src, Workers: workers,
		ExtractConfig: extract.CloneRecordMatch(f.ext), SignatureConfig: f.sig,
		RuntimeProvenance: provenance.RuntimeOptions{},
	}
}

func (f jsonFixture) run(t *testing.T, o ExtractionOptions) (*captureSink, extract.FileEmissionSummary, error) {
	t.Helper()
	sink := &captureSink{}
	summary, err := NewParallelExtractor(o).ExtractToSink(context.Background(), sink)
	return sink, summary, err
}

// projection is each record's number and data, in order.
func projection(records []map[string]interface{}) string {
	var b strings.Builder
	for _, r := range records {
		rt, _ := r["_runtime"].(map[string]interface{})
		ex, _ := r["extract"].(map[string]interface{})
		data, _ := json.Marshal(ex["data"])
		fmt.Fprintf(&b, "%v:%s;", rt["record_num"], data)
	}
	return b.String()
}

// TestJSONIndexedRouteParity requires the indexed route to emit exactly what
// the streaming route emits for the same recipe and source.
func TestJSONIndexedRouteParity(t *testing.T) {
	for _, tc := range []struct{ name, body, sel string }{
		{"keyed array", `{"d":{"rows":[{"id":1},{"id":2},{"id":3}]}}`, "//rows"},
		{"bom and scalars", "\xef\xbb\xbf{\"n\":[1, 9007199254740993 ,\"s\",null,true,-4.5e+6]}", "//n"},
		{"nested same-name", `{"m":[[1,2],[3]],"x":{"m":"z"}}`, "//m"},
		{"anchored bare name", `{"a":{"r":"deep"},"r":"top"}`, "r"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJSONFixture(t, tc.body, tc.sel)
			streamSink := &captureSink{}
			res := extract.ProcessFileStreamingToSink(context.Background(), f.src, f.sig, extract.CloneRecordMatch(f.ext), nil, provenance.RuntimeOptions{}, streamSink)
			if res.Error != nil {
				t.Fatalf("stream: %v", res.Error)
			}
			for _, workers := range []int{1, 4} {
				sink, summary, err := f.run(t, f.opts(workers))
				if err != nil {
					t.Fatalf("indexed (workers=%d): %v", workers, err)
				}
				if got, want := projection(sink.records), projection(streamSink.records); got != want {
					t.Fatalf("workers=%d:\nindexed %s\nstream  %s", workers, got, want)
				}
				if summary.RecordCount != len(sink.records) || len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != extract.DispositionApplied {
					t.Fatalf("summary %+v boundaries %+v", summary, sink.boundaries)
				}
			}
			rows, err := NewParallelExtractor(f.opts(2)).Extract()
			if err != nil || projection(rows) != projection(streamSink.records) {
				t.Fatalf("Extract: %v\n%s", err, projection(rows))
			}
		})
	}
}

// mutateIndex rewrites the fixture's index JSON through fn.
func mutateIndex(t *testing.T, f jsonFixture, fn func(map[string]any)) {
	t.Helper()
	raw, err := os.ReadFile(f.idx)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	fn(doc)
	// Keep the writer's field order: header fields, records, then summary.
	var b strings.Builder
	b.WriteString("{")
	first := true
	for _, k := range []string{"version", "source", "selector", "namespace_contexts", "records", "summary", "metadata"} {
		v, ok := doc[k]
		if !ok {
			continue
		}
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if !first {
			b.WriteString(",")
		}
		first = false
		fmt.Fprintf(&b, "%q:%s", k, raw)
	}
	b.WriteString("}")
	if err := os.WriteFile(f.idx, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

func recs(doc map[string]any) []any { return doc["records"].([]any) }
func rec(doc map[string]any, i int) map[string]any {
	return recs(doc)[i].(map[string]any)
}

// assertFailedInput requires a failed input: an error naming want, a failed
// boundary with no trusted count, and no rows from Extract.
func assertFailedInput(t *testing.T, f jsonFixture, o ExtractionOptions, want string) {
	t.Helper()
	sink, summary, err := f.run(t, o)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
	if len(sink.boundaries) > 0 && (sink.boundaries[0].Disposition != extract.DispositionFailed || summary.RecordCount != 0) {
		t.Fatalf("boundary %+v summary count %d", sink.boundaries, summary.RecordCount)
	}
	if rows, err := NewParallelExtractor(o).Extract(); err == nil || rows != nil {
		t.Fatalf("Extract returned %d rows, %v", len(rows), err)
	}
}

const tenRows = `{"rows":[{"id":1},{"id":2},{"id":3},{"id":4},{"id":5},{"id":6},{"id":7},{"id":8},{"id":9},{"id":10}]}`

func TestJSONIndexedRouteRefusesBadIndexes(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{"huge declared count, short stream", func(d map[string]any) {
			d["summary"].(map[string]any)["total_records"] = float64(1 << 40)
		}, "declares 1099511627776 records but lists 10"},
		{"negative declared count", func(d map[string]any) { d["summary"].(map[string]any)["total_records"] = float64(-1) }, "negative total_records"},
		{"missing summary", func(d map[string]any) { delete(d, "summary") }, "no summary field"},
		{"duplicate ordinal", func(d map[string]any) { rec(d, 3)["record_num"] = float64(3) }, "lists record 3 where record 4 was expected"},
		{"gap", func(d map[string]any) { d["records"] = append(recs(d)[:4], recs(d)[5:]...) }, "lists record 6 where record 5 was expected"},
		{"out of order", func(d map[string]any) { r := recs(d); r[1], r[2] = r[2], r[1] }, "lists record 3 where record 2 was expected"},
		{"excess record", func(d map[string]any) { d["records"] = append(recs(d), recs(d)[9]) }, "lists record 10 where record 11 was expected"},
		{"range past source", func(d map[string]any) { rec(d, 2)["end_offset"] = float64(1 << 30) }, "is outside the"},
		{"size disagrees with range", func(d map[string]any) { rec(d, 2)["size_bytes"] = float64(3) }, "does not match its range"},
		{"negative start", func(d map[string]any) { rec(d, 0)["start_offset"] = float64(-5) }, "is outside the"},
		{"forged name", func(d map[string]any) { rec(d, 1)["element_name"] = "other" }, `record 2 is named "other"`},
		{"namespace ref", func(d map[string]any) { rec(d, 1)["namespace_context_ref"] = float64(1) }, "refers to namespace context 1"},
		{"bad hash text", func(d map[string]any) { rec(d, 1)["sha256"] = "zz" }, "is not a SHA-256 hex digest"},
		{"corrupted hash", func(d map[string]any) { rec(d, 6)["sha256"] = strings.Repeat("0", 64) }, "record 7 bytes do not match the index hash"},
		{"forged selector", func(d map[string]any) { d["selector"] = map[string]any{"xpath": "//id", "element_name": "id"} }, "rebuild the index with the recipe's selector"},
		{"selector and name disagree", func(d map[string]any) { d["selector"].(map[string]any)["element_name"] = "x" }, "but the header records element_name"},
		{"missing namespace table", func(d map[string]any) { delete(d, "namespace_contexts") }, "requires a namespace_contexts table"},
		{"empty namespace table", func(d map[string]any) { d["namespace_contexts"] = []any{} }, "requires a namespace_contexts table"},
		{"wrong namespace context id", func(d map[string]any) {
			d["namespace_contexts"] = []any{map[string]any{"id": 7, "declarations": []any{}}}
		}, "found context 7"},
		{"extra namespace context", func(d map[string]any) {
			d["namespace_contexts"] = []any{map[string]any{"id": 0, "declarations": []any{}}, map[string]any{"id": 1, "declarations": []any{}}}
		}, "exactly one namespace context"},
		{"duplicate namespace context", func(d map[string]any) {
			d["namespace_contexts"] = []any{map[string]any{"id": 0, "declarations": []any{}}, map[string]any{"id": 0, "declarations": []any{}}}
		}, "more than once"},
		{"namespace declarations", func(d map[string]any) {
			d["namespace_contexts"] = []any{map[string]any{"id": 0, "declarations": []any{map[string]any{"prefix": "p", "uri": "urn:x"}}}}
		}, "carries namespace declarations"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newJSONFixture(t, tenRows, "//rows")
			mutateIndex(t, f, tc.edit)
			assertFailedInput(t, f, f.opts(4), tc.want)
		})
	}
}

func TestJSONIndexedRouteSourceIntegrity(t *testing.T) {
	t.Run("replaced path after open", func(t *testing.T) {
		f := newJSONFixture(t, tenRows, "//rows")
		testHookJSONSourceOpened = func() {
			data, _ := os.ReadFile(f.src)
			tmp := f.src + ".new"
			_ = os.WriteFile(tmp, data, 0o600)
			_ = os.Rename(tmp, f.src) // same bytes, different file
		}
		defer func() { testHookJSONSourceOpened = nil }()
		assertFailedInputOnce(t, f, f.opts(4), "source was replaced during extraction")
	})
	t.Run("same-size in-place change before reads", func(t *testing.T) {
		f := newJSONFixture(t, tenRows, "//rows")
		testHookJSONSourceOpened = func() {
			w, _ := os.OpenFile(f.src, os.O_WRONLY, 0)
			_, _ = w.WriteAt([]byte("9"), int64(strings.Index(tenRows, `"id":5`)+5))
			_ = w.Close()
		}
		defer func() { testHookJSONSourceOpened = nil }()
		assertFailedInputOnce(t, f, f.opts(4), "record 5 bytes do not match the index hash")
	})
	t.Run("truncated after open", func(t *testing.T) {
		f := newJSONFixture(t, tenRows, "//rows")
		testHookJSONSourceOpened = func() { _ = os.Truncate(f.src, 40) }
		defer func() { testHookJSONSourceOpened = nil }()
		assertFailedInputOnce(t, f, f.opts(1), "source ends inside its range")
	})
	t.Run("unselected byte change with full verification", func(t *testing.T) {
		body := `{"pad":"aaaa","rows":[{"id":1},{"id":2}]}`
		f := newJSONFixture(t, body, "//rows")
		o := f.opts(2)
		o.VerifyIndex = true
		testHookJSONSourceOpened = func() {
			w, _ := os.OpenFile(f.src, os.O_WRONLY, 0)
			_, _ = w.WriteAt([]byte("b"), int64(strings.Index(body, "aaaa")))
			_ = w.Close()
		}
		defer func() { testHookJSONSourceOpened = nil }()
		assertFailedInputOnce(t, f, o, "source changed during extraction")
		// Without full verification only the consumed record bytes are
		// checked, so a change outside every record is not detected.
		f2 := newJSONFixture(t, body, "//rows")
		testHookJSONSourceOpened = func() {
			w, _ := os.OpenFile(f2.src, os.O_WRONLY, 0)
			_, _ = w.WriteAt([]byte("b"), int64(strings.Index(body, "aaaa")))
			_ = w.Close()
		}
		if _, _, err := f2.run(t, f2.opts(2)); err != nil {
			t.Fatalf("without full verification: %v", err)
		}
	})
	t.Run("changed source fails preflight verification", func(t *testing.T) {
		f := newJSONFixture(t, tenRows, "//rows")
		if err := os.WriteFile(f.src, []byte(strings.Replace(tenRows, `"id":1}`, `"id":0}`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		o := f.opts(2)
		o.VerifyIndex = true
		assertFailedInput(t, f, o, "source hash does not match the index")
	})
	t.Run("compressed source", func(t *testing.T) {
		f := newJSONFixture(t, tenRows, "//rows")
		o := f.opts(2)
		o.SourcePath = f.src + ".gz"
		if _, _, err := f.run(t, o); err == nil || !strings.Contains(err.Error(), "compressed") {
			t.Fatalf("error %v", err)
		}
	})
}

// assertFailedInputOnce is assertFailedInput for a hook that fires once.
func assertFailedInputOnce(t *testing.T, f jsonFixture, o ExtractionOptions, want string) {
	t.Helper()
	sink, summary, err := f.run(t, o)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
	if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != extract.DispositionFailed || summary.RecordCount != 0 {
		t.Fatalf("boundary %+v summary count %d", sink.boundaries, summary.RecordCount)
	}
}

func TestJSONIndexedRouteRecordLimit(t *testing.T) {
	big := `{"rows":[{"id":1},{"blob":"` + strings.Repeat("x", 2<<20) + `"}]}`
	f := newJSONFixture(t, big, "//rows")
	o := f.opts(2)
	o.MaxRecordSizeMB = 1
	o.SkipLargeRecords = true // never skips: oversize fails the input
	assertFailedInput(t, f, o, "record 2 is 2097163 bytes, above the 1048576-byte record limit")
	for _, mb := range []int{-1, int(^uint(0) >> 1)} {
		o := f.opts(2)
		o.MaxRecordSizeMB = mb
		if _, _, err := f.run(t, o); err == nil || !strings.Contains(err.Error(), "max record size") {
			t.Fatalf("MaxRecordSizeMB=%d: %v", mb, err)
		}
	}
	if got, err := jsonRecordLimit(0); err != nil || got != 104857600 {
		t.Fatalf("default limit %d, %v", got, err)
	}
	o = f.opts(2)
	o.ReorderWindow = -1
	if _, _, err := f.run(t, o); err == nil || !strings.Contains(err.Error(), "reorder window -1 is negative") {
		t.Fatalf("negative window: %v", err)
	}
	o.ReorderWindow = maxReorderWindow + 1
	if _, _, err := f.run(t, o); err == nil || !strings.Contains(err.Error(), "above the limit") {
		t.Fatalf("huge window: %v", err)
	}
}

// TestJSONIndexedRouteLowestFailingOrdinal fails records 3 and 8 on the
// signature; with any worker count, record 3 is the reported failure and no
// rows are trusted.
func TestJSONIndexedRouteLowestFailingOrdinal(t *testing.T) {
	body := `{"rows":[{"k":"ok"},{"k":"ok"},{"k":"bad"},{"k":"ok"},{"k":"ok"},{"k":"ok"},{"k":"ok"},{"k":"bad"},{"k":"ok"}]}`
	f := newJSONFixture(t, body, "//rows")
	f.sig.MatchPatterns = []extract.MatchPattern{{PatternID: "k", Selector: "/rows/k='ok'", Weight: 1}}
	for i := 0; i < 20; i++ {
		for _, workers := range []int{1, 3, 8} {
			sink, summary, err := f.run(t, f.opts(workers))
			if err == nil || !strings.Contains(err.Error(), "signature mismatch: record 3") {
				t.Fatalf("workers=%d: error %v", workers, err)
			}
			if len(sink.boundaries) != 1 || sink.boundaries[0].DispositionReason != extract.DispositionReasonSignatureMismatch || summary.RecordCount != 0 {
				t.Fatalf("workers=%d: boundary %+v", workers, sink.boundaries)
			}
			if len(sink.records) > 2 {
				t.Fatalf("workers=%d: %d rows reached the sink past the failure", workers, len(sink.records))
			}
		}
	}
}

func TestJSONIndexedRouteRefusesDocumentScope(t *testing.T) {
	f := newJSONFixture(t, tenRows, "//rows")
	f.sig.MatchScope = ""
	sink, _, err := f.run(t, f.opts(2))
	if err == nil || !strings.Contains(err.Error(), `declare "match_scope: record"`) || strings.Contains(err.Error(), "allow-large-files") {
		t.Fatalf("error %v", err)
	}
	if len(sink.records) != 0 {
		t.Fatalf("%d rows", len(sink.records))
	}
}

func TestJSONIndexedRouteCancellationLeavesNoGoroutines(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"rows":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":%d}`, i)
	}
	body.WriteString(`]}`)
	f := newJSONFixture(t, body.String(), "//rows")
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	sink := &cancelAfterSink{cancel: cancel, after: 10}
	_, err := NewParallelExtractor(f.opts(8)).ExtractToSink(ctx, sink)
	if err == nil {
		t.Fatal("cancelled run succeeded")
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("%d goroutines left running (before %d)", n, before)
	}
}

// TestJSONIndexedRouteCancelledRunFails requires a canceled run to fail the
// input even when the sink itself never fails: a failed boundary, no trusted
// count, no applied boundary, and every goroutine joined.
func TestJSONIndexedRouteCancelledRunFails(t *testing.T) {
	var body strings.Builder
	body.WriteString(`{"rows":[`)
	for i := 0; i < 2000; i++ {
		if i > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"id":%d}`, i)
	}
	body.WriteString(`]}`)
	f := newJSONFixture(t, body.String(), "//rows")
	for _, tc := range []struct {
		name  string
		after int // records emitted before cancel; -1 cancels before the run
	}{
		{"pre-cancelled", -1},
		{"cancelled after first record", 1},
		{"cancelled mid-run", 100},
		{"cancelled at last record", 2000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sink := &quietCancelSink{cancel: cancel, after: tc.after}
			if tc.after < 0 {
				cancel()
			}
			summary, err := NewParallelExtractor(f.opts(8)).ExtractToSink(ctx, sink)
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("error %v, want a canceled failure", err)
			}
			if summary.RecordCount != 0 || summary.Disposition != extract.DispositionFailed {
				t.Fatalf("summary %+v", summary)
			}
			if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != extract.DispositionFailed || sink.boundaries[0].RecordCount != 0 {
				t.Fatalf("boundaries %+v", sink.boundaries)
			}
			deadline := time.Now().Add(2 * time.Second)
			for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > before {
				t.Fatalf("%d goroutines left running (before %d)", n, before)
			}
		})
	}
}

// TestJSONIndexedRouteCancelledAtFinalCheckFails cancels after every record
// has reached the sink: as the final source rehash starts, and after the
// final checks just before success.
func TestJSONIndexedRouteCancelledAtFinalCheckFails(t *testing.T) {
	f := newJSONFixture(t, `{"rows":[{"id":1},{"id":2},{"id":3}]}`, "//rows")
	for _, tc := range []struct {
		name   string
		stage  string
		verify bool
	}{
		{"cancelled during final rehash", "rehash", true},
		{"cancelled after final rehash", "commit", true},
		{"cancelled after identity check", "commit", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := runtime.NumGoroutine()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			fired := false
			testHookJSONFinalCheck = func(stage string) {
				if stage == tc.stage {
					fired = true
					cancel()
				}
			}
			defer func() { testHookJSONFinalCheck = nil }()
			o := f.opts(4)
			o.VerifyIndex = tc.verify
			sink := &quietCancelSink{after: -1}
			summary, err := NewParallelExtractor(o).ExtractToSink(ctx, sink)
			if !fired {
				t.Fatalf("stage %q never ran", tc.stage)
			}
			if err == nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("error %v, want a canceled failure", err)
			}
			if len(sink.records) != 3 {
				t.Fatalf("sink took %d records before the final check, want 3", len(sink.records))
			}
			if summary.RecordCount != 0 || summary.Disposition != extract.DispositionFailed {
				t.Fatalf("summary %+v", summary)
			}
			if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != extract.DispositionFailed || sink.boundaries[0].RecordCount != 0 {
				t.Fatalf("boundaries %+v", sink.boundaries)
			}
			deadline := time.Now().Add(2 * time.Second)
			for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := runtime.NumGoroutine(); n > before {
				t.Fatalf("%d goroutines left running (before %d)", n, before)
			}
		})
	}
}

// quietCancelSink cancels the run once it has taken after records and keeps
// accepting records without error.
type quietCancelSink struct {
	captureSink
	cancel func()
	after  int
}

func (s *quietCancelSink) OnRecord(ctx context.Context, r extract.EmittedRecord) error {
	if err := s.captureSink.OnRecord(ctx, r); err != nil {
		return err
	}
	if len(s.records) == s.after {
		s.cancel()
	}
	return nil
}

type cancelAfterSink struct {
	captureSink
	cancel func()
	after  int
}

func (s *cancelAfterSink) OnRecord(ctx context.Context, r extract.EmittedRecord) error {
	if len(s.records) == s.after {
		s.cancel()
		return ctx.Err()
	}
	return s.captureSink.OnRecord(ctx, r)
}

// TestJSONIndexedRouteConformanceParity runs every conformance fixture and
// every selectable element name, both selector forms, through the streaming
// and indexed routes and requires the same records.
func TestJSONIndexedRouteConformanceParity(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "tests", "fixtures", "docnode", "json")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.Name() == "conformance.json" || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range fixtureNames(t, raw) {
			for _, sel := range []string{name, "//" + name} {
				f := newJSONFixture(t, string(raw), sel)
				streamSink := &captureSink{}
				res := extract.ProcessFileStreamingToSink(context.Background(), f.src, f.sig, extract.CloneRecordMatch(f.ext), nil, provenance.RuntimeOptions{}, streamSink)
				if res.Error != nil {
					t.Fatalf("%s %s stream: %v", e.Name(), sel, res.Error)
				}
				sink, _, err := f.run(t, f.opts(3))
				if err != nil {
					t.Fatalf("%s %s indexed: %v", e.Name(), sel, err)
				}
				if got, want := projection(sink.records), projection(streamSink.records); got != want {
					t.Fatalf("%s %s:\nindexed %s\nstream  %s", e.Name(), sel, got, want)
				}
				checked++
			}
		}
	}
	if checked == 0 {
		t.Fatal("nothing checked")
	}
}

// fixtureNames returns the distinct object keys in raw that are valid record
// selector names.
func fixtureNames(t *testing.T, raw []byte) []string {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	var names []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for k, c := range x {
				if !seen[k] && isSelectorName(k) {
					seen[k] = true
					names = append(names, k)
				}
				walk(c)
			}
		case []any:
			for _, c := range x {
				walk(c)
			}
		}
	}
	walk(v)
	if _, isArray := v.([]any); isArray {
		names = append(names, "item")
	}
	return names
}

func isSelectorName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		ok := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && (r == '-' || r == '.' || (r >= '0' && r <= '9')))
		if !ok {
			return false
		}
	}
	return true
}
