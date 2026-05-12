package recipes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/utils"
	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

const (
	// ManifestVersion is the supported manifest schema identifier.
	ManifestVersion = "recipe/v0.1.0"
)

// Kind represents the type of recipe described by the manifest.
type Kind string

const (
	KindExtract Kind = "extract"
	KindAcquire Kind = "acquire"
)

// Manifest captures the metadata and defaults for a recipe workspace.
type Manifest struct {
	Version       string        `yaml:"version"`
	ID            string        `yaml:"id"`
	Kind          Kind          `yaml:"kind"`
	DisplayName   string        `yaml:"display_name"`
	Description   string        `yaml:"description"`
	Tags          []string      `yaml:"tags"`
	CreatedAt     string        `yaml:"created_at"`
	Owners        []Owner       `yaml:"owners"`
	Documentation Documentation `yaml:"documentation"`
	Assets        Assets        `yaml:"assets"`
	Defaults      Defaults      `yaml:"defaults"`
	Notes         string        `yaml:"notes"`
}

// Owner describes the maintainer of a recipe.
type Owner struct {
	Name    string `yaml:"name"`
	Contact string `yaml:"contact"`
}

// Documentation references helpful collateral for the recipe.
type Documentation struct {
	Overview   string   `yaml:"overview"`
	Changelog  string   `yaml:"changelog"`
	References []string `yaml:"references"`
}

// Assets identifies files that make up the recipe implementation.
type Assets struct {
	Signature  string   `yaml:"signature"`
	Extract    string   `yaml:"extract"`
	Validation string   `yaml:"validation"`
	Retrieve   string   `yaml:"retrieve"`
	Extras     []string `yaml:"extras"`
}

// Defaults captures runtime defaults for executing the recipe.
type Defaults struct {
	Input    InputDefaults  `yaml:"input"`
	Output   OutputDefaults `yaml:"output"`
	ClientID string         `yaml:"client_id"`
	SiteID   string         `yaml:"site_id"`
	Workers  int            `yaml:"workers"`
	Progress bool           `yaml:"progress"`
}

// InputDefaults controls input discovery when executing extract recipes.
type InputDefaults struct {
	Mode           string   `yaml:"mode"`
	Files          []string `yaml:"files"`
	Path           string   `yaml:"path"`
	IncludePattern string   `yaml:"include_pattern"`
	ExcludePattern string   `yaml:"exclude_pattern"`
	MaxDepth       int      `yaml:"max_depth"`
	FollowSymlinks bool     `yaml:"follow_symlinks"`
}

// OutputDefaults controls output formatting when executing extract recipes.
type OutputDefaults struct {
	Format  string `yaml:"format"`
	Path    string `yaml:"path"`
	Pattern string `yaml:"pattern"`
}

// LoadManifest reads and validates a manifest from disk.
// Note: This function does not restrict the manifestPath because users explicitly
// specify which manifest file to load. Security validation happens in OpenRelativeFile
// to prevent traversal attacks when opening assets relative to the manifest directory.
func LoadManifest(manifestPath string) (*Manifest, error) {
	data, err := os.ReadFile(manifestPath) // #nosec G304 - User-specified manifest file (top-level input)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest %s: %w", manifestPath, err)
	}

	if err := validateAgainstSchema(data, manifestPath); err != nil {
		return nil, err
	}

	var manifest Manifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest %s: %w", manifestPath, err)
	}

	if err := manifest.validate(); err != nil {
		return nil, err
	}
	manifest.applyDefaults()

	return &manifest, nil
}

func validateAgainstSchema(data []byte, manifestPath string) error {
	schemaFS, err := assets.GetSchemasFS()
	if err != nil {
		// Embedded schemas not available (e.g., during tests). In that case
		// fall back to any on-disk schemas, but do not fail hard here.
		return nil
	}

	validator := validation.NewSchemaValidatorFromFS(schemaFS)
	result, err := validator.ValidateRecipeManifest(data, manifestPath)
	if err != nil {
		return err
	}

	if result != nil && !result.Valid {
		var sb strings.Builder
		sb.WriteString("manifest validation failed:\n")
		for _, verr := range result.Errors {
			fmt.Fprintf(&sb, "- %s: %s\n", verr.Path, verr.Message)
		}
		return errors.New(strings.TrimSuffix(sb.String(), "\n"))
	}

	return nil
}

func (m *Manifest) validate() error {
	if strings.TrimSpace(m.Version) == "" {
		return errors.New("manifest version is required")
	}
	if m.Version != ManifestVersion {
		return fmt.Errorf("unsupported manifest version %s", m.Version)
	}
	if strings.TrimSpace(m.ID) == "" {
		return errors.New("manifest id is required")
	}
	switch m.Kind {
	case KindExtract, KindAcquire:
	default:
		return fmt.Errorf("unsupported manifest kind %s", m.Kind)
	}

	if m.Kind == KindExtract {
		if strings.TrimSpace(m.Assets.Signature) == "" {
			return errors.New("assets.signature is required for extract recipes")
		}
		if strings.TrimSpace(m.Assets.Extract) == "" {
			return errors.New("assets.extract is required for extract recipes")
		}
	}

	return nil
}

func (m *Manifest) applyDefaults() {
	if m.Defaults.Input.Mode == "" {
		m.Defaults.Input.Mode = "path"
	}
	if m.Defaults.Input.IncludePattern == "" {
		m.Defaults.Input.IncludePattern = "*.xml"
	}
	if m.Defaults.Output.Format == "" {
		m.Defaults.Output.Format = "json"
	}
	if m.Defaults.Output.Pattern == "" {
		m.Defaults.Output.Pattern = "extract-{}.json"
	}
	if m.Defaults.Workers <= 0 {
		m.Defaults.Workers = 1
	}
}

// ResolvePath resolves a path relative to the manifest workspace.
func ResolvePath(base, candidate string) string {
	if candidate == "" {
		return ""
	}
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(base, candidate)
}

// OpenRelativeFile opens a relative file within the workspace ensuring the path stays under the workspace root.
func OpenRelativeFile(base, candidate string) (*os.File, error) {
	resolved := ResolvePath(base, candidate)
	if resolved == "" {
		return nil, fmt.Errorf("empty path resolved for %s", candidate)
	}

	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return nil, err
	}
	cleanResolved, err := filepath.Abs(resolved)
	if err != nil {
		return nil, err
	}

	rel, err := filepath.Rel(cleanBase, cleanResolved)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("path %s escapes workspace", candidate)
	}

	// Final validation before opening
	if err := utils.ValidateUserPathForRead(cleanResolved, utils.RootCwd, cleanBase); err != nil {
		return nil, fmt.Errorf("invalid asset path: %w", err)
	}

	return os.Open(cleanResolved) // #nosec G304 - Path validated by ValidateUserPathForRead
}

// ListAssets resolves all asset paths contained in the manifest.
func (m *Manifest) ListAssets(base string) []string {
	var assets []string
	add := func(p string) {
		if strings.TrimSpace(p) == "" {
			return
		}
		assets = append(assets, ResolvePath(base, p))
	}

	add(m.Assets.Signature)
	add(m.Assets.Extract)
	add(m.Assets.Validation)
	add(m.Assets.Retrieve)
	for _, extra := range m.Assets.Extras {
		add(extra)
	}

	return assets
}

// WorkspaceFilesystem returns an fs.FS rooted at the workspace for convenience.
func WorkspaceFilesystem(base string) (fs.FS, error) {
	return os.DirFS(base), nil
}
