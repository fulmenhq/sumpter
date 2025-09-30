package extract

import (
	"encoding/json"
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

	result := ProcessFile(inputPath, signature, extractCfg, nil)
	if result.Error != nil {
		t.Fatalf("unexpected extraction error: %v", result.Error)
	}

	if len(result.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result.Records))
	}

	record := result.Records[0]
	if gotID, ok := record["identifier"].(string); !ok || gotID != "rec-001" {
		t.Fatalf("unexpected identifier value: %#v", record["identifier"])
	}

	entriesVal, ok := record["entries"].([]map[string]interface{})
	if !ok {
		t.Fatalf("entries field missing or wrong type: %#v", record["entries"])
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
