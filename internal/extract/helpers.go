package extract

import (
	"fmt"

	"github.com/antchfx/xmlquery"
)

// ExtractFields extracts field mappings from an XML document node
// This is a public wrapper around extractRecords for use by parallel extraction
func ExtractFields(doc *xmlquery.Node, cfg *ExtractRecordMatch) (map[string]interface{}, error) {
	return ExtractFieldsWithExternal(doc, cfg, nil)
}

// ExtractFieldsWithExternal extracts field mappings with external fields in scope.
func ExtractFieldsWithExternal(doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}) (map[string]interface{}, error) {
	if doc == nil {
		return nil, fmt.Errorf("document node cannot be nil")
	}
	if cfg == nil {
		return nil, fmt.Errorf("extract config cannot be nil")
	}

	// Extract using the internal function
	records, err := extractRecords(doc, cfg, externalFields)
	if err != nil {
		return nil, err
	}

	if len(records) == 0 {
		return copyExternalFields(externalFields), nil
	}

	// Return the first record (in parallel mode, each doc is a single record)
	return records[0], nil
}

func copyExternalFields(externalFields map[string]interface{}) map[string]interface{} {
	fields := make(map[string]interface{}, len(externalFields))
	for key, value := range externalFields {
		// Internal (derive-only) captures are never emitted, including on the
		// zero-record path where external fields are returned as the record.
		if isInternalField(value) {
			continue
		}
		fields[key] = value
	}
	return fields
}
