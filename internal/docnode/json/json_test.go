package json

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

func mustParse(t *testing.T, src string) docnode.Document {
	t.Helper()
	doc, err := Format{}.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return doc
}

// selectNodes maps every position of expr's node set through NodeOf.
func selectNodes(t *testing.T, doc docnode.Document, expr string) []docnode.Node {
	t.Helper()
	iter := xpath.MustCompile(expr).Select(doc.Root().Navigator())
	var out []docnode.Node
	for iter.MoveNext() {
		n, ok := doc.Format().NodeOf(iter.Current())
		if !ok {
			t.Fatalf("NodeOf(%s) returned ok=false", expr)
		}
		out = append(out, n)
	}
	return out
}

func TestRegistered(t *testing.T) {
	f, ok := docnode.Lookup(Token)
	if !ok {
		t.Fatal("json format is not registered")
	}
	if f.Token() != "json" {
		t.Fatalf("token %q", f.Token())
	}
	if _, isFormat := f.(Format); !isFormat {
		t.Fatalf("registered format is %T", f)
	}
	doc := mustParse(t, `{}`)
	if doc.Format().Token() != Token {
		t.Fatalf("document format %q", doc.Format().Token())
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"duplicate key", `{"b":0,"a":1,"a":2}`, []string{`duplicate key "a"`, "byte offset 13"}},
		{"nested duplicate key", `{"o":{"k":1,"k":2}}`, []string{`duplicate key "k"`, "byte offset 12"}},
		{"escaped duplicate key", `{"a\"b":1,"a\"b":2}`, []string{`duplicate key "a\"b"`, "byte offset 10"}},
		{"invalid UTF-8", "{\"s\":\"ab\xffcd\"}", []string{"invalid UTF-8"}},
		{"second top-level value", `{"a":1} {"b":2}`, []string{"unexpected data after top-level value", "byte offset 8"}},
		{"adjacent top-level value", `[1][2]`, []string{"unexpected data after top-level value", "byte offset 3"}},
		{"trailing garbage", `{"a":1} x`, []string{"unexpected data after top-level value"}},
		{"depth exceeded", strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1), []string{"nesting depth exceeds 1024"}},
		{"depth exceeded in object", strings.Repeat(`{"a":`, MaxDepth+1) + "1" + strings.Repeat("}", MaxDepth+1), []string{"nesting depth exceeds 1024"}},
		{"truncated array", `{"a":[1,2`, []string{"unexpected end of JSON input"}},
		{"truncated object", `{"a":`, []string{"unexpected end of JSON input"}},
		{"truncated string", `{"a":"x`, []string{"json:"}},
		{"empty input", ``, []string{"empty input"}},
		{"whitespace input", " \n\t ", []string{"empty input"}},
		{"top-level number", `42`, []string{"top-level value must be an object or array"}},
		{"top-level string", `"x"`, []string{"top-level value must be an object or array"}},
		{"top-level null", `null`, []string{"top-level value must be an object or array"}},
		{"syntax error", `{"a" 1}`, []string{"json:"}},
		{"trailing comma", `[1,]`, []string{"json:"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Format{}.Parse(strings.NewReader(tc.src))
			if err == nil {
				t.Fatalf("Parse(%q) succeeded", tc.src)
			}
			if doc != nil {
				t.Fatal("Parse returned a document with an error")
			}
			if errors.Is(err, docnode.ErrRouteUnsupported) {
				t.Fatal("Parse error must not be ErrRouteUnsupported")
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q does not contain %q", err, w)
				}
			}
		})
	}
}

func TestParseAccepts(t *testing.T) {
	for _, src := range []string{
		strings.Repeat("[", MaxDepth) + strings.Repeat("]", MaxDepth),
		`{"a":1}` + " \n\t\r",
		`[]`,
		`{}`,
		`{"a":{"k":1},"b":{"k":2}}`,
	} {
		if _, err := (Format{}).Parse(strings.NewReader(src)); err != nil {
			t.Fatalf("Parse(%.40q): %v", src, err)
		}
	}
}

