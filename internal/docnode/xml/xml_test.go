package xml

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/index"
)

const sample = `<root><rec id="r1"><item a="1">t1</item><item a="2">t2</item><!--c--><note>n</note></rec></root>`

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

func TestRegisteredAsDefault(t *testing.T) {
	f, ok := docnode.Lookup(docnode.Default)
	if !ok {
		t.Fatal("xml format is not registered under docnode.Default")
	}
	if f.Token() != Token || Token != docnode.Default {
		t.Fatalf("token %q, want %q", f.Token(), docnode.Default)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("registering xml twice did not panic")
		}
	}()
	docnode.Register(Format{})
}

func TestParseMatchesXMLQuery(t *testing.T) {
	doc := mustParse(t, sample)
	want, err := xmlquery.Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("xmlquery.Parse: %v", err)
	}
	got, ok := XMLNode(doc.Root())
	if !ok {
		t.Fatal("root is not an XML node")
	}
	if got.OutputXML(true) != want.OutputXML(true) {
		t.Fatalf("Parse differs from xmlquery.Parse:\n got %s\nwant %s", got.OutputXML(true), want.OutputXML(true))
	}
	if doc.Format().Token() != Token {
		t.Fatalf("document format %q", doc.Format().Token())
	}
}

func TestNodeAccessorsMatchXMLQuery(t *testing.T) {
	doc := mustParse(t, sample)
	rec := selectNodes(t, doc, "//rec")[0]
	raw, _ := XMLNode(rec)
	if rec.LocalName() != raw.Data || rec.LocalName() != "rec" {
		t.Fatalf("LocalName %q, Data %q", rec.LocalName(), raw.Data)
	}
	if rec.Text() != raw.InnerText() || rec.Text() != "t1t2n" {
		t.Fatalf("Text %q, InnerText %q", rec.Text(), raw.InnerText())
	}
	ns := mustParse(t, `<p:a xmlns:p="urn:x"><p:b>v</p:b></p:a>`)
	if got := ns.Root().Navigator(); got == nil {
		t.Fatal("nil navigator")
	}
	b := selectNodes(t, ns, "//*[local-name()='b']")[0]
	if b.LocalName() != "b" {
		t.Fatalf("prefixed element LocalName %q, want b (unprefixed)", b.LocalName())
	}
}

func TestNavigatorIsFreshPerCall(t *testing.T) {
	doc := mustParse(t, sample)
	rec := selectNodes(t, doc, "//rec")[0]
	first := rec.Navigator()
	second := rec.Navigator()
	if !first.MoveToChild() {
		t.Fatal("MoveToChild failed")
	}
	if second.LocalName() != "rec" {
		t.Fatalf("moving one navigator moved another: second at %q", second.LocalName())
	}
	if rec.Navigator().LocalName() != "rec" {
		t.Fatal("a new navigator does not start at the node")
	}
}

// NodeOf reproduces the cast-back in collectNodes on 2769a91: element
// positions return the element, attribute positions the owning element, and
// text positions the text node.
func TestNodeOfMatchesCastBack(t *testing.T) {
	doc := mustParse(t, sample)

	elems := selectNodes(t, doc, "//rec/item")
	if len(elems) != 2 || elems[0].LocalName() != "item" || elems[0].Text() != "t1" {
		t.Fatalf("element positions: %d nodes", len(elems))
	}

	attrs := selectNodes(t, doc, "//rec/item/@a")
	if len(attrs) != 2 {
		t.Fatalf("attribute positions: %d nodes, want 2", len(attrs))
	}
	for i, want := range []string{"t1", "t2"} {
		raw, _ := XMLNode(attrs[i])
		if raw.Type != xmlquery.ElementNode || attrs[i].LocalName() != "item" || attrs[i].Text() != want {
			t.Fatalf("attribute position %d: got %v %q %q, want owning element with text %q", i, raw.Type, attrs[i].LocalName(), attrs[i].Text(), want)
		}
	}

	texts := selectNodes(t, doc, "//rec/item/text()")
	if len(texts) != 2 {
		t.Fatalf("text positions: %d nodes, want 2", len(texts))
	}
	for i, want := range []string{"t1", "t2"} {
		raw, _ := XMLNode(texts[i])
		if raw.Type != xmlquery.TextNode || texts[i].Text() != want {
			t.Fatalf("text position %d: type %v text %q, want text node %q", i, raw.Type, texts[i].Text(), want)
		}
	}
}

func TestNodeOfRejectsForeignOrEmptyNavigator(t *testing.T) {
	var f Format
	if _, ok := f.NodeOf(nil); ok {
		t.Fatal("NodeOf(nil) returned ok=true")
	}
	if _, ok := f.NodeOf(xmlquery.CreateXPathNavigator(nil)); ok {
		t.Fatal("NodeOf on a navigator with no current node returned ok=true")
	}
	if _, ok := f.NodeOf(foreignNavigator{}); ok {
		t.Fatal("NodeOf on a foreign navigator returned ok=true")
	}
}

