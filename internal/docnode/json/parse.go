package json

import (
	"bytes"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// MaxDepth is the deepest container nesting Parse and Walk accept.
const MaxDepth = 1024

// parse builds the tree for one JSON document through the same validating
// reader and walker as Walk, so both report the same first fault. It never
// returns a partial tree: any error discards it.
func parse(data []byte) (*jnode, error) {
	b := &treeBuilder{root: &jnode{typ: rootNode}}
	b.cur = b.root
	if err := walk(newValidatingReader(bytes.NewReader(data)), b); err != nil {
		return nil, err
	}
	return b.root, nil
}

// treeBuilder is the Handler that builds the navigable tree.
type treeBuilder struct {
	root, cur *jnode
}

func (b *treeBuilder) StartElement(name string, kind Kind) error {
	el := &jnode{typ: elementNode, name: name}
	switch kind {
	case KindString:
		el.scalar, el.kind = true, docnode.ScalarString
	case KindNumber:
		el.scalar, el.kind = true, docnode.ScalarNumber
	case KindBool:
		el.scalar, el.kind = true, docnode.ScalarBool
	case KindNull:
		el.scalar, el.kind = true, docnode.ScalarNull
	}
	b.cur.appendChild(el)
	b.cur = el
	return nil
}

func (b *treeBuilder) Text(value string) error {
	b.cur.appendChild(&jnode{typ: textNode, value: value})
	return nil
}

func (b *treeBuilder) EndElement() error {
	b.cur = b.cur.parent
	return nil
}
