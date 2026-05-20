package recipes

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/utils"
	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

// semverPattern matches a strict semver string (MAJOR.MINOR.PATCH with
// optional pre-release and build metadata per SemVer 2.0.0). Anchored.
var semverPattern = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)
var sourceExtractionIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

const (
	// ManifestVersion is the supported manifest schema identifier.
	ManifestVersion = "recipe/v0.1.0"
	// SourceExtractionFilename extracts fields from the source file base name.
	SourceExtractionFilename = "filename"
	// SourceExtractionRelativePath extracts fields from the source path relative to the explicit input root.
	SourceExtractionRelativePath = "relative_path"
	// SourceExtractionAbsolutePath extracts fields from the absolute source path.
	SourceExtractionAbsolutePath = "absolute_path"
	// SourceExtractionMaxIDLength caps optional pattern identifiers.
	SourceExtractionMaxIDLength = 64
	// SourceExtractionMaxPatterns caps per-recipe source extraction patterns.
	SourceExtractionMaxPatterns = 32
	// SourceExtractionMaxPatternLength caps each source extraction regexp.
	SourceExtractionMaxPatternLength = 1024
	// SourceExtractionMaxCaptureNames caps unique non-empty named captures per pattern.
	SourceExtractionMaxCaptureNames = 32
	// OutputFormatJSON writes newline-delimited JSON records.
	OutputFormatJSON = "json"
	// OutputFormatNDJSON is an alias for JSONL/NDJSON record output.
	OutputFormatNDJSON = "ndjson"
	// OutputFormatParquet writes extract.data records as Parquet.
	OutputFormatParquet = "parquet"
	// ParquetCompressionZSTD is the default Parquet compression codec.
	ParquetCompressionZSTD = "zstd"
	// ParquetCompressionSnappy uses Snappy compression for Parquet output.
	ParquetCompressionSnappy = "snappy"
	// ParquetCompressionGzip uses Gzip compression for Parquet output.
	ParquetCompressionGzip = "gzip"
	// ParquetCompressionNone disables Parquet compression.
	ParquetCompressionNone = "none"
)

// Kind represents the type of recipe described by the manifest.
type Kind string

const (
	KindExtract Kind = "extract"
	KindAcquire Kind = "acquire"
)

// Manifest captures the metadata and defaults for a recipe workspace.
type Manifest struct {
	Version        string        `yaml:"version"`
	ID             string        `yaml:"id"`
	Kind           Kind          `yaml:"kind"`
	DisplayName    string        `yaml:"display_name"`
	Description    string        `yaml:"description"`
	Tags           []string      `yaml:"tags"`
	CreatedAt      string        `yaml:"created_at"`
	ContentVersion string        `yaml:"content_version,omitempty"`
	Owners         []Owner       `yaml:"owners"`
	Documentation  Documentation `yaml:"documentation"`
	Assets         Assets        `yaml:"assets"`
	Defaults       Defaults      `yaml:"defaults"`
	Notes          string        `yaml:"notes"`

	// Warnings is populated during Load with non-fatal validation messages
	// (e.g., missing content_version on v0.1.3). Not serialized.
	Warnings []string `yaml:"-"`
}

// Owner describes the maintainer of a recipe.
type Owner struct {
	Name    string `yaml:"name"`
	Contact string `yaml:"contact,omitempty"`
	Role    string `yaml:"role,omitempty"`
}

// UnversionedContent is the placeholder recorded in provenance when a
// recipe manifest is missing content_version. Per ADR-0006 this emits a
// deprecation warning in v0.1.3 and a hard error in v0.1.4.
const UnversionedContent = "unversioned"

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
	Cadence                  string                    `yaml:"cadence,omitempty"`
	Input                    InputDefaults             `yaml:"input"`
	Output                   OutputDefaults            `yaml:"output"`
	ClientID                 string                    `yaml:"client_id"`
	SiteID                   string                    `yaml:"site_id"`
	Parameters               map[string]string         `yaml:"parameters,omitempty"`
	ParametersRequired       []string                  `yaml:"parameters_required,omitempty"`
	SourceExtraction         []SourceExtractionPattern `yaml:"source_extraction,omitempty"`
	SourceExtractionRequired []string                  `yaml:"source_extraction_required,omitempty"`
	Workers                  int                       `yaml:"workers"`
	Progress                 bool                      `yaml:"progress"`
}

// SourceExtractionPattern extracts fields from a source file location.
type SourceExtractionPattern struct {
	ID              string         `yaml:"id,omitempty" json:"id,omitempty"`
	Source          string         `yaml:"source" json:"source"`
	Pattern         string         `yaml:"pattern" json:"pattern"`
	CompiledPattern *regexp.Regexp `yaml:"-" json:"-"`
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
	Format   string            `yaml:"format,omitempty"`
	Formats  []string          `yaml:"formats,omitempty"`
	Path     string            `yaml:"path"`
	Pattern  string            `yaml:"pattern,omitempty"`
	Patterns map[string]string `yaml:"patterns,omitempty"`
	Parquet  *ParquetDefaults  `yaml:"parquet,omitempty"`
}

