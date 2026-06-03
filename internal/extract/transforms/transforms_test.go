package transforms

import (
	"testing"
)

// TestNewTransformRegistry tests registry creation and initialization
func TestNewTransformRegistry(t *testing.T) {
	registry := NewTransformRegistry()

	if registry == nil {
		t.Fatal("expected registry, got nil")
		return
	}

	if registry.RegistryVersion != "v0.1.0" {
		t.Errorf("version = %q, want v0.1.0", registry.RegistryVersion)
	}

	// Registry should have transforms loaded
	transforms := registry.List()
	if len(transforms) == 0 {
		t.Error("expected transforms to be registered, got empty registry")
	}

	// Should have string category
	categories := registry.ListByCategory()
	if _, exists := categories["string"]; !exists {
		t.Error("expected 'string' category to exist")
	}
}

// TestRegistryRegister tests manual transform registration
func TestRegistryRegister(t *testing.T) {
	registry := NewTransformRegistry()

	customTransform := &Transform{
		Name:        "custom",
		Description: "Custom transform",
		Category:    "test",
		Function: func(value string, params map[string]interface{}) (string, error) {
			return "custom:" + value, nil
		},
	}

	registry.Register(customTransform)

	// Verify it was registered
	retrieved, err := registry.Get("custom")
	if err != nil {
		t.Fatalf("failed to get registered transform: %v", err)
	}

	if retrieved.Name != "custom" {
		t.Errorf("retrieved transform name = %q, want 'custom'", retrieved.Name)
	}

	// Verify category was added
	categories := registry.ListByCategory()
	if _, exists := categories["test"]; !exists {
		t.Error("expected 'test' category to exist")
	}
}

// TestRegistryGet tests retrieving transforms by name
func TestRegistryGet(t *testing.T) {
	registry := NewTransformRegistry()

	// Get existing transform
	trim, err := registry.Get("trim")
	if err != nil {
		t.Fatalf("failed to get 'trim' transform: %v", err)
	}
	if trim.Name != "trim" {
		t.Errorf("transform name = %q, want 'trim'", trim.Name)
	}

	// Get non-existent transform
	_, err = registry.Get("nonexistent")
	if err == nil {
		t.Error("expected error for non-existent transform, got nil")
	}
}

// TestRegistryApply tests applying transforms through the registry
func TestRegistryApply(t *testing.T) {
	registry := NewTransformRegistry()

	result, err := registry.Apply("upper", "hello", nil)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if result != "HELLO" {
		t.Errorf("result = %q, want 'HELLO'", result)
	}

	// Test with non-existent transform
	_, err = registry.Apply("nonexistent", "value", nil)
	if err == nil {
		t.Error("expected error for non-existent transform, got nil")
	}
}

