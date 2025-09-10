package logging

import (
	"testing"

	"go.uber.org/zap"
)

func TestComponent(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	tests := []struct {
		name      string
		component string
	}{
		{"Basic component", "test-component"},
		{"Empty component", ""},
		{"Complex component name", "xml-processor.v1.2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := Component(tt.component)

			if logger == nil {
				t.Error("Component() returned nil")
				return
			}

			if logger.name != tt.component {
				t.Errorf("Expected component name %s, got %s", tt.component, logger.name)
			}

			// Verify logger instances are created
			if logger.logger == nil {
				t.Error("Component logger is nil")
			}

			if logger.sugar == nil {
				t.Error("Component sugar logger is nil")
			}
		})
	}
}

func TestComponentLoggerMethods(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("test")

	// Test that methods don't panic (we can't easily test the actual logging output)
	// These tests verify the methods exist and can be called

	t.Run("Info", func(t *testing.T) {
		logger.Info("test info message")
		logger.Info("test info with fields", zap.String("key", "value"))
	})

	t.Run("Debug", func(t *testing.T) {
		logger.Debug("test debug message")
		logger.Debug("test debug with fields", zap.Int("count", 42))
	})

	t.Run("Warn", func(t *testing.T) {
		logger.Warn("test warn message")
		logger.Warn("test warn with fields", zap.Bool("flag", true))
	})

	t.Run("Error", func(t *testing.T) {
		logger.Error("test error message")
		logger.Error("test error with fields", zap.Error(err))
	})

	t.Run("Infof", func(t *testing.T) {
		logger.Infof("test infof: %s %d", "string", 123)
	})

	t.Run("Debugf", func(t *testing.T) {
		logger.Debugf("test debugf: %v", []string{"a", "b"})
	})

	t.Run("Warnf", func(t *testing.T) {
		logger.Warnf("test warnf: %t", true)
	})

	t.Run("Errorf", func(t *testing.T) {
		logger.Errorf("test errorf: %v", err)
	})
}

func TestComponentLoggerWithFields(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("test")

	t.Run("WithFields", func(t *testing.T) {
		fields := []zap.Field{
			zap.String("component", "test"),
			zap.Int("version", 1),
		}

		result := logger.WithFields(fields...)
		if result == nil {
			t.Error("WithFields returned nil")
		}
	})

	t.Run("WithField", func(t *testing.T) {
		result := logger.WithField("test_key", "test_value")

		if result == nil {
			t.Error("WithField returned nil")
			return
		}

		if result.name != logger.name {
			t.Errorf("WithField changed component name from %s to %s", logger.name, result.name)
		}

		// Verify the field was added (we can't easily test the actual logging)
		result.Info("test message with field")
	})

	t.Run("WithError", func(t *testing.T) {
		testErr := err
		result := logger.WithError(testErr)

		if result == nil {
			t.Error("WithError returned nil")
			return
		}

		if result.name != logger.name {
			t.Errorf("WithError changed component name from %s to %s", logger.name, result.name)
		}

		// Verify the error field was added
		result.Error("test error message")
	})
}

func TestComponentLoggerLogXMLProcessing(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("xml-processor")

	tests := []struct {
		name       string
		event      string
		element    string
		attributes map[string]string
		data       interface{}
	}{
		{
			name:       "Basic XML processing",
			event:      "start",
			element:    "Envelope",
			attributes: map[string]string{"version": "1.0"},
			data:       "processing envelope",
		},
		{
			name:       "XML with sensitive data",
			event:      "element",
			element:    "UserData",
			attributes: map[string]string{"email": "user@example.com", "id": "123"},
			data:       "user information",
		},
		{
			name:       "Empty attributes",
			event:      "end",
			element:    "Document",
			attributes: map[string]string{},
			data:       nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This should not panic and should handle PII sanitization
			logger.LogXMLProcessing(tt.event, tt.element, tt.attributes, tt.data)
		})
	}
}

