package json

import "github.com/antchfx/xpath"

// navigator is the xpath.NodeNavigator over a parsed JSON tree.
type navigator struct {
	root, curr *jnode
}

func (x *navigator) NodeType() xpath.NodeType {
	switch x.curr.typ {
	case rootNode:
		return xpath.RootNode
	case textNode:
		return xpath.TextNode
	default:
		return xpath.ElementNode
	}
}

func (x *navigator) LocalName() string { return x.curr.name }

// Prefix is always empty: JSON has no namespaces.
func (x *navigator) Prefix() string { return "" }

// NamespaceURL is always empty: JSON has no namespaces.
func (x *navigator) NamespaceURL() string { return "" }

func (x *navigator) Value() string { return x.curr.stringValue() }

func (x *navigator) Copy() xpath.NodeNavigator {
	c := *x
	return &c
}

func (x *navigator) MoveToRoot() { x.curr = x.root }

func (x *navigator) MoveToParent() bool {
	if p := x.curr.parent; p != nil {
		x.curr = p
		return true
	}
	return false
}

// MoveToNextAttribute always fails: JSON has no attribute nodes.
func (x *navigator) MoveToNextAttribute() bool { return false }

func (x *navigator) MoveToChild() bool {
	if c := x.curr.firstChild; c != nil {
		x.curr = c
		return true
	}
	return false
}

func (x *navigator) MoveToFirst() bool {
	if x.curr.prev == nil {
		return false
	}
	for x.curr.prev != nil {
		x.curr = x.curr.prev
	}
	return true
}

func (x *navigator) MoveToNext() bool {
	if n := x.curr.next; n != nil {
		x.curr = n
		return true
	}
	return false
}

func (x *navigator) MoveToPrevious() bool {
	if p := x.curr.prev; p != nil {
		x.curr = p
		return true
	}
	return false
}

func (x *navigator) MoveTo(other xpath.NodeNavigator) bool {
	o, ok := other.(*navigator)
	if !ok || o.root != x.root {
		return false
	}
	x.curr = o.curr
	return true
}

func (x *navigator) String() string { return x.Value() }
