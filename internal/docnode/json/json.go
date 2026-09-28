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

// ParseRecord parses one record's bytes. JSON records carry no context; a
// non-nil Context is a *docnode.ContextError.
func (f Format) ParseRecord(rec *docnode.Record) (docnode.Document, error) {
	if rec.Context != nil {
		return nil, &docnode.ContextError{Err: fmt.Errorf("unsupported record context type %T", rec.Context)}
	}
	return f.Parse(bytes.NewReader(rec.Raw))
}

// NewScanner reports that streaming JSON input is not supported.
func (Format) NewScanner(io.Reader, string, bool) (docnode.RecordScanner, error) {
	return nil, fmt.Errorf("json: streaming input is not supported for json in this release; use --allow-large-files for whole-document parsing: %w", docnode.ErrRouteUnsupported)
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
