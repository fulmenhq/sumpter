package json

import (
	"strings"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

type nodeType uint8

const (
	rootNode nodeType = iota
	elementNode
	textNode
)

// jnode is one node of the parsed tree.
type jnode struct {
	typ nodeType
	// scalar is set on elements holding a scalar value; kind is then valid.
	scalar bool
	kind   docnode.ScalarKind
	// name is an element's local name; value is a text node's string-value.
	name  string
	value string

	parent, firstChild, lastChild, prev, next *jnode
}

func (n *jnode) appendChild(c *jnode) {
	c.parent = n
	if n.lastChild == nil {
		n.firstChild = c
	} else {
		n.lastChild.next = c
		c.prev = n.lastChild
	}
	n.lastChild = c
}

func (n *jnode) root() *jnode {
	for n.parent != nil {
		n = n.parent
	}
	return n
}

// stringValue is the XPath string-value: all descendant text, concatenated.
func (n *jnode) stringValue() string {
	switch {
	case n.typ == textNode:
		return n.value
	case n.firstChild == nil:
		return ""
	case n.firstChild == n.lastChild && n.firstChild.typ == textNode:
		return n.firstChild.value
	}
	var sb strings.Builder
	n.writeText(&sb)
	return sb.String()
}

func (n *jnode) writeText(sb *strings.Builder) {
	for c := n.firstChild; c != nil; c = c.next {
		if c.typ == textNode {
			sb.WriteString(c.value)
		} else {
			c.writeText(sb)
		}
	}
}
