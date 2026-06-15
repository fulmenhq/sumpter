package validation

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/assets"
)

func TestNewSchemaValidator(t *testing.T) {
	validator := NewSchemaValidator("/tmp/schemas")
	if validator == nil {
		t.Error("NewSchemaValidator() returned nil")
		return
	}
	if validator.schemaDir != "/tmp/schemas" {
		t.Errorf("NewSchemaValidator() schemaDir = %v, want %v", validator.schemaDir, "/tmp/schemas")
	}
}

func TestNewSchemaValidatorFromFS(t *testing.T) {
	// Create a temporary filesystem for testing
	tempDir, err := os.MkdirTemp("", "sumpter-schema-fs-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create a simple test schema file
	schemaContent := `{"type": "object"}`
	schemaPath := filepath.Join(tempDir, "test.schema.json")
	if err := os.WriteFile(schemaPath, []byte(schemaContent), 0644); err != nil {
		t.Fatalf("Failed to write test schema: %v", err)
	}

	// Create filesystem from temp dir
	fsys := os.DirFS(tempDir)

	validator := NewSchemaValidatorFromFS(fsys)
	if validator == nil {
		t.Error("NewSchemaValidatorFromFS() returned nil")
		return
	}
	if validator.schemaFS == nil {
		t.Error("NewSchemaValidatorFromFS() schemaFS should not be nil")
	}
	if validator.schemaDir != "" {
		t.Errorf("NewSchemaValidatorFromFS() schemaDir should be empty, got %v", validator.schemaDir)
	}
}

func TestValidationResult_IsValid(t *testing.T) {
	validResult := &ValidationResult{Valid: true}
	if !validResult.IsValid() {
		t.Error("IsValid() should return true for valid result")
	}

	invalidResult := &ValidationResult{Valid: false}
	if invalidResult.IsValid() {
		t.Error("IsValid() should return false for invalid result")
	}
}

func TestValidationResult_ErrorCount(t *testing.T) {
	result := &ValidationResult{
		Errors: []ValidationError{
			{Path: "test1", Message: "error1"},
			{Path: "test2", Message: "error2"},
		},
	}

	if count := result.ErrorCount(); count != 2 {
		t.Errorf("ErrorCount() = %v, want %v", count, 2)
	}
}

func TestValidationResult_ErrorSummary(t *testing.T) {
	// Test valid result
	validResult := &ValidationResult{Valid: true}
	summary := validResult.ErrorSummary()
	if summary != "✅ Validation passed" {
		t.Errorf("ErrorSummary() for valid result = %v", summary)
	}

	// Test invalid result
	invalidResult := &ValidationResult{
		Valid: false,
		Errors: []ValidationError{
			{Path: "field1", Message: "error message", Line: 5},
			{Path: "field2", Message: "another error"},
		},
	}
	summary = invalidResult.ErrorSummary()
	if !contains(summary, "Validation failed (2 errors)") {
		t.Errorf("ErrorSummary() for invalid result = %v", summary)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsAt(s, substr)))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestValidationError(t *testing.T) {
	err := ValidationError{
		Path:    "test.path",
		Message: "test message",
		File:    "test.yaml",
		Line:    42,
	}

	if err.Path != "test.path" {
		t.Errorf("Path = %v, want %v", err.Path, "test.path")
	}
	if err.Message != "test message" {
		t.Errorf("Message = %v, want %v", err.Message, "test message")
	}
	if err.File != "test.yaml" {
		t.Errorf("File = %v, want %v", err.File, "test.yaml")
	}
	if err.Line != 42 {
		t.Errorf("Line = %v, want %v", err.Line, 42)
	}
}

func TestValidateRecipeManifestEmbeddedSchemas(t *testing.T) {
	schemaFS, err := assets.GetSchemasFS()
	if err != nil {
		t.Skipf("embedded schemas unavailable: %v", err)
	}

	validator := NewSchemaValidatorFromFS(schemaFS)
	manifestYAML := `version: recipe/v0.1.0
kind: extract
id: test_recipe
assets:
  signature: signature.yaml
  extract: extract.yaml
`

	result, err := validator.ValidateRecipeManifest([]byte(manifestYAML), "recipe.yaml")
	if err != nil {
		t.Fatalf("ValidateRecipeManifest returned error: %v", err)
	}
	if result == nil {
		t.Fatal("ValidateRecipeManifest returned nil result")
		return
	}
	if !result.Valid {
		t.Fatalf("expected manifest to be valid, got errors: %+v", result.Errors)
	}
}

// TestValidateRecipeManifestCredentialsHandle covers the cloud credential-handle
// fields on defaults.{input,output}: a valid handle slug is accepted, and a
// malformed name (whitespace) is rejected by the schema's handle-name pattern.
func TestValidateRecipeManifestCredentialsHandle(t *testing.T) {
	schemaFS, err := assets.GetSchemasFS()
	if err != nil {
		t.Skipf("embedded schemas unavailable: %v", err)
	}
	validator := NewSchemaValidatorFromFS(schemaFS)

	valid := `version: recipe/v0.1.0
kind: extract
id: cloud_recipe
assets:
  signature: signature.yaml
  extract: extract.yaml
defaults:
  input:
    path: s3://my-bucket/in/
    credentials_handle: reader
  output:
    path: s3://my-bucket/out/
    credentials_handle: writer
`
	result, err := validator.ValidateRecipeManifest([]byte(valid), "recipe.yaml")
	if err != nil {
		t.Fatalf("ValidateRecipeManifest(valid) error: %v", err)
	}
	if !result.Valid {
		t.Fatalf("expected valid manifest with credential handles, got: %+v", result.Errors)
	}

	invalid := `version: recipe/v0.1.0
kind: extract
id: cloud_recipe
assets:
  signature: signature.yaml
  extract: extract.yaml
defaults:
  output:
    path: s3://my-bucket/out/
    credentials_handle: "bad handle name"
`
	result, err = validator.ValidateRecipeManifest([]byte(invalid), "recipe.yaml")
	if err != nil {
		t.Fatalf("ValidateRecipeManifest(invalid) error: %v", err)
	}
	if result.Valid {
		t.Fatal("expected malformed credentials_handle to be rejected by the handle-name pattern")
	}
}

func TestValidateMainConfig(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Copy the extract config schema
	schemaDir := filepath.Join(tempDir, "schemas", "extract", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/extract/v0.1.0/extract-record-match-schema.yaml"
	destSchema := filepath.Join(schemaDir, "extract-record-match-schema.yaml")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(tempDir)

	// Test valid extract config
	validConfig := `{
		"record_type": "test_record",
		"match_selectors": [
			{
				"xpath": "//Record"
			}
		],
		"field_mappings": [
			{
				"output_field": "id",
				"xpath": "Identifier",
				"type": "string"
			}
		],
		"output_schema": {
			"type": "object",
			"properties": {
				"id": {"type": "string"}
			}
		}
	}`

	result, err := validator.ValidateExtractConfig([]byte(validConfig), "test-extract.yaml")
	if err != nil {
		t.Errorf("ValidateExtractConfig() returned error for valid config: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidateExtractConfig() should validate valid config, got errors: %v", result.Errors)
	}

	// Test invalid extract config (missing record_type)
	invalidConfig := `{
		"match_selectors": [
			{
				"xpath": "//Record"
			}
		],
		"field_mappings": [
			{
				"output_field": "id",
				"xpath": "Identifier",
				"type": "string"
			}
		],
		"output_schema": {
			"type": "object",
			"properties": {
				"id": {"type": "string"}
			}
		}
	}`

	result, err = validator.ValidateExtractConfig([]byte(invalidConfig), "test-extract-invalid.yaml")
	if err != nil {
		t.Errorf("ValidateExtractConfig() returned error for invalid config: %v", err)
	}
	if result.IsValid() {
		t.Error("ValidateExtractConfig() should reject invalid config")
	}
	if result.ErrorCount() == 0 {
		t.Error("ValidateExtractConfig() should return validation errors for invalid config")
	}

	schemaCases := []struct {
		name  string
		field string
		valid bool
	}{
		{
			name: "expression field",
			field: `{
				"output_field": "total_count",
				"expression": "a_count + b_count",
				"type": "integer"
			}`,
			valid: true,
		},
		{
			name: "both xpath and expression",
			field: `{
				"output_field": "total_count",
				"xpath": "Total",
				"expression": "a_count + b_count",
				"type": "integer"
			}`,
			valid: false,
		},
		{
			name: "neither xpath nor expression",
			field: `{
				"output_field": "total_count",
				"type": "integer"
			}`,
			valid: false,
		},
		{
			name: "unknown field mapping key",
			field: `{
				"output_field": "total_count",
				"xpath": "Total",
				"type": "integer",
				"exprsesion": "a_count + b_count"
			}`,
			valid: false,
		},
		{
			name: "array item mapping remains valid",
			field: `{
				"output_field": "items",
				"xpath": "Item",
				"type": "array",
				"item_mapping": [
					{
						"output_field": "sku",
						"xpath": "SKU",
						"type": "string"
					}
				]
			}`,
			valid: true,
		},
		{
			name: "array item mapping transform params remain valid",
			field: `{
				"output_field": "items",
				"xpath": "Item",
				"type": "array",
				"item_mapping": [
					{
						"output_field": "sku",
						"xpath": "SKU",
						"type": "string",
						"transform": "regex_extract",
						"transform_params": {
							"pattern": "SKU-(.*)",
							"group": 1
						}
					}
				]
			}`,
			valid: true,
		},
		{
			name: "polymorphic mapping remains valid",
			field: `{
				"output_field": "items",
				"xpath": "Item",
				"type": "array",
				"polymorphic_mapping": [
					{
						"element_type": "Sale",
						"field_mappings": [
							{
								"output_field": "amount",
								"xpath": "Amount",
								"type": "number"
							}
						]
					}
				]
			}`,
			valid: true,
		},
		{
			name: "polymorphic nested item mapping remains valid",
			field: `{
				"output_field": "items",
				"xpath": "Item",
				"type": "array",
				"polymorphic_mapping": [
					{
						"element_type": "Sale",
						"field_mappings": [
							{
								"output_field": "discounts",
								"xpath": "Discount",
								"type": "array",
								"item_mapping": [
									{
										"output_field": "amount",
										"xpath": "Amount",
										"type": "number"
									}
								]
							}
						]
					}
				]
			}`,
			valid: true,
		},
		{
			name: "nested expression remains out of scope",
			field: `{
				"output_field": "items",
				"xpath": "Item",
				"type": "array",
				"item_mapping": [
					{
						"output_field": "derived",
						"expression": "a + b",
						"type": "integer"
					}
				]
			}`,
			valid: false,
		},
	}

	for _, tt := range schemaCases {
		t.Run(tt.name, func(t *testing.T) {
			config := fmt.Sprintf(`{
				"record_type": "test_record",
				"match_selectors": [{"xpath": "//Record"}],
				"field_mappings": [%s],
				"output_schema": {"type": "object"}
			}`, tt.field)
			result, err := validator.ValidateExtractConfig([]byte(config), "test-extract.yaml")
			if err != nil {
				t.Fatalf("ValidateExtractConfig() error = %v", err)
			}
			if result.IsValid() != tt.valid {
				t.Fatalf("IsValid() = %v, want %v; errors: %v", result.IsValid(), tt.valid, result.Errors)
			}
		})
	}
}

func TestValidateExtractReconciliationGroupByAnyOf(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "sumpter-reconciliation-schema-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	schemaDir := filepath.Join(tempDir, "schemas", "extract", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/extract/v0.1.0/extract-record-match-schema.yaml"
	destSchema := filepath.Join(schemaDir, "extract-record-match-schema.yaml")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(tempDir)
	cases := []struct {
		name           string
		reconciliation string
		valid          bool
		wantMessage    []string
	}{
		{
			name: "group_by only accepts",
			reconciliation: `{
				"name": "total_by_category",
				"base_expression": "reported_total",
				"group_by": {
					"source": "lines[]",
					"field": "category_code",
					"value_expression": "line_amount"
				}
			}`,
			valid: true,
		},
		{
			name: "components only accepts",
			reconciliation: `{
				"name": "total_by_category",
				"base_expression": "reported_total",
				"components": [
					{"name": "line_amounts", "expression": "line_amount_total"}
				]
			}`,
			valid: true,
		},
		{
			name: "components and group_by accepts",
			reconciliation: `{
				"name": "total_by_category",
				"base_expression": "reported_total",
				"group_by": {
					"source": "lines[]",
					"field": "category_code",
					"value_expression": "line_amount"
				},
				"components": [
					{"name": "manual_adjustments", "expression": "manual_adjustment_total"}
				]
			}`,
			valid: true,
		},
		{
			name: "neither components nor group_by rejects clearly",
			reconciliation: `{
				"name": "total_by_category",
				"base_expression": "reported_total"
			}`,
			valid:       false,
			wantMessage: []string{"components", "group_by"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			config := fmt.Sprintf(`{
				"record_type": "test_record",
				"match_selectors": [{"xpath": "//Record"}],
				"field_mappings": [
					{"output_field": "reported_total", "xpath": "ReportedTotal", "type": "number"},
					{"output_field": "line_amount_total", "expression": "reported_total", "type": "number"},
					{"output_field": "manual_adjustment_total", "expression": "0", "type": "number"},
					{
						"output_field": "lines",
						"xpath": "Lines/Line",
						"type": "array",
						"item_mapping": [
							{"output_field": "category_code", "xpath": "Category", "type": "string"},
							{"output_field": "line_amount", "xpath": "Amount", "type": "number"}
						]
					}
				],
				"output_schema": {"type": "object"},
				"validation_metadata": {
					"enable": true,
					"array_path": "lines",
					"reconciliations": [%s]
				}
			}`, tt.reconciliation)

			result, err := validator.ValidateExtractConfig([]byte(config), "test-extract.yaml")
			if err != nil {
				t.Fatalf("ValidateExtractConfig() error = %v", err)
			}
			if result.IsValid() != tt.valid {
				t.Fatalf("IsValid() = %v, want %v; errors: %v", result.IsValid(), tt.valid, result.Errors)
			}
			for _, term := range tt.wantMessage {
				if !validationErrorsContain(result.Errors, term) {
					t.Fatalf("expected validation errors to mention %q, got %+v", term, result.Errors)
				}
			}
		})
	}
}

func validationErrorsContain(errors []ValidationError, term string) bool {
	for _, err := range errors {
		if strings.Contains(err.Message, term) {
			return true
		}
	}
	return false
}

func TestValidateFile(t *testing.T) {
	// Create a temporary directory for schemas and test files
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Copy schemas
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	// Copy all config schemas
	schemas := []string{
		"sumpter-config.schema.json",
		"logger-config.schema.json",
		"pii-config.schema.json",
	}
	for _, schema := range schemas {
		source := filepath.Join("../../schemas", "config", "v0.1.0", schema)
		dest := filepath.Join(schemaDir, schema)
		if err := copyFile(source, dest); err != nil {
			t.Fatalf("Failed to copy schema %s: %v", schema, err)
		}
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Test sumpter.yaml file
	sumpterFile := filepath.Join(tempDir, "sumpter.yaml")
	sumpterContent := `version: config/v0.1.0
logging:
  level: info
  format: pretty
pii:
  mode: safe
paths:
  cache_dir: cache
  temp_dir: temp
  output_dir: output`
	if err := os.WriteFile(sumpterFile, []byte(sumpterContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result, err := validator.ValidateFile(sumpterFile)
	if err != nil {
		t.Errorf("ValidateFile() returned error for sumpter.yaml: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidateFile() should validate sumpter.yaml, got errors: %v", result.Errors)
	}

	// Test logger.yaml file
	loggerFile := filepath.Join(tempDir, "logger.yaml")
	loggerContent := `version: logger-config/v0.1.0
level: info
format: pretty`
	if err := os.WriteFile(loggerFile, []byte(loggerContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	result, err = validator.ValidateFile(loggerFile)
	if err != nil {
		t.Errorf("ValidateFile() returned error for logger.yaml: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidateFile() should validate logger.yaml, got errors: %v", result.Errors)
	}

	// Test unknown file type
	unknownFile := filepath.Join(tempDir, "unknown.yaml")
	unknownContent := `version: unknown/v1.0`
	if err := os.WriteFile(unknownFile, []byte(unknownContent), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = validator.ValidateFile(unknownFile)
	if err == nil {
		t.Error("ValidateFile() should return error for unknown file type")
	}
	if !strings.Contains(err.Error(), "unable to determine schema") {
		t.Errorf("ValidateFile() error should mention schema determination, got: %s", err.Error())
	}
}

func TestValidateDirectory(t *testing.T) {
	// Create a temporary directory for schemas and test files
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Copy schemas
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	// Copy all config schemas
	schemas := []string{
		"sumpter-config.schema.json",
		"logger-config.schema.json",
		"pii-config.schema.json",
	}
	for _, schema := range schemas {
		source := filepath.Join("../../schemas", "config", "v0.1.0", schema)
		dest := filepath.Join(schemaDir, schema)
		if err := copyFile(source, dest); err != nil {
			t.Fatalf("Failed to copy schema %s: %v", schema, err)
		}
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Create test directory with config files
	configDir := filepath.Join(tempDir, "configs")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("Failed to create config dir: %v", err)
	}

	// Create valid sumpter.yaml
	sumpterFile := filepath.Join(configDir, "sumpter.yaml")
	sumpterContent := `version: config/v0.1.0
logging:
  level: info
pii:
  mode: safe
paths:
  cache_dir: cache`
	if err := os.WriteFile(sumpterFile, []byte(sumpterContent), 0644); err != nil {
		t.Fatalf("Failed to write sumpter.yaml: %v", err)
	}

	// Create valid logger.yaml
	loggerFile := filepath.Join(configDir, "logger.yaml")
	loggerContent := `version: logger-config/v0.1.0
level: info
format: pretty`
	if err := os.WriteFile(loggerFile, []byte(loggerContent), 0644); err != nil {
		t.Fatalf("Failed to write logger.yaml: %v", err)
	}

	// Create invalid pii.yaml
	piiFile := filepath.Join(configDir, "pii.yaml")
	piiContent := `version: pii-config/v0.1.0
mode: invalid_mode`
	if err := os.WriteFile(piiFile, []byte(piiContent), 0644); err != nil {
		t.Fatalf("Failed to write pii.yaml: %v", err)
	}

	// Validate directory
	results, err := validator.ValidateDirectory(configDir)
	if err != nil {
		t.Errorf("ValidateDirectory() returned error: %v", err)
	}

	// Should have 3 results
	if len(results) != 3 {
		t.Errorf("ValidateDirectory() should return 3 results, got %d", len(results))
	}

	// Check sumpter.yaml (should be valid)
	if result, ok := results[sumpterFile]; !ok {
		t.Errorf("ValidateDirectory() should include sumpter.yaml")
	} else if !result.IsValid() {
		t.Errorf("sumpter.yaml should be valid, got errors: %v", result.Errors)
	}

	// Check logger.yaml (should be valid)
	if result, ok := results[loggerFile]; !ok {
		t.Errorf("ValidateDirectory() should include logger.yaml")
	} else if !result.IsValid() {
		t.Errorf("logger.yaml should be valid, got errors: %v", result.Errors)
	}

	// Check pii.yaml (should be invalid)
	if result, ok := results[piiFile]; !ok {
		t.Errorf("ValidateDirectory() should include pii.yaml")
	} else if result.IsValid() {
		t.Error("pii.yaml should be invalid")
	}
}

func TestValidateDirectoryEmpty(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Copy schemas
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/config/v0.1.0/sumpter-config.schema.json"
	destSchema := filepath.Join(schemaDir, "sumpter-config.schema.json")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Create empty directory
	emptyDir := filepath.Join(tempDir, "empty")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("Failed to create empty dir: %v", err)
	}

	// Validate empty directory
	results, err := validator.ValidateDirectory(emptyDir)
	if err != nil {
		t.Errorf("ValidateDirectory() returned error for empty dir: %v", err)
	}

	// Should return empty results
	if len(results) != 0 {
		t.Errorf("ValidateDirectory() should return empty results for empty dir, got %d", len(results))
	}
}

// Helper function to copy files
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}
