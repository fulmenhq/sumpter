package inspect

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

//go:embed embedded_dialects/*.yaml
var embeddedDialects embed.FS

// RegistryLoader handles loading and merging dialect registries
type RegistryLoader struct {
	logger Logger
}

// Logger interface for logging
type Logger interface {
	Info(msg string, fields ...zap.Field)
	Debug(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
}

// NewRegistryLoader creates a new registry loader
func NewRegistryLoader(logger Logger) *RegistryLoader {
	return &RegistryLoader{
		logger: logger,
	}
}

// LoadRegistry loads the complete dialect registry from embedded and external sources
func (r *RegistryLoader) LoadRegistry(dialectsDir string) (*DialectRegistry, error) {
	return r.LoadRegistryWithExtensions(dialectsDir, "")
}

// LoadRegistryWithExtensions loads registry with support for user extensions
func (r *RegistryLoader) LoadRegistryWithExtensions(dialectsDir, extensionsDir string) (*DialectRegistry, error) {
	registry := &DialectRegistry{
		RegistryVersion: "v0.1.0",
		LastUpdated:     time.Now(),
		Dialects:        []Dialect{},
		Validation: &ValidationRules{
			RequiredFields: []string{"dialect_id", "patterns"},
			UniqueItems:    []string{"dialect_id"},
			NoDuplicates:   true,
		},
	}

	// Load embedded dialects
	if err := r.loadEmbeddedDialects(registry); err != nil {
		r.logger.Warn("failed to load embedded dialects", zap.Error(err))
	}

	// Load external dialects if directory provided
	if dialectsDir != "" {
		if err := r.loadExternalDialects(registry, dialectsDir); err != nil {
			r.logger.Warn("failed to load external dialects", zap.Error(err))
		}
	}

	// Load extensions if directory provided
	if extensionsDir != "" {
		if err := r.loadExtensions(registry, extensionsDir); err != nil {
			r.logger.Warn("failed to load dialect extensions", zap.Error(err))
		}
	}

	// Validate the final registry
	if err := r.validateRegistry(registry); err != nil {
		return nil, fmt.Errorf("registry validation failed: %w", err)
	}

	r.logger.Info("registry loaded successfully",
		zap.Int("dialect_count", len(registry.Dialects)),
		zap.String("version", registry.RegistryVersion))

	return registry, nil
}

// loadEmbeddedDialects loads dialects from embedded YAML files
func (r *RegistryLoader) loadEmbeddedDialects(registry *DialectRegistry) error {
	entries, err := embeddedDialects.ReadDir("embedded_dialects")
	if err != nil {
		if os.IsNotExist(err) {
			r.logger.Info("no embedded dialects directory found")
			return nil
		}
		return fmt.Errorf("failed to read embedded dialects directory: %w", err)
	}

	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".yaml") && !strings.HasSuffix(entry.Name(), ".yml") {
			continue
		}

		data, err := embeddedDialects.ReadFile(filepath.Join("embedded_dialects", entry.Name()))
		if err != nil {
			r.logger.Warn("failed to read embedded dialect file", zap.String("file", entry.Name()), zap.Error(err))
			continue
		}

		var dialect Dialect
		if err := yaml.Unmarshal(data, &dialect); err != nil {
			r.logger.Warn("failed to parse embedded dialect", zap.String("file", entry.Name()), zap.Error(err))
			continue
		}

		registry.Dialects = append(registry.Dialects, dialect)
		r.logger.Debug("loaded embedded dialect", zap.String("dialect_id", dialect.DialectID), zap.String("name", dialect.Name))
	}

	return nil
}

// loadExternalDialects loads dialects from external directory
func (r *RegistryLoader) loadExternalDialects(registry *DialectRegistry, dialectsDir string) error {
	return filepath.WalkDir(dialectsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			r.logger.Warn("failed to read external dialect file", zap.String("file", path), zap.Error(err))
			return nil
		}

		var dialect Dialect
		if err := yaml.Unmarshal(data, &dialect); err != nil {
			r.logger.Warn("failed to parse external dialect", zap.String("file", path), zap.Error(err))
			return nil
		}

		registry.Dialects = append(registry.Dialects, dialect)
		r.logger.Debug("loaded external dialect", zap.String("dialect_id", dialect.DialectID), zap.String("name", dialect.Name))

		return nil
	})
}

