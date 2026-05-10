package extract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
	"github.com/fulmenhq/goneat/pkg/schema"
)

func TestProcessFilePolymorphicArray(t *testing.T) {
	tmpDir := t.TempDir()
	inputPath := filepath.Join(tmpDir, "sample.xml")
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<Envelope>
  <Record>
    <Identifier>rec-001</Identifier>
    <Entry>
      <TypeA>
        <Value>3.14</Value>
        <Flag>true</Flag>
      </TypeA>
    </Entry>
    <Entry>
      <TypeB>
        <Label>second</Label>
        <Count>7</Count>
      </TypeB>
    </Entry>
    <Entry>
      <TypeC amount="-2.5" note="credit" />
    </Entry>
  </Record>
</Envelope>`
	if err := os.WriteFile(inputPath, []byte(xmlContent), 0o600); err != nil {
		t.Fatalf("failed to write xml fixture: %v", err)
	}

	signature := &FileSignature{
		SignatureID:         "test-signature",
		ConfidenceThreshold: 0.2,
		MatchPatterns: []MatchPattern{
			{PatternID: "root", Selector: "/Envelope", Weight: 1.0},
		},
	}

	extractCfg := &ExtractRecordMatch{
		RecordType: "generic_record",
		MatchSelectors: []MatchSelector{
			{XPath: "//Record"},
		},
		FieldMappings: []FieldMapping{
			{OutputField: "identifier", XPath: "Identifier", Type: "string"},
			{
				OutputField: "entries",
				XPath:       "Entry",
				Type:        "array",
				Polymorphic: []PolymorphicMapping{
					{
						MatchXPath: "TypeA",
						ItemType:   "alpha",
						FieldMappings: []FieldMapping{
							{OutputField: "value", XPath: "Value", Type: "number"},
							{OutputField: "flag", XPath: "Flag", Type: "boolean"},
						},
					},
					{
						ElementType: "TypeB",
						ItemType:    "beta",
						FieldMappings: []FieldMapping{
							{OutputField: "label", XPath: "Label", Type: "string"},
							{OutputField: "count", XPath: "Count", Type: "integer"},
						},
					},
					{
						ElementType: "TypeC",
						ItemType:    "gamma",
						FieldMappings: []FieldMapping{
							{OutputField: "amount", XPath: "@amount", Type: "number"},
							{OutputField: "note", XPath: "@note", Type: "string"},
						},
					},
				},
			},
		},
	}

	schemaMap := map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"identifier": map[string]interface{}{"type": "string"},
			"entries": map[string]interface{}{
				"type":  "array",
				"items": map[string]interface{}{"type": "object"},
			},
		},
		"required": []interface{}{"identifier", "entries"},
	}
	extractCfg.OutputSchema = schemaMap

	result := ProcessFile(inputPath, signature, extractCfg, nil, false)
	if result.Error != nil {
		t.Fatalf("unexpected extraction error: %v", result.Error)
	}

	if len(result.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result.Records))
	}

	record := result.Records[0]
	extractBlock, ok := record["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("extract block missing or wrong type: %#v", record["extract"])
	}
	dataBlock, ok := extractBlock["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("extract data missing or wrong type: %#v", extractBlock["data"])
	}

	if gotID, ok := dataBlock["identifier"].(string); !ok || gotID != "rec-001" {
		t.Fatalf("unexpected identifier value: %#v", dataBlock["identifier"])
	}

	var entriesVal []map[string]interface{}
	switch v := dataBlock["entries"].(type) {
	case []map[string]interface{}:
		entriesVal = v
	case []interface{}:
		entriesVal = make([]map[string]interface{}, 0, len(v))
		for _, entry := range v {
			m, ok := entry.(map[string]interface{})
			if !ok {
				t.Fatalf("entries element has wrong type: %#v", entry)
			}
			entriesVal = append(entriesVal, m)
		}
	default:
		t.Fatalf("entries field missing or wrong type: %#v", dataBlock["entries"])
	}

	expected := []map[string]interface{}{
		{"item_type": "alpha", "value": 3.14, "flag": true},
		{"item_type": "beta", "label": "second", "count": int64(7)},
		{"item_type": "gamma", "amount": -2.5, "note": "credit"},
	}

	if !reflect.DeepEqual(entriesVal, expected) {
		t.Fatalf("entries mismatch\nexpected: %#v\nactual:   %#v", expected, entriesVal)
	}
}

func TestLoadExtractConfig_ValidatesSchema(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "extract.yaml")
	configContent := `record_type: "sample"
match_selectors:
  - xpath: "//Envelope"
field_mappings:
  - output_field: "identifier"
    xpath: "Identifier"
    type: "string"
  - output_field: "entries"
    xpath: "Entry"
    type: "array"
    polymorphic_mapping:
      - element_type: "TypeA"
        item_type: "alpha"
        field_mappings:
          - output_field: "value"
            xpath: "Value"
            type: "number"
output_schema:
  type: "object"
  properties:
    identifier:
      type: "string"
    entries:
      type: "array"
      items:
        type: "object"
  required:
    - identifier
    - entries
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadExtractConfig(configPath)
	if err != nil {
		t.Fatalf("expected validation success, got error: %v", err)
	}
	if cfg.RecordType != "sample" {
		t.Fatalf("unexpected record type: %s", cfg.RecordType)
	}
	if len(cfg.FieldMappings) != 2 {
		t.Fatalf("expected 2 field mappings, got %d", len(cfg.FieldMappings))
	}
}

