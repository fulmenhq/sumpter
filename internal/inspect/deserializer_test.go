package inspect

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestRegistryDeserializer_DeserializeRegistryFile(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "sumpter-test-deserializer")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create schema directory structure
	schemaDir := filepath.Join(tempDir, "schemas")
	dialectsDir := filepath.Join(schemaDir, "dialects", "v0.1.0")
	if err := os.MkdirAll(dialectsDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dirs: %v", err)
	}

	// Create a minimal test schema file for registry structure
	testSchema := `$schema: https://json-schema.org/draft/2020-12/schema
title: Test Dialect Registry
type: object
required:
  - registry_version
  - last_updated
  - dialects
properties:
  registry_version:
    type: string
    pattern: '^v[0-9]+\.[0-9]+\.[0-9]+(-alpha)?$'
  last_updated:
    type: string
    format: date-time
  dialects:
    type: array
    minItems: 1
    items:
      type: object
      required:
        - dialect_id
        - name
        - description
        - status
        - priority
        - realm
        - patterns
      properties:
        dialect_id:
          type: string
          pattern: '^[a-z0-9-]+$'
        name:
          type: string
        description:
          type: string
        status:
          type: string
          enum:
            - active
            - development
            - deprecated
        priority:
          type: string
          enum:
            - high
            - medium
            - low
        realm:
          type: string
          enum:
            - retail
            - finance
            - healthcare
            - environment
            - legal
            - general
        patterns:
          type: array
          minItems: 1
          items:
            type: object
            required:
              - pattern_id
              - name
              - selector
              - weight
              - ecosystem
            properties:
              pattern_id:
                type: string
              name:
                type: string
              selector:
                type: string
              weight:
                type: number
                minimum: 0
                maximum: 1
              ecosystem:
                type: string
            additionalProperties: false
      additionalProperties: false
additionalProperties: false`

	destSchema := filepath.Join(dialectsDir, "dialect-registry.schema.yaml")
	if err := os.WriteFile(destSchema, []byte(testSchema), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	// Create a test registry file
	testRegistry := `registry_version: "v0.1.0"
last_updated: "2025-09-17T17:00:00Z"
dialects:
  - dialect_id: "test-dialect"
    name: "Test Dialect"
    description: "Test dialect for deserializer"
    status: "active"
    priority: "medium"
    realm: "general"
    patterns:
      - pattern_id: "test-pattern"
        name: "Test Pattern"
        selector: "local-name()='test'"
        weight: 0.8
        ecosystem: "test"
`

	registryPath := filepath.Join(tempDir, "test-registry.yaml")
	if err := os.WriteFile(registryPath, []byte(testRegistry), 0644); err != nil {
		t.Fatalf("Failed to write test registry: %v", err)
	}

	// Test deserialization
	deserializer := NewRegistryDeserializer(logger, schemaDir)
	registry, err := deserializer.DeserializeRegistryFile(registryPath)
	if err != nil {
		t.Fatalf("Failed to deserialize registry: %v", err)
	}

	if registry == nil {
		t.Fatal("Registry is nil")
		return
	}

	if len(registry.Dialects) != 1 {
		t.Errorf("Expected 1 dialect, got %d", len(registry.Dialects))
	}

	if registry.Dialects[0].DialectID != "test-dialect" {
		t.Errorf("Expected dialect ID 'test-dialect', got %s", registry.Dialects[0].DialectID)
	}
}

func TestRegistryDeserializer_ValidateRegistryFile(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "sumpter-test-validator")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create schema directory structure
	schemaDir := filepath.Join(tempDir, "schemas")
	dialectsDir := filepath.Join(schemaDir, "dialects", "v0.1.0")
	if err := os.MkdirAll(dialectsDir, 0755); err != nil {
		t.Fatalf("Failed to create schema dirs: %v", err)
	}

	// Create a minimal test schema file for registry structure
	testSchema := `$schema: https://json-schema.org/draft/2020-12/schema
title: Test Dialect Registry
type: object
required:
  - registry_version
  - last_updated
  - dialects
properties:
  registry_version:
    type: string
    pattern: '^v[0-9]+\.[0-9]+\.[0-9]+(-alpha)?$'
  last_updated:
    type: string
    format: date-time
  dialects:
    type: array
    minItems: 1
    items:
      type: object
      required:
        - dialect_id
        - name
        - description
        - status
        - priority
        - realm
        - patterns
      properties:
        dialect_id:
          type: string
          pattern: '^[a-z0-9-]+$'
        name:
          type: string
        description:
          type: string
        status:
          type: string
          enum:
            - active
            - development
            - deprecated
        priority:
          type: string
          enum:
            - high
            - medium
            - low
        realm:
          type: string
          enum:
            - retail
            - finance
            - healthcare
            - environment
            - legal
            - general
        patterns:
          type: array
          minItems: 1
          items:
            type: object
            required:
              - pattern_id
              - name
              - selector
              - weight
              - ecosystem
            properties:
              pattern_id:
                type: string
              name:
                type: string
              selector:
                type: string
              weight:
                type: number
                minimum: 0
                maximum: 1
              ecosystem:
                type: string
            additionalProperties: false
      additionalProperties: false
additionalProperties: false`

	destSchema := filepath.Join(dialectsDir, "dialect-registry.schema.yaml")
	if err := os.WriteFile(destSchema, []byte(testSchema), 0644); err != nil {
		t.Fatalf("Failed to write schema file: %v", err)
	}

	// Create a valid test registry file
	validRegistry := `registry_version: "v0.1.0"
last_updated: "2025-09-17T17:00:00Z"
dialects:
  - dialect_id: "valid-dialect"
    name: "Valid Dialect"
    description: "Valid test dialect"
    status: "active"
    priority: "low"
    realm: "general"
    patterns:
      - pattern_id: "valid-pattern"
        name: "Valid Pattern"
        selector: "local-name()='valid'"
        weight: 0.5
        ecosystem: "test"
`

	validPath := filepath.Join(tempDir, "valid-registry.yaml")
	if err := os.WriteFile(validPath, []byte(validRegistry), 0644); err != nil {
		t.Fatalf("Failed to write valid registry: %v", err)
	}

	// Test validation
	deserializer := NewRegistryDeserializer(logger, schemaDir)
	err = deserializer.ValidateRegistryFile(validPath)
	if err != nil {
		t.Fatalf("Valid registry should pass validation: %v", err)
	}

	// Create an invalid test registry file (missing required field)
	invalidRegistry := `registry_version: "v0.1.0"
last_updated: "2025-09-17T17:00:00Z"
dialects:
  - name: "Invalid Dialect"
    description: "Invalid test dialect - missing dialect_id"
    status: "active"
    priority: "low"
    realm: "general"
    patterns:
      - pattern_id: "invalid-pattern"
        name: "Invalid Pattern"
        selector: "local-name()='invalid'"
        weight: 0.5
        ecosystem: "test"
`

	invalidPath := filepath.Join(tempDir, "invalid-registry.yaml")
	if err := os.WriteFile(invalidPath, []byte(invalidRegistry), 0644); err != nil {
		t.Fatalf("Failed to write invalid registry: %v", err)
	}

	// Test validation of invalid file
	err = deserializer.ValidateRegistryFile(invalidPath)
	if err == nil {
		t.Error("Invalid registry should fail validation")
	}
}