type foreignNavigator struct{ xpath.NodeNavigator }

func TestXMLNodesDoNotImplementScalar(t *testing.T) {
	doc := mustParse(t, sample)
	if _, ok := doc.Root().(docnode.Scalar); ok {
		t.Fatal("XML nodes must not implement docnode.Scalar")
	}
}

func TestParseRecordNilContextParsesUnchanged(t *testing.T) {
	raw := []byte(`<p:item xmlns:p="urn:x">v</p:item>`)
	doc, err := Format{}.ParseRecord(&docnode.Record{Raw: raw})
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	want, _ := xmlquery.Parse(bytes.NewReader(raw))
	got, _ := XMLNode(doc.Root())
	if got.OutputXML(true) != want.OutputXML(true) {
		t.Fatalf("nil Context changed the parse")
	}
}

// ParseRecord with index namespace declarations applies the same byte splice
// the indexed route applied before parsing.
func TestParseRecordAppliesNamespaceSplice(t *testing.T) {
	raw := []byte(`<p:item attr='x>y'>v</p:item>`)
	decls := []index.NamespaceDeclaration{{Prefix: "p", URI: "urn:x"}, {Prefix: "", URI: "urn:default"}}

	spliced, err := injectNamespaceContext(raw, decls)
	if err != nil {
		t.Fatalf("injectNamespaceContext: %v", err)
	}
	doc, err := Format{}.ParseRecord(&docnode.Record{Raw: raw, Context: decls})
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	want, _ := xmlquery.Parse(bytes.NewReader(spliced))
	got, _ := XMLNode(doc.Root())
	if got.OutputXML(true) != want.OutputXML(true) {
		t.Fatalf("ParseRecord did not apply the splice:\n got %s\nwant %s", got.OutputXML(true), want.OutputXML(true))
	}
	if !strings.Contains(string(spliced), `xmlns:p="urn:x"`) {
		t.Fatalf("splice missing declaration: %s", spliced)
	}
}

func TestParseRecordContextFailures(t *testing.T) {
	var ce *docnode.ContextError

	_, err := Format{}.ParseRecord(&docnode.Record{Raw: []byte(`<a/>`), Context: "not declarations"})
	if !errors.As(err, &ce) {
		t.Fatalf("wrong Context type: want *docnode.ContextError, got %v", err)
	}

	decls := []index.NamespaceDeclaration{{Prefix: "p", URI: "urn:x"}}
	_, err = Format{}.ParseRecord(&docnode.Record{Raw: []byte(`no element here`), Context: decls})
	if !errors.As(err, &ce) || ce.Error() != "record fragment has no root element" {
		t.Fatalf("splice failure: want ContextError with the splice text, got %v", err)
	}

	_, err = Format{}.ParseRecord(&docnode.Record{Raw: []byte(`<a><b></a>`)})
	if err == nil || errors.As(err, &ce) {
		t.Fatalf("parse failure must be a plain error, got %v", err)
	}
}

func TestNewScanner(t *testing.T) {
	src := `<root><rec>1</rec><rec>2</rec><rec>3</rec></root>`
	for _, sizeOnly := range []bool{false, true} {
		sc, err := Format{}.NewScanner(strings.NewReader(src), "//rec", sizeOnly)
		if err != nil {
			t.Fatalf("NewScanner(sizeOnly=%v): %v", sizeOnly, err)
		}
		n := 0
		for {
			rec, err := sc.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("Next: %v", err)
			}
			n++
			if rec.Num != n || rec.Name != "rec" {
				t.Fatalf("record %d: Num %d Name %q", n, rec.Num, rec.Name)
			}
			if sizeOnly && rec.Raw != nil {
				t.Fatalf("size-only record carries Raw bytes")
			}
			if !sizeOnly && !strings.Contains(string(rec.Raw), "<rec>") {
				t.Fatalf("record Raw = %q", rec.Raw)
			}
		}
		if n != 3 || sc.RecordCount() != 3 || sc.BytesRead() == 0 {
			t.Fatalf("sizeOnly=%v: scanned %d, RecordCount %d, BytesRead %d", sizeOnly, n, sc.RecordCount(), sc.BytesRead())
		}
		if err := sc.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}
}