func TestLoadExtractConfig_InvalidSchema(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "extract.yaml")
	// Missing record_type and field_mappings
	configContent := `match_selectors:
  - xpath: "//Envelope"
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, err := LoadExtractConfig(configPath)
	if err == nil {
		t.Fatal("expected validation error, got nil")
	}
	if !strings.Contains(err.Error(), "extract config validation failed") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestExtractRecordsOutputSchemaValidation(t *testing.T) {
	docContent := `<?xml version="1.0"?><Envelope><Record><Identifier>id-1</Identifier></Record></Envelope>`
	doc, err := xmlquery.Parse(strings.NewReader(docContent))
	if err != nil {
		t.Fatalf("failed to parse xml: %v", err)
	}

	schemaMap := map[string]interface{}{
		"type":     "object",
		"required": []interface{}{"identifier", "value"},
		"properties": map[string]interface{}{
			"identifier": map[string]interface{}{"type": "string"},
			"value":      map[string]interface{}{"type": "number"},
		},
	}

	cfg := &ExtractRecordMatch{
		RecordType:     "test",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "identifier", XPath: "Identifier", Type: "string"},
		},
		OutputSchema: schemaMap,
	}

	// Set up the validator like LoadExtractConfig does
	schemaBytes, err := json.Marshal(schemaMap)
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	validator, err := schema.NewValidatorFromBytes(schemaBytes)
	if err != nil {
		t.Fatalf("failed to create validator: %v", err)
	}
	cfg.OutputValidator = validator

	_, err = extractRecords(doc, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "output schema validation failed") {
		t.Fatalf("expected output schema validation failure, got %v", err)
	}
}

func TestCoerceString(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{"nil", nil, nil},
		{"empty string", "", nil},
		{"whitespace string", "   ", nil},
		{"valid string", "hello", "hello"},
		{"int to string", 42, "42"},
		{"float to string", 3.14, "3.14"},
		{"bool true to string", true, "true"},
		{"bool false to string", false, "false"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := coerceString(tt.input)
			if err != nil {
				t.Errorf("coerceString(%v) returned error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("coerceString(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCoerceNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
		hasError bool
	}{
		{"nil", nil, nil, false},
		{"valid float", 3.14, 3.14, false},
		{"string number", "2.5", 2.5, false},
		{"empty string", "", nil, false},
		{"bool true", true, float64(1), false},
		{"bool false", false, float64(0), false},
		{"invalid string", "not-a-number", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := coerceNumber(tt.input)
			if tt.hasError && err == nil {
				t.Errorf("coerceNumber(%v) expected error but got none", tt.input)
			}
			if !tt.hasError && err != nil {
				t.Errorf("coerceNumber(%v) unexpected error: %v", tt.input, err)
			}
			if !tt.hasError && result != tt.expected {
				t.Errorf("coerceNumber(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCoerceInteger(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
		hasError bool
	}{
		{"nil", nil, nil, false},
		{"float to int", 3.7, int64(3), false},
		{"string int", "123", int64(123), false},
		{"string float", "45.6", int64(45), false},
		{"empty string", "", nil, false},
		{"bool true", true, int64(1), false},
		{"bool false", false, int64(0), false},
		{"invalid string", "not-a-number", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := coerceInteger(tt.input)
			if tt.hasError && err == nil {
				t.Errorf("coerceInteger(%v) expected error but got none", tt.input)
			}
			if !tt.hasError && err != nil {
				t.Errorf("coerceInteger(%v) unexpected error: %v", tt.input, err)
			}
			if !tt.hasError && result != tt.expected {
				t.Errorf("coerceInteger(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestCoerceBoolean(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected interface{}
	}{
		{"nil", nil, false},
		{"bool true", true, true},
		{"bool false", false, false},
		{"float 2.5", 2.5, true},
		{"float 0.0", 0.0, false},
		{"string true", "true", true},
		{"string false", "false", false},
		{"string 1", "1", true},
		{"string 0", "0", false},
		{"string yes", "yes", true},
		{"string no", "no", false},
		{"string on", "on", true},
		{"string off", "off", false},
		{"string empty", "", false},
		{"string random", "hello", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := coerceBoolean(tt.input)
			if err != nil {
				t.Errorf("coerceBoolean(%v) returned error: %v", tt.input, err)
			}
			if result != tt.expected {
				t.Errorf("coerceBoolean(%v) = %v, want %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestLoadSignatureConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "signature.yaml")
	configContent := `signature_id: "test-sig"