func TestNull(t *testing.T) {
	doc := mustParse(t, `{"n":null}`)
	nodes := selectNodes(t, doc, "/n")
	if len(nodes) != 1 {
		t.Fatalf("got %d n elements", len(nodes))
	}
	n := nodes[0]
	s, ok := n.(docnode.Scalar)
	if !ok || s.Kind() != docnode.ScalarNull {
		t.Fatalf("null element: Scalar %v", ok)
	}
	if n.Text() != "" {
		t.Fatalf("null Text %q", n.Text())
	}
	if nav := n.Navigator(); nav.MoveToChild() {
		t.Fatal("null element has a child")
	}
}

func TestScalarKinds(t *testing.T) {
	doc := mustParse(t, `{"r":{"s":"x","e":"","n":-1.5e2,"t":true,"f":false,"z":null,"o":{"k":1},"a":[{"k":2},3]}}`)
	want := map[string]docnode.ScalarKind{
		"s": docnode.ScalarString, "e": docnode.ScalarString, "n": docnode.ScalarNumber,
		"t": docnode.ScalarBool, "f": docnode.ScalarBool, "z": docnode.ScalarNull,
	}
	for name, kind := range want {
		n := selectNodes(t, doc, "/r/"+name)[0]
		s, ok := n.(docnode.Scalar)
		if !ok || s.Kind() != kind {
			t.Fatalf("%s: Scalar %v kind %v, want %v", name, ok, s, kind)
		}
	}
	for _, expr := range []string{"/r", "/r/o", "/r/a[1]"} {
		if _, ok := selectNodes(t, doc, expr)[0].(docnode.Scalar); ok {
			t.Fatalf("%s implements docnode.Scalar", expr)
		}
	}
	if s, ok := selectNodes(t, doc, "/r/a[2]")[0].(docnode.Scalar); !ok || s.Kind() != docnode.ScalarNumber {
		t.Fatal("scalar array item is not a number Scalar")
	}
	if _, ok := doc.Root().(docnode.Scalar); ok {
		t.Fatal("root implements docnode.Scalar")
	}
	if got := selectNodes(t, doc, "/r/n")[0].Text(); got != "-1.5e2" {
		t.Fatalf("number lexeme %q", got)
	}
}

func TestNodeOf(t *testing.T) {
	doc := mustParse(t, `{"Record":[{"id":"r1"},{"id":"r2"}]}`)

	elems := selectNodes(t, doc, "//Record/id")
	if len(elems) != 2 || elems[0].LocalName() != "id" || elems[1].Text() != "r2" {
		t.Fatalf("element positions: %d nodes", len(elems))
	}

	texts := selectNodes(t, doc, "//id/text()")
	if len(texts) != 2 {
		t.Fatalf("text positions: %d nodes, want 2", len(texts))
	}
	for i, want := range []string{"r1", "r2"} {
		n := texts[i]
		if n.LocalName() != "" || n.Text() != want {
			t.Fatalf("text position %d: name %q text %q", i, n.LocalName(), n.Text())
		}
		if _, ok := n.(docnode.Scalar); ok {
			t.Fatal("text node implements docnode.Scalar")
		}
		if n.Navigator().NodeType() != xpath.TextNode {
			t.Fatal("text node navigator is not at a text node")
		}
	}

	roots := selectNodes(t, doc, "/")
	if len(roots) != 1 || roots[0].LocalName() != "" || roots[0].Navigator().NodeType() != xpath.RootNode {
		t.Fatal("root position did not map to the root")
	}
}

func TestNodeOfRejectsForeignOrEmptyNavigator(t *testing.T) {
	var f Format
	if _, ok := f.NodeOf(nil); ok {
		t.Fatal("NodeOf(nil) returned ok=true")
	}
	if _, ok := f.NodeOf(&navigator{}); ok {
		t.Fatal("NodeOf on a navigator with no current node returned ok=true")
	}
	if _, ok := f.NodeOf(foreignNavigator{}); ok {
		t.Fatal("NodeOf on a foreign navigator returned ok=true")
	}
	x, err := xmlquery.Parse(strings.NewReader(`<a/>`))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := f.NodeOf(xmlquery.CreateXPathNavigator(x)); ok {
		t.Fatal("NodeOf on an XML navigator returned ok=true")
	}
}