// TestTrimTransform tests the trim transform
func TestTrimTransform(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"leading space", "  hello", "hello"},
		{"trailing space", "hello  ", "hello"},
		{"both sides", "  hello  ", "hello"},
		{"no whitespace", "hello", "hello"},
		{"tabs", "\t\thello\t\t", "hello"},
		{"newlines", "\n\nhello\n\n", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := TrimTransform(tt.input, nil)
			if err != nil {
				t.Fatalf("TrimTransform() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("TrimTransform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestLTrimTransform tests left trim
func TestLTrimTransform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"  hello", "hello"},
		{"hello  ", "hello  "}, // Preserves trailing
		{"\t\nhello", "hello"},
	}

	for _, tt := range tests {
		got, err := LTrimTransform(tt.input, nil)
		if err != nil {
			t.Fatalf("LTrimTransform() error = %v", err)
		}
		if got != tt.want {
			t.Errorf("LTrimTransform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestRTrimTransform tests right trim
func TestRTrimTransform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello  ", "hello"},
		{"  hello", "  hello"}, // Preserves leading
		{"hello\t\n", "hello"},
	}

	for _, tt := range tests {
		got, err := RTrimTransform(tt.input, nil)
		if err != nil {
			t.Fatalf("RTrimTransform() error = %v", err)
		}
		if got != tt.want {
			t.Errorf("RTrimTransform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestUpperTransform tests uppercase conversion
func TestUpperTransform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "HELLO"},
		{"Hello World", "HELLO WORLD"},
		{"ALREADY UPPER", "ALREADY UPPER"},
		{"123abc", "123ABC"},
	}

	for _, tt := range tests {
		got, err := UpperTransform(tt.input, nil)
		if err != nil {
			t.Fatalf("UpperTransform() error = %v", err)
		}
		if got != tt.want {
			t.Errorf("UpperTransform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestLowerTransform tests lowercase conversion
func TestLowerTransform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"HELLO", "hello"},
		{"Hello World", "hello world"},
		{"already lower", "already lower"},
		{"123ABC", "123abc"},
	}

	for _, tt := range tests {
		got, err := LowerTransform(tt.input, nil)
		if err != nil {
			t.Fatalf("LowerTransform() error = %v", err)
		}
		if got != tt.want {
			t.Errorf("LowerTransform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestTitleTransform tests title case conversion
func TestTitleTransform(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello world", "Hello World"},
		{"HELLO WORLD", "Hello World"},
		{"hello", "Hello"},
	}

	for _, tt := range tests {
		got, err := TitleTransform(tt.input, nil)
		if err != nil {
			t.Fatalf("TitleTransform() error = %v", err)
		}
		if got != tt.want {
			t.Errorf("TitleTransform(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// TestReplaceTransform tests string replacement
func TestReplaceTransform(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		params map[string]interface{}
		want   string
		errMsg string
	}{
		{
			name:   "basic replace",
			input:  "hello world",
			params: map[string]interface{}{"old": "world", "new": "everyone"},
			want:   "hello everyone",
		},
		{
			name:   "multiple occurrences",
			input:  "foo bar foo",
			params: map[string]interface{}{"old": "foo", "new": "baz"},
			want:   "baz bar baz",
		},
		{
			name:   "missing old param",
			input:  "test",
			params: map[string]interface{}{"new": "value"},
			errMsg: "requires 'old' parameter",
		},
		{
			name:   "missing new param",
			input:  "test",
			params: map[string]interface{}{"old": "value"},
			errMsg: "requires 'new' parameter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReplaceTransform(tt.input, tt.params)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReplaceTransform() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ReplaceTransform(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestBlindStringTransform tests string blinding for PII protection
func TestBlindStringTransform(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		params map[string]interface{}
		want   string
	}{
		{
			name:   "keep_first default",
			input:  "sensitive123",
			params: nil, // Uses defaults: keep_first, count=4
			want:   "sens********",
		},
		{
			name:   "keep_first custom count",
			input:  "test",
			params: map[string]interface{}{"mode": "keep_first", "count": 2},
			want:   "te**",
		},
		{
			name:   "keep_last",
			input:  "account1234",
			params: map[string]interface{}{"mode": "keep_last", "count": 4},
			want:   "*******1234",
		},
		{
			name:   "mask_all",
			input:  "secret",
			params: map[string]interface{}{"mode": "mask_all"},
			want:   "******",
		},
		{
			name:   "keep_domain",
			input:  "user@example.com",
			params: map[string]interface{}{"mode": "keep_domain", "count": 2},
			want:   "us**@example.com",
		},
		{
			name:   "custom mask char",
			input:  "data",
			params: map[string]interface{}{"mode": "mask_all", "mask_char": "#"},
			want:   "####",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BlindStringTransform(tt.input, tt.params)
			if err != nil {
				t.Fatalf("BlindStringTransform() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("BlindStringTransform(%q, %v) = %q, want %q", tt.input, tt.params, got, tt.want)
			}
		})
	}
}

// TestBlindStringTransform_ErrorCases tests error handling
func TestBlindStringTransform_ErrorCases(t *testing.T) {
	_, err := BlindStringTransform("test", map[string]interface{}{"mode": "invalid"})
	if err == nil {
		t.Error("expected error for invalid mode, got nil")
	}
}
