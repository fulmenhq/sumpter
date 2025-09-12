package validation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/goneat/pkg/schema"
	"gopkg.in/yaml.v3"
)

// SchemaValidator provides schema validation for Sumpter configs
type SchemaValidator struct {
	schemaDir string
}

// NewSchemaValidator creates a new schema validator
func NewSchemaValidator(schemaDir string) *SchemaValidator {
	return &SchemaValidator{
		schemaDir: schemaDir,
	}
}

// ValidationResult represents the result of schema validation
type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
	File   string            `json:"file,omitempty"`
	Schema string            `json:"schema,omitempty"`
}

// ValidationError represents a validation error with context
type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// ValidateMainConfig validates a main config against its schema
func (v *SchemaValidator) ValidateMainConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaPath := filepath.Join(v.schemaDir, "config", "v0.1.0", "sumpter-config.schema.json")
	return v.validateAgainstSchema(configData, schemaPath, configFile, "sumpter-config-v0.1.0")
}

// ValidateLoggerConfig validates a logger config against its schema
func (v *SchemaValidator) ValidateLoggerConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaPath := filepath.Join(v.schemaDir, "config", "v0.1.0", "logger-config.schema.json")
	return v.validateAgainstSchema(configData, schemaPath, configFile, "logger-config-v0.1.0")
}

// ValidatePIIConfig validates a PII config against its schema
func (v *SchemaValidator) ValidatePIIConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaPath := filepath.Join(v.schemaDir, "config", "v0.1.0", "pii-config.schema.json")
	return v.validateAgainstSchema(configData, schemaPath, configFile, "pii-config-v0.1.0")
}

// validateAgainstSchema validates data against a schema file
func (v *SchemaValidator) validateAgainstSchema(data []byte, schemaPath, dataFile, schemaName string) (*ValidationResult, error) {
	// Read schema file
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Parse data to interface{} for validation
	var dataInterface interface{}

	// Try YAML first, then JSON
	if err := yaml.Unmarshal(data, &dataInterface); err != nil {
		// Fallback to JSON
		if jsonErr := json.Unmarshal(data, &dataInterface); jsonErr != nil {
			return nil, fmt.Errorf("failed to parse data as YAML or JSON: yaml error: %w, json error: %w", err, jsonErr)
		}
	}

	// Validate using goneat schema library
	result, err := schema.ValidateFromBytes(schemaBytes, dataInterface)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}

	// Convert result to our format
	validationResult := &ValidationResult{
		Valid:  result.Valid,
		File:   dataFile,
		Schema: schemaName,
	}

	// Convert errors
	for _, err := range result.Errors {
		validationResult.Errors = append(validationResult.Errors, ValidationError{
			Path:    err.Path,
			Message: err.Message,
			File:    dataFile,
			Line:    err.Context.LineNumber,
		})
	}

	return validationResult, nil
}

// ValidateFile validates a config file against its appropriate schema
func (v *SchemaValidator) ValidateFile(configFile string) (*ValidationResult, error) {
	// Read config file
	data, err := os.ReadFile(configFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configFile, err)
	}

	// Determine schema based on filename or content
	filename := filepath.Base(configFile)

	switch filename {
	case "sumpter.yaml", "sumpter.yml":
		return v.ValidateMainConfig(data, configFile)
	case "logger.yaml", "logger.yml":
		return v.ValidateLoggerConfig(data, configFile)
	case "pii.yaml", "pii.yml":
		return v.ValidatePIIConfig(data, configFile)
	default:
		// Try to detect from content by looking for version field
		var config map[string]interface{}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return nil, fmt.Errorf("failed to parse config file %s: %w", configFile, err)
		}

		if version, ok := config["version"].(string); ok {
			switch version {
			case "config/v0.1.0":
				return v.ValidateMainConfig(data, configFile)
			case "logger-config/v0.1.0":
				return v.ValidateLoggerConfig(data, configFile)
			case "pii-config/v0.1.0":
				return v.ValidatePIIConfig(data, configFile)
			}
		}

		return nil, fmt.Errorf("unable to determine schema for config file %s", configFile)
	}
}

// ValidateDirectory validates all config files in a directory
func (v *SchemaValidator) ValidateDirectory(dirPath string) (map[string]*ValidationResult, error) {
	results := make(map[string]*ValidationResult)

	// Find all YAML files in the directory
	pattern := filepath.Join(dirPath, "*.yaml")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %s: %w", pattern, err)
	}

	// Also check for .yml files
	pattern = filepath.Join(dirPath, "*.yml")
	ymlMatches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to glob pattern %s: %w", pattern, err)
	}
	matches = append(matches, ymlMatches...)

	// Validate each file
	for _, file := range matches {
		result, err := v.ValidateFile(file)
		if err != nil {
			// Store error as invalid result
			results[file] = &ValidationResult{
				Valid: false,
				File:  file,
				Errors: []ValidationError{{
					Path:    "",
					Message: err.Error(),
					File:    file,
				}},
			}
		} else {
			results[file] = result
		}
	}

	return results, nil
}

// IsValid checks if a validation result is valid
func (r *ValidationResult) IsValid() bool {
	return r.Valid
}

// ErrorCount returns the number of validation errors
func (r *ValidationResult) ErrorCount() int {
	return len(r.Errors)
}

// ErrorSummary returns a summary of validation errors
func (r *ValidationResult) ErrorSummary() string {
	if r.Valid {
		return "✅ Validation passed"
	}

	summary := fmt.Sprintf("❌ Validation failed (%d errors):\n", len(r.Errors))
	for i, err := range r.Errors {
		summary += fmt.Sprintf("  %d. %s: %s", i+1, err.Path, err.Message)
		if err.Line > 0 {
			summary += fmt.Sprintf(" (line %d)", err.Line)
		}
		summary += "\n"
	}

	return summary
}