type foreignNavigator struct{ xpath.NodeNavigator }

func TestNewScanner(t *testing.T) {
	for _, sizeOnly := range []bool{false, true} {
		sc, err := Format{}.NewScanner(strings.NewReader(`[{"a":1},{"a":2}]`), "//item", sizeOnly)
		if err != nil {
			t.Fatalf("NewScanner(sizeOnly=%v): %v", sizeOnly, err)
		}
		for i := 1; i <= 2; i++ {
			rec, err := sc.Next()
			if err != nil || rec.Num != i || rec.Name != ItemName || (rec.Raw == nil) != sizeOnly {
				t.Fatalf("record %d: %+v, %v", i, rec, err)
			}
		}
		if _, err := sc.Next(); err != io.EOF {
			t.Fatalf("after last record: %v", err)
		}
		if sc.RecordCount() != 2 {
			t.Fatalf("RecordCount %d", sc.RecordCount())
		}
		if err := sc.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestParseRecordNamedBuildsRecordElement(t *testing.T) {
	doc, err := Format{}.ParseRecord(&docnode.Record{Raw: []byte(`{"id":"r1","n":[1,2]}`), Name: "Record", StartOffset: 40})
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	if got := selectNodes(t, doc, "/Record/id"); len(got) != 1 || got[0].Text() != "r1" {
		t.Fatal("record element does not hold the value's members")
	}
	if got := selectNodes(t, doc, "/Record/n"); len(got) != 2 {
		t.Fatalf("array member: %d elements", len(got))
	}
	scalar, err := Format{}.ParseRecord(&docnode.Record{Raw: []byte(`2.50`), Name: "qty"})
	if err != nil {
		t.Fatalf("scalar record: %v", err)
	}
	if got := selectNodes(t, scalar, "/qty"); len(got) != 1 || got[0].Text() != "2.50" {
		t.Fatal("scalar record lost its lexeme")
	}
	_, err = Format{}.ParseRecord(&docnode.Record{Raw: []byte(`{"a":1,"a":2}`), Name: "r", StartOffset: 100})
	if err == nil || !strings.Contains(err.Error(), "byte offset 107") {
		t.Fatalf("named record errors must carry file offsets: %v", err)
	}
}

func TestParseRecord(t *testing.T) {
	doc, err := Format{}.ParseRecord(&docnode.Record{Raw: []byte(`{"id":"r1"}`)})
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	if got := selectNodes(t, doc, "/id"); len(got) != 1 || got[0].Text() != "r1" {
		t.Fatal("ParseRecord did not parse the raw bytes")
	}

	var ce *docnode.ContextError
	_, err = Format{}.ParseRecord(&docnode.Record{Raw: []byte(`{}`), Context: "ctx"})
	if !errors.As(err, &ce) || !strings.Contains(ce.Error(), "string") {
		t.Fatalf("non-nil Context: want *docnode.ContextError, got %v", err)
	}

	_, err = Format{}.ParseRecord(&docnode.Record{Raw: []byte(`{`)})
	if err == nil || errors.As(err, &ce) {
		t.Fatalf("parse failure must be a plain error, got %v", err)
	}
}

func TestElementCursor(t *testing.T) {
	doc := mustParse(t, `{"r":{"a":1,"B":{"x":1},"t":["p","q"],"e":[],"z":null}}`)
	r := selectNodes(t, doc, "/r")[0]
	var names []string
	for c, ok := r.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
		names = append(names, c.LocalName())
	}
	if strings.Join(names, ",") != "a,B,t,t,z" {
		t.Fatalf("element children %v", names)
	}
	leaf := selectNodes(t, doc, "/r/a")[0]
	if _, ok := leaf.FirstElementChild(); ok {
		t.Fatal("scalar element reported an element child")
	}
	last := selectNodes(t, doc, "/r/z")[0]
	if _, ok := last.NextElementSibling(); ok {
		t.Fatal("last element reported a next sibling")
	}
	top, ok := doc.Root().FirstElementChild()
	if !ok || top.LocalName() != "r" {
		t.Fatal("root has no element child r")
	}
}

func TestNavigator(t *testing.T) {
	doc := mustParse(t, `{"a":{"b":"1","c":"2"},"d":"3"}`)
	b := selectNodes(t, doc, "/a/b")[0]
	nav := b.Navigator()
	second := b.Navigator()

	if nav.MoveToNextAttribute() {
		t.Fatal("MoveToNextAttribute succeeded")
	}
	if nav.Prefix() != "" || nav.LocalName() != "b" || nav.NodeType() != xpath.ElementNode {
		t.Fatal("navigator not at b")
	}
	if nav.MoveToPrevious() || nav.MoveToFirst() {
		t.Fatal("first sibling moved backwards")
	}
	if !nav.MoveToNext() || nav.LocalName() != "c" || nav.MoveToNext() {
		t.Fatal("MoveToNext")
	}
	if !nav.MoveToFirst() || nav.LocalName() != "b" {
		t.Fatal("MoveToFirst")
	}
	if !nav.MoveToChild() || nav.NodeType() != xpath.TextNode || nav.Value() != "1" || nav.MoveToChild() {
		t.Fatal("MoveToChild to text")
	}
	if second.LocalName() != "b" {
		t.Fatal("moving one navigator moved another")
	}
	cp := nav.Copy()
	if !nav.MoveToParent() || nav.LocalName() != "b" {
		t.Fatal("MoveToParent from text")
	}
	if !nav.MoveToParent() || nav.Value() != "12" {
		t.Fatalf("MoveToParent value %q", nav.Value())
	}
	if cp.NodeType() != xpath.TextNode {
		t.Fatal("Copy shares position")
	}
	nav.MoveToRoot()
	if nav.NodeType() != xpath.RootNode || nav.MoveToParent() || nav.Value() != "123" {
		t.Fatal("MoveToRoot")
	}
	if !nav.MoveTo(cp) || nav.Value() != "1" {
		t.Fatal("MoveTo same tree")
	}
	other := mustParse(t, `{"a":1}`).Root().Navigator()
	if nav.MoveTo(other) || nav.MoveTo(foreignNavigator{}) {
		t.Fatal("MoveTo across trees succeeded")
	}
}

func TestLargeNumberNotFloat(t *testing.T) {
	doc := mustParse(t, `[9007199254740993, 1.0, 1e400]`)
	var got []string
	for _, n := range selectNodes(t, doc, "/item") {
		got = append(got, n.Text())
	}
	if strings.Join(got, ",") != "9007199254740993,1.0,1e400" {
		t.Fatalf("lexemes %v", got)
	}
}

func TestAllocations(t *testing.T) {
	doc := mustParse(t, `{"r":{"a":"x","o":{"k":1}}}`)
	format := doc.Format()
	for _, expr := range []string{"/r/a", "/r/o", "/r/a/text()"} {
		iter := xpath.MustCompile(expr).Select(doc.Root().Navigator())
		if !iter.MoveNext() {
			t.Fatalf("%s selected nothing", expr)
		}
		nav := iter.Current()
		if n := testing.AllocsPerRun(1000, func() { _, _ = format.NodeOf(nav) }); n != 0 {
			t.Fatalf("NodeOf(%s) allocates %.0f per call, want 0", expr, n)
		}
	}
	r := selectNodes(t, doc, "/r")[0]
	if n := testing.AllocsPerRun(1000, func() {
		for c, ok := r.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
			_ = c.LocalName()
		}
	}); n != 0 {
		t.Fatalf("element cursor allocates %.0f per walk, want 0", n)
	}
	a := selectNodes(t, doc, "/r/a")[0]
	if n := testing.AllocsPerRun(1000, func() { _ = a.Text() }); n != 0 {
		t.Fatalf("scalar Text allocates %.0f, want 0", n)
	}
}