// ParquetDefaults controls recipe-level Parquet output behavior.
type ParquetDefaults struct {
	Compression     string   `yaml:"compression,omitempty"`
	WithholdColumns []string `yaml:"withhold_columns,omitempty"`
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

	// Per ADR-0006: content_version is optional in v0.1.3 with a
	// deprecation warning when missing; v0.1.4 will make it a hard error.
	contentVersion := strings.TrimSpace(m.ContentVersion)
	switch {
	case contentVersion == "":
		m.ContentVersion = UnversionedContent
		m.Warnings = append(m.Warnings, fmt.Sprintf(
			"recipe %s is missing `content_version` (semver). Provenance will "+
				"record `recipe_version` as %q. v0.1.4 will treat this as a hard "+
				"error. Run `sumpter recipes migrate` to stamp a starter version, "+
				"or edit recipe.yaml manually.",
			m.ID, UnversionedContent,
		))
	case !semverPattern.MatchString(contentVersion):
		return fmt.Errorf(
			"content_version %q is not a valid SemVer 2.0.0 string "+
				"(expected MAJOR.MINOR.PATCH, e.g. \"0.1.0\")",
			contentVersion,
		)
	}

	if err := m.validateSourceExtraction(); err != nil {
		return err
	}
	if err := m.validateOutputDefaults(); err != nil {
		return err
	}

	return nil
}

func (m *Manifest) validateOutputDefaults() error {
	output := m.Defaults.Output
	if strings.TrimSpace(output.Format) != "" && len(output.Formats) > 0 {
		return errors.New("defaults.output.format and defaults.output.formats are mutually exclusive")
	}
	if strings.TrimSpace(output.Pattern) != "" && len(output.Patterns) > 0 {
		return errors.New("defaults.output.pattern and defaults.output.patterns are mutually exclusive")
	}

	formats, err := output.FormatsOrDefault()
	if err != nil {
		return err
	}
	seenFormats := make(map[string]struct{}, len(formats))
	for _, format := range formats {
		effective := EffectiveOutputFormat(format)
		if _, ok := seenFormats[effective]; ok {
			return fmt.Errorf("defaults.output.formats contains duplicate effective format %q", effective)
		}
		seenFormats[effective] = struct{}{}
	}

	for format := range output.Patterns {
		if _, err := NormalizeOutputFormat(format); err != nil {
			return fmt.Errorf("defaults.output.patterns key %q: %w", format, err)
		}
	}

	if _, err := output.ParquetCompression(); err != nil {
		return err
	}

	return nil
}

// NormalizeOutputFormat validates and canonicalizes configured output format
// names. JSON and NDJSON are both accepted because the extract writer emits
// newline-delimited JSON records for both names.
func NormalizeOutputFormat(format string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(format))
	switch normalized {
	case OutputFormatJSON, OutputFormatNDJSON, OutputFormatParquet:
		return normalized, nil
	default:
		if normalized == "" {
			return "", errors.New("output format cannot be empty")
		}
		return "", fmt.Errorf("unsupported output format %q", format)
	}
}

// EffectiveOutputFormat returns the writer family used by a normalized format.
func EffectiveOutputFormat(format string) string {
	if format == OutputFormatNDJSON {
		return OutputFormatJSON
	}
	return format
}

// FormatsOrDefault returns the configured formats or the legacy single format,
// defaulting to JSON when no output format is specified.
func (o OutputDefaults) FormatsOrDefault() ([]string, error) {
	if len(o.Formats) > 0 {
		formats := make([]string, 0, len(o.Formats))
		for _, format := range o.Formats {
			normalized, err := NormalizeOutputFormat(format)
			if err != nil {
				return nil, err
			}
			formats = append(formats, normalized)
		}
		return formats, nil
	}
	format := o.Format
	if strings.TrimSpace(format) == "" {
		format = OutputFormatJSON
	}
	normalized, err := NormalizeOutputFormat(format)
	if err != nil {
		return nil, err
	}
	return []string{normalized}, nil
}

// PatternForFormat resolves a format-specific output pattern. Pattern map keys
// may use either JSON or NDJSON for the newline-delimited JSON output family.
func (o OutputDefaults) PatternForFormat(format string) string {
	if len(o.Patterns) > 0 {
		if pattern := o.Patterns[format]; pattern != "" {
			return pattern
		}
		if EffectiveOutputFormat(format) == OutputFormatJSON {
			if pattern := o.Patterns[OutputFormatJSON]; pattern != "" {
				return pattern
			}
			if pattern := o.Patterns[OutputFormatNDJSON]; pattern != "" {
				return pattern
			}
		}
	}
	if o.Pattern != "" {
		return o.Pattern
	}
	return "extract-{}.json"
}

