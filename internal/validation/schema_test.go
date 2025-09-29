package validation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	defer os.RemoveAll(tempDir)

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

func TestValidateMainConfig(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy the actual schema file
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	// Copy the sumpter config schema
	sourceSchema := "../../schemas/config/v0.1.0/sumpter-config.schema.json"
	destSchema := filepath.Join(schemaDir, "sumpter-config.schema.json")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Test valid main config
	validConfig := `{
		"version": "config/v0.1.0",
		"logging": {
			"level": "info",
			"format": "pretty"
		},
		"pii": {
			"mode": "safe"
		},
		"paths": {
			"cache_dir": "cache",
			"temp_dir": "temp",
			"output_dir": "output"
		}
	}`

	result, err := validator.ValidateMainConfig([]byte(validConfig), "test-main.yaml")
	if err != nil {
		t.Errorf("ValidateMainConfig() returned error for valid config: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidateMainConfig() should validate valid config, got errors: %v", result.Errors)
	}

	// Test invalid main config (missing version)
	invalidConfig := `{
		"logging": {
			"level": "info"
		},
		"pii": {
			"mode": "safe"
		},
		"paths": {
			"cache_dir": "cache"
		}
	}`

	result, err = validator.ValidateMainConfig([]byte(invalidConfig), "test-main-invalid.yaml")
	if err != nil {
		t.Errorf("ValidateMainConfig() returned error for invalid config: %v", err)
	}
	if result.IsValid() {
		t.Error("ValidateMainConfig() should reject invalid config")
	}
	if result.ErrorCount() == 0 {
		t.Error("ValidateMainConfig() should return validation errors for invalid config")
	}
}

func TestValidateLoggerConfig(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy the logger config schema
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/config/v0.1.0/logger-config.schema.json"
	destSchema := filepath.Join(schemaDir, "logger-config.schema.json")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Test valid logger config
	validConfig := `{
		"version": "logger-config/v0.1.0",
		"level": "info",
		"format": "pretty",
		"use_color": true,
		"component": "test"
	}`

	result, err := validator.ValidateLoggerConfig([]byte(validConfig), "test-logger.yaml")
	if err != nil {
		t.Errorf("ValidateLoggerConfig() returned error for valid config: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidateLoggerConfig() should validate valid config, got errors: %v", result.Errors)
	}

	// Test invalid logger config (invalid level)
	invalidConfig := `{
		"version": "logger-config/v0.1.0",
		"level": "invalid",
		"format": "pretty"
	}`

	result, err = validator.ValidateLoggerConfig([]byte(invalidConfig), "test-logger-invalid.yaml")
	if err != nil {
		t.Errorf("ValidateLoggerConfig() returned error for invalid config: %v", err)
	}
	if result.IsValid() {
		t.Error("ValidateLoggerConfig() should reject invalid config")
	}
}

func TestValidatePIIConfig(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy the PII config schema
	schemaDir := filepath.Join(tempDir, "schemas", "config", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/config/v0.1.0/pii-config.schema.json"
	destSchema := filepath.Join(schemaDir, "pii-config.schema.json")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

	// Test valid PII config
	validConfig := `{
		"version": "pii-config/v0.1.0",
		"mode": "safe",
		"safe_only": true
	}`

	result, err := validator.ValidatePIIConfig([]byte(validConfig), "test-pii.yaml")
	if err != nil {
		t.Errorf("ValidatePIIConfig() returned error for valid config: %v", err)
	}
	if !result.IsValid() {
		t.Errorf("ValidatePIIConfig() should validate valid config, got errors: %v", result.Errors)
	}

	// Test invalid PII config (invalid mode)
	invalidConfig := `{
		"version": "pii-config/v0.1.0",
		"mode": "invalid"
	}`

	result, err = validator.ValidatePIIConfig([]byte(invalidConfig), "test-pii-invalid.yaml")
	if err != nil {
		t.Errorf("ValidatePIIConfig() returned error for invalid config: %v", err)
	}
	if result.IsValid() {
		t.Error("ValidatePIIConfig() should reject invalid config")
	}
}

func TestValidateExtractConfig(t *testing.T) {
	// Create a temporary directory for schemas
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Copy the extract config schema
	schemaDir := filepath.Join(tempDir, "schemas", "extract", "v0.1.0")
	if err := os.MkdirAll(schemaDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dir: %v", err)
	}

	sourceSchema := "../../schemas/extract/v0.1.0/extract-record-match.schema.json"
	destSchema := filepath.Join(schemaDir, "extract-record-match.schema.json")
	if err := copyFile(sourceSchema, destSchema); err != nil {
		t.Fatalf("Failed to copy schema: %v", err)
	}

	validator := NewSchemaValidator(filepath.Join(tempDir, "schemas"))

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
}

func TestValidateFile(t *testing.T) {
	// Create a temporary directory for schemas and test files
	tempDir, err := os.MkdirTemp("", "sumpter-validation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

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
	defer os.RemoveAll(tempDir)

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
	defer os.RemoveAll(tempDir)

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
