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
	ReferenceTables    []ReferenceTable  `json:"reference_tables,omitempty"`
	Attestations       []json.RawMessage `json:"attestations,omitempty"`
	// OutputMode is "aggregate" when records were streamed to one NDJSON writer per
	// invocation (rolled shards) instead of one file per input; omitted (per-input)
	// otherwise, keeping existing manifests byte-identical.
	OutputMode string `json:"output_mode,omitempty"`
	// AggregateOutputs is the per-shard summary in aggregate mode (path, format,
	// record count, content digest, contributing input-ordinal span). Resolved input
	// order is the inputs[] order. Completeness is global — Σ shard record_count ==
	// Σ inputs[].record_count == the counts_by_record_type total — not a per-shard
	// sum, because a multi-record input can straddle a roll boundary and so appear in
	// two adjacent shards' ordinal spans (see AggregateOutput).
	AggregateOutputs []AggregateOutput `json:"aggregate_outputs,omitempty"`
	// Incomplete marks a FAILED aggregate run that had already published one or more
	// cloud shards before failing (R8). Those objects cannot be un-published, so this
	// manifest is written on the failure path to make the committed shards (in
	// AggregateOutputs) discoverable for cleanup / idempotent rerun. Present only on a
	// failed partial run; omitted (false) on success and per-input mode, so successful
	// manifests stay byte-identical. A run whose manifest carries incomplete:true must
	// be treated as failed, not as successful output.
	Incomplete bool `json:"incomplete,omitempty"`
	// InputsTotal, InputsApplied, InputsNotApplicable, and InputsFailed are the
	// optional input-accounting integers: a single-field completeness answer
	// derived from the gap-free inputs[] inventory, mirroring the closed input
	// disposition enum (applied / not_applicable / failed) exactly. They are
	// emitted only on aggregate manifests with an authoritative input inventory
	// (SetInputAccounting is the sole producer) and are pointers so the tri-state
	// holds: nil (omitted) on per-input/default and incomplete:true manifests,
	// keeping those byte-identical; an explicit value — including 0, e.g.
	// inputs_failed:0 on an all-applied run — in aggregate mode. The reconciliation
	// invariant inputs_applied + inputs_failed + inputs_not_applicable ==
	// inputs_total == len(inputs) holds by construction. The portable semantic
	// lifecycle (complete / partial / incomplete) is not stored here: when
	// --artifact-descriptor is enabled, dataartifact.LifecycleFromManifest maps
	// Incomplete + these counts + inputs[].disposition onto the data-artifact/v0
	// lifecycle field without inventing new accounting.
	InputsTotal         *int `json:"inputs_total,omitempty"`
	InputsApplied       *int `json:"inputs_applied,omitempty"`
	InputsNotApplicable *int `json:"inputs_not_applicable,omitempty"`
	InputsFailed        *int `json:"inputs_failed,omitempty"`
}

// Input disposition wire values, mirroring internal/extract's Disposition enum.
// They are duplicated here (not imported) because internal/extract imports this
// package; referencing it back would create an import cycle. The set is closed
// and stable as the v1 provenance contract, so the duplication is safe.
const (
	dispositionApplied       = "applied"
	dispositionNotApplicable = "not_applicable"
	dispositionFailed        = "failed"
)

// SetInputAccounting computes the optional input-accounting integers
// (inputs_total / inputs_applied / inputs_not_applicable / inputs_failed) from
// the manifest's inputs[] and sets them. It is the single producer of those
// fields and is called only by aggregate finalize paths that hold an
// authoritative, gap-free input inventory.
//
// It counts strictly from inputs[].disposition over the closed set
// {applied, not_applicable, failed}; any input carrying a disposition outside
// that set (including an empty one) returns an error rather than guessing, so a
// producer can never emit completeness counts it cannot substantiate. By
// construction the invariant inputs_applied + inputs_failed +
// inputs_not_applicable == inputs_total == len(inputs) holds.
func (m *Manifest) SetInputAccounting() error {
	total := len(m.Inputs)
	var applied, notApplicable, failed int
	for i := range m.Inputs {
		switch m.Inputs[i].Disposition {
		case dispositionApplied:
			applied++
		case dispositionNotApplicable:
			notApplicable++
		case dispositionFailed:
			failed++
		default:
			return fmt.Errorf("input %d (%s): cannot compute input accounting for unaccounted disposition %q",
				i+1, m.Inputs[i].Path, m.Inputs[i].Disposition)
		}
	}
	m.InputsTotal = &total
	m.InputsApplied = &applied
	m.InputsNotApplicable = &notApplicable
	m.InputsFailed = &failed
	return nil
}

