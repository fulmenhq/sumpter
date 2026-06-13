package provenance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/uriio"
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
	Cadence               string            `json:"cadence,omitempty"`
	ManifestYAML          string            `json:"manifest_yaml,omitempty"`
	SignatureYAML         string            `json:"signature_yaml"`
	ExtractYAML           string            `json:"extract_yaml"`
	ApplicabilityYAML     string            `json:"applicability_yaml,omitempty"`
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

// Input describes an input file considered by the extract run.
type Input struct {
	Path              string `json:"path"`
	SHA256            string `json:"sha256"`
	SizeBytes         int64  `json:"size_bytes"`
	RecordType        string `json:"record_type,omitempty"`
	Disposition       string `json:"disposition,omitempty"`
	DispositionReason string `json:"disposition_reason,omitempty"`
	DispositionDetail string `json:"disposition_detail,omitempty"`
}

// Output describes an output artifact written by the extract run.
type Output struct {
	Path            string   `json:"path"`
	Format          string   `json:"format"`
	RecordCount     int      `json:"record_count"`
	WithholdColumns []string `json:"withhold_columns,omitempty"`
}

// WriteManifest writes a deterministic, indented JSON sidecar to a local path
// (bare path or file:// URI). It is the local-only convenience over
// WriteManifestVia: it resolves the destination through the credential-less uriio
// seam, so a cloud sidecar destination is rejected here. Cloud sidecar
// publication goes through WriteManifestVia with a session-resolved target so it
// publishes alongside its output under the run's output credentials.
func WriteManifest(path string, manifest Manifest) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("manifest path is required")
	}
	tgt, err := uriio.OpenOutput(context.Background(), uriio.OutputRequest{Reference: path})
	if err != nil {
		return err
	}
	return WriteManifestVia(context.Background(), tgt, manifest)
}

// WriteManifestVia writes the deterministic, indented JSON sidecar to the given
// output target and publishes it. The target may be local (no-op Publish) or a
// session-resolved cloud target (staging file + PutObject on Publish); the
// package stays session-agnostic — it only knows the target. The sidecar is
// written fully to the target's local path and only then published, so a publish
// failure never leaves a truncated manifest object.
func WriteManifestVia(ctx context.Context, target *uriio.OutputTarget, manifest Manifest) error {
	if target == nil {
		return fmt.Errorf("manifest output target is required")
	}
	manifest.SchemaVersion = ManifestSchemaVersion
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal provenance manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(target.LocalPath), 0o750); err != nil {
		return fmt.Errorf("create provenance manifest directory: %w", err)
	}
	if err := os.WriteFile(target.LocalPath, data, 0o600); err != nil {
		return fmt.Errorf("write provenance manifest %s: %w", target.LogicalURI, err)
	}
	return target.Publish(ctx)
}

// BuildInputLedger hashes and stats a processed input. localPath is the file the
// bytes are read from — a staged working copy for cloud sources, the source file
// itself for local ones. logicalURI is the canonical identity recorded in the
// manifest (a bare path, file:// URI, or s3:// URI), so a staged working path
// never leaks into a published artifact. For local sources the two coincide.
func BuildInputLedger(localPath, logicalURI string, roots ...string) (Input, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return Input{}, fmt.Errorf("stat input %s: %w", logicalURI, err)
	}
	hash, err := fileSHA256(localPath)
	if err != nil {
		return Input{}, err
	}
	return Input{
		Path:      SanitizePath(logicalURI, roots...),
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
	// A scheme-qualified cloud URI (e.g. s3://bucket/key) is a logical identity,
	// not a host-local path: it carries no local root to strip, and filepath.Clean
	// would collapse the "//" in the scheme (s3://bucket -> s3:/bucket). Return it
	// verbatim. file:// is excluded because it embeds a host-local path — but in
	// practice it never reaches here, having been resolved to a local path upstream.
	if strings.Contains(candidate, "://") && !strings.HasPrefix(candidate, "file://") {
		return candidate
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