name: "Test Signature"
description: "A test signature"
status: "active"
priority: "high"
realm: "finance"
match_patterns:
  - pattern_id: "root"
    name: "Root element"
    selector: "/Envelope"
    weight: 1.0
confidence_threshold: 0.8
format_type: "xml"
tags: ["test"]
use_cases: ["validation"]
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o600); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	cfg, err := LoadSignatureConfig(configPath)
	if err != nil {
		t.Fatalf("LoadSignatureConfig() returned error: %v", err)
	}

	if cfg.SignatureID != "test-sig" {
		t.Errorf("SignatureID = %v, want %v", cfg.SignatureID, "test-sig")
	}
	if cfg.Name != "Test Signature" {
		t.Errorf("Name = %v, want %v", cfg.Name, "Test Signature")
	}
	if len(cfg.MatchPatterns) != 1 {
		t.Errorf("MatchPatterns length = %v, want %v", len(cfg.MatchPatterns), 1)
	}
	if cfg.ConfidenceThreshold != 0.8 {
		t.Errorf("ConfidenceThreshold = %v, want %v", cfg.ConfidenceThreshold, 0.8)
	}
}

func TestMatchesSignature(t *testing.T) {
	xmlContent := `<?xml version="1.0"?><Envelope><Record><ID>123</ID></Record></Envelope>`
	doc, err := xmlquery.Parse(strings.NewReader(xmlContent))
	if err != nil {
		t.Fatalf("failed to parse XML: %v", err)
	}

	tests := []struct {
		name        string
		signature   *FileSignature
		expected    bool
		expectError bool
	}{
		{
			name: "matching signature",
			signature: &FileSignature{
				ConfidenceThreshold: 0.5,
				MatchPatterns: []MatchPattern{
					{Selector: "/Envelope", Weight: 1.0},
				},
			},
			expected:    true,
			expectError: false,
		},
		{
			name: "non-matching signature",
			signature: &FileSignature{
				ConfidenceThreshold: 0.5,
				MatchPatterns: []MatchPattern{
					{Selector: "/Document", Weight: 1.0},
				},
			},
			expected:    false,
			expectError: false,
		},
		{
			name: "no patterns with weight",
			signature: &FileSignature{
				ConfidenceThreshold: 0.5,
				MatchPatterns: []MatchPattern{
					{Selector: "/Envelope", Weight: 0.0},
				},
			},
			expected:    false,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := matchesSignature(doc, tt.signature)
			if tt.expectError && err == nil {
				t.Errorf("expected error but got none")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.expectError && result != tt.expected {
				t.Errorf("matchesSignature() = %v, want %v", result, tt.expected)
			}
		})
	}
}