// ReferenceTable records the logical identity of an external reference table loaded
// for a run (the in_reference / lookup_reference sources). It is sidecar-manifest
// only — never repeated per output record — and carries NO row values: a content
// hash plus name/source/shape/caps are enough to reproduce and audit the lookup
// without multiplying the table's exposure across every record (the content hash of a
// small sensitive set is itself a confirmation oracle, so it lives here once).
type ReferenceTable struct {
	Name string `json:"name"`
	// Source is the logical workspace-relative path (or, in a later delivery, the
	// logical s3:// URI) — the effective source after any --reference-table override,
	// never the resolved absolute/staged path.
	Source string `json:"source"`
	// CredentialsHandle is the logical credential handle NAME that authorized reading
	// a cloud (s3://) source — the non-secret indirection label, same posture as
	// Input.CredentialsHandle. Empty (omitted) for local sources.
	CredentialsHandle string `json:"credentials_handle,omitempty"`
	Format            string `json:"format"`         // csv | tsv | ndjson
	Mode              string `json:"mode"`           // membership | lookup
	RowCount          int    `json:"row_count"`      // physical source rows loaded
	ContentSHA256     string `json:"content_sha256"` // "sha256:"-prefixed hash of source bytes
	MaxRows           int    `json:"max_rows"`
	MaxBytes          int64  `json:"max_bytes"`
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
	Path string `json:"path"`
	// CredentialsHandle is the resolved logical credential handle NAME that
	// authorized acquiring this input, recorded only for cloud (s3://) sources.
	// It is logical identity — the non-secret indirection label, the same class
	// as the s3:// URI in Path — never the resolved profile, endpoint, region, or
	// secret it points at (S8). Empty (omitted) for local/file inputs, which stay
	// byte-identical. See BuildInputLedger.
	CredentialsHandle string `json:"credentials_handle,omitempty"`
	SHA256            string `json:"sha256"`
	SizeBytes         int64  `json:"size_bytes"`
	RecordType        string `json:"record_type,omitempty"`
	Disposition       string `json:"disposition,omitempty"`
	DispositionReason string `json:"disposition_reason,omitempty"`
	DispositionDetail string `json:"disposition_detail,omitempty"`
	// RecordCount is the number of records this input contributed. It is a pointer
	// so the value is tri-state: nil (omitted) in per-input mode, keeping those
	// manifests byte-identical; an explicit value — including 0 for a zero-record
	// success, a not-applicable, or a failed input — in aggregate mode, where the
	// per-input count is part of the input-set provenance contract. The input's
	// ordinal is its position (1-based) in the manifest's resolved inputs[] order.
	RecordCount *int `json:"record_count,omitempty"`
}

// AggregateOutput summarizes one aggregate NDJSON output file (or rolled shard) in
// aggregate output mode: the span of resolved input ordinals that contributed
// records to it, how many records, and a content digest over the fully-written
// file (R3 — a self-verifying integrity/tamper digest, NOT a cross-run determinism
// check, which is payload-only and excludes _runtime). The manifest, not filename
// parsing, is authoritative for shard order.
//
// Completeness is a GLOBAL invariant, not per-shard: Σ shard RecordCount equals
// Σ input RecordCount equals the counts_by_record_type total. Per-shard ordinal
// ranges are the first/last input that wrote ANY record to the shard and may
// OVERLAP by one input at a roll boundary — a single multi-record input whose
// records straddle a cap appears in both the closing and opening shard's range —
// so they are not a per-shard partition and cannot be summed per shard.
type AggregateOutput struct {
	Path string `json:"path"`
	// CredentialsHandle is the logical handle NAME for a cloud (s3://) shard object;
	// empty for local. Same name-only posture as Output.CredentialsHandle.
	CredentialsHandle string `json:"credentials_handle,omitempty"`
	Format            string `json:"format"`
	RecordCount       int    `json:"record_count"`
	SHA256            string `json:"sha256"`
	// InputOrdinalStart/End are the first and last resolved input ordinal (1-based
	// inputs[] position) that contributed at least one record to this shard. Ranges
	// of adjacent shards may overlap by one input at a roll boundary (see the type
	// doc); they locate coverage, they do not partition the inputs.
	InputOrdinalStart int `json:"input_ordinal_start"`
	InputOrdinalEnd   int `json:"input_ordinal_end"`
}

// Output describes an output artifact written by the extract run.
type Output struct {
	Path string `json:"path"`
	// CredentialsHandle is the resolved logical credential handle NAME that
	// authorized publishing this artifact, recorded only for cloud (s3://)
	// destinations. Same logical-identity, name-only rule as Input.CredentialsHandle:
	// never the resolved profile/endpoint/region/secret (S8). Empty (omitted) for
	// local outputs, which stay byte-identical.
	CredentialsHandle string   `json:"credentials_handle,omitempty"`
	Format            string   `json:"format"`
	RecordCount       int      `json:"record_count"`
	WithholdColumns   []string `json:"withhold_columns,omitempty"`
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
//
// credentialsHandle is the resolved logical handle NAME that authorized this
// acquisition (after CLI > recipe-default > default precedence). It is recorded
// only when logicalURI is a cloud (s3://) reference — the single gate that keeps
// local/file inputs byte-identical and ensures the name rides only on entries
// that actually crossed the cloud credential boundary. Callers always pass the
// resolved name; the cloud test here decides whether it is persisted. Pass "" to
// record no handle.
func BuildInputLedger(localPath, logicalURI, credentialsHandle string, roots ...string) (Input, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return Input{}, fmt.Errorf("stat input %s: %w", logicalURI, err)
	}
	hash, err := fileSHA256(localPath)
	if err != nil {
		return Input{}, err
	}
	input := Input{
		Path:      SanitizePath(logicalURI, roots...),
		SHA256:    hash,
		SizeBytes: info.Size(),
	}
	if credentialsHandle != "" {
		if ref, classifyErr := uriio.Classify(logicalURI); classifyErr == nil && ref.IsCloud() {
			input.CredentialsHandle = credentialsHandle
		}
	}
	return input, nil
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
	return sanitizeArgv(args, nil, roots...)
}

