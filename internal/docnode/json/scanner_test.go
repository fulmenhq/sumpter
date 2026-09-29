package json

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/iotest"
	"unsafe"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// scanAll drains a scanner over src and returns its records and final error
// (nil at a clean end).
func scanAll(t *testing.T, r io.Reader, selector string) ([]*docnode.Record, error) {
	t.Helper()
	sc, err := Format{}.NewScanner(r, selector, false)
	if err != nil {
		t.Fatalf("NewScanner(%q): %v", selector, err)
	}
	var recs []*docnode.Record
	for {
		rec, err := sc.Next()
		if err == io.EOF {
			return recs, nil
		}
		if err != nil {
			return recs, err
		}
		recs = append(recs, rec)
	}
}

// dump renders a subtree as the node model sees it: names, kinds, and text in
// document order.
func dump(n *jnode) string {
	var b strings.Builder
	var walk func(n *jnode)
	walk = func(n *jnode) {
		switch n.typ {
		case textNode:
			fmt.Fprintf(&b, "%q", n.value)
			return
		case elementNode:
			fmt.Fprintf(&b, "<%s", n.name)
			if n.scalar {
				fmt.Fprintf(&b, ":%d", n.kind)
			}
			b.WriteString(">")
		}
		for c := n.firstChild; c != nil; c = c.next {
			walk(c)
		}
		if n.typ == elementNode {
			b.WriteString("</>")
		}
	}
	walk(n)
	return b.String()
}

func jnodeOf(t *testing.T, n docnode.Node) *jnode {
	t.Helper()
	switch x := n.(type) {
	case node:
		return x.n
	case scalarNode:
		return x.n
	}
	t.Fatalf("not a JSON node: %T", n)
	return nil
}

// assertParity requires the stream over src to yield exactly the DOM's
// //name elements, in order, with each record's range replaying to that
// element.
func assertParity(t *testing.T, label string, src []byte, name string) {
	t.Helper()
	assertSelectorParity(t, label, src, "//"+name)
	assertSelectorParity(t, label, src, name)
}

// assertSelectorParity requires the stream over src with selector sel to
// yield exactly the elements the same XPath selects on the whole document.
func assertSelectorParity(t *testing.T, label string, src []byte, sel string) {
	t.Helper()
	doc, err := Format{}.Parse(bytes.NewReader(src))
	if err != nil {
		t.Fatalf("%s: Parse: %v", label, err)
	}
	name := strings.TrimPrefix(sel, "//")
	want := selectNodes(t, doc, sel)
	for _, feed := range []struct {
		kind string
		r    io.Reader
	}{
		{"whole", bytes.NewReader(src)},
		{"one-byte", iotest.OneByteReader(bytes.NewReader(src))},
	} {
		recs, err := scanAll(t, feed.r, sel)
		if err != nil {
			t.Fatalf("%s %s %s: scan: %v", label, feed.kind, sel, err)
		}
		if len(recs) != len(want) {
			t.Fatalf("%s %s %s: %d records, DOM has %d", label, feed.kind, sel, len(recs), len(want))
		}
		for i, rec := range recs {
			if rec.Num != i+1 {
				t.Fatalf("%s //%s record %d: Num %d", label, name, i, rec.Num)
			}
			if rec.StartOffset < 0 || rec.EndOffset > int64(len(src)) || rec.StartOffset >= rec.EndOffset {
				t.Fatalf("%s //%s record %d: range [%d,%d)", label, name, i, rec.StartOffset, rec.EndOffset)
			}
			if !bytes.Equal(rec.Raw, src[rec.StartOffset:rec.EndOffset]) {
				t.Fatalf("%s //%s record %d: Raw %q is not raw[%d:%d] %q", label, name, i, rec.Raw, rec.StartOffset, rec.EndOffset, src[rec.StartOffset:rec.EndOffset])
			}
			if rec.SizeBytes != rec.EndOffset-rec.StartOffset {
				t.Fatalf("%s //%s record %d: SizeBytes %d", label, name, i, rec.SizeBytes)
			}
			if b, e := src[rec.StartOffset], src[rec.EndOffset-1]; isSpace(b) || b == ',' || isSpace(e) || e == ',' {
				t.Fatalf("%s //%s record %d: range %q includes a separator or whitespace", label, name, i, rec.Raw)
			}
			replay := &docnode.Record{Raw: src[rec.StartOffset:rec.EndOffset], Name: rec.Name, StartOffset: rec.StartOffset}
			rdoc, err := Format{}.ParseRecord(replay)
			if err != nil {
				t.Fatalf("%s //%s record %d: replay: %v", label, name, i, err)
			}
			got := dump(rdoc.(document).root.firstChild)
			if exp := dump(jnodeOf(t, want[i])); got != exp {
				t.Fatalf("%s //%s record %d:\nreplay %s\nDOM    %s", label, name, i, got, exp)
			}
		}
	}
}

