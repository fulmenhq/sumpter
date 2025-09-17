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
