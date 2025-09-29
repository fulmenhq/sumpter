package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

// Loader handles configuration file loading and validation
type Loader struct {
	paths     *Paths
	validator *validation.SchemaValidator
}

// NewLoader creates a new configuration loader
func NewLoader(paths *Paths) *Loader {
	schemaFS, err := assets.GetSchemasFS()
	var validator *validation.SchemaValidator
	if err != nil {
		schemaDir := filepath.Join(paths.Home, "schemas")
		validator = validation.NewSchemaValidator(schemaDir)
	} else {
		validator = validation.NewSchemaValidatorFromFS(schemaFS)
	}
	return NewLoaderWithValidator(paths, validator)
}

// NewLoaderWithValidator creates a loader using the supplied schema validator.
func NewLoaderWithValidator(paths *Paths, validator *validation.SchemaValidator) *Loader {
	return &Loader{
		paths:     paths,
		validator: validator,
	}
}

// LoadMainConfig loads the main Sumpter configuration
func (l *Loader) LoadMainConfig() (*MainConfig, error) {
	configPath := l.paths.GetDefaultConfigPath()

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return l.getDefaultMainConfig(), nil
	}

	// Load config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file %s: %w", configPath, err)
	}

	// Validate against schema first
	validationResult, err := l.validator.ValidateMainConfig(data, configPath)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed for %s: %w", configPath, err)
	}

	if !validationResult.IsValid() {
		return nil, fmt.Errorf("config validation failed for %s:\n%s", configPath, validationResult.ErrorSummary())
	}

	// Parse config after validation
	var config MainConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file %s: %w", configPath, err)
	}

	return &config, nil
}

// LoadLoggerConfig loads the logger-specific configuration
func (l *Loader) LoadLoggerConfig() (*LoggerConfig, error) {
	configPath := l.paths.GetLoggerConfigPath()

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return l.getDefaultLoggerConfig(), nil
	}

	// Load config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read logger config file %s: %w", configPath, err)
	}

	// Validate against schema first
	validationResult, err := l.validator.ValidateLoggerConfig(data, configPath)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed for %s: %w", configPath, err)
	}

	if !validationResult.IsValid() {
		return nil, fmt.Errorf("logger config validation failed for %s:\n%s", configPath, validationResult.ErrorSummary())
	}

	// Parse config after validation
	var config LoggerConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse logger config file %s: %w", configPath, err)
	}

	return &config, nil
}

// LoadPIIConfig loads the PII configuration
func (l *Loader) LoadPIIConfig() (*PIIConfig, error) {
	configPath := l.paths.GetPIIConfigPath()

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return l.getDefaultPIIConfig(), nil
	}

	// Load config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read PII config file %s: %w", configPath, err)
	}

	// Validate against schema first
	validationResult, err := l.validator.ValidatePIIConfig(data, configPath)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed for %s: %w", configPath, err)
	}

	if !validationResult.IsValid() {
		return nil, fmt.Errorf("PII config validation failed for %s:\n%s", configPath, validationResult.ErrorSummary())
	}

	// Parse config after validation
	var config PIIConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse PII config file %s: %w", configPath, err)
	}

	return &config, nil
}

// SaveMainConfig saves the main configuration to file
func (l *Loader) SaveMainConfig(config *MainConfig) error {
	return l.saveConfig(l.paths.GetDefaultConfigPath(), config)
}

// SaveLoggerConfig saves the logger configuration to file
func (l *Loader) SaveLoggerConfig(config *LoggerConfig) error {
	return l.saveConfig(l.paths.GetLoggerConfigPath(), config)
}

// SavePIIConfig saves the PII configuration to file
func (l *Loader) SavePIIConfig(config *PIIConfig) error {
	return l.saveConfig(l.paths.GetPIIConfigPath(), config)
}

// saveConfig is a helper to save any config to YAML file
func (l *Loader) saveConfig(path string, config interface{}) error {
	// Ensure config directory exists
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := yaml.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file %s: %w", path, err)
	}

	return nil
}

// Default config getters
func (l *Loader) getDefaultMainConfig() *MainConfig {
	return &MainConfig{
		Version: "config/v0.1.0",
		Logging: *l.getDefaultLoggerConfig(),
		PII:     *l.getDefaultPIIConfig(),
		Paths:   l.getDefaultPathsConfig(),
		Performance: PerformanceConfig{
			MaxMemoryMB:    512,
			BufferSizeKB:   64,
			WorkerCount:    4,
			TimeoutSeconds: 300,
		},
		Telemetry: TelemetryConfig{
			Enabled:              false,
			ServiceName:          "sumpter",
			ServiceVersion:       "dev",
			Environment:          "development",
			BatchSize:            100,
			FlushIntervalSeconds: 30,
		},
	}
}

