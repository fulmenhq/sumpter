package streaming

import (
	"encoding/xml"
	"io"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// RecordScanner scans an XML stream and extracts individual records
// based on a specified XPath selector, enabling constant-memory processing
// of large XML files.
type RecordScanner struct {
	decoder        *xml.Decoder
	reader         *countingReader // Counting reader to track byte position
	recordSelector string          // XPath pattern to match record boundaries (e.g., "//VariationArchive")
	buffer         []xml.Token     // Token buffer for current record
	depth          int             // Current nesting depth
	recordDepth    int             // Depth at which we found the record start
	inRecord       bool            // Whether we're currently inside a record
	elementName    string          // Element name we're looking for (extracted from selector)
	recordCount    int             // Number of records scanned so far
	err            error           // Last error encountered
	sizeOnly       bool            // If true, skip buffering and serialization for size-only analysis
	namespaceStack []map[string]string
}

// countingReader wraps an io.Reader to track bytes read
type countingReader struct {
	r     io.Reader
	count int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.count += int64(n)
	return n, err
}

// Bytes returns the total number of bytes read so far
func (c *countingReader) Bytes() int64 {
	return c.count
}

// Records are returned as *docnode.Record: Raw holds the record's XML (nil in
// size-only mode), Name the record element name, Depth the element's nesting
// depth (1 = top-level), and Context the []NamespaceDeclaration in scope at
// the record root.

// NamespaceDeclaration records one in-scope XML namespace declaration. Prefix is
// empty for the default namespace.
type NamespaceDeclaration struct {
	Prefix string
	URI    string
}

// ScanResult represents the result of scanning for the next record
type ScanResult struct {
	Record *docnode.Record
	Error  error
}
