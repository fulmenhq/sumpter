package inspect

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

func TestRegistryLoader_LoadRegistry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	registry, err := loader.LoadRegistry("")
	if err != nil {
		t.Fatalf("Failed to load registry: %v", err)
	}

	if registry.RegistryVersion != "v0.1.0" {
		t.Errorf("Expected registry version v0.1.0, got %s", registry.RegistryVersion)
	}

	if len(registry.Dialects) == 0 {
		t.Error("Expected at least one dialect to be loaded")
	}

	// Verify weather-xml dialect is loaded
	found := false
	for _, dialect := range registry.Dialects {
		if dialect.DialectID == "weather-xml" {
			found = true
			if dialect.Name != "Weather XML" {
				t.Errorf("Expected dialect name 'Weather XML', got %s", dialect.Name)
			}
			if len(dialect.Patterns) == 0 {
				t.Error("Expected weather-xml dialect to have patterns")
			}
			break
		}
	}
	if !found {
		t.Error("Expected weather-xml dialect to be loaded")
	}
}

func TestRegistryLoader_LoadRegistryWithExtensions(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	// Test without extensions directory
	registry, err := loader.LoadRegistryWithExtensions("", "")
	if err != nil {
		t.Fatalf("Failed to load registry with extensions: %v", err)
	}

	if len(registry.Dialects) == 0 {
		t.Error("Expected at least one dialect to be loaded")
	}
}

func TestRegistryLoader_ValidateRegistry(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	// Test valid registry
	validRegistry := &DialectRegistry{
		RegistryVersion: "v0.1.0",
		LastUpdated:     time.Now(),
		Dialects: []Dialect{
			{
				DialectID: "test-dialect",
				Name:      "Test Dialect",
				Patterns: []Pattern{
					{
						PatternID: "test-pattern",
						Name:      "Test Pattern",
						Selector:  "local-name()='test'",
						Weight:    0.8,
						Ecosystem: "test",
					},
				},
			},
		},
		Validation: &ValidationRules{
			RequiredFields: []string{"dialect_id", "patterns"},
			UniqueItems:    []string{"dialect_id"},
			NoDuplicates:   true,
		},
	}

	err := loader.validateRegistry(validRegistry)
	if err != nil {
		t.Errorf("Expected valid registry to pass validation, got error: %v", err)
	}

	// Test invalid registry - missing required field
	invalidRegistry := &DialectRegistry{
		Dialects: []Dialect{
			{
				Name: "Invalid Dialect",
				Patterns: []Pattern{
					{
						PatternID: "test-pattern",
						Name:      "Test Pattern",
						Selector:  "local-name()='test'",
						Weight:    0.8,
						Ecosystem: "test",
					},
				},
			},
		},
		Validation: &ValidationRules{
			RequiredFields: []string{"dialect_id"},
		},
	}

	err = loader.validateRegistry(invalidRegistry)
	if err == nil {
		t.Error("Expected invalid registry to fail validation")
	}
}

func TestRegistryLoader_LoadExternalDialects(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	// Create temporary directory with test dialect
	tempDir, err := os.MkdirTemp("", "sumpter-test-dialects")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	// Create test dialect file
	testDialect := `dialect_id: "test-external"
name: "Test External Dialect"
description: "Test dialect for external loading"
status: "active"
priority: "low"
realm: "general"
patterns:
  - pattern_id: "test-pattern"
    name: "Test Pattern"
    selector: "local-name()='test'"
    weight: 0.5
    ecosystem: "test"
`

	dialectFile := filepath.Join(tempDir, "test-external.yaml")
	err = os.WriteFile(dialectFile, []byte(testDialect), 0644)
	if err != nil {
		t.Fatalf("Failed to write test dialect file: %v", err)
	}

	registry := &DialectRegistry{
		Dialects: []Dialect{},
	}

	err = loader.loadExternalDialects(registry, tempDir)
	if err != nil {
		t.Fatalf("Failed to load external dialects: %v", err)
	}

	if len(registry.Dialects) != 1 {
		t.Errorf("Expected 1 dialect, got %d", len(registry.Dialects))
	}

	if registry.Dialects[0].DialectID != "test-external" {
		t.Errorf("Expected dialect ID 'test-external', got %s", registry.Dialects[0].DialectID)
	}
}

