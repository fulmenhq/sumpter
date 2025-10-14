package streaming

import (
	"encoding/xml"
	"io"
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

// RecordBuffer holds a complete XML record as a string
type RecordBuffer struct {
	XML         string // Raw XML content of the record (empty in size-only mode)
	RecordNum   int    // Sequential record number (1-based)
	StartOffset int64  // Byte offset where record started (if available)
	EndOffset   int64  // Byte offset where record ended (if available)
	SizeBytes   int64  // Size of the record in bytes
	ElementName string // Name of the root element for this record
	Depth       int    // Nesting depth of the record element (1 = top-level)
}

// ScanResult represents the result of scanning for the next record
type ScanResult struct {
	Record *RecordBuffer
	Error  error
}