// elementNames returns every element name in a parsed tree that is a valid
// streaming selector.
func elementNames(n *jnode, into map[string]bool) {
	if n.typ == elementNode && streaming.ValidateRecordSelector(n.name) == nil {
		into[n.name] = true
	}
	for c := n.firstChild; c != nil; c = c.next {
		elementNames(c, into)
	}
}

func TestScannerParityOnConformanceFixtures(t *testing.T) {
	entries, err := os.ReadDir(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if e.Name() == "conformance.json" || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		src, err := os.ReadFile(filepath.Join(fixtureDir(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		root, err := parse(src)
		if err != nil {
			t.Fatalf("%s: %v", e.Name(), err)
		}
		names := map[string]bool{}
		elementNames(root, names)
		sorted := make([]string, 0, len(names))
		for n := range names {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			assertParity(t, e.Name(), src, name)
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("no fixture names checked")
	}
}

func TestScannerShapes(t *testing.T) {
	for _, tc := range []struct {
		name, src, sel string
		raws           []string
	}{
		{"top-level array", `[{"id":1}, {"id":2} ,{"id":3}]`, "item", []string{`{"id":1}`, `{"id":2}`, `{"id":3}`}},
		{"keyed array at depth", `{"a":{"b":{"rows":[{"x":1},{"x":2}]}}}`, "//rows", []string{`{"x":1}`, `{"x":2}`}},
		{"repeated keyed objects", `{"p":{"rec":{"v":1}},"q":{"rec":{"v":2}}}`, "//rec", []string{`{"v":1}`, `{"v":2}`}},
		{"scalar items keep exact lexemes", `{"n":[1, 22 ,333,-4.5e+6,"s\"q",true,null]}`, "n", []string{`1`, `22`, `333`, `-4.5e+6`, `"s\"q"`, `true`, `null`}},
		{"nested same-name in start order", `{"m":[[1,2],[3]]}`, "//m", []string{`[1,2]`, `1`, `2`, `[3]`, `3`}},
		{"object record holding same-name records", `{"r":{"id":1,"r":[{"id":2}]}}`, "//r", []string{`{"id":1,"r":[{"id":2}]}`, `{"id":2}`}},
		{"empty array yields none", `{"a":[],"b":1}`, "a", nil},
		{"member array itself is not a record", `{"a":[[]]}`, "a", []string{`[]`}},
		{"no match", `{"a":1}`, "zzz", nil},
		{"bare name is anchored under the root", `{"m":[[1,2],[3]]}`, "m", []string{`[1,2]`, `[3]`}},
		{"bare name skips deeper matches", `{"a":{"rec":1},"rec":2}`, "rec", []string{`2`}},
		{"whitespace everywhere", " \n[ \r\n {\"a\" : 1 } \t,\n2 ] \n", "item", []string{`{"a" : 1 }`, `2`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recs, err := scanAll(t, strings.NewReader(tc.src), tc.sel)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			var raws []string
			for _, r := range recs {
				raws = append(raws, string(r.Raw))
			}
			if fmt.Sprint(raws) != fmt.Sprint(tc.raws) {
				t.Fatalf("raws %q, want %q", raws, tc.raws)
			}
			assertParity(t, tc.name, []byte(tc.src), strings.TrimPrefix(tc.sel, "//"))
		})
	}
}

func TestScannerOffsetsAreAbsoluteWithByteOrderMark(t *testing.T) {
	src := []byte("\xef\xbb\xbf[{\"a\":1},\n  {\"a\":2}]")
	recs, err := scanAll(t, bytes.NewReader(src), "item")
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 || recs[0].StartOffset != 4 || recs[0].EndOffset != 11 || recs[1].StartOffset != 15 {
		t.Fatalf("records %+v", recs)
	}
	for _, r := range recs {
		if !bytes.Equal(r.Raw, src[r.StartOffset:r.EndOffset]) {
			t.Fatalf("raw %q is not src[%d:%d]", r.Raw, r.StartOffset, r.EndOffset)
		}
	}
	assertParity(t, "bom", src, "item")
}

// TestScannerErrorsMatchParse requires a drained scanner to end with the
// same fault Parse reports, whatever the selector, and to yield nothing past
// it.
func TestScannerErrorsMatchParse(t *testing.T) {
	cases := append(walkParityCases(),
		"\xef\xbb\xbf{\"a\":1,\"a\":2}",
		`{"a":[{"x":1},{"x":1,"x":2}]}`,
		`{"a":[{"x":1},{"x":2}]} trailing`,
		`{"a":[{"x":1},{"x":2}`,
		`[{"x":1},{"x":2}] [3]`,
	)
	for _, src := range cases {
		_, perr := parse([]byte(src))
		for _, sel := range []string{"a", "item", "x", "zzz"} {
			for _, feed := range []io.Reader{strings.NewReader(src), iotest.OneByteReader(strings.NewReader(src))} {
				_, serr := scanAll(t, feed, sel)
				if errText(serr) != errText(perr) {
					t.Fatalf("%.60q //%s: scan %q, Parse %q", src, sel, errText(serr), errText(perr))
				}
			}
		}
	}
}

func TestScannerFaultAfterRecordsIsSticky(t *testing.T) {
	sc, err := Format{}.NewScanner(strings.NewReader(`{"a":[{"x":1},{"x":2},{"x":3,"x":4}]}`), "a", false)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		rec, err := sc.Next()
		if err != nil || rec.Num != i {
			t.Fatalf("record %d: %v, %v", i, rec, err)
		}
	}
	_, err = sc.Next()
	if err == nil || !strings.Contains(err.Error(), `duplicate key "x"`) {
		t.Fatalf("third Next: %v", err)
	}
	if _, again := sc.Next(); !errors.Is(again, err) && errText(again) != errText(err) {
		t.Fatalf("error not sticky: %v then %v", err, again)
	}
	if sc.RecordCount() != 2 {
		t.Fatalf("RecordCount %d, want 2", sc.RecordCount())
	}
}

func TestScannerRejectsUnsupportedSelector(t *testing.T) {
	for _, sel := range []string{"", "/a/b", "a[1]", "@k"} {
		if _, err := (Format{}).NewScanner(strings.NewReader(`{}`), sel, false); err == nil {
			t.Fatalf("selector %q accepted", sel)
		}
	}
}

func TestScannerSizeOnlyCarriesNoBytes(t *testing.T) {
	sc, err := Format{}.NewScanner(strings.NewReader(`[{"a":1},{"a":22}]`), "item", true)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{7, 8} {
		rec, err := sc.Next()
		if err != nil || rec.Raw != nil || rec.SizeBytes != want {
			t.Fatalf("record %+v, %v", rec, err)
		}
	}
}

// TestScannerCaptureStaysBounded scans many small records and requires the
// retained bytes to stay near one record plus the decoder's read-ahead,
// however long the input.
func TestScannerCaptureStaysBounded(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"rows":[`)
	const n = 200000
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"id":%d,"pad":"%s"}`, i, strings.Repeat("x", 32))
	}
	b.WriteString(`]}`)
	src := b.String()
	sc, err := newRecordScanner(strings.NewReader(src), "rows", false)
	if err != nil {
		t.Fatal(err)
	}
	peak, count := 0, 0
	for {
		_, err := sc.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		if l := len(sc.vr.capture.buf); l > peak {
			peak = l
		}
	}
	if count != n {
		t.Fatalf("%d records, want %d", count, n)
	}
	if peak > 128<<10 {
		t.Fatalf("capture peaked at %d bytes over a %d-byte input", peak, len(src))
	}
}

// TestScannerLateFaultInsideOuterRecordYieldsNothingFromIt requires a fault
// inside an open outer record to end the scan without yielding the outer
// record or the inner records that closed before the fault.
func TestScannerLateFaultInsideOuterRecordYieldsNothingFromIt(t *testing.T) {
	src := `{"r":[{"id":1},{"id":2,"r":[{"id":3},{"id":4}],"x":tru}]}`
	recs, err := scanAll(t, strings.NewReader(src), "//r")
	if err == nil || !strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("error = %v", err)
	}
	if len(recs) != 1 || string(recs[0].Raw) != `{"id":1}` {
		t.Fatalf("yielded %d records before the fault", len(recs))
	}
}

// retainedState scans src and reports the largest capture and pending-record
// queue the scanner held, and checks that every nested record shares its
// outer record's bytes.
func retainedState(t *testing.T, src, selector string) (records, peakCapture, peakPending int) {
	t.Helper()
	sc, err := newRecordScanner(strings.NewReader(src), selector, false)
	if err != nil {
		t.Fatal(err)
	}
	var outer []byte
	var outerStart, outerEnd int64
	for {
		if l := len(sc.vr.capture.buf); l > peakCapture {
			peakCapture = l
		}
		if l := len(sc.open); l > peakPending {
			peakPending = l
		}
		rec, err := sc.Next()
		if err == io.EOF {
			return records, peakCapture, peakPending
		}
		if err != nil {
			t.Fatal(err)
		}
		records++
		if outer == nil || rec.StartOffset < outerStart || rec.EndOffset > outerEnd {
			outer, outerStart, outerEnd = rec.Raw, rec.StartOffset, rec.EndOffset
			continue
		}
		// A record nested in the last outer record is a subslice of that
		// record's copy, not a copy of its own.
		base := uintptr(unsafe.Pointer(unsafe.SliceData(outer)))
		p := uintptr(unsafe.Pointer(unsafe.SliceData(rec.Raw)))
		if p < base || p >= base+uintptr(len(outer)) {
			t.Fatalf("record %d was copied instead of shared", rec.Num)
		}
	}
}

func TestScannerNestedRetentionIsBounded(t *testing.T) {
	t.Run("deep", func(t *testing.T) {
		const depth = 900
		src := `{"n":` + strings.Repeat(`{"v":1,"n":`, depth) + `{"v":1}` + strings.Repeat("}", depth) + `}`
		records, peakCapture, peakPending := retainedState(t, src, "//n")
		if records != depth+1 {
			t.Fatalf("%d records, want %d", records, depth+1)
		}
		// The outer record spans the input, so B is the input; pending
		// metadata grows with the nesting depth, and nothing else is kept.
		if peakCapture > len(src)+fillSize || peakPending > depth+1 {
			t.Fatalf("peak capture %d for a %d-byte input, peak pending %d", peakCapture, len(src), peakPending)
		}
		assertParity(t, "deep", []byte(src), "n")
	})
	t.Run("wide", func(t *testing.T) {
		const width = 20000
		var b strings.Builder
		b.WriteString(`{"r":{"r":[`)
		for i := 0; i < width; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":%d}`, i)
		}
		b.WriteString(`]}}`)
		src := b.String()
		records, peakCapture, peakPending := retainedState(t, src, "//r")
		if records != width+1 {
			t.Fatalf("%d records, want %d", records, width+1)
		}
		if peakCapture > len(src)+fillSize || peakPending > width+1 {
			t.Fatalf("peak capture %d for a %d-byte input, peak pending %d", peakCapture, len(src), peakPending)
		}
	})
	t.Run("siblings release as they close", func(t *testing.T) {
		var b strings.Builder
		b.WriteString(`{"rows":[`)
		for i := 0; i < 50000; i++ {
			if i > 0 {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `{"id":%d,"r":{"k":%d}}`, i, i)
		}
		b.WriteString(`]}`)
		_, peakCapture, peakPending := retainedState(t, b.String(), "//rows")
		if peakCapture > 128<<10 || peakPending > 1 {
			t.Fatalf("peak capture %d, peak pending %d", peakCapture, peakPending)
		}
	})
}