func TestRegistryLoader_LoadExtensionsRejectsEscapingSource(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	tempDir := t.TempDir()
	outsideDialect := filepath.Join(t.TempDir(), "outside.yaml")
	if err := os.WriteFile(outsideDialect, []byte(`dialect_id: "outside"
name: "Outside"
patterns: []
`), 0o600); err != nil {
		t.Fatalf("WriteFile outside dialect: %v", err)
	}

	extension := `type: "blend"
source: "../outside.yaml"
version: "v0.1.0"
`
	if err := os.WriteFile(filepath.Join(tempDir, "extension.yaml"), []byte(extension), 0o600); err != nil {
		t.Fatalf("WriteFile extension: %v", err)
	}

	registry := &DialectRegistry{Dialects: []Dialect{}}
	if err := loader.loadExtensions(registry, tempDir); err != nil {
		t.Fatalf("loadExtensions: %v", err)
	}
	if len(registry.Dialects) != 0 {
		t.Fatalf("escaping extension source loaded %d dialects, want 0", len(registry.Dialects))
	}
}

func TestRegistryLoader_ApplyBlend(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	registry := &DialectRegistry{
		Dialects: []Dialect{
			{
				DialectID: "test-dialect",
				Name:      "Test Dialect",
				Patterns: []Pattern{
					{
						PatternID: "existing-pattern",
						Name:      "Existing Pattern",
						Selector:  "local-name()='existing'",
						Weight:    0.6,
						Ecosystem: "test",
					},
				},
			},
		},
	}

	extension := Extension{
		Type:     "blend",
		Priority: "user",
	}

	newDialect := Dialect{
		DialectID: "test-dialect",
		Patterns: []Pattern{
			{
				PatternID: "new-pattern",
				Name:      "New Pattern",
				Selector:  "local-name()='new'",
				Weight:    0.7,
				Ecosystem: "test",
			},
		},
	}

	err := loader.applyBlend(registry, extension, newDialect)
	if err != nil {
		t.Fatalf("Failed to apply blend: %v", err)
	}

	// Should have both patterns now
	dialect := registry.Dialects[0]
	if len(dialect.Patterns) != 2 {
		t.Errorf("Expected 2 patterns after blend, got %d", len(dialect.Patterns))
	}
}

func TestRegistryLoader_ApplyOverride(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	registry := &DialectRegistry{
		Dialects: []Dialect{
			{
				DialectID: "test-dialect",
				Name:      "Original Name",
				Patterns: []Pattern{
					{
						PatternID: "original-pattern",
						Name:      "Original Pattern",
						Selector:  "local-name()='original'",
						Weight:    0.5,
						Ecosystem: "test",
					},
				},
			},
		},
	}

	extension := Extension{
		Type:      "override",
		Priority:  "user",
		MergeKeys: []string{"dialect_id"},
	}

	overrideDialect := Dialect{
		DialectID: "test-dialect",
		Name:      "Override Name",
		Patterns: []Pattern{
			{
				PatternID: "override-pattern",
				Name:      "Override Pattern",
				Selector:  "local-name()='override'",
				Weight:    0.8,
				Ecosystem: "test",
			},
		},
	}

	err := loader.applyOverride(registry, extension, overrideDialect)
	if err != nil {
		t.Fatalf("Failed to apply override: %v", err)
	}

	dialect := registry.Dialects[0]
	if dialect.Name != "Override Name" {
		t.Errorf("Expected dialect name to be overridden to 'Override Name', got %s", dialect.Name)
	}
}

func TestRegistryLoader_ApplyReplace(t *testing.T) {
	logger := zaptest.NewLogger(t)
	loader := NewRegistryLoader(logger)

	registry := &DialectRegistry{
		Dialects: []Dialect{
			{
				DialectID: "test-dialect",
				Name:      "Original Name",
				Patterns: []Pattern{
					{
						PatternID: "original-pattern",
						Name:      "Original Pattern",
						Selector:  "local-name()='original'",
						Weight:    0.5,
						Ecosystem: "test",
					},
				},
			},
		},
	}

	extension := Extension{
		Type: "replace",
	}

	replaceDialect := Dialect{
		DialectID: "test-dialect",
		Name:      "Replace Name",
		Patterns: []Pattern{
			{
				PatternID: "replace-pattern",
				Name:      "Replace Pattern",
				Selector:  "local-name()='replace'",
				Weight:    0.9,
				Ecosystem: "test",
			},
		},
	}

	err := loader.applyReplace(registry, extension, replaceDialect)
	if err != nil {
		t.Fatalf("Failed to apply replace: %v", err)
	}

	dialect := registry.Dialects[0]
	if dialect.Name != "Replace Name" {
		t.Errorf("Expected dialect name to be replaced to 'Replace Name', got %s", dialect.Name)
	}

	if len(dialect.Patterns) != 1 {
		t.Errorf("Expected 1 pattern after replace, got %d", len(dialect.Patterns))
	}

	if dialect.Patterns[0].PatternID != "replace-pattern" {
		t.Errorf("Expected pattern ID to be 'replace-pattern', got %s", dialect.Patterns[0].PatternID)
	}
}
