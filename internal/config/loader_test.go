package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

func newTestPaths(t *testing.T) *Paths {
	t.Helper()
	tempDir := t.TempDir()
	return &Paths{
		Home:    tempDir,
		WorkDir: filepath.Join(tempDir, "work"),
		Cache:   filepath.Join(tempDir, "cache"),
		Logs:    filepath.Join(tempDir, "logs"),
		Configs: filepath.Join(tempDir, "configs"),
		Temp:    filepath.Join(tempDir, "temp"),
	}
}

func newLoaderForTest(t *testing.T) (*Loader, *Paths) {
	t.Helper()
	paths := newTestPaths(t)
	copyRetrieveSchema(t, paths.Home)
	validator := newTestSchemaValidator(t)
	loader := NewLoaderWithValidator(paths, validator)
	return loader, paths
}

func newTestSchemaValidator(t *testing.T) *validation.SchemaValidator {
	t.Helper()
	template := `{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "type": "object",
  "additionalProperties": true
}`
	fakeFS := fstest.MapFS{
		"config/v0.1.0/sumpter-config.schema.json": {Data: []byte(template)},
		"config/v0.1.0/logger-config.schema.json":  {Data: []byte(template)},
		"config/v0.1.0/pii-config.schema.json":     {Data: []byte(template)},
	}
	return validation.NewSchemaValidatorFromFS(fakeFS)
}

func copyRetrieveSchema(t *testing.T, home string) {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(filename), "../..")
	src := filepath.Join(root, "schemas", "retrieve", "v0.1.0", "retrieve-config.schema.yaml")
	dst := filepath.Join(home, "schemas", "retrieve", "v0.1.0", "retrieve-config.schema.yaml")

	// Create destination directory
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("failed to create retrieve schema directory: %v", err)
	}

	// Copy the file
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("failed to read source schema from %s: %v", src, err)
	}

	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("failed to write schema copy: %v", err)
	}
}

func TestNewLoader(t *testing.T) {
	paths := newTestPaths(t)
	loader := NewLoader(paths)

	if loader == nil {
		t.Error("NewLoader returned nil")
		return
	}

	if loader.paths != paths {
		t.Error("Loader paths not set correctly")
	}
}

func TestLoadMainConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	// Test loading default config when no file exists
	config, err := loader.LoadMainConfig()
	if err != nil {
		t.Errorf("LoadMainConfig failed with default config: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadMainConfig returned nil config")
		return
	}

	// Verify default config values
	if config.Version != "config/v0.1.0" {
		t.Errorf("Expected version 'config/v0.1.0', got '%s'", config.Version)
	}

	if config.Logging.Level != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", config.Logging.Level)
	}

	if config.Performance.MaxMemoryMB != 512 {
		t.Errorf("Expected default max memory 512, got %d", config.Performance.MaxMemoryMB)
	}
}

func TestLoadMainConfigWithFile(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create config directory
	os.MkdirAll(paths.Configs, 0o755)

	// Create a test config file
	testConfig := &MainConfig{
		Version: "config/v0.1.0",
		Logging: LoggerConfig{
			Version: "logger-config/v0.1.0",
			Level:   "debug",
			Format:  "json",
		},
		Performance: PerformanceConfig{
			MaxMemoryMB:    1024,
			BufferSizeKB:   128,
			WorkerCount:    8,
			TimeoutSeconds: 600,
		},
	}

	configData, err := yaml.Marshal(testConfig)
	if err != nil {
		t.Fatalf("Failed to marshal test config: %v", err)
	}

	configPath := filepath.Join(paths.Configs, "sumpter.yaml")
	err = os.WriteFile(configPath, configData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	// Test loading config from file
	config, err := loader.LoadMainConfig()
	if err != nil {
		t.Errorf("LoadMainConfig failed: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadMainConfig returned nil config")
		return
	}

	// Verify loaded config values
	if config.Logging.Level != "debug" {
		t.Errorf("Expected log level 'debug', got '%s'", config.Logging.Level)
	}

	if config.Logging.Format != "json" {
		t.Errorf("Expected log format 'json', got '%s'", config.Logging.Format)
	}

	if config.Performance.MaxMemoryMB != 1024 {
		t.Errorf("Expected max memory 1024, got %d", config.Performance.MaxMemoryMB)
	}
}

func TestLoadMainConfigInvalidFile(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create config directory
	os.MkdirAll(paths.Configs, 0o755)

	// Create an invalid YAML file
	configPath := filepath.Join(paths.Configs, "sumpter.yaml")
	err := os.WriteFile(configPath, []byte("invalid: yaml: content: [unclosed"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid config file: %v", err)
	}

	// Test loading invalid config
	_, err = loader.LoadMainConfig()
	if err == nil {
		t.Error("Expected error when loading invalid YAML config")
		return
	}

	if !strings.Contains(err.Error(), "failed to parse config file") &&
		!strings.Contains(err.Error(), "failed to parse data as YAML or JSON") {
		t.Errorf("Unexpected error text: %v", err)
	}
}

func TestLoadLoggerConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	// Test loading default logger config
	config, err := loader.LoadLoggerConfig()
	if err != nil {
		t.Errorf("LoadLoggerConfig failed: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadLoggerConfig returned nil config")
		return
	}

	// Verify default values
	if config.Version != "logger-config/v0.1.0" {
		t.Errorf("Expected version 'logger-config/v0.1.0', got '%s'", config.Version)
	}

	if config.Level != "info" {
		t.Errorf("Expected default log level 'info', got '%s'", config.Level)
	}

	if config.Format != "pretty" {
		t.Errorf("Expected default log format 'pretty', got '%s'", config.Format)
	}

	if !config.UseColor {
		t.Error("Expected UseColor to be true by default")
	}
}

func TestLoadPIIConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	// Test loading default PII config
	config, err := loader.LoadPIIConfig()
	if err != nil {
		t.Errorf("LoadPIIConfig failed: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadPIIConfig returned nil config")
		return
	}

	// Verify default values
	if config.Version != "pii-config/v0.1.0" {
		t.Errorf("Expected version 'pii-config/v0.1.0', got '%s'", config.Version)
	}

	if config.Mode != "safe" {
		t.Errorf("Expected default mode 'safe', got '%s'", config.Mode)
	}

	if !config.SafeOnly {
		t.Error("Expected SafeOnly to be true by default")
	}
}

