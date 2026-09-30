// Package xml is the XML format behind docnode: it parses with xmlquery and
// scans with the streaming record scanner. Importing it registers the format.
package xml

import (
	"bytes"
	"fmt"
	"io"

	"github.com/antchfx/xmlquery"
	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// Token is the format token for XML.
const Token = "xml"

func init() {
	docnode.Register(Format{})
}

// Format is the XML docnode.Format.
type Format struct{}

// Token returns "xml".
func (Format) Token() string { return Token }

// Parse parses a whole XML document.
func (f Format) Parse(r io.Reader) (docnode.Document, error) {
	root, err := xmlquery.Parse(r)
	if err != nil {
		return nil, err
	}
	return document{root: root}, nil
}

// ParseRecord parses one record's bytes. When rec.Context holds index
// namespace declarations, the declarations missing from the record's root
// start tag are added to it first; a failure to do so is a
// *docnode.ContextError. A nil Context parses the bytes as they are.
func (f Format) ParseRecord(rec *docnode.Record) (docnode.Document, error) {
	data := rec.Raw
	switch ctx := rec.Context.(type) {
	case nil:
	case []NamespaceDeclaration:
		injected, err := injectNamespaceContext(data, ctx)
		if err != nil {
			return nil, &docnode.ContextError{Err: err}
		}
		data = injected
	default:
		return nil, &docnode.ContextError{Err: fmt.Errorf("unsupported record context type %T", rec.Context)}
	}
	return f.Parse(bytes.NewReader(data))
}

// NewScanner returns the streaming XML record scanner.
func (Format) NewScanner(r io.Reader, selector string, sizeOnly bool) (docnode.RecordScanner, error) {
	if sizeOnly {
		return streaming.NewRecordScannerSizeOnly(r, selector), nil
	}
	return streaming.NewRecordScanner(r, selector), nil
}

// NodeOf returns the node at an iterator position: the element for element
// positions, the owning element for attribute positions, and the text node
// for text positions. ok is false only for a navigator that is not an XML
// navigator or has no current node.
func (Format) NodeOf(nav xpath.NodeNavigator) (docnode.Node, bool) {
	xn, ok := nav.(*xmlquery.NodeNavigator)
	if !ok || xn == nil {
		return nil, false
	}
	current := xn.Current()
	if current == nil {
		return nil, false
	}
	return node{n: current}, true
}

type document struct {
	root *xmlquery.Node
}

func (d document) Root() docnode.Node     { return node{n: d.root} }
func (d document) Format() docnode.Format { return Format{} }

// node wraps an xmlquery node. It must stay a single-pointer struct so that
// storing it in a docnode.Node interface does not allocate.
type node struct {
	n *xmlquery.Node
}

func (x node) Navigator() xpath.NodeNavigator { return xmlquery.CreateXPathNavigator(x.n) }
func (x node) LocalName() string              { return x.n.Data }
func (x node) Text() string                   { return x.n.InnerText() }

func (x node) FirstElementChild() (docnode.Node, bool) {
	return firstElement(x.n.FirstChild)
}

func (x node) NextElementSibling() (docnode.Node, bool) {
	return firstElement(x.n.NextSibling)
}

// firstElement returns the first element node at or after n among its siblings.
func firstElement(n *xmlquery.Node) (docnode.Node, bool) {
	for ; n != nil; n = n.NextSibling {
		if n.Type == xmlquery.ElementNode {
			return node{n: n}, true
		}
	}
	return nil, false
}

// XMLNode returns the xmlquery node behind a docnode.Node produced by this
// format, for XML-specific callers such as tests.
func XMLNode(n docnode.Node) (*xmlquery.Node, bool) {
	x, ok := n.(node)
	return x.n, ok
}
