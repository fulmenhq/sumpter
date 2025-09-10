package logging

import (
	"path/filepath"
)

// Config holds logger configuration
type Config struct {
	Level              LogLevel
	UseColor           bool
	Component          string
	PIIMode            PIIMode
	PIISafeOnly        bool
	AllowedPIIContexts []string
	LogFile            string
	LogRotation        LogRotationConfig
	EnableTelemetry    bool
	ServiceName        string
	ServiceVersion     string
	Environment        string
}

// PIIMode represents PII handling mode
type PIIMode string

const (
	PIIModeOff     PIIMode = "off"     // No PII filtering
	PIIModeSafe    PIIMode = "safe"    // Filter all PII (default)
	PIIModeContext PIIMode = "context" // Allow PII in specified contexts
)

// LogRotationConfig holds log rotation settings
type LogRotationConfig struct {
	Enabled    bool
	MaxSizeMB  int
	MaxAgeDays int
	MaxBackups int
	Compress   bool
	TimeFormat string
}

// DefaultConfig returns a default logging configuration
func DefaultConfig() Config {
	return Config{
		Level:       InfoLevel,
		UseColor:    true,
		Component:   "sumpter",
		PIIMode:     PIIModeSafe,
		PIISafeOnly: true,
		LogRotation: LogRotationConfig{
			Enabled:    true,
			MaxSizeMB:  10,
			MaxAgeDays: 30,
			MaxBackups: 5,
			Compress:   true,
			TimeFormat: "2006-01-02",
		},
		EnableTelemetry: false,
		ServiceName:     "sumpter",
		ServiceVersion:  "dev",
		Environment:     "development",
	}
}

// ConfigFromYAML creates a logging Config from YAML-compatible values
func ConfigFromYAML(level, format, component string, useColor bool, logFile string, rotation map[string]interface{}, telemetry map[string]interface{}) Config {
	config := Config{
		Level:       LogLevel(level),
		UseColor:    useColor,
		Component:   component,
		PIIMode:     PIIModeSafe,
		PIISafeOnly: true,
	}

	// File logging
	if logFile != "" {
		config.LogFile = logFile
		config.LogRotation = LogRotationConfig{
			Enabled:    getBool(rotation, "enabled", true),
			MaxSizeMB:  getInt(rotation, "max_size_mb", 10),
			MaxAgeDays: getInt(rotation, "max_age_days", 30),
			MaxBackups: getInt(rotation, "max_backups", 5),
			Compress:   getBool(rotation, "compress", true),
			TimeFormat: getString(rotation, "time_format", "2006-01-02"),
		}
	}

	// Telemetry
	config.EnableTelemetry = getBool(telemetry, "enabled", false)
	config.ServiceName = getString(telemetry, "service_name", "sumpter")
	config.ServiceVersion = getString(telemetry, "service_version", "")
	config.Environment = getString(telemetry, "environment", "development")

	return config
}

// PIIFromYAML creates a PII detector from YAML-compatible values
func PIIFromYAML(mode string, safeOnly bool, allowedContexts []string) *PIIDetector {
	var piiMode PIIMode
	switch mode {
	case "off":
		piiMode = PIIModeOff
	case "safe":
		piiMode = PIIModeSafe
	case "context":
		piiMode = PIIModeContext
	default:
		piiMode = PIIModeSafe
	}

	return NewPIIDetector(piiMode, allowedContexts)
}

// UpdateConfigFromPaths updates logging config with resolved paths
func (c *Config) UpdateConfigFromPaths(logsDir string) {
	// If log file is not absolute, make it relative to logs directory
	if c.LogFile != "" && !filepath.IsAbs(c.LogFile) {
		c.LogFile = filepath.Join(logsDir, c.LogFile)
	}
}

// Helper functions for type conversion
func getBool(m map[string]interface{}, key string, defaultVal bool) bool {
	if val, ok := m[key]; ok {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultVal
}

func getInt(m map[string]interface{}, key string, defaultVal int) int {
	if val, ok := m[key]; ok {
		if i, ok := val.(int); ok {
			return i
		}
	}
	return defaultVal
}

func getString(m map[string]interface{}, key, defaultVal string) string {
	if val, ok := m[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return defaultVal
}
