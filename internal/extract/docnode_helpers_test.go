package extract

import (
	"io"

	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// parseXMLDoc parses a test document through the registered input format.
func parseXMLDoc(r io.Reader) (docnode.Document, error) {
	format, err := inputFormat(nil)
	if err != nil {
		return nil, err
	}
	return format.Parse(r)
}

// findNode returns the first node matching expr, or nil.
func findNode(doc docnode.Document, expr string) docnode.Node {
	iter := xpath.MustCompile(expr).Select(doc.Root().Navigator())
	if !iter.MoveNext() {
		return nil
	}
	node, _ := doc.Format().NodeOf(iter.Current())
	return node
}

// subDoc presents a node of doc as a document of its own, as the parallel
// route presents each parsed record.
func subDoc(doc docnode.Document, node docnode.Node) docnode.Document {
	return nodeDocument{root: node, format: doc.Format()}
}

type nodeDocument struct {
	root   docnode.Node
	format docnode.Format
}

func (d nodeDocument) Root() docnode.Node     { return d.root }
func (d nodeDocument) Format() docnode.Format { return d.format }