// SanitizeArgvWithInternalParameters redacts argv values for --parameter keys
// declared derive-only by the recipe. The parameter key remains visible in
// provenance; only the value is suppressed.
func SanitizeArgvWithInternalParameters(args []string, internalParameters []string, roots ...string) []string {
	internalSet := make(map[string]struct{}, len(internalParameters))
	for _, key := range internalParameters {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		internalSet[key] = struct{}{}
	}
	return sanitizeArgv(args, internalSet, roots...)
}

func sanitizeArgv(args []string, internalParameters map[string]struct{}, roots ...string) []string {
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
		if hasValue && isParameterInternalFlag(key) {
			out = append(out, key+"="+sanitizeInternalParameterValue(value))
			continue
		}
		// A --parameter value is itself an inner key=value pair injected into every
		// record (the value carries the secret-shape, not the flag), so the inner
		// key — not "parameter" — decides redaction. Joined form: --parameter=k=v.
		if hasValue && isParameterFlag(key) {
			out = append(out, key+"="+sanitizeParameterValue(value, internalParameters, roots...))
			continue
		}
		if hasValue {
			out = append(out, key+"="+sanitizeValue(value, roots...))
			continue
		}

		out = append(out, arg)
		// Split form: --parameter-internal <k=v>. Redact by flag, not by the
		// recipe's internal key set, so every per-recipe manifest suppresses a
		// run-level internal value even for bystander recipes.
		if isParameterInternalFlag(arg) && i+1 < len(args) {
			i++
			out = append(out, sanitizeInternalParameterValue(strings.TrimSpace(args[i])))
			continue
		}
		// Split form: --parameter <k=v>. Consume the next token as the parameter
		// value and redact by its inner key, mirroring the joined form above.
		if isParameterFlag(arg) && i+1 < len(args) {
			i++
			out = append(out, sanitizeParameterValue(strings.TrimSpace(args[i]), internalParameters, roots...))
			continue
		}
		if isSecretKey(arg) && i+1 < len(args) {
			i++
			out = append(out, "<redacted>")
		}
	}
	return out
}

// isParameterFlag reports whether a flag token is the --parameter injection flag,
// whose value is an inner key=value pair (not a path or a secret-shaped flag
// value). Matches both --parameter and -parameter spellings.
func isParameterFlag(key string) bool {
	return strings.ToLower(strings.TrimLeft(strings.TrimSpace(key), "-")) == "parameter"
}

// isParameterInternalFlag reports whether a flag token is the extract-multi
// --parameter-internal injection flag. It is intentionally distinct from
// isParameterFlag because the redaction trigger is the flag itself, not recipe
// membership in a key set.
func isParameterInternalFlag(key string) bool {
	return strings.ToLower(strings.TrimLeft(strings.TrimSpace(key), "-")) == "parameter-internal"
}

// sanitizeParameterValue redacts/normalizes a --parameter value, which is an
// inner key=value pair. When the inner key is secret-shaped (token, secret,
// password, credential, ...) the inner value is redacted while the parameter key
// stays visible (so provenance still records WHICH parameter was set). A
// non-secret inner value is path-sanitized like any other argv value. A value
// with no inner "=" is malformed for --parameter; sanitize it as a plain value.
func sanitizeParameterValue(value string, internalParameters map[string]struct{}, roots ...string) string {
	innerKey, innerValue, ok := strings.Cut(value, "=")
	if !ok {
		return sanitizeValue(value, roots...)
	}
	innerKey = strings.TrimSpace(innerKey)
	if isSecretKey(innerKey) {
		return innerKey + "=<redacted>"
	}
	if _, internal := internalParameters[innerKey]; internal {
		return innerKey + "=<internal>"
	}
	return innerKey + "=" + sanitizeValue(innerValue, roots...)
}

func sanitizeInternalParameterValue(value string) string {
	innerKey, _, ok := strings.Cut(value, "=")
	if !ok {
		return "<internal>"
	}
	innerKey = strings.TrimSpace(innerKey)
	if innerKey == "" {
		return "<internal>"
	}
	return innerKey + "=<internal>"
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
