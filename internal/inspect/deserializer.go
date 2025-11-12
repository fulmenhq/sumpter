package inspect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/goneat/pkg/schema"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// RegistryDeserializer handles deserialization and validation of dialect registry files
type RegistryDeserializer struct {
	logger    Logger
	schemaDir string
}

// NewRegistryDeserializer creates a new registry deserializer
func NewRegistryDeserializer(logger Logger, schemaDir string) *RegistryDeserializer {
	return &RegistryDeserializer{
		logger:    logger,
		schemaDir: schemaDir,
	}
}

// DeserializeRegistryFile reads and validates a dialect registry file
func (d *RegistryDeserializer) DeserializeRegistryFile(registryPath string) (*DialectRegistry, error) {
	// Read the registry file
	registryBytes, err := os.ReadFile(registryPath) // #nosec G304 - Internal dialect registry loading
	if err != nil {
		return nil, fmt.Errorf("failed to read registry file %s: %w", registryPath, err)
	}

	d.logger.Debug("read registry file",
		zap.String("path", registryPath),
		zap.Int("size_bytes", len(registryBytes)))

	// Validate against schema first (fail-fast)
	if err := d.validateRegistryFile(registryPath, registryBytes); err != nil {
		return nil, fmt.Errorf("registry validation failed: %w", err)
	}

	// Parse the registry data
	var registry DialectRegistry
	if err := d.parseRegistryData(registryBytes, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry data: %w", err)
	}

	d.logger.Info("successfully deserialized registry",
		zap.String("path", registryPath),
		zap.Int("dialect_count", len(registry.Dialects)),
		zap.String("version", registry.RegistryVersion))

	return &registry, nil
}

// validateRegistryFile validates the registry file against the schema
func (d *RegistryDeserializer) validateRegistryFile(registryPath string, registryBytes []byte) error {
	// Get the schema file path
	schemaPath := filepath.Join(d.schemaDir, "dialects", "v0.1.0", "dialect-registry.schema.yaml")

	// Read schema file
	schemaBytes, err := os.ReadFile(schemaPath) // #nosec G304 - Internal schema path, controlled
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Validate using goneat schema library
	result, err := schema.ValidateDataFromBytes(schemaBytes, registryBytes)
	if err != nil {
		return fmt.Errorf("schema validation error: %w", err)
	}

	if !result.Valid {
		d.logger.Error("registry schema validation failed",
			zap.String("registry_path", registryPath),
			zap.String("schema_path", schemaPath))

		// Log detailed validation errors
		for _, validationErr := range result.Errors {
			d.logger.Error("validation error",
				zap.String("path", validationErr.Path),
				zap.String("message", validationErr.Message))
		}

		return fmt.Errorf("registry file failed schema validation: %d errors found", len(result.Errors))
	}

	d.logger.Debug("registry schema validation passed",
		zap.String("registry_path", registryPath),
		zap.String("schema_path", schemaPath))

	return nil
}

// parseRegistryData parses the registry data from bytes
func (d *RegistryDeserializer) parseRegistryData(data []byte, registry *DialectRegistry) error {
	// Try YAML first (our standard format), then JSON for backward compatibility
	if err := yaml.Unmarshal(data, registry); err != nil {
		d.logger.Debug("failed to parse as YAML, trying JSON", zap.Error(err))

		// Try JSON as fallback
		if jsonErr := json.Unmarshal(data, registry); jsonErr != nil {
			return fmt.Errorf("failed to parse registry as YAML or JSON: yaml error: %w, json error: %w", err, jsonErr)
		}
	}

	d.logger.Debug("successfully parsed registry data",
		zap.String("format", "yaml"), // Assume YAML since it's our standard
		zap.Int("dialect_count", len(registry.Dialects)))

	return nil
}

// DeserializeRegistryFromBytes deserializes registry data from bytes with validation
func (d *RegistryDeserializer) DeserializeRegistryFromBytes(registryBytes []byte, sourceName string) (*DialectRegistry, error) {
	d.logger.Debug("deserializing registry from bytes",
		zap.String("source", sourceName),
		zap.Int("size_bytes", len(registryBytes)))

	// Read schema file
	schemaPath := filepath.Join(d.schemaDir, "dialects", "v0.1.0", "dialect-registry.schema.yaml")
	schemaBytes, err := os.ReadFile(schemaPath) // #nosec G304 - Internal schema path, controlled
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Validate against schema
	result, err := schema.ValidateDataFromBytes(schemaBytes, registryBytes)
	if err != nil {
		return nil, fmt.Errorf("schema validation error: %w", err)
	}

	if !result.Valid {
		d.logger.Error("registry bytes validation failed",
			zap.String("source", sourceName))

		for _, validationErr := range result.Errors {
			d.logger.Error("validation error",
				zap.String("path", validationErr.Path),
				zap.String("message", validationErr.Message))
		}

		return nil, fmt.Errorf("registry data failed schema validation: %d errors found", len(result.Errors))
	}

	// Parse the registry data
	var registry DialectRegistry
	if err := d.parseRegistryData(registryBytes, &registry); err != nil {
		return nil, fmt.Errorf("failed to parse registry data: %w", err)
	}

	d.logger.Info("successfully deserialized registry from bytes",
		zap.String("source", sourceName),
		zap.Int("dialect_count", len(registry.Dialects)),
		zap.String("version", registry.RegistryVersion))

	return &registry, nil
}

// ValidateRegistryFile validates a registry file without deserializing it
func (d *RegistryDeserializer) ValidateRegistryFile(registryPath string) error {
	d.logger.Debug("validating registry file", zap.String("path", registryPath))

	// Read the file
	registryBytes, err := os.ReadFile(registryPath) // #nosec G304 - Internal dialect registry loading
	if err != nil {
		return fmt.Errorf("failed to read registry file: %w", err)
	}

	// Read schema file
	schemaPath := filepath.Join(d.schemaDir, "dialects", "v0.1.0", "dialect-registry.schema.yaml")
	schemaBytes, err := os.ReadFile(schemaPath) // #nosec G304 - Internal schema path, controlled
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Validate against schema
	result, err := schema.ValidateDataFromBytes(schemaBytes, registryBytes)
	if err != nil {
		return fmt.Errorf("schema validation error: %w", err)
	}

	if !result.Valid {
		d.logger.Error("registry file validation failed",
			zap.String("registry_path", registryPath),
			zap.String("schema_path", schemaPath))

		for _, validationErr := range result.Errors {
			d.logger.Error("validation error",
				zap.String("path", validationErr.Path),
				zap.String("message", validationErr.Message))
		}

		return fmt.Errorf("registry file validation failed: %d errors found", len(result.Errors))
	}

	d.logger.Info("registry file validation passed",
		zap.String("registry_path", registryPath))

	return nil
}