func TestComponentLoggerLogPerformance(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("performance")

	tests := []struct {
		name      string
		operation string
		duration  int64
		size      int64
	}{
		{
			name:      "File processing",
			operation: "xml_parse",
			duration:  1500,        // 1.5 seconds in milliseconds
			size:      1024 * 1024, // 1MB
		},
		{
			name:      "Database query",
			operation: "db_query",
			duration:  500,
			size:      0,
		},
		{
			name:      "Network request",
			operation: "http_request",
			duration:  200,
			size:      2048,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.LogPerformance(tt.operation, tt.duration, tt.size)
		})
	}
}

func TestComponentLoggerLogSecurityEvent(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("security")

	tests := []struct {
		name     string
		event    string
		severity string
		details  map[string]interface{}
	}{
		{
			name:     "Low severity event",
			event:    "login_attempt",
			severity: "low",
			details: map[string]interface{}{
				"user":    "testuser",
				"ip":      "192.168.1.1",
				"success": true,
			},
		},
		{
			name:     "Medium severity event",
			event:    "suspicious_activity",
			severity: "medium",
			details: map[string]interface{}{
				"user":   "testuser",
				"action": "multiple_failed_logins",
				"count":  5,
			},
		},
		{
			name:     "High severity event",
			event:    "security_breach",
			severity: "high",
			details: map[string]interface{}{
				"user":        "testuser",
				"breach_type": "unauthorized_access",
				"resource":    "sensitive_data",
			},
		},
		{
			name:     "Critical severity event",
			event:    "data_exfiltration",
			severity: "critical",
			details: map[string]interface{}{
				"user":      "testuser",
				"data_type": "pii",
				"volume":    "large",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger.LogSecurityEvent(tt.event, tt.severity, tt.details)
		})
	}
}

func TestComponentLoggerWithoutGlobalLogger(t *testing.T) {
	// Ensure no global logger is initialized
	globalLogger = nil
	sugar = nil
	piiDetector = nil

	// This should initialize with default config
	logger := Component("test")

	if logger == nil {
		t.Error("Component() returned nil when no global logger exists")
		return
	}

	// Verify logger was created
	if logger.logger == nil {
		t.Error("Component logger was not initialized")
	}

	if logger.sugar == nil {
		t.Error("Component sugar logger was not initialized")
	}

	// Clean up
	globalLogger = nil
	sugar = nil
	piiDetector = nil
}

func TestComponentLoggerPIIIntegration(t *testing.T) {
	// Initialize logger with PII detection
	config := DefaultConfig()
	config.PIIMode = PIIModeSafe
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("test")

	// Test that XML processing with PII gets sanitized
	attributes := map[string]string{
		"email":  "user@example.com",
		"ssn":    "123-45-6789",
		"normal": "safe_value",
	}

	// This should sanitize the PII in attributes
	logger.LogXMLProcessing("element", "User", attributes, "user data")
}

func TestComponentLoggerName(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	tests := []struct {
		name         string
		component    string
		expectedName string
	}{
		{"Simple name", "api", "api"},
		{"Complex name", "xml.parser.v2", "xml.parser.v2"},
		{"Name with spaces", "my component", "my component"},
		{"Empty name", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := Component(tt.component)

			if logger.name != tt.expectedName {
				t.Errorf("Expected component name %q, got %q", tt.expectedName, logger.name)
			}
		})
	}
}

func TestComponentLoggerChaining(t *testing.T) {
	// Initialize logger for testing
	config := DefaultConfig()
	err := Initialize(config)
	if err != nil {
		t.Fatalf("Failed to initialize logger: %v", err)
	}
	defer func() {
		// Clean up global state
		globalLogger = nil
		sugar = nil
		piiDetector = nil
	}()

	logger := Component("base")

	// Test chaining WithField and WithError
	chained := logger.WithField("request_id", "123").WithError(err)

	if chained == nil {
		t.Error("Chained logger is nil")
		return
	}

	if chained.name != logger.name {
		t.Errorf("Chained logger name changed from %s to %s", logger.name, chained.name)
	}

	// Verify the chained logger works
	chained.Info("test chained logging")
}
