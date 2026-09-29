// Package docnode is the format-neutral view of a parsed input document that
// the extraction engine works against. A Format parses one input syntax into
// Documents and scans streams into Records; everything else in the engine
// reaches a document only through Node and XPath navigators.
package docnode

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/antchfx/xpath"
)

// Node is one position in a parsed document. Everything the extractor needs
// beyond XPath evaluation is here; nothing else may reach into a format's tree.
type Node interface {
	// Navigator returns a fresh navigator positioned at this node. Each call
	// returns a new navigator; do not share one across goroutines.
	Navigator() xpath.NodeNavigator
	// LocalName is the element (or key) name without any prefix.
	LocalName() string
	// Text is the XPath string-value: all descendant text, concatenated.
	Text() string
	// FirstElementChild returns the first element child in document order.
	FirstElementChild() (Node, bool)
	// NextElementSibling returns the next element sibling in document order.
	NextElementSibling() (Node, bool)
}

// ScalarKind classifies a scalar value in formats whose values are typed.
type ScalarKind int

// Scalar kinds.
const (
	ScalarString ScalarKind = iota
	ScalarNumber
	ScalarBool
	ScalarNull
)

// Scalar is an optional Node capability for formats whose values carry a
// scalar kind. XML nodes do not implement it; callers must behave as they do
// for XML when a node does not.
type Scalar interface {
	Kind() ScalarKind
}

// ErrRouteUnsupported is returned, wrapped, by Parse, ParseRecord, or
// NewScanner when a format does not support that route. Callers report it as
// an error and never fall back to another route.
var ErrRouteUnsupported = errors.New("docnode: route not supported by format")

// ContextError reports that a record's scan context could not be applied
// before parsing. It keeps the underlying error's text unchanged.
type ContextError struct {
	Err error
}

func (e *ContextError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying error.
func (e *ContextError) Unwrap() error { return e.Err }

// Document is a parsed input: a whole document or one record.
type Document interface {
	Root() Node
	// Format is the Format that produced the document.
	Format() Format
}

// Record is one record found by a RecordScanner.
type Record struct {
	// Raw is the record's bytes (nil when the scanner runs in size-only mode).
	Raw []byte
	// Num is the 1-based record number.
	Num         int
	StartOffset int64
	EndOffset   int64
	SizeBytes   int64
	// Name is the record's element or key name. It may be empty for formats
	// whose records have no name.
	Name string
	// Depth is the record's nesting depth; its meaning is format-defined.
	Depth int
	// Context is format-owned scan context, read only by that format.
	Context any
}

// RecordScanner yields the records of a stream in order.
type RecordScanner interface {
	// Next returns the next record, or io.EOF when the stream is exhausted.
	Next() (*Record, error)
	RecordCount() int
	BytesRead() int64
	Close() error
}

// Format is one input syntax behind the engine.
type Format interface {
	Token() string
	// Parse parses a whole document.
	Parse(r io.Reader) (Document, error)
	// ParseRecord parses one record, applying rec.Context. A failure to apply
	// the context is returned as a *ContextError.
	ParseRecord(rec *Record) (Document, error)
	// NewScanner scans r for records matching selector. In size-only mode the
	// records carry no Raw bytes.
	NewScanner(r io.Reader, selector string, sizeOnly bool) (RecordScanner, error)
	// NodeOf maps an iterator position back to a Node, matching a cast of the
	// navigator's current node: an element position gives that element, an
	// attribute position gives its owning element, and a text position gives
	// the text node. ok is false only when nav belongs to another format or
	// has no current node.
	NodeOf(nav xpath.NodeNavigator) (Node, bool)
}

// RecordDocumenter is an optional Format capability. RecordDocument returns a
// new document whose only element is a copy of n: the same tree ParseRecord
// builds from that element's scanned bytes. n must come from a document of
// the same format.
type RecordDocumenter interface {
	RecordDocument(n Node) (Document, error)
}

// Default is the format token used when none is selected.
const Default = "xml"

var (
	registryMu sync.RWMutex
	registry   = map[string]Format{}
)

// Register adds a format. It panics if the token is already registered.
func Register(f Format) {
	registryMu.Lock()
	defer registryMu.Unlock()
	token := f.Token()
	if _, dup := registry[token]; dup {
		panic(fmt.Sprintf("docnode: format %q registered twice", token))
	}
	registry[token] = f
}

// Lookup returns the format registered under token.
func Lookup(token string) (Format, bool) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	f, ok := registry[token]
	return f, ok
}
