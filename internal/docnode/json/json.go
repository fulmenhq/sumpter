// Package json is the JSON format behind docnode: it parses a whole document
// into an ordered XPath 1.0 tree of root, element, and text nodes. Importing
// it registers the format.
//
// Object members become elements named by their key, verbatim. A member
// whose value is an array becomes one element per item, repeated in source
// order; an empty array yields no element. The items of a top-level array are
// elements named "item". A scalar value becomes a single text child holding
// its string-value (numbers keep their exact source lexeme); null has no text
// child. There are no attribute or namespace nodes.
package json

import (
	"bytes"
	"fmt"
	"io"

	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// Token is the format token for JSON.
const Token = "json"

// ItemName is the element name given to the items of a top-level array.
const ItemName = "item"

func init() {
	docnode.Register(Format{})
}

// Format is the JSON docnode.Format.
type Format struct{}

// Token returns "json".
func (Format) Token() string { return Token }

// Parse parses a whole JSON document. The top-level value must be an object
// or an array.
func (Format) Parse(r io.Reader) (docnode.Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	root, err := parse(data)
	if err != nil {
		return nil, err
	}
	return document{root: root}, nil
}

// ParseRecord parses one record's bytes. A record from a scanner carries its
// element name: the result is a document holding that one element, built
// from the record's value as the whole-document route builds it. A record
// without a name is parsed as a whole document. JSON records carry no
// context; a non-nil Context is a *docnode.ContextError.
func (f Format) ParseRecord(rec *docnode.Record) (docnode.Document, error) {
	return parseRecord(rec)
}

func parseRecord(rec *docnode.Record) (docnode.Document, error) {
	if rec.Context != nil {
		return nil, &docnode.ContextError{Err: fmt.Errorf("unsupported record context type %T", rec.Context)}
	}
	if rec.Name == "" {
		return Format{}.Parse(bytes.NewReader(rec.Raw))
	}
	root, err := parseRecordValue(rec.Raw, rec.Name, rec.StartOffset)
	if err != nil {
		return nil, err
	}
	return document{root: root}, nil
}

// NewScanner scans one JSON document for the elements named by selector
// ("Name" or "//Name"). Each element is one record, in document order;
// records may nest.
func (Format) NewScanner(r io.Reader, selector string, sizeOnly bool) (docnode.RecordScanner, error) {
	return newRecordScanner(r, selector, sizeOnly)
}

// NodeOf returns the node at an iterator position: the element for element
// positions, the text node for text positions, and the root for the root
// position. ok is false only for a navigator that is not a JSON navigator or
// has no current node.
func (Format) NodeOf(nav xpath.NodeNavigator) (docnode.Node, bool) {
	jn, ok := nav.(*navigator)
	if !ok || jn == nil || jn.curr == nil {
		return nil, false
	}
	return wrap(jn.curr), true
}

// RecordDocument returns a document holding a copy of the element n, the
// tree a record-scoped signature is evaluated against on every route.
func (Format) RecordDocument(n docnode.Node) (docnode.Document, error) {
	return recordDocument(n)
}

func recordDocument(n docnode.Node) (docnode.Document, error) {
	var src *jnode
	switch x := n.(type) {
	case node:
		src = x.n
	case scalarNode:
		src = x.n
	}
	if src == nil || src.typ != elementNode {
		return nil, fmt.Errorf("json: record document needs a JSON element, got %T", n)
	}
	root := &jnode{typ: rootNode}
	root.appendChild(copyTree(src))
	return document{root: root}, nil
}

// copyTree returns a detached copy of n and its descendants.
func copyTree(n *jnode) *jnode {
	c := &jnode{typ: n.typ, scalar: n.scalar, kind: n.kind, name: n.name, value: n.value}
	for ch := n.firstChild; ch != nil; ch = ch.next {
		c.appendChild(copyTree(ch))
	}
	return c
}

type document struct {
	root *jnode
}

func (d document) Root() docnode.Node     { return wrap(d.root) }
func (d document) Format() docnode.Format { return Format{} }

// wrap returns the docnode.Node for n: a scalarNode for scalar elements and a
// node otherwise.
func wrap(n *jnode) docnode.Node {
	if n.typ == elementNode && n.scalar {
		return scalarNode{node{n: n}}
	}
	return node{n: n}
}

// node wraps a tree node. It and scalarNode must stay single-pointer structs
// so that storing them in a docnode.Node interface does not allocate.
type node struct {
	n *jnode
}

func (x node) Navigator() xpath.NodeNavigator { return &navigator{root: x.n.root(), curr: x.n} }
func (x node) LocalName() string              { return x.n.name }
func (x node) Text() string                   { return x.n.stringValue() }

func (x node) FirstElementChild() (docnode.Node, bool) {
	return firstElement(x.n.firstChild)
}

func (x node) NextElementSibling() (docnode.Node, bool) {
	return firstElement(x.n.next)
}

// scalarNode is an element holding a scalar value; it implements
// docnode.Scalar.
type scalarNode struct {
	node
}

// Kind returns the scalar kind of the element's value.
func (x scalarNode) Kind() docnode.ScalarKind { return x.n.kind }

// firstElement returns the first element node at or after n among its siblings.
func firstElement(n *jnode) (docnode.Node, bool) {
	for ; n != nil; n = n.next {
		if n.typ == elementNode {
			return wrap(n), true
		}
	}
	return nil, false
}
