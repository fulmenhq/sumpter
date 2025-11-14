package extract

import (
	"fmt"

	"github.com/antchfx/xmlquery"
)

// ExtractFields extracts field mappings from an XML document node
// This is a public wrapper around extractRecords for use by parallel extraction
func ExtractFields(doc *xmlquery.Node, cfg *ExtractRecordMatch) (map[string]interface{}, error) {
	if doc == nil {
		return nil, fmt.Errorf("document node cannot be nil")
	}
	if cfg == nil {
		return nil, fmt.Errorf("extract config cannot be nil")
	}

	// Extract using the internal function
	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return make(map[string]interface{}), nil
	}

	// Return the first record (in parallel mode, each doc is a single record)
	return records[0], nil
}
