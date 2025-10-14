package streaming

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"golang.org/x/net/html/charset"
)

// NewRecordScanner creates a new scanner for streaming XML records.
// The recordSelector should be an XPath expression like "//RecordElement"
// which will be simplified to just match the element name.
//
// Example:
//
//	scanner := NewRecordScanner(reader, "//VariationArchive")
//	for {
//	    record, err := scanner.Next()
//	    if err == io.EOF {
//	        break
//	    }
//	    // Process record.XML
//	}
func NewRecordScanner(reader io.Reader, recordSelector string) *RecordScanner {
	return newRecordScanner(reader, recordSelector, false)
}

// NewRecordScannerSizeOnly creates a scanner that only tracks record sizes and offsets
// without buffering or serializing XML content. This enables constant-memory analysis
// of large XML files for record boundary detection.
func NewRecordScannerSizeOnly(reader io.Reader, recordSelector string) *RecordScanner {
	return newRecordScanner(reader, recordSelector, true)
}

// NewRecordScannerSizeOnlyWithEncoding creates a size-only scanner with encoding support
// for proper offset tracking in transcoded streams.
func NewRecordScannerSizeOnlyWithEncoding(reader io.Reader, recordSelector string, encoding string) *RecordScanner {
	scanner := newRecordScanner(reader, recordSelector, true)
	// Set CharsetReader to handle encoding internally, ensuring InputOffset tracks raw bytes
	scanner.decoder.CharsetReader = charset.NewReaderLabel
	return scanner
}

func newRecordScanner(reader io.Reader, recordSelector string, sizeOnly bool) *RecordScanner {
	// Extract element name from XPath selector
	// For now, we support simple patterns like "//ElementName" or "/path/to/ElementName"
	elementName := extractElementName(recordSelector)

	// Wrap reader to track byte position
	cr := &countingReader{r: reader, count: 0}

	return &RecordScanner{
		decoder:        xml.NewDecoder(cr),
		reader:         cr,
		recordSelector: recordSelector,
		elementName:    elementName,
		buffer:         make([]xml.Token, 0, 128),
		depth:          0,
		recordDepth:    -1,
		inRecord:       false,
		recordCount:    0,
		sizeOnly:       sizeOnly,
	}
}

// extractElementName extracts the element name from an XPath selector.
// Supports patterns like:
//
//	"//ElementName" -> "ElementName"
//	"/root/child/ElementName" -> "ElementName"
//	"ElementName" -> "ElementName"
func extractElementName(selector string) string {
	selector = strings.TrimSpace(selector)

	// Remove leading "//" or "/"
	selector = strings.TrimPrefix(selector, "//")
	selector = strings.TrimPrefix(selector, "/")

	// Get the last path component
	parts := strings.Split(selector, "/")
	if len(parts) > 0 {
		elementName := parts[len(parts)-1]

		// Remove predicates like [1] or [@attr='value']
		if idx := strings.Index(elementName, "["); idx >= 0 {
			elementName = elementName[:idx]
		}

		// If a namespace prefix is present (e.g., ns:Record), drop the prefix
		if colon := strings.Index(elementName, ":"); colon >= 0 {
			elementName = elementName[colon+1:]
		}

		return strings.TrimSpace(elementName)
	}

	return selector
}

// Next returns the next record from the XML stream.
// Returns io.EOF when no more records are available.
// Returns other errors if XML parsing fails.
func (s *RecordScanner) Next() (*RecordBuffer, error) {
	// If we had a previous error, return it
	if s.err != nil {
		return nil, s.err
	}

	// Reset buffer for next record
	s.buffer = s.buffer[:0]
	s.inRecord = false
	s.recordDepth = -1
	var recordStartOffset int64 // Track XML stream offset when entering record

	for {
		// Capture XML stream offset before reading next token
		offsetBeforeToken := s.decoder.InputOffset()

		token, err := s.decoder.Token()
		if err != nil {
			if err == io.EOF {
				// If we were in a record, return incomplete record error
				if s.inRecord {
					s.err = fmt.Errorf("unexpected EOF while scanning record")
					return nil, s.err
				}
				s.err = io.EOF
				return nil, io.EOF
			}
			s.err = fmt.Errorf("failed to read XML token: %w", err)
			return nil, s.err
		}

		switch t := token.(type) {
		case xml.StartElement:
			s.depth++

			// Check if this is the start of a record we're looking for
			if !s.inRecord && s.matchesRecordElement(t.Name.Local) {
				s.inRecord = true
				s.recordDepth = s.depth
				s.recordCount++
				// Use XML stream offset from BEFORE this token was read
				recordStartOffset = offsetBeforeToken
			}

			// If we're in a record, buffer this token (unless size-only mode)
			if s.inRecord && !s.sizeOnly {
				s.buffer = append(s.buffer, xml.CopyToken(token))
			}

		case xml.EndElement:
			// If we're in a record, buffer this token (unless size-only mode)
			if s.inRecord && !s.sizeOnly {
				s.buffer = append(s.buffer, xml.CopyToken(token))
			}

			// Check if this closes the current record
			if s.inRecord && s.depth == s.recordDepth {
				var recordXML string
				if !s.sizeOnly {
					// We've completed a record - serialize and return it
					var err error
					recordXML, err = s.serializeTokens()
					if err != nil {
						s.err = fmt.Errorf("failed to serialize record %d: %w", s.recordCount, err)
						return nil, s.err
					}
				}

				// Capture XML stream offset at end of record (after reading end tag)
				endOffset := s.decoder.InputOffset()
				sizeBytes := endOffset - recordStartOffset

				result := &RecordBuffer{
					XML:         recordXML,
					RecordNum:   s.recordCount,
					StartOffset: recordStartOffset,
					EndOffset:   endOffset,
					SizeBytes:   sizeBytes,
					ElementName: s.elementName,
					Depth:       s.recordDepth,
				}

				s.depth--
				return result, nil
			}

			s.depth--

		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// If we're in a record, buffer these tokens (unless size-only mode)
			if s.inRecord && !s.sizeOnly {
				s.buffer = append(s.buffer, xml.CopyToken(token))
			}
		}
	}
}

// matchesRecordElement checks if the given element name matches our record selector
func (s *RecordScanner) matchesRecordElement(elementName string) bool {
	return strings.EqualFold(elementName, s.elementName)
}

// serializeTokens converts buffered tokens back into XML string
func (s *RecordScanner) serializeTokens() (string, error) {
	if len(s.buffer) == 0 {
		return "", nil
	}

	var buf bytes.Buffer
	encoder := xml.NewEncoder(&buf)

	for _, token := range s.buffer {
		if err := encoder.EncodeToken(token); err != nil {
			return "", fmt.Errorf("failed to encode token: %w", err)
		}
	}

	if err := encoder.Flush(); err != nil {
		return "", fmt.Errorf("failed to flush encoder: %w", err)
	}

	return buf.String(), nil
}

// RecordCount returns the number of records scanned so far
func (s *RecordScanner) RecordCount() int {
	return s.recordCount
}

// BytesRead returns the total number of bytes read from the input stream so far
func (s *RecordScanner) BytesRead() int64 {
	if s.reader == nil {
		return 0
	}
	return s.reader.Bytes()
}

// Close closes the scanner and releases resources
func (s *RecordScanner) Close() error {
	// xml.Decoder doesn't need explicit closing
	return nil
}