func TestSaveMainConfig(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create a test config
	testConfig := &MainConfig{
		Version: "config/v0.1.0",
		Logging: LoggerConfig{
			Version: "logger-config/v0.1.0",
			Level:   "warn",
			Format:  "json",
		},
		Performance: PerformanceConfig{
			MaxMemoryMB:    2048,
			BufferSizeKB:   256,
			WorkerCount:    16,
			TimeoutSeconds: 1200,
		},
	}

	// Save the config
	err := loader.SaveMainConfig(testConfig)
	if err != nil {
		t.Errorf("SaveMainConfig failed: %v", err)
		return
	}

	// Verify the file was created
	configPath := filepath.Join(paths.Configs, "sumpter.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("Config file was not created at %s", configPath)
		return
	}

	// Load the config back and verify it matches
	loadedConfig, err := loader.LoadMainConfig()
	if err != nil {
		t.Errorf("Failed to load saved config: %v", err)
		return
	}

	if loadedConfig.Logging.Level != "warn" {
		t.Errorf("Loaded config log level mismatch: expected 'warn', got '%s'", loadedConfig.Logging.Level)
	}

	if loadedConfig.Performance.MaxMemoryMB != 2048 {
		t.Errorf("Loaded config max memory mismatch: expected 2048, got %d", loadedConfig.Performance.MaxMemoryMB)
	}
}

func TestSaveLoggerConfig(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create a test logger config
	testConfig := &LoggerConfig{
		Version:  "logger-config/v0.1.0",
		Level:    "error",
		Format:   "json",
		UseColor: false,
	}

	// Save the config
	err := loader.SaveLoggerConfig(testConfig)
	if err != nil {
		t.Errorf("SaveLoggerConfig failed: %v", err)
		return
	}

	// Verify the file was created
	configPath := filepath.Join(paths.Configs, "logger.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("Logger config file was not created at %s", configPath)
		return
	}

	// Load the config back and verify it matches
	loadedConfig, err := loader.LoadLoggerConfig()
	if err != nil {
		t.Errorf("Failed to load saved logger config: %v", err)
		return
	}

	if loadedConfig.Level != "error" {
		t.Errorf("Loaded logger config level mismatch: expected 'error', got '%s'", loadedConfig.Level)
	}

	if loadedConfig.UseColor {
		t.Error("Loaded logger config UseColor should be false")
	}
}

func TestSavePIIConfig(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create a test PII config
	testConfig := &PIIConfig{
		Version:  "pii-config/v0.1.0",
		Mode:     "context",
		SafeOnly: false,
	}

	// Save the config
	err := loader.SavePIIConfig(testConfig)
	if err != nil {
		t.Errorf("SavePIIConfig failed: %v", err)
		return
	}

	// Verify the file was created
	configPath := filepath.Join(paths.Configs, "pii.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("PII config file was not created at %s", configPath)
		return
	}

	// Load the config back and verify it matches
	loadedConfig, err := loader.LoadPIIConfig()
	if err != nil {
		t.Errorf("Failed to load saved PII config: %v", err)
		return
	}

	if loadedConfig.Mode != "context" {
		t.Errorf("Loaded PII config mode mismatch: expected 'context', got '%s'", loadedConfig.Mode)
	}

	if loadedConfig.SafeOnly {
		t.Error("Loaded PII config SafeOnly should be false")
	}
}

func TestValidateMainConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	tests := []struct {
		name        string
		version     string
		expectError bool
	}{
		{
			name:        "Valid version",
			version:     "config/v0.1.0",
			expectError: false,
		},
		{
			name:        "Invalid version",
			version:     "config/v0.2.0",
			expectError: true,
		},
		{
			name:        "Empty version",
			version:     "",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &MainConfig{Version: tt.version}

			err := loader.validateMainConfig(config)

			if tt.expectError {
				if err == nil {
					t.Error("Expected validation error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestValidateLoggerConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	tests := []struct {
		name        string
		version     string
		level       string
		format      string
		expectError bool
	}{
		{
			name:        "Valid config",
			version:     "logger-config/v0.1.0",
			level:       "info",
			format:      "pretty",
			expectError: false,
		},
		{
			name:        "Invalid version",
			version:     "logger-config/v0.2.0",
			level:       "info",
			format:      "pretty",
			expectError: true,
		},
		{
			name:        "Invalid log level",
			version:     "logger-config/v0.1.0",
			level:       "invalid",
			format:      "pretty",
			expectError: true,
		},
		{
			name:        "Invalid log format",
			version:     "logger-config/v0.1.0",
			level:       "info",
			format:      "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &LoggerConfig{
				Version: tt.version,
				Level:   tt.level,
				Format:  tt.format,
			}

			err := loader.validateLoggerConfig(config)

			if tt.expectError {
				if err == nil {
					t.Error("Expected validation error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestValidatePIIConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	tests := []struct {
		name        string
		version     string
		mode        string
		expectError bool
	}{
		{
			name:        "Valid config",
			version:     "pii-config/v0.1.0",
			mode:        "safe",
			expectError: false,
		},
		{
			name:        "Invalid version",
			version:     "pii-config/v0.2.0",
			mode:        "safe",
			expectError: true,
		},
		{
			name:        "Invalid mode",
			version:     "pii-config/v0.1.0",
			mode:        "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &PIIConfig{
				Version: tt.version,
				Mode:    tt.mode,
			}

			err := loader.validatePIIConfig(config)

			if tt.expectError {
				if err == nil {
					t.Error("Expected validation error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestLoadRetrieveConfig(t *testing.T) {
	loader, _ := newLoaderForTest(t)

	// Test loading default config when no file exists
	config, err := loader.LoadRetrieveConfig("")
	if err != nil {
		t.Errorf("LoadRetrieveConfig failed with default config: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadRetrieveConfig returned nil config")
		return
	}

	// Verify default config values
	if config.Version != "retrieve/v0.1.0" {
		t.Errorf("Expected version 'retrieve/v0.1.0', got '%s'", config.Version)
	}

	if config.Realms == nil {
		t.Error("Expected default realms map to be initialized")
	}
}

func TestLoadRetrieveConfigWithFile(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create config directory
	os.MkdirAll(paths.Configs, 0o755)

	// Create a test config file
	testConfig := &RetrieveConfig{
		Version: "retrieve/v0.1.0",
		Realms: map[string]RealmConfig{
			"finance": {
				Enabled: true,
				Client: ClientConfig{
					UserAgent:      "Test Company test@example.com",
					TimeoutSeconds: 45,
				},
				RateLimits: RateLimitConfig{
					RequestsPerSecond: 5,
					BurstLimit:        3,
					BackoffSeconds:    2,
				},
				Endpoints: map[string]string{
					"sec_edgar_base": "https://data.sec.gov",
				},
			},
		},
	}

	configData, err := yaml.Marshal(testConfig)
	if err != nil {
		t.Fatalf("Failed to marshal test config: %v", err)
	}

	configPath := filepath.Join(paths.Configs, "retrieve.yaml")
	err = os.WriteFile(configPath, configData, 0644)
	if err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}

	// Test loading config from file
	config, err := loader.LoadRetrieveConfig("")
	if err != nil {
		t.Errorf("LoadRetrieveConfig failed: %v", err)
		return
	}

	if config == nil {
		t.Error("LoadRetrieveConfig returned nil config")
		return
	}

	// Verify loaded config values
	financeRealm, exists := config.Realms["finance"]
	if !exists {
		t.Error("Expected finance realm to exist in loaded config")
		return
	}

	if financeRealm.Client.UserAgent != "Test Company test@example.com" {
		t.Errorf("Expected user agent 'Test Company test@example.com', got '%s'", financeRealm.Client.UserAgent)
	}

	if financeRealm.RateLimits.RequestsPerSecond != 5 {
		t.Errorf("Expected requests per second 5, got %f", financeRealm.RateLimits.RequestsPerSecond)
	}
}

func TestLoadRetrieveConfigInvalidFile(t *testing.T) {
	loader, paths := newLoaderForTest(t)

	// Create config directory
	os.MkdirAll(paths.Configs, 0o755)

	// Create an invalid YAML file
	configPath := filepath.Join(paths.Configs, "retrieve.yaml")
	err := os.WriteFile(configPath, []byte("invalid: yaml: content: [unclosed"), 0644)
	if err != nil {
		t.Fatalf("Failed to write invalid config file: %v", err)
	}

	// Test loading invalid config
	_, err = loader.LoadRetrieveConfig("")
	if err == nil {
		t.Error("Expected error when loading invalid YAML config")
		return
	}

	if !strings.Contains(err.Error(), "failed to parse config YAML") {
		t.Errorf("Unexpected error text: %v", err)
	}
}
