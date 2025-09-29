package inspect

import (
	"strings"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestNewDialectDetector(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := &DialectRegistry{}
	options := DetectorOptions{
		MaxTokens:     1000,
		MinConfidence: 0.8,
	}

	detector := NewDialectDetector(registry, logger, options)
	if detector == nil {
		t.Error("NewDialectDetector() returned nil")
		return
	}
	if detector.registry != registry {
		t.Error("NewDialectDetector() did not set registry correctly")
	}
	if detector.options.MaxTokens != 1000 {
		t.Errorf("NewDialectDetector() MaxTokens = %v, want %v", detector.options.MaxTokens, 1000)
	}
	if detector.options.MinConfidence != 0.8 {
		t.Errorf("NewDialectDetector() MinConfidence = %v, want %v", detector.options.MinConfidence, 0.8)
	}
}

func TestNewDialectDetector_DefaultOptions(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := &DialectRegistry{}
	options := DetectorOptions{} // Empty options should get defaults

	detector := NewDialectDetector(registry, logger, options)
	if detector == nil {
		t.Error("NewDialectDetector() returned nil")
		return
	}
	if detector.options.MaxTokens != 500 {
		t.Errorf("NewDialectDetector() default MaxTokens = %v, want %v", detector.options.MaxTokens, 500)
	}
	if detector.options.MinConfidence != 0.5 {
		t.Errorf("NewDialectDetector() default MinConfidence = %v, want %v", detector.options.MinConfidence, 0.5)
	}
}

func TestDialectDetector_DetectDialect(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a test registry with a simple dialect
	registry := &DialectRegistry{
		Dialects: []Dialect{
			{
				DialectID:   "test-dialect",
				Name:        "Test Dialect",
				Description: "Test dialect for detection",
				Status:      "active",
				Priority:    "high",
				Realm:       "general",
				Patterns: []Pattern{
					{
						PatternID: "test-pattern",
						Name:      "Test Pattern",
						Selector:  "local-name()='root'",
						Weight:    1.0,
						Ecosystem: "test",
					},
				},
			},
		},
	}

	detector := NewDialectDetector(registry, logger, DetectorOptions{})

	// Test XML that should match
	xmlContent := `<root><child>content</child></root>`
	reader := strings.NewReader(xmlContent)

	result, err := detector.DetectDialect(reader)
	if err != nil {
		t.Fatalf("DetectDialect failed: %v", err)
	}

	if result.DialectName != "Test Dialect" {
		t.Errorf("Expected dialect 'Test Dialect', got '%s'", result.DialectName)
	}

	if result.Confidence < 0.5 {
		t.Errorf("Expected confidence >= 0.5, got %f", result.Confidence)
	}
}

func TestDialectDetector_DetectDialect_NoMatch(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Create a test registry with a dialect that won't match
	registry := &DialectRegistry{
		Dialects: []Dialect{
			{
				DialectID:   "test-dialect",
				Name:        "Test Dialect",
				Description: "Test dialect for detection",
				Status:      "active",
				Priority:    "high",
				Realm:       "general",
				Patterns: []Pattern{
					{
						PatternID: "test-pattern",
						Name:      "Test Pattern",
						Selector:  "local-name()='nonexistent'",
						Weight:    1.0,
						Ecosystem: "test",
					},
				},
			},
		},
	}

	detector := NewDialectDetector(registry, logger, DetectorOptions{})

	// Test XML that won't match
	xmlContent := `<different><child>content</child></different>`
	reader := strings.NewReader(xmlContent)

	result, err := detector.DetectDialect(reader)
	if err != nil {
		t.Fatalf("DetectDialect failed: %v", err)
	}

	if result.DialectName != "unknown" {
		t.Errorf("Expected dialect 'unknown', got '%s'", result.DialectName)
	}

	if result.Confidence != 0.0 {
		t.Errorf("Expected confidence 0.0 for no match, got %f", result.Confidence)
	}
}

func TestDialectDetector_scorePattern(t *testing.T) {
	logger := zaptest.NewLogger(t)
	registry := &DialectRegistry{}
	detector := NewDialectDetector(registry, logger, DetectorOptions{})

	elementCounts := map[string]int{
		"root":       1,
		"root.child": 1,
	}
	attributeCounts := map[string]int{
		"id": 1,
	}
	namespaceCounts := map[string]int{}

	tests := []struct {
		name     string
		pattern  Pattern
		expected float64
	}{
		{
			name: "element match",
			pattern: Pattern{
				PatternID: "test",
				Selector:  "local-name()='root'",
				Weight:    1.0,
			},
			expected: 1.0,
		},
		{
			name: "attribute match",
			pattern: Pattern{
				PatternID: "test",
				Selector:  "@id",
				Weight:    1.0,
			},
			expected: 1.0,
		},
		{
			name: "no match",
			pattern: Pattern{
				PatternID: "test",
				Selector:  "local-name()='nonexistent'",
				Weight:    1.0,
			},
			expected: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := detector.scorePattern(tt.pattern, elementCounts, attributeCounts, namespaceCounts)
			if score != tt.expected {
				t.Errorf("Expected score %f, got %f", tt.expected, score)
			}
		})
	}
}
