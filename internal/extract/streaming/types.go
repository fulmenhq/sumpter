package streaming

import (
	"encoding/xml"
)

// RecordScanner scans an XML stream and extracts individual records
// based on a specified XPath selector, enabling constant-memory processing
// of large XML files.
type RecordScanner struct {
	decoder        *xml.Decoder
	recordSelector string      // XPath pattern to match record boundaries (e.g., "//VariationArchive")
	buffer         []xml.Token // Token buffer for current record
	depth          int         // Current nesting depth
	recordDepth    int         // Depth at which we found the record start
	inRecord       bool        // Whether we're currently inside a record
	elementName    string      // Element name we're looking for (extracted from selector)
	recordCount    int         // Number of records scanned so far
	err            error       // Last error encountered
}

// RecordBuffer holds a complete XML record as a string
type RecordBuffer struct {
	XML         string // Raw XML content of the record
	RecordNum   int    // Sequential record number (1-based)
	StartOffset int64  // Byte offset where record started (if available)
}

// ScanResult represents the result of scanning for the next record
type ScanResult struct {
	Record *RecordBuffer
	Error  error
}
