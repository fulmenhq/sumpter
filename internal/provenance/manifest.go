package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// ManifestSchemaVersion is the stable schema identifier emitted in
	// provenance sidecars.
	ManifestSchemaVersion = "sumpter.provenance/v1"
	// ManifestFileName is the sidecar file written beside extract outputs.
	ManifestFileName = "manifest.json"
)

// Manifest is the canonical provenance sidecar for an extract run.
type Manifest struct {
	SchemaVersion      string            `json:"schema_version"`
	RunID              string            `json:"run_id"`
	SumpterVersion     string            `json:"sumpter_version"`
	StartedAt          time.Time         `json:"started_at"`
	CompletedAt        time.Time         `json:"completed_at"`
	CLI                CLI               `json:"cli"`
	Recipe             *Recipe           `json:"recipe,omitempty"`
	Inputs             []Input           `json:"inputs"`
	Outputs            []Output          `json:"outputs"`
	CountsByRecordType map[string]int    `json:"counts_by_record_type"`
	Attestations       []json.RawMessage `json:"attestations,omitempty"`
}

// CLI captures the sanitized command surface used for a run.
type CLI struct {
	Command       string   `json:"command"`
	ArgvSanitized []string `json:"argv_sanitized"`
}

// Recipe captures recipe-backed provenance. Direct extract runs omit this
// object rather than emitting an empty recipe block.
type Recipe struct {
	ID                    string            `json:"id"`
	ManifestSchemaVersion string            `json:"manifest_schema_version"`
	ContentVersion        string            `json:"content_version"`
	ContentHash           string            `json:"content_hash"`
	Owners                []Owner           `json:"owners,omitempty"`
	ManifestYAML          string            `json:"manifest_yaml,omitempty"`
	SignatureYAML         string            `json:"signature_yaml"`
	ExtractYAML           string            `json:"extract_yaml"`
	FieldProvenance       []FieldProvenance `json:"field_provenance,omitempty"`
}

// Owner describes a recipe owner copied from a recipe manifest.
type Owner struct {
	Name    string `json:"name"`
	Contact string `json:"contact,omitempty"`
	Role    string `json:"role,omitempty"`
}

// FieldProvenance describes the source selector or expression for an output field.
type FieldProvenance struct {
	OutputField string `json:"output_field"`
	XPath       string `json:"xpath,omitempty"`
	Expression  string `json:"expression,omitempty"`
	Type        string `json:"type,omitempty"`
	Description string `json:"description,omitempty"`
	Transform   string `json:"transform,omitempty"`
}

// Input describes an input file that was successfully processed.
type Input struct {
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
	SizeBytes  int64  `json:"size_bytes"`
	RecordType string `json:"record_type,omitempty"`
}

// Output describes an output artifact written by the extract run.
type Output struct {
	Path        string `json:"path"`
	Format      string `json:"format"`
	RecordCount int    `json:"record_count"`
}

// WriteManifest writes a deterministic, indented JSON sidecar.
func WriteManifest(path string, manifest Manifest) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("manifest path is required")
	}
	manifest.SchemaVersion = ManifestSchemaVersion
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provenance manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create provenance manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write provenance manifest %s: %w", path, err)
	}
	return nil
}

// BuildInputLedger hashes and stats a processed input path.
func BuildInputLedger(path string, roots ...string) (Input, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Input{}, fmt.Errorf("stat input %s: %w", path, err)
	}
	hash, err := fileSHA256(path)
	if err != nil {
		return Input{}, err
	}
	return Input{
		Path:      SanitizePath(path, roots...),
		SHA256:    hash,
		SizeBytes: info.Size(),
	}, nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 - caller supplies processed input path
	if err != nil {
		return "", fmt.Errorf("open input for hash %s: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", fmt.Errorf("hash input %s: %w", path, err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// SanitizePath removes host-local absolute roots from Sumpter-generated path
// surfaces. User-authored raw YAML is intentionally not sanitized here.
func SanitizePath(candidate string, roots ...string) string {
	candidate = strings.TrimSpace(candidate)
	if candidate == "" {
		return ""
	}
	clean := filepath.Clean(candidate)
	if !filepath.IsAbs(clean) {
		return filepath.ToSlash(clean)
	}

	for _, root := range sanitizeRoots(roots...) {
		rel, err := filepath.Rel(root, clean)
		if err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			return filepath.ToSlash(rel)
		}
	}
	return filepath.Base(clean)
}

// SanitizeArgv redacts secret-like flag values and normalizes path-looking
// values before writing argv to the sidecar.
func SanitizeArgv(args []string, roots ...string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}

		key, value, hasValue := strings.Cut(arg, "=")
		if hasValue && isSecretKey(key) {
			out = append(out, key+"=<redacted>")
			continue
		}
		if hasValue {
			out = append(out, key+"="+sanitizeValue(value, roots...))
			continue
		}

		out = append(out, arg)
		if isSecretKey(arg) && i+1 < len(args) {
			i++
			out = append(out, "<redacted>")
		}
	}
	return out
}

func sanitizeValue(value string, roots ...string) string {
	if strings.Contains(value, string(filepath.Separator)) || filepath.IsAbs(value) {
		return SanitizePath(value, roots...)
	}
	return value
}

func sanitizeRoots(roots ...string) []string {
	var out []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		if abs, err := filepath.Abs(root); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, filepath.Clean(cwd))
	}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, filepath.Clean(home))
	}
	out = append(out, filepath.Clean(os.TempDir()))
	return out
}

func isSecretKey(key string) bool {
	key = strings.ToLower(strings.TrimLeft(strings.TrimSpace(key), "-"))
	for _, marker := range []string{"token", "secret", "password", "passwd", "apikey", "api-key", "credential"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
