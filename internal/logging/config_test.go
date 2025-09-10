package logging

import (
	"reflect"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	// Test basic configuration values
	if config.Level != InfoLevel {
		t.Errorf("Expected default level to be InfoLevel, got %s", config.Level)
	}

	if !config.UseColor {
		t.Error("Expected UseColor to be true by default")
	}

	if config.Component != "sumpter" {
		t.Errorf("Expected default component to be 'sumpter', got %s", config.Component)
	}

	if config.PIIMode != PIIModeSafe {
		t.Errorf("Expected default PII mode to be PIIModeSafe, got %s", config.PIIMode)
	}

	if !config.PIISafeOnly {
		t.Error("Expected PIISafeOnly to be true by default")
	}

	// Test log rotation defaults
	if !config.LogRotation.Enabled {
		t.Error("Expected log rotation to be enabled by default")
	}

	if config.LogRotation.MaxSizeMB != 10 {
		t.Errorf("Expected default max size to be 10MB, got %d", config.LogRotation.MaxSizeMB)
	}

	if config.LogRotation.MaxAgeDays != 30 {
		t.Errorf("Expected default max age to be 30 days, got %d", config.LogRotation.MaxAgeDays)
	}

	if config.LogRotation.MaxBackups != 5 {
		t.Errorf("Expected default max backups to be 5, got %d", config.LogRotation.MaxBackups)
	}

	if !config.LogRotation.Compress {
		t.Error("Expected compression to be enabled by default")
	}

	if config.LogRotation.TimeFormat != "2006-01-02" {
		t.Errorf("Expected default time format to be '2006-01-02', got %s", config.LogRotation.TimeFormat)
	}

	// Test telemetry defaults
	if config.EnableTelemetry {
		t.Error("Expected telemetry to be disabled by default")
	}

	if config.ServiceName != "sumpter" {
		t.Errorf("Expected default service name to be 'sumpter', got %s", config.ServiceName)
	}

	if config.ServiceVersion != "dev" {
		t.Errorf("Expected default service version to be 'dev', got %s", config.ServiceVersion)
	}

	if config.Environment != "development" {
		t.Errorf("Expected default environment to be 'development', got %s", config.Environment)
	}
}

func TestConfigFromYAML(t *testing.T) {
	tests := []struct {
		name        string
		level       string
		format      string
		component   string
		useColor    bool
		logFile     string
		rotation    map[string]interface{}
		telemetry   map[string]interface{}
		expectLevel LogLevel
		expectColor bool
	}{
		{
			name:        "Basic config",
			level:       "debug",
			format:      "json",
			component:   "test-component",
			useColor:    false,
			expectLevel: DebugLevel,
			expectColor: false,
		},
		{
			name:      "With file logging",
			level:     "info",
			format:    "console",
			component: "file-logger",
			useColor:  true,
			logFile:   "/tmp/test.log",
			rotation: map[string]interface{}{
				"enabled":      true,
				"max_size_mb":  20,
				"max_age_days": 60,
				"max_backups":  10,
				"compress":     false,
				"time_format":  "2006-01-02-15-04",
			},
			expectLevel: InfoLevel,
			expectColor: true,
		},
		{
			name:      "With telemetry",
			level:     "warn",
			format:    "json",
			component: "telemetry-logger",
			useColor:  false,
			telemetry: map[string]interface{}{
				"enabled":         true,
				"service_name":    "test-service",
				"service_version": "1.0.0",
				"environment":     "production",
			},
			expectLevel: WarnLevel,
			expectColor: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ConfigFromYAML(tt.level, tt.format, tt.component, tt.useColor, tt.logFile, tt.rotation, tt.telemetry)

			if config.Level != tt.expectLevel {
				t.Errorf("Expected level %s, got %s", tt.expectLevel, config.Level)
			}

			if config.UseColor != tt.expectColor {
				t.Errorf("Expected UseColor %v, got %v", tt.expectColor, config.UseColor)
			}

			if config.Component != tt.component {
				t.Errorf("Expected component %s, got %s", tt.component, config.Component)
			}

			// Test file logging configuration
			if tt.logFile != "" {
				if config.LogFile != tt.logFile {
					t.Errorf("Expected log file %s, got %s", tt.logFile, config.LogFile)
				}

				if tt.rotation != nil {
					if config.LogRotation.MaxSizeMB != 20 {
						t.Errorf("Expected max size 20, got %d", config.LogRotation.MaxSizeMB)
					}
					if config.LogRotation.MaxAgeDays != 60 {
						t.Errorf("Expected max age 60, got %d", config.LogRotation.MaxAgeDays)
					}
					if config.LogRotation.MaxBackups != 10 {
						t.Errorf("Expected max backups 10, got %d", config.LogRotation.MaxBackups)
					}
					if config.LogRotation.Compress {
						t.Error("Expected compression to be false")
					}
					if config.LogRotation.TimeFormat != "2006-01-02-15-04" {
						t.Errorf("Expected time format '2006-01-02-15-04', got %s", config.LogRotation.TimeFormat)
					}
				}
			}

			// Test telemetry configuration
			if tt.telemetry != nil {
				if !config.EnableTelemetry {
					t.Error("Expected telemetry to be enabled")
				}
				if config.ServiceName != "test-service" {
					t.Errorf("Expected service name 'test-service', got %s", config.ServiceName)
				}
				if config.ServiceVersion != "1.0.0" {
					t.Errorf("Expected service version '1.0.0', got %s", config.ServiceVersion)
				}
				if config.Environment != "production" {
					t.Errorf("Expected environment 'production', got %s", config.Environment)
				}
			}
		})
	}
}