// loadExtensions loads dialect extensions from user directory
func (r *RegistryLoader) loadExtensions(registry *DialectRegistry, extensionsDir string) error {
	return filepath.WalkDir(extensionsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			r.logger.Warn("failed to read extension file", zap.String("file", path), zap.Error(err))
			return nil
		}

		var extension Extension
		if err := yaml.Unmarshal(data, &extension); err != nil {
			r.logger.Warn("failed to parse extension", zap.String("file", path), zap.Error(err))
			return nil
		}

		// Load the dialect file referenced by the extension
		dialectPath := filepath.Join(extensionsDir, extension.Source)
		dialectData, err := os.ReadFile(dialectPath)
		if err != nil {
			r.logger.Warn("failed to read dialect file for extension", zap.String("dialect_file", dialectPath), zap.Error(err))
			return nil
		}

		var dialect Dialect
		if err := yaml.Unmarshal(dialectData, &dialect); err != nil {
			r.logger.Warn("failed to parse dialect for extension", zap.String("dialect_file", dialectPath), zap.Error(err))
			return nil
		}

		// Apply the extension operation
		if err := r.applyExtension(registry, extension, dialect); err != nil {
			r.logger.Warn("failed to apply extension", zap.String("extension", extension.Type), zap.Error(err))
			return nil
		}

		r.logger.Debug("applied extension", zap.String("type", extension.Type), zap.String("dialect_id", dialect.DialectID))

		return nil
	})
}

// applyExtension applies an extension operation to the registry
func (r *RegistryLoader) applyExtension(registry *DialectRegistry, extension Extension, dialect Dialect) error {
	switch extension.Type {
	case "blend":
		return r.applyBlend(registry, extension, dialect)
	case "override":
		return r.applyOverride(registry, extension, dialect)
	case "replace":
		return r.applyReplace(registry, extension, dialect)
	default:
		return fmt.Errorf("unknown extension type: %s", extension.Type)
	}
}

// applyBlend merges dialect patterns with existing dialect
func (r *RegistryLoader) applyBlend(registry *DialectRegistry, extension Extension, dialect Dialect) error {
	// Find existing dialect or add new one
	var targetDialect *Dialect
	for i := range registry.Dialects {
		if registry.Dialects[i].DialectID == dialect.DialectID {
			targetDialect = &registry.Dialects[i]
			break
		}
	}

	if targetDialect == nil {
		// Add as new dialect
		registry.Dialects = append(registry.Dialects, dialect)
		return nil
	}

	// Merge patterns
	for _, newPattern := range dialect.Patterns {
		// Check if pattern already exists
		exists := false
		for j, existingPattern := range targetDialect.Patterns {
			if existingPattern.PatternID == newPattern.PatternID {
				// Override existing pattern based on priority
				if extension.Priority == "user" {
					targetDialect.Patterns[j] = newPattern
				}
				exists = true
				break
			}
		}
		if !exists {
			targetDialect.Patterns = append(targetDialect.Patterns, newPattern)
		}
	}

	return nil
}

// applyOverride replaces specific fields in existing dialect
func (r *RegistryLoader) applyOverride(registry *DialectRegistry, extension Extension, dialect Dialect) error {
	for i := range registry.Dialects {
		if registry.Dialects[i].DialectID == dialect.DialectID {
			// Apply overrides based on merge keys
			if len(extension.MergeKeys) == 0 {
				extension.MergeKeys = []string{"dialect_id"}
			}

			// For now, simple field replacement
			registry.Dialects[i] = dialect
			return nil
		}
	}

	// If dialect doesn't exist, add it
	registry.Dialects = append(registry.Dialects, dialect)
	return nil
}

// applyReplace completely replaces existing dialect
func (r *RegistryLoader) applyReplace(registry *DialectRegistry, extension Extension, dialect Dialect) error {
	for i := range registry.Dialects {
		if registry.Dialects[i].DialectID == dialect.DialectID {
			registry.Dialects[i] = dialect
			return nil
		}
	}

	// If dialect doesn't exist, add it
	registry.Dialects = append(registry.Dialects, dialect)
	return nil
}

// validateRegistry validates the complete registry
func (r *RegistryLoader) validateRegistry(registry *DialectRegistry) error {
	if registry.Validation == nil {
		return nil
	}

	// Check required fields
	for i, dialect := range registry.Dialects {
		for _, required := range registry.Validation.RequiredFields {
			switch required {
			case "dialect_id":
				if dialect.DialectID == "" {
					return fmt.Errorf("dialect %d missing required field: dialect_id", i)
				}
			case "patterns":
				if len(dialect.Patterns) == 0 {
					return fmt.Errorf("dialect %s missing required field: patterns", dialect.DialectID)
				}
			}
		}
	}

	// Check for duplicates if required
	if registry.Validation.NoDuplicates {
		seen := make(map[string]bool)
		for _, dialect := range registry.Dialects {
			for _, unique := range registry.Validation.UniqueItems {
				switch unique {
				case "dialect_id":
					if seen[dialect.DialectID] {
						return fmt.Errorf("duplicate dialect_id found: %s", dialect.DialectID)
					}
					seen[dialect.DialectID] = true
				}
			}
		}
	}

	return nil
}
