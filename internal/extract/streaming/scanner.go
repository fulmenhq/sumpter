package streaming

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
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
	// Extract element name from XPath selector
	// For now, we support simple patterns like "//ElementName" or "/path/to/ElementName"
	elementName := extractElementName(recordSelector)

	return &RecordScanner{
		decoder:        xml.NewDecoder(reader),
		recordSelector: recordSelector,
		elementName:    elementName,
		buffer:         make([]xml.Token, 0, 128),
		depth:          0,
		recordDepth:    -1,
		inRecord:       false,
		recordCount:    0,
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

	for {
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
			}

			// If we're in a record, buffer this token
			if s.inRecord {
				s.buffer = append(s.buffer, xml.CopyToken(token))
			}

		case xml.EndElement:
			// If we're in a record, buffer this token
			if s.inRecord {
				s.buffer = append(s.buffer, xml.CopyToken(token))
			}

			// Check if this closes the current record
			if s.inRecord && s.depth == s.recordDepth {
				// We've completed a record - serialize and return it
				recordXML, err := s.serializeTokens()
				if err != nil {
					s.err = fmt.Errorf("failed to serialize record %d: %w", s.recordCount, err)
					return nil, s.err
				}

				result := &RecordBuffer{
					XML:       recordXML,
					RecordNum: s.recordCount,
				}

				s.depth--
				return result, nil
			}

			s.depth--

		case xml.CharData, xml.Comment, xml.ProcInst, xml.Directive:
			// If we're in a record, buffer these tokens
			if s.inRecord {
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

// Close closes the scanner and releases resources
func (s *RecordScanner) Close() error {
	// xml.Decoder doesn't need explicit closing
	return nil
}