func TestXMLNeverReturnsRouteUnsupported(t *testing.T) {
	var f Format
	for _, src := range []string{sample, `<a><b></a>`} {
		if _, err := f.Parse(strings.NewReader(src)); errors.Is(err, docnode.ErrRouteUnsupported) {
			t.Fatalf("Parse returned ErrRouteUnsupported")
		}
		if _, err := f.ParseRecord(&docnode.Record{Raw: []byte(src)}); errors.Is(err, docnode.ErrRouteUnsupported) {
			t.Fatalf("ParseRecord returned ErrRouteUnsupported")
		}
	}
	for _, sizeOnly := range []bool{false, true} {
		if _, err := f.NewScanner(strings.NewReader(sample), "//rec", sizeOnly); err != nil {
			t.Fatalf("NewScanner: %v", err)
		}
	}
}

func TestAllocationParity(t *testing.T) {
	doc := mustParse(t, sample)
	rec := selectNodes(t, doc, "//rec")[0]
	raw, _ := XMLNode(rec)
	format := doc.Format()
	expr := xpath.MustCompile("string(item[1]/@a)")

	today := testing.AllocsPerRun(1000, func() { _ = expr.Evaluate(xmlquery.CreateXPathNavigator(raw)) })
	seam := testing.AllocsPerRun(1000, func() { _ = expr.Evaluate(rec.Navigator()) })
	if seam > today {
		t.Fatalf("Navigator() evaluation allocates %.0f, CreateXPathNavigator %.0f", seam, today)
	}

	nav := xmlquery.CreateXPathNavigator(raw)
	if n := testing.AllocsPerRun(1000, func() { _, _ = format.NodeOf(nav) }); n != 0 {
		t.Fatalf("NodeOf allocates %.0f per call, want 0", n)
	}
}

// ParseRecord with a reused Record allocates per call exactly what parsing the
// same bytes directly does, for records of any size: the reused Record adds
// nothing per record.
func TestParseRecordAddsNoPerRecordAllocations(t *testing.T) {
	var f Format
	var reused docnode.Record
	for _, items := range []int{1, 10, 100} {
		raw := []byte("<rec>" + strings.Repeat("<item a=\"1\">t</item>", items) + "</rec>")
		direct := testing.AllocsPerRun(200, func() {
			if _, err := f.Parse(bytes.NewReader(raw)); err != nil {
				t.Fatal(err)
			}
		})
		viaRecord := testing.AllocsPerRun(200, func() {
			reused.Raw = raw
			reused.Num++
			if _, err := f.ParseRecord(&reused); err != nil {
				t.Fatal(err)
			}
		})
		if viaRecord != direct {
			t.Fatalf("%d items: ParseRecord allocates %.0f per call, direct parse %.0f", items, viaRecord, direct)
		}
	}
}

func TestElementCursor(t *testing.T) {
	doc := mustParse(t, `<r>text<a/><!--c--><B x="1"/>more<a>in<i/></a></r>`)
	r := selectNodes(t, doc, "/r")[0]
	var names []string
	for c, ok := r.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
		names = append(names, c.LocalName())
	}
	if strings.Join(names, ",") != "a,B,a" {
		t.Fatalf("element children %v, want [a B a] in document order, unfiltered", names)
	}
	leaf := selectNodes(t, doc, "/r/B")[0]
	if _, ok := leaf.FirstElementChild(); ok {
		t.Fatal("element with no element children reported one")
	}
	last := selectNodes(t, doc, "/r/a[2]")[0]
	if _, ok := last.NextElementSibling(); ok {
		t.Fatal("last element reported a next sibling")
	}
}

// walkByName is the extractor's element walk, called through the interface.
//
//go:noinline
func walkByName(n docnode.Node, name string) []docnode.Node {
	var result []docnode.Node
	for c, ok := n.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
		if strings.EqualFold(c.LocalName(), name) {
			result = append(result, c)
		}
	}
	return result
}

// pointerWalkByName is the pre-seam walk over xmlquery nodes.
//
//go:noinline
func pointerWalkByName(n *xmlquery.Node, name string) []*xmlquery.Node {
	var result []*xmlquery.Node
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == xmlquery.ElementNode && strings.EqualFold(c.Data, name) {
			result = append(result, c)
		}
	}
	return result
}

var (
	walkSinkNode []docnode.Node
	walkSinkRaw  []*xmlquery.Node
)

// The element cursor allocates exactly what the pointer walk did, with and
// without matches, when called through the docnode.Node interface.
func TestElementCursorAllocationParity(t *testing.T) {
	doc := mustParse(t, `<r><a/><B/><a/><c/></r>`)
	n := selectNodes(t, doc, "/r")[0] // a docnode.Node: the walk goes through the interface
	raw, _ := XMLNode(n)
	for _, name := range []string{"a", "zzz"} {
		before := testing.AllocsPerRun(1000, func() { walkSinkRaw = pointerWalkByName(raw, name) })
		after := testing.AllocsPerRun(1000, func() { walkSinkNode = walkByName(n, name) })
		if after != before {
			t.Fatalf("name %q: cursor walk allocates %.0f, pointer walk %.0f", name, after, before)
		}
	}
}
