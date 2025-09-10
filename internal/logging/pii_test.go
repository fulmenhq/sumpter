package logging

import (
	"reflect"
	"testing"
)

func TestNewPIIDetector(t *testing.T) {
	tests := []struct {
		name            string
		mode            PIIMode
		allowedContexts []string
		expectedMode    PIIMode
	}{
		{
			name:            "Safe mode",
			mode:            PIIModeSafe,
			allowedContexts: []string{"test"},
			expectedMode:    PIIModeSafe,
		},
		{
			name:            "Off mode",
			mode:            PIIModeOff,
			allowedContexts: []string{},
			expectedMode:    PIIModeOff,
		},
		{
			name:            "Context mode",
			mode:            PIIModeContext,
			allowedContexts: []string{"debug", "internal"},
			expectedMode:    PIIModeContext,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detector := NewPIIDetector(tt.mode, tt.allowedContexts)

			if detector == nil {
				t.Fatal("NewPIIDetector returned nil")
			}

			if detector.mode != tt.expectedMode {
				t.Errorf("Expected mode %s, got %s", tt.expectedMode, detector.mode)
			}

			if !reflect.DeepEqual(detector.allowedContexts, tt.allowedContexts) {
				t.Errorf("Expected allowed contexts %v, got %v", tt.allowedContexts, detector.allowedContexts)
			}

			// Verify patterns are initialized
			if len(detector.patterns) == 0 {
				t.Error("Expected PII patterns to be initialized")
			}

			// Verify each pattern has required fields
			for _, pattern := range detector.patterns {
				if pattern.Name == "" {
					t.Error("Pattern name is empty")
				}
				if pattern.Pattern == nil {
					t.Error("Pattern regex is nil")
				}
				if pattern.Replacement == "" {
					t.Error("Pattern replacement is empty")
				}
				if pattern.Severity == "" {
					t.Error("Pattern severity is empty")
				}
				if pattern.Category == "" {
					t.Error("Pattern category is empty")
				}
			}
		})
	}
}

func TestSanitizeData(t *testing.T) {
	detector := NewPIIDetector(PIIModeSafe, []string{})

	tests := []struct {
		name     string
		input    string
		context  string
		expected string
	}{
		{
			name:     "Credit card number",
			input:    "Payment with card 4111-1111-1111-1111 approved",
			context:  "log",
			expected: "Payment with card [CARD-****] approved",
		},
		{
			name:     "SSN",
			input:    "User SSN: 123-45-6789",
			context:  "log",
			expected: "User SSN: [SSN-***]",
		},
		{
			name:     "Email address",
			input:    "Contact user@example.com for support",
			context:  "log",
			expected: "Contact [EMAIL-***] for support",
		},
		{
			name:     "Phone number",
			input:    "Call (555) 123-4567",
			context:  "log",
			expected: "Call ([PHONE-***]",
		},
		{
			name:     "Bearer token",
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
			context:  "log",
			expected: "Authorization: [AUTH-***]",
		},
		{
			name:     "AWS key",
			input:    "AWS_ACCESS_KEY_ID=AKIAIOSFODNN7EXAMPLE",
			context:  "log",
			expected: "AWS_ACCESS_KEY_ID=[AWS-KEY-***]",
		},
		{
			name:     "Multiple PII types",
			input:    "User john@example.com with SSN 123-45-6789 and card 4111-1111-1111-1111",
			context:  "log",
			expected: "User [EMAIL-***] with SSN [SSN-***] and card [CARD-****]",
		},
		{
			name:     "No PII",
			input:    "This is a normal log message",
			context:  "log",
			expected: "This is a normal log message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.SanitizeData(tt.input, tt.context)
			if result != tt.expected {
				t.Errorf("SanitizeData(%q, %q) = %q, expected %q", tt.input, tt.context, result, tt.expected)
			}
		})
	}
}

func TestSanitizeDataWithContext(t *testing.T) {
	detector := NewPIIDetector(PIIModeContext, []string{"debug", "internal"})

	tests := []struct {
		name     string
		input    string
		context  string
		expected string
	}{
		{
			name:     "PII in allowed context",
			input:    "Debug: user@example.com",
			context:  "debug",
			expected: "Debug: user@example.com", // Should not be sanitized
		},
		{
			name:     "PII in non-allowed context",
			input:    "Info: user@example.com",
			context:  "info",
			expected: "Info: [EMAIL-***]", // Should be sanitized
		},
		{
			name:     "PII in allowed context with partial match",
			input:    "Internal processing: 123-45-6789",
			context:  "internal_processing",
			expected: "Internal processing: 123-45-6789", // Should not be sanitized due to "internal" in context
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.SanitizeData(tt.input, tt.context)
			if result != tt.expected {
				t.Errorf("SanitizeData(%q, %q) = %q, expected %q", tt.input, tt.context, result, tt.expected)
			}
		})
	}
}

func TestSanitizeDataOffMode(t *testing.T) {
	detector := NewPIIDetector(PIIModeOff, []string{})

	input := "User email: user@example.com and SSN: 123-45-6789"
	expected := input // Should not be sanitized

	result := detector.SanitizeData(input, "log")
	if result != expected {
		t.Errorf("SanitizeData with PIIModeOff should not sanitize: got %q, expected %q", result, expected)
	}
}

func TestIsAllowedContext(t *testing.T) {
	detector := NewPIIDetector(PIIModeContext, []string{"debug", "internal", "test"})

	tests := []struct {
		context  string
		expected bool
	}{
		{"debug", true},
		{"internal", true},
		{"test", true},
		{"info", false},
		{"debug_processing", true}, // Contains "debug"
		{"internal_logs", true},    // Contains "internal"
		{"testing", true},          // Contains "test" (partial match)
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.context, func(t *testing.T) {
			result := detector.isAllowedContext(tt.context)
			if result != tt.expected {
				t.Errorf("isAllowedContext(%q) = %v, expected %v", tt.context, result, tt.expected)
			}
		})
	}
}