func TestPIIFromYAML(t *testing.T) {
	tests := []struct {
		name            string
		mode            string
		safeOnly        bool
		allowedContexts []string
		expectedMode    PIIMode
	}{
		{
			name:            "Safe mode",
			mode:            "safe",
			safeOnly:        true,
			allowedContexts: []string{"test"},
			expectedMode:    PIIModeSafe,
		},
		{
			name:            "Off mode",
			mode:            "off",
			safeOnly:        false,
			allowedContexts: []string{},
			expectedMode:    PIIModeOff,
		},
		{
			name:            "Context mode",
			mode:            "context",
			safeOnly:        false,
			allowedContexts: []string{"debug", "internal"},
			expectedMode:    PIIModeContext,
		},
		{
			name:            "Invalid mode defaults to safe",
			mode:            "invalid",
			safeOnly:        true,
			allowedContexts: []string{},
			expectedMode:    PIIModeSafe,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := PIIFromYAML(tt.mode, tt.safeOnly, tt.allowedContexts)

			if detector == nil {
				t.Fatal("PIIFromYAML returned nil detector")
			}

			if detector.mode != tt.expectedMode {
				t.Errorf("Expected mode %s, got %s", tt.expectedMode, detector.mode)
			}

			if !reflect.DeepEqual(detector.allowedContexts, tt.allowedContexts) {
				t.Errorf("Expected allowed contexts %v, got %v", tt.allowedContexts, detector.allowedContexts)
			}
		})
	}
}

func TestUpdateConfigFromPaths(t *testing.T) {
	tests := []struct {
		name         string
		initialPath  string
		logsDir      string
		expectedPath string
	}{
		{
			name:         "Absolute path unchanged",
			initialPath:  "/absolute/path/to/log.log",
			logsDir:      "/logs",
			expectedPath: "/absolute/path/to/log.log",
		},
		{
			name:         "Relative path resolved",
			initialPath:  "app.log",
			logsDir:      "/var/log/sumpter",
			expectedPath: "/var/log/sumpter/app.log",
		},
		{
			name:         "Empty path unchanged",
			initialPath:  "",
			logsDir:      "/logs",
			expectedPath: "",
		},
		{
			name:         "Complex relative path",
			initialPath:  "subdir/nested/app.log",
			logsDir:      "/opt/logs",
			expectedPath: "/opt/logs/subdir/nested/app.log",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &Config{
				LogFile: tt.initialPath,
			}

			config.UpdateConfigFromPaths(tt.logsDir)

			if config.LogFile != tt.expectedPath {
				t.Errorf("Expected path %s, got %s", tt.expectedPath, config.LogFile)
			}
		})
	}
}

