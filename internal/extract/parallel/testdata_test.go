package parallel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/index"
)

// createTestXMLFile creates a test XML file with sample records
//
//nolint:unused // Reserved for future integration tests
func createTestXMLFile(t *testing.T, dir string) string {
	t.Helper()

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<root>
  <record id="1">
    <name>Alice</name>
    <value>100</value>
  </record>
  <record id="2">
    <name>Bob</name>
    <value>200</value>
  </record>
  <record id="3">
    <name>Charlie</name>
    <value>300</value>
  </record>
</root>`

	path := filepath.Join(dir, "test.xml")
	err := os.WriteFile(path, []byte(xmlContent), 0600)
	if err != nil {
		t.Fatalf("Failed to create test XML file: %v", err)
	}

	return path
}

// createTestIndex creates a test record index for the XML file
//
//nolint:unused // Reserved for future integration tests
func createTestIndex(t *testing.T, dir, xmlPath string) string {
	t.Helper()

	idx := &index.RecordIndex{
		Version: "1.0.0",
		Source: index.SourceInfo{
			Path:              xmlPath,
			SizeBytes:         250,
			SHA256:            "abc123", // Simplified for testing
			Compressed:        false,
			CompressionFormat: "",
		},
		Selector: index.SelectorInfo{
			XPath: "//record",
		},
		Records: []index.RecordMetadata{
			{
				RecordNum:   0,
				StartOffset: 45,
				EndOffset:   130,
				SizeBytes:   85,
			},
			{
				RecordNum:   1,
				StartOffset: 133,
				EndOffset:   215,
				SizeBytes:   82,
			},
			{
				RecordNum:   2,
				StartOffset: 218,
				EndOffset:   305,
				SizeBytes:   87,
			},
		},
	}

	indexPath := filepath.Join(dir, "test.recordindex.json")
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		t.Fatalf("Failed to marshal index: %v", err)
	}

	err = os.WriteFile(indexPath, data, 0600)
	if err != nil {
		t.Fatalf("Failed to write index file: %v", err)
	}

	return indexPath
}