func (l *Loader) getDefaultLoggerConfig() *LoggerConfig {
	return &LoggerConfig{
		Version:   "logger-config/v0.1.0",
		Level:     "info",
		Format:    "pretty",
		UseColor:  true,
		Component: "sumpter",
		File: FileLoggingConfig{
			Enabled: false,
			Rotation: LogRotationConfig{
				Enabled:    true,
				MaxSizeMB:  10,
				MaxAgeDays: 30,
				MaxBackups: 5,
				Compress:   true,
				TimeFormat: "2006-01-02",
			},
		},
		Telemetry: TelemetryConfig{
			Enabled:     false,
			ServiceName: "sumpter",
			Environment: "development",
		},
	}
}

func (l *Loader) getDefaultPIIConfig() *PIIConfig {
	return &PIIConfig{
		Version:  "pii-config/v0.1.0",
		Mode:     "safe",
		SafeOnly: true,
		Patterns: PIIDetectionConfig{
			EnabledDefaults: true,
		},
		Reporting: PIIReportingConfig{
			LogDetections: false,
			LogSummary:    true,
			AlertThresholds: PIIAlertThresholds{
				HighSeverityCount:     5,
				CriticalSeverityCount: 1,
			},
		},
	}
}

func (l *Loader) getDefaultPathsConfig() PathsConfig {
	return PathsConfig{
		CacheDir:  "cache",
		TempDir:   "temp",
		OutputDir: "output",
	}
}

// Validation methods
func (l *Loader) validateMainConfig(config *MainConfig) error {
	if config.Version != "config/v0.1.0" {
		return fmt.Errorf("unsupported config version: %s", config.Version)
	}
	return nil
}

func (l *Loader) validateLoggerConfig(config *LoggerConfig) error {
	if config.Version != "logger-config/v0.1.0" {
		return fmt.Errorf("unsupported logger config version: %s", config.Version)
	}

	validLevels := map[string]bool{
		"trace": true, "debug": true, "info": true, "warn": true, "error": true,
	}
	if !validLevels[config.Level] {
		return fmt.Errorf("invalid log level: %s", config.Level)
	}

	validFormats := map[string]bool{
		"pretty": true, "json": true,
	}
	if !validFormats[config.Format] {
		return fmt.Errorf("invalid log format: %s", config.Format)
	}

	return nil
}

func (l *Loader) validatePIIConfig(config *PIIConfig) error {
	if config.Version != "pii-config/v0.1.0" {
		return fmt.Errorf("unsupported PII config version: %s", config.Version)
	}

	validModes := map[string]bool{
		"off": true, "safe": true, "context": true,
	}
	if !validModes[config.Mode] {
		return fmt.Errorf("invalid PII mode: %s", config.Mode)
	}

	return nil
}

// ValidateConfigFile validates a config file against its schema without loading it
func (l *Loader) ValidateConfigFile(configPath string) (*validation.ValidationResult, error) {
	return l.validator.ValidateFile(configPath)
}

// ValidateConfigDirectory validates all config files in a directory
func (l *Loader) ValidateConfigDirectory(dirPath string) (map[string]*validation.ValidationResult, error) {
	return l.validator.ValidateDirectory(dirPath)
}

// LoadRetrieveConfig loads the retrieve configuration with optional custom path
func (l *Loader) LoadRetrieveConfig(configPath string) (*RetrieveConfig, error) {
	if configPath == "" {
		configPath = filepath.Join(l.paths.Configs, "retrieve.yaml")
	}

	// Check if config file exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Return default config if file doesn't exist
		return l.getDefaultRetrieveConfig(), nil
	}

	// Load config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source data config file %s: %w", configPath, err)
	}

	// Validate against schema first
	schemaPath := filepath.Join(l.paths.Home, "schemas", "retrieve", "v0.1.0", "retrieve-config.schema.yaml")
	schemaBytes, err := os.ReadFile(schemaPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Convert YAML schema to JSON for validation
	var schemaInterface interface{}
	if err := yaml.Unmarshal(schemaBytes, &schemaInterface); err != nil {
		return nil, fmt.Errorf("failed to parse schema YAML: %w", err)
	}
	jsonSchemaBytes, err := json.Marshal(schemaInterface)
	if err != nil {
		return nil, fmt.Errorf("failed to convert schema to JSON: %w", err)
	}

	// Validate data against schema
	var dataInterface interface{}
	if err := yaml.Unmarshal(data, &dataInterface); err != nil {
		return nil, fmt.Errorf("failed to parse config YAML: %w", err)
	}

	result, err := schema.ValidateFromBytes(jsonSchemaBytes, dataInterface)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed: %w", err)
	}
	if !result.Valid {
		return nil, fmt.Errorf("config validation failed: %v", result.Errors)
	}

	// Parse config after validation
	var config RetrieveConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse retrieve config file %s: %w", configPath, err)
	}

	return &config, nil
}

// getDefaultRetrieveConfig returns default retrieve configuration
func (l *Loader) getDefaultRetrieveConfig() *RetrieveConfig {
	return &RetrieveConfig{
		Version: "retrieve/v0.1.0",
		Realms:  make(map[string]RealmConfig),
	}
}