// TestMatchesPattern_SelectorForms exercises the matcher across the full
// range of selector forms the extractor supports — including the boolean
// comparison forms that previously fell into a TODO branch and silently
// returned false (count(X) > 1, count(X) = 1, etc.).
func TestMatchesPattern_SelectorForms(t *testing.T) {
	xmlContent := `<?xml version="1.0"?>
<Envelope>
  <Record><ID>1</ID></Record>
  <Record><ID>2</ID></Record>
  <Record><ID>3</ID></Record>
</Envelope>`
	doc, err := xmlquery.Parse(strings.NewReader(xmlContent))
	if err != nil {
		t.Fatalf("failed to parse XML: %v", err)
	}

	tests := []struct {
		name     string
		selector string
		want     bool
	}{
		{"absolute path matches", "/Envelope", true},
		{"absolute path no match", "/Document", false},
		{"descendant node-set matches", "//Record", true},
		{"descendant node-set no match", "//Missing", false},
		{"count > 0 (legacy form)", "count(//Record) > 0", true},
		{"count > 0 false", "count(//Missing) > 0", false},
		{"count > 1 true (regression: was always false)", "count(//Record) > 1", true},
		{"count > 5 false", "count(//Record) > 5", false},
		{"count = 3 true (regression: was always false)", "count(//Record) = 3", true},
		{"count = 1 false", "count(//Record) = 1", false},
		{"count >= 3 true", "count(//Record) >= 3", true},
		{"count < 5 true", "count(//Record) < 5", true},
		{"boolean(...) true", "boolean(//Record)", true},
		{"boolean(...) false", "boolean(//Missing)", false},
		{"compound and true", "count(//Record) > 0 and count(//Envelope) > 0", true},
		{"compound and false", "count(//Record) > 0 and count(//Missing) > 0", false},
		{"compound or true", "count(//Missing) > 0 or count(//Record) > 0", true},
		{"empty selector returns false", "", false},
		{"malformed xpath returns false", "count(//Record", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesPattern(doc, MatchPattern{Selector: tt.selector, Weight: 1.0})
			if got != tt.want {
				t.Errorf("matchesPattern(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}

func TestProcessFile_LargeFileProtection(t *testing.T) {
	tmpDir := t.TempDir()

	// Test 1: Small file should work without flag
	t.Run("small file without flag", func(t *testing.T) {
		smallFile := filepath.Join(tmpDir, "small.xml")
		xmlContent := `<?xml version="1.0"?><Envelope><Record><ID>123</ID></Record></Envelope>`
		if err := os.WriteFile(smallFile, []byte(xmlContent), 0o600); err != nil {
			t.Fatalf("failed to write small file: %v", err)
		}

		signature := &FileSignature{
			ConfidenceThreshold: 0.5,
			MatchPatterns: []MatchPattern{
				{Selector: "/Envelope", Weight: 1.0},
			},
		}

		extractCfg := &ExtractRecordMatch{
			RecordType:     "test",
			MatchSelectors: []MatchSelector{{XPath: "//Record"}},
			FieldMappings: []FieldMapping{
				{OutputField: "id", XPath: "ID", Type: "string"},
			},
		}

		result := ProcessFile(smallFile, signature, extractCfg, nil, false)
		if result.Error != nil {
			t.Fatalf("small file should process without error, got: %v", result.Error)
		}
	})

	// Test 2: Large file (>1GB) should fail without flag
	t.Run("large file without flag", func(t *testing.T) {
		largeFile := filepath.Join(tmpDir, "large.xml")
		// Create a file > 1GB
		file, err := os.Create(largeFile)
		if err != nil {
			t.Fatalf("failed to create large file: %v", err)
		}
		// Write 1.1GB of data
		const chunkSize = 1024 * 1024 // 1MB
		chunk := make([]byte, chunkSize)
		for i := range chunk {
			chunk[i] = 'x'
		}
		for i := 0; i < 1100; i++ { // 1.1GB
			if _, err := file.Write(chunk); err != nil {
				_ = file.Close()
				t.Fatalf("failed to write to large file: %v", err)
			}
		}
		_ = file.Close()

		signature := &FileSignature{
			ConfidenceThreshold: 0.5,
			MatchPatterns:       []MatchPattern{{Selector: "/Envelope", Weight: 1.0}},
		}
		extractCfg := &ExtractRecordMatch{
			RecordType:     "test",
			MatchSelectors: []MatchSelector{{XPath: "//Record"}},
			FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "ID", Type: "string"}},
		}

		result := ProcessFile(largeFile, signature, extractCfg, nil, false)
		if result.Error == nil {
			t.Fatal("expected error for large file without flag, got nil")
		}
		if !strings.Contains(result.Error.Error(), "exceeds 1GB limit") {
			t.Fatalf("expected size limit error, got: %v", result.Error)
		}
	})

	// Test 3: Large file should work WITH flag
	t.Run("large file with flag", func(t *testing.T) {
		largeFile := filepath.Join(tmpDir, "large-with-flag.xml")
		// Create a file just > 1GB but with valid XML
		file, err := os.Create(largeFile)
		if err != nil {
			t.Fatalf("failed to create large file: %v", err)
		}

		// Write header
		if _, err := file.WriteString(`<?xml version="1.0"?><Envelope><Record><ID>123</ID></Record>`); err != nil {
			_ = file.Close()
			t.Fatalf("failed to write header: %v", err)
		}

		// Pad to > 1GB
		const chunkSize = 1024 * 1024 // 1MB
		chunk := make([]byte, chunkSize)
		for i := range chunk {
			chunk[i] = ' '
		}
		for i := 0; i < 1100; i++ { // 1.1GB
			if _, err := file.Write(chunk); err != nil {
				_ = file.Close()
				t.Fatalf("failed to write padding: %v", err)
			}
		}

		// Write footer
		if _, err := file.WriteString(`</Envelope>`); err != nil {
			_ = file.Close()
			t.Fatalf("failed to write footer: %v", err)
		}
		_ = file.Close()

		signature := &FileSignature{
			ConfidenceThreshold: 0.5,
			MatchPatterns:       []MatchPattern{{Selector: "/Envelope", Weight: 1.0}},
		}
		extractCfg := &ExtractRecordMatch{
			RecordType:     "test",
			MatchSelectors: []MatchSelector{{XPath: "//Record"}},
			FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "ID", Type: "string"}},
		}

		// With allowLargeFiles=true, it should attempt to process (though it may fail for other reasons like memory)
		result := ProcessFile(largeFile, signature, extractCfg, nil, true)
		// We don't check for success here since we may actually OOM, but we verify it doesn't fail with size check error
		if result.Error != nil && strings.Contains(result.Error.Error(), "exceeds 1GB limit") {
			t.Fatalf("should not fail with size limit when flag is set, got: %v", result.Error)
		}
	})
}

// TestProcessFileStreaming_vs_NonStreaming verifies that streaming mode produces
// identical results to non-streaming mode for the same input
func TestProcessFileStreaming_vs_NonStreaming(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "multi-record.xml")

	// Create test XML with multiple records
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<Envelope>
  <Record id="1">
    <Name>First Record</Name>
    <Value>100.5</Value>
    <Active>true</Active>
  </Record>
  <Record id="2">
    <Name>Second Record</Name>
    <Value>200.75</Value>
    <Active>false</Active>
  </Record>
  <Record id="3">
    <Name>Third Record</Name>
    <Value>300.25</Value>
    <Active>true</Active>
  </Record>
</Envelope>`

	if err := os.WriteFile(testFile, []byte(xmlContent), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	signature := &FileSignature{
		SignatureID:         "test-streaming",
		ConfidenceThreshold: 0.5,
		MatchPatterns: []MatchPattern{
			{Selector: "/Envelope", Weight: 1.0},
		},
	}

	extractCfg := &ExtractRecordMatch{
		RecordType: "test_record",
		MatchSelectors: []MatchSelector{
			{XPath: "//Record"}, // Same XPath for both modes now
		},
		FieldMappings: []FieldMapping{
			{OutputField: "id", XPath: "@id", Type: "string"},
			{OutputField: "name", XPath: "Name", Type: "string"},
			{OutputField: "value", XPath: "Value", Type: "number"},
			{OutputField: "active", XPath: "Active", Type: "boolean"},
		},
	}

	externalFields := map[string]interface{}{
		"test_run": "comparison",
	}

	// Run non-streaming extraction
	resultNonStreaming := ProcessFile(testFile, signature, extractCfg, externalFields, false)
	if resultNonStreaming.Error != nil {
		t.Fatalf("non-streaming extraction failed: %v", resultNonStreaming.Error)
	}

	// Run streaming extraction (same config works now!)
	resultStreaming := ProcessFileStreaming(testFile, signature, extractCfg, externalFields)
	if resultStreaming.Error != nil {
		t.Fatalf("streaming extraction failed: %v", resultStreaming.Error)
	}

	// Compare record counts
	if len(resultNonStreaming.Records) != len(resultStreaming.Records) {
		t.Fatalf("record count mismatch: non-streaming=%d, streaming=%d",
			len(resultNonStreaming.Records), len(resultStreaming.Records))
	}

	// Compare each record
	for i := range resultNonStreaming.Records {
		nonStreamRec := resultNonStreaming.Records[i]
		streamRec := resultStreaming.Records[i]

		// Extract data blocks for comparison (ignore runtime metadata)
		nonStreamData := extractDataBlock(t, nonStreamRec)
		streamData := extractDataBlock(t, streamRec)

		if !reflect.DeepEqual(nonStreamData, streamData) {
			nonStreamJSON, _ := json.MarshalIndent(nonStreamData, "", "  ")
			streamJSON, _ := json.MarshalIndent(streamData, "", "  ")
			t.Fatalf("record %d data mismatch:\nNon-streaming:\n%s\n\nStreaming:\n%s",
				i, string(nonStreamJSON), string(streamJSON))
		}
	}

	t.Logf("✓ Streaming and non-streaming produce identical results for %d records", len(resultNonStreaming.Records))
}

// extractDataBlock extracts the data portion from a record for comparison
func extractDataBlock(t *testing.T, record map[string]interface{}) map[string]interface{} {
	t.Helper()

	extractBlock, ok := record["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("extract block missing or wrong type: %T", record["extract"])
	}

	dataBlock, ok := extractBlock["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("data block missing or wrong type: %T", extractBlock["data"])
	}

	return dataBlock
}

// TestProcessFileStreaming_LargeRecordCount tests streaming with many records
func TestProcessFileStreaming_LargeRecordCount(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large record count test in short mode")
	}

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "many-records.xml")

	// Create XML with 1000 records
	file, err := os.Create(testFile)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	defer func() {
		_ = file.Close() // Best effort close in test
	}()

	if _, err := file.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Envelope>`); err != nil {
		t.Fatalf("failed to write XML header: %v", err)
	}
	for i := 0; i < 1000; i++ {
		if _, err := fmt.Fprintf(file, `<Record><ID>%d</ID><Value>%d.5</Value></Record>`, i, i*10); err != nil {
			t.Fatalf("failed to write record %d: %v", i, err)
		}
	}
	if _, err := file.WriteString(`</Envelope>`); err != nil {
		t.Fatalf("failed to write XML footer: %v", err)
	}

	signature := &FileSignature{
		ConfidenceThreshold: 0.5,
		MatchPatterns: []MatchPattern{
			{Selector: "/Envelope", Weight: 1.0},
		},
	}

	extractCfg := &ExtractRecordMatch{
		RecordType: "test_record",
		MatchSelectors: []MatchSelector{
			{XPath: "//Record"}, // Use normal XPath - ProcessFileStreaming will adjust internally
		},
		FieldMappings: []FieldMapping{
			{OutputField: "id", XPath: "ID", Type: "string"},
			{OutputField: "value", XPath: "Value", Type: "number"},
		},
	}

	result := ProcessFileStreaming(testFile, signature, extractCfg, nil)
	if result.Error != nil {
		t.Fatalf("streaming extraction failed: %v", result.Error)
	}

	if len(result.Records) != 1000 {
		t.Fatalf("expected 1000 records, got %d", len(result.Records))
	}

	// Verify first and last records
	firstData := extractDataBlock(t, result.Records[0])
	if firstData["id"] != "0" {
		t.Errorf("first record id = %v, want '0'", firstData["id"])
	}

	lastData := extractDataBlock(t, result.Records[999])
	if lastData["id"] != "999" {
		t.Errorf("last record id = %v, want '999'", lastData["id"])
	}

	t.Logf("✓ Successfully processed 1000 records via streaming")
}

// TestProcessFileStreaming_Debug is a simple debug test
func TestProcessFileStreaming_Debug(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "debug.xml")

	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<Envelope>
  <Record id="1">
    <Name>Test</Name>
  </Record>
</Envelope>`

	if err := os.WriteFile(testFile, []byte(xmlContent), 0o600); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	signature := &FileSignature{
		SignatureID:         "test",
		ConfidenceThreshold: 0.5,
		MatchPatterns:       []MatchPattern{{Selector: "/Envelope", Weight: 1.0}},
	}

	extractCfg := &ExtractRecordMatch{
		RecordType:     "test",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}}, // Use normal XPath
		FieldMappings: []FieldMapping{
			{OutputField: "id", XPath: "@id", Type: "string"},
			{OutputField: "name", XPath: "Name", Type: "string"},
		},
	}

	result := ProcessFileStreaming(testFile, signature, extractCfg, nil)

	t.Logf("Result error: %v", result.Error)
	t.Logf("Result records: %d", len(result.Records))

	if result.Error != nil {
		t.Fatalf("streaming failed: %v", result.Error)
	}

	if len(result.Records) == 0 {
		t.Fatal("expected at least 1 record, got 0")
	}

	t.Logf("First record: %+v", result.Records[0])
}