func TestSanitizeFields(t *testing.T) {
	detector := NewPIIDetector(PIIModeSafe, []string{})

	// Test with sample fields (current implementation returns fields unchanged)
	fields := []interface{}{"test", 123, true}
	result := detector.SanitizeFields(fields)

	if !reflect.DeepEqual(result, fields) {
		t.Errorf("SanitizeFields should return fields unchanged, got %v, expected %v", result, fields)
	}
}

func TestGetPIISummary(t *testing.T) {
	detector := NewPIIDetector(PIIModeSafe, []string{})

	tests := []struct {
		name     string
		input    string
		expected map[string]int
	}{
		{
			name:     "No PII",
			input:    "This is a normal message",
			expected: map[string]int{},
		},
		{
			name:  "Single email",
			input: "Contact user@example.com",
			expected: map[string]int{
				"email": 1,
			},
		},
		{
			name:  "Multiple same PII",
			input: "Emails: user1@example.com and user2@example.com",
			expected: map[string]int{
				"email": 2,
			},
		},
		{
			name:  "Multiple different PII",
			input: "User user@example.com with SSN 123-45-6789 and card 4111-1111-1111-1111",
			expected: map[string]int{
				"email":       1,
				"ssn":         1,
				"credit_card": 1,
			},
		},
		{
			name:  "PII with multiple matches",
			input: "SSN: 123-45-6789 and another 987-65-4321",
			expected: map[string]int{
				"ssn": 2,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.GetPIISummary(tt.input)

			if !reflect.DeepEqual(result, tt.expected) {
				t.Errorf("GetPIISummary(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestPIIDetectorInitialization(t *testing.T) {
	detector := NewPIIDetector(PIIModeSafe, []string{})

	// Verify expected patterns are present
	expectedPatterns := []string{
		"credit_card", "ssn", "email", "phone", "full_name",
		"medical_record", "health_id", "diagnosis_code",
		"auth_header", "bearer_token", "api_key", "jwt_token",
		"aws_key", "aws_secret",
	}

	foundPatterns := make(map[string]bool)
	for _, pattern := range detector.patterns {
		foundPatterns[pattern.Name] = true
	}

	for _, expected := range expectedPatterns {
		if !foundPatterns[expected] {
			t.Errorf("Expected pattern %s not found in detector", expected)
		}
	}

	// Verify pattern compilation
	for _, pattern := range detector.patterns {
		if pattern.Pattern == nil {
			t.Errorf("Pattern %s has nil regex", pattern.Name)
		}
	}
}

func TestPIIPatterns(t *testing.T) {
	detector := NewPIIDetector(PIIModeSafe, []string{})

	// Test specific patterns
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"Credit card match", "4111-1111-1111-1111", "credit_card", true},
		{"Credit card no match", "invalid-card", "credit_card", false},
		{"SSN match", "123-45-6789", "ssn", true},
		{"SSN no match", "123-456-789", "ssn", false},
		{"Email match", "user@example.com", "email", true},
		{"Email no match", "not-an-email", "email", false},
		{"Phone match", "(555) 123-4567", "phone", true},
		{"Phone no match", "not-a-phone", "phone", false},
		{"Bearer token match", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", "bearer_token", true},
		{"Bearer token no match", "Basic dXNlcjpwYXNz", "bearer_token", false},
		{"AWS key match", "AKIAIOSFODNN7EXAMPLE", "aws_key", true},
		{"AWS key no match", "not-an-aws-key", "aws_key", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var foundPattern *PIIPattern
			for _, pattern := range detector.patterns {
				if pattern.Name == tt.pattern {
					foundPattern = &pattern
					break
				}
			}

			if foundPattern == nil {
				t.Fatalf("Pattern %s not found", tt.pattern)
			}

			matches := foundPattern.Pattern.MatchString(tt.input)
			if matches != tt.expected {
				t.Errorf("Pattern %s on input %q: expected %v, got %v", tt.pattern, tt.input, tt.expected, matches)
			}
		})
	}
}

func TestPIIDetectorModes(t *testing.T) {
	tests := []struct {
		mode     PIIMode
		contexts []string
		input    string
		context  string
		expected string
	}{
		{
			mode:     PIIModeOff,
			contexts: []string{},
			input:    "Email: user@example.com",
			context:  "log",
			expected: "Email: user@example.com", // No sanitization
		},
		{
			mode:     PIIModeSafe,
			contexts: []string{},
			input:    "Email: user@example.com",
			context:  "log",
			expected: "Email: [EMAIL-***]", // Always sanitize
		},
		{
			mode:     PIIModeContext,
			contexts: []string{"debug"},
			input:    "Email: user@example.com",
			context:  "debug",
			expected: "Email: user@example.com", // Don't sanitize in allowed context
		},
		{
			mode:     PIIModeContext,
			contexts: []string{"debug"},
			input:    "Email: user@example.com",
			context:  "info",
			expected: "Email: [EMAIL-***]", // Sanitize in non-allowed context
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			detector := NewPIIDetector(tt.mode, tt.contexts)
			result := detector.SanitizeData(tt.input, tt.context)

			if result != tt.expected {
				t.Errorf("Mode %s: SanitizeData(%q, %q) = %q, expected %q",
					tt.mode, tt.input, tt.context, result, tt.expected)
			}
		})
	}
}
