package validation

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/goneat/pkg/schema"
	"gopkg.in/yaml.v3"
)

// SchemaValidator provides schema validation for Sumpter configs
type SchemaValidator struct {
	schemaDir string
	schemaFS  fs.FS
}

const schemaNameExtractRecordMatchPrefix = "extract-record-match-"

// NewSchemaValidator creates a new schema validator backed by a filesystem path.
func NewSchemaValidator(schemaDir string) *SchemaValidator {
	return &SchemaValidator{
		schemaDir: schemaDir,
	}
}

// NewSchemaValidatorFromFS creates a schema validator backed by the supplied filesystem.
func NewSchemaValidatorFromFS(schemaFS fs.FS) *SchemaValidator {
	return &SchemaValidator{
		schemaFS: schemaFS,
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
	schemaBytes, err := v.loadSchema("config", "v0.1.0", "sumpter-config.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("config", "v0.1.0", "sumpter-config.schema.json"), err)
	}
	return v.validateAgainstSchema(configData, schemaBytes, configFile, "sumpter-config-v0.1.0")
}

// ValidateLoggerConfig validates a logger config against its schema
func (v *SchemaValidator) ValidateLoggerConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("config", "v0.1.0", "logger-config.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("config", "v0.1.0", "logger-config.schema.json"), err)
	}
	return v.validateAgainstSchema(configData, schemaBytes, configFile, "logger-config-v0.1.0")
}

// ValidatePIIConfig validates a PII config against its schema
func (v *SchemaValidator) ValidatePIIConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("config", "v0.1.0", "pii-config.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("config", "v0.1.0", "pii-config.schema.json"), err)
	}
	return v.validateAgainstSchema(configData, schemaBytes, configFile, "pii-config-v0.1.0")
}

// ValidateExtractConfig validates an extract configuration against its schema.
func (v *SchemaValidator) ValidateExtractConfig(configData []byte, configFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("schemas", "extract", "v0.1.0", "extract-record-match-schema.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("schemas", "extract", "v0.1.0", "extract-record-match-schema.yaml"), err)
	}
	return v.validateAgainstSchema(configData, schemaBytes, configFile, "extract-record-match-v0.1.0")
}

// ValidateRecipeManifest validates a recipe manifest against the embedded schema.
func (v *SchemaValidator) ValidateRecipeManifest(data []byte, manifestFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("recipes", "v0.1.0", "recipe.schema.yaml")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("recipes", "v0.1.0", "recipe.schema.yaml"), err)
	}
	return v.validateAgainstSchema(data, schemaBytes, manifestFile, "recipe-manifest-v0.1.0")
}

// ValidateProvenanceManifest validates a provenance sidecar manifest.
func (v *SchemaValidator) ValidateProvenanceManifest(data []byte, manifestFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("provenance", "v1.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("provenance", "v1.json"), err)
	}
	return v.validateAgainstSchema(data, schemaBytes, manifestFile, "provenance-v1")
}

// ValidateDispositionSummary validates an extract applicability disposition summary.
func (v *SchemaValidator) ValidateDispositionSummary(data []byte, summaryFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("extract", "v0.1.0", "dispositions.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("extract", "v0.1.0", "dispositions.schema.json"), err)
	}
	return v.validateAgainstSchema(data, schemaBytes, summaryFile, "extract-dispositions-v0.1.0")
}

// ValidateFailureManifest validates an extract continue-on-error failure manifest.
func (v *SchemaValidator) ValidateFailureManifest(data []byte, manifestFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("extract", "v0.1.0", "failures.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("extract", "v0.1.0", "failures.schema.json"), err)
	}
	return v.validateAgainstSchema(data, schemaBytes, manifestFile, "extract-failures-v0.1.0")
}

// ValidateExtractRecordEnvelope validates one extract NDJSON record envelope.
func (v *SchemaValidator) ValidateExtractRecordEnvelope(data []byte, recordFile string) (*ValidationResult, error) {
	schemaBytes, err := v.loadSchema("extract", "v0.1.0", "extract-record-envelope.schema.json")
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", path.Join("extract", "v0.1.0", "extract-record-envelope.schema.json"), err)
	}
	return v.validateAgainstSchema(data, schemaBytes, recordFile, "extract-record-envelope-v0.1.0")
}