func TestHelperFunctions(t *testing.T) {
	// Test getBool
	t.Run("getBool", func(t *testing.T) {
		m := map[string]interface{}{
			"true_key":   true,
			"false_key":  false,
			"string_key": "not_bool",
		}

		if !getBool(m, "true_key", false) {
			t.Error("Expected true for true_key")
		}

		if getBool(m, "false_key", true) {
			t.Error("Expected false for false_key")
		}

		if !getBool(m, "missing_key", true) {
			t.Error("Expected default true for missing key")
		}

		if getBool(m, "string_key", false) {
			t.Error("Expected default false for non-bool value")
		}
	})

	// Test getInt
	t.Run("getInt", func(t *testing.T) {
		m := map[string]interface{}{
			"int_key":    42,
			"string_key": "not_int",
		}

		if getInt(m, "int_key", 0) != 42 {
			t.Error("Expected 42 for int_key")
		}

		if getInt(m, "missing_key", 100) != 100 {
			t.Error("Expected default 100 for missing key")
		}

		if getInt(m, "string_key", 50) != 50 {
			t.Error("Expected default 50 for non-int value")
		}
	})

	// Test getString
	t.Run("getString", func(t *testing.T) {
		m := map[string]interface{}{
			"string_key": "test_value",
			"int_key":    123,
		}

		if getString(m, "string_key", "") != "test_value" {
			t.Error("Expected 'test_value' for string_key")
		}

		if getString(m, "missing_key", "default") != "default" {
			t.Error("Expected 'default' for missing key")
		}

		if getString(m, "int_key", "fallback") != "fallback" {
			t.Error("Expected 'fallback' for non-string value")
		}
	})
}

func TestPIIModeConstants(t *testing.T) {
	if PIIModeOff != "off" {
		t.Errorf("Expected PIIModeOff to be 'off', got %s", PIIModeOff)
	}

	if PIIModeSafe != "safe" {
		t.Errorf("Expected PIIModeSafe to be 'safe', got %s", PIIModeSafe)
	}

	if PIIModeContext != "context" {
		t.Errorf("Expected PIIModeContext to be 'context', got %s", PIIModeContext)
	}
}

func TestLogLevelConstants(t *testing.T) {
	if TraceLevel != "trace" {
		t.Errorf("Expected TraceLevel to be 'trace', got %s", TraceLevel)
	}

	if DebugLevel != "debug" {
		t.Errorf("Expected DebugLevel to be 'debug', got %s", DebugLevel)
	}

	if InfoLevel != "info" {
		t.Errorf("Expected InfoLevel to be 'info', got %s", InfoLevel)
	}

	if WarnLevel != "warn" {
		t.Errorf("Expected WarnLevel to be 'warn', got %s", WarnLevel)
	}

	if ErrorLevel != "error" {
		t.Errorf("Expected ErrorLevel to be 'error', got %s", ErrorLevel)
	}
}

func TestConfigImmutability(t *testing.T) {
	original := DefaultConfig()

	// Modify the returned config (this should not affect the default)
	original.Level = "modified"
	original.Component = "modified"
	original.UseColor = false

	// Get default config again and verify it's unchanged
	fresh := DefaultConfig()

	if fresh.Level != InfoLevel {
		t.Error("DefaultConfig() returned a mutable config that was modified")
	}

	if fresh.Component != "sumpter" {
		t.Error("DefaultConfig() returned a mutable config that was modified")
	}

	if !fresh.UseColor {
		t.Error("DefaultConfig() returned a mutable config that was modified")
	}
}