// ParquetCompression validates and returns the configured Parquet compression
// codec, defaulting to zstd.
func (o OutputDefaults) ParquetCompression() (string, error) {
	if o.Parquet == nil {
		return ParquetCompressionZSTD, nil
	}
	compression := strings.ToLower(strings.TrimSpace(o.Parquet.Compression))
	if compression == "" {
		return ParquetCompressionZSTD, nil
	}
	switch compression {
	case ParquetCompressionZSTD, ParquetCompressionSnappy, ParquetCompressionGzip, ParquetCompressionNone:
		return compression, nil
	default:
		return "", fmt.Errorf("defaults.output.parquet.compression %q must be one of zstd, snappy, gzip, none", o.Parquet.Compression)
	}
}

func (m *Manifest) validateSourceExtraction() error {
	patterns := m.Defaults.SourceExtraction
	if len(patterns) > SourceExtractionMaxPatterns {
		return fmt.Errorf("defaults.source_extraction has %d patterns, maximum is %d", len(patterns), SourceExtractionMaxPatterns)
	}

	for i := range patterns {
		pattern := &m.Defaults.SourceExtraction[i]
		label := sourceExtractionPatternLabel(i, pattern.ID)

		if strings.TrimSpace(pattern.ID) != "" {
			if len(pattern.ID) > SourceExtractionMaxIDLength {
				return fmt.Errorf("defaults.source_extraction %s id length %d exceeds maximum %d", label, len(pattern.ID), SourceExtractionMaxIDLength)
			}
			if !sourceExtractionIDPattern.MatchString(pattern.ID) {
				return fmt.Errorf("defaults.source_extraction %s id %q must be kebab-case", label, pattern.ID)
			}
		}

		switch pattern.Source {
		case SourceExtractionFilename, SourceExtractionRelativePath, SourceExtractionAbsolutePath:
		default:
			return fmt.Errorf("defaults.source_extraction %s source %q must be one of filename, relative_path, absolute_path", label, pattern.Source)
		}

		if len(pattern.Pattern) > SourceExtractionMaxPatternLength {
			return fmt.Errorf("defaults.source_extraction %s pattern length %d exceeds maximum %d", label, len(pattern.Pattern), SourceExtractionMaxPatternLength)
		}

		compiled, err := regexp.Compile(pattern.Pattern)
		if err != nil {
			return fmt.Errorf("defaults.source_extraction %s pattern failed to compile: %w", label, err)
		}
		pattern.CompiledPattern = compiled

		captureIndexes := make(map[string]int)
		for idx, name := range compiled.SubexpNames() {
			if name == "" {
				continue
			}
			if firstIdx, ok := captureIndexes[name]; ok {
				return fmt.Errorf("defaults.source_extraction %s pattern has duplicate named capture %q at indexes %d and %d", label, name, firstIdx, idx)
			}
			captureIndexes[name] = idx
		}
		if len(captureIndexes) > SourceExtractionMaxCaptureNames {
			return fmt.Errorf("defaults.source_extraction %s pattern has %d named captures, maximum is %d", label, len(captureIndexes), SourceExtractionMaxCaptureNames)
		}
	}

	seenRequired := make(map[string]struct{})
	for _, required := range m.Defaults.SourceExtractionRequired {
		required = strings.TrimSpace(required)
		if required == "" {
			return fmt.Errorf("defaults.source_extraction_required key cannot be empty")
		}
		if _, ok := seenRequired[required]; ok {
			return fmt.Errorf("defaults.source_extraction_required key %q is duplicated", required)
		}
		seenRequired[required] = struct{}{}
	}

	return nil
}

func sourceExtractionPatternLabel(index int, id string) string {
	if strings.TrimSpace(id) != "" {
		return fmt.Sprintf("%q", id)
	}
	return fmt.Sprintf("at index %d", index)
}

func (m *Manifest) applyDefaults() {
	if m.Defaults.Input.Mode == "" {
		m.Defaults.Input.Mode = "path"
	}
	if m.Defaults.Input.IncludePattern == "" {
		m.Defaults.Input.IncludePattern = "*.xml"
	}
	if m.Defaults.Output.Format == "" && len(m.Defaults.Output.Formats) == 0 {
		m.Defaults.Output.Format = OutputFormatJSON
	}
	if m.Defaults.Output.Pattern == "" && len(m.Defaults.Output.Patterns) == 0 {
		m.Defaults.Output.Pattern = "extract-{}.json"
	}
	if m.Defaults.Workers <= 0 {
		m.Defaults.Workers = 1
	}
}

// DrainWarnings returns the manifest's accumulated non-fatal warnings and
// clears them in place so subsequent callers do not re-emit the same
// messages. Returns nil when there is nothing to emit, allowing callers to
// guard with `if w := m.DrainWarnings(); len(w) > 0 { ... }`.
func (m *Manifest) DrainWarnings() []string {
	if m == nil || len(m.Warnings) == 0 {
		return nil
	}
	w := m.Warnings
	m.Warnings = nil
	return w
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