func (v *SchemaValidator) loadSchema(parts ...string) ([]byte, error) {
	rel := path.Join(parts...)

	if v.schemaFS != nil {
		data, err := fs.ReadFile(v.schemaFS, rel)
		if err == nil {
			return data, nil
		}
		if errors.Is(err, fs.ErrNotExist) {
			alt := path.Join("schemas", rel)
			if data, altErr := fs.ReadFile(v.schemaFS, alt); altErr == nil {
				return data, nil
			} else if !errors.Is(altErr, fs.ErrNotExist) {
				return nil, altErr
			}
		} else {
			return nil, err
		}
	}

	full := filepath.Join(append([]string{v.schemaDir}, parts...)...)
	return os.ReadFile(full) // #nosec G304 - Internal schema loading, controlled path
}

// validateAgainstSchema validates data against a schema file
func (v *SchemaValidator) validateAgainstSchema(data []byte, schemaBytes []byte, dataFile, schemaName string) (*ValidationResult, error) {
	// Parse data to interface{} for validation
	var dataInterface interface{}
	// Try YAML first, then JSON
	if err := yaml.Unmarshal(data, &dataInterface); err != nil {
		// Fallback to JSON
		if jsonErr := json.Unmarshal(data, &dataInterface); jsonErr != nil {
			return nil, fmt.Errorf("failed to parse data as YAML or JSON: yaml error: %w, json error: %w", err, jsonErr)
		}
	}

	// Parse schema - try YAML first, then JSON, then convert to JSON for goneat
	var schemaInterface interface{}
	if err := yaml.Unmarshal(schemaBytes, &schemaInterface); err != nil {
		// Try JSON
		if jsonErr := json.Unmarshal(schemaBytes, &schemaInterface); jsonErr != nil {
			return nil, fmt.Errorf("failed to parse schema as YAML or JSON: yaml error: %w, json error: %w", err, jsonErr)
		}
	}

	// Convert schema to JSON bytes for goneat
	schemaJSONBytes, err := json.Marshal(schemaInterface)
	if err != nil {
		return nil, fmt.Errorf("failed to convert schema to JSON: %w", err)
	}

	// Validate using goneat schema library
	result, err := schema.ValidateFromBytes(schemaJSONBytes, dataInterface)
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
	enrichExtractValidationErrors(validationResult, dataInterface, dataFile, schemaName)

	return validationResult, nil
}

func enrichExtractValidationErrors(result *ValidationResult, data interface{}, dataFile, schemaName string) {
	if result == nil || result.Valid || !strings.HasPrefix(schemaName, schemaNameExtractRecordMatchPrefix) {
		return
	}

	root, ok := data.(map[string]interface{})
	if !ok {
		return
	}
	metadata, ok := root["validation_metadata"].(map[string]interface{})
	if !ok {
		return
	}
	rawReconciliations, ok := metadata["reconciliations"].([]interface{})
	if !ok {
		return
	}

	for idx, raw := range rawReconciliations {
		reconciliation, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if _, hasComponents := reconciliation["components"]; hasComponents {
			continue
		}
		if _, hasGroupBy := reconciliation["group_by"]; hasGroupBy {
			continue
		}
		path := fmt.Sprintf("validation_metadata.reconciliations[%d]", idx)
		if hasValidationError(result.Errors, path, "components", "group_by") {
			continue
		}
		result.Errors = append(result.Errors, ValidationError{
			Path:    path,
			Message: "at least one of `components` or `group_by` is required",
			File:    dataFile,
		})
	}
}

func hasValidationError(errors []ValidationError, path string, terms ...string) bool {
	for _, err := range errors {
		if err.Path != path {
			continue
		}
		matches := true
		for _, term := range terms {
			if !strings.Contains(err.Message, term) {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

// ValidateFile validates a config file against its appropriate schema
func (v *SchemaValidator) ValidateFile(configFile string) (*ValidationResult, error) {
	// Read config file
	data, err := os.ReadFile(configFile) // #nosec G304 - Internal config validation, controlled path
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
			case "recipe/v0.1.0":
				return v.ValidateRecipeManifest(data, configFile)
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
