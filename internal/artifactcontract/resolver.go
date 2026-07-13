package artifactcontract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/goneat/pkg/schema"
)

const (
	DataArtifactCapability = "contract: data-artifact/v0"
	ProcessRunCapability   = "contract: process-run/v0"

	// Data-artifact pin metadata (independently evolvable from process-run).
	BaselineSource      = "3leaps/crucible"
	BaselineReleasedTag = "v0.1.19"
	// Data-artifact resolved-bundle pin (contract.json + entry schema only).
	BaselineBundleSHA256 = "sha256:37eca167cfa9a86357c14239eb9c3274c40c5cfee48f48ebb81480d737104b82"

	// Process-run pin metadata (independently evolvable from data-artifact).
	ProcessRunBaselineSource      = "3leaps/crucible"
	ProcessRunBaselineReleasedTag = "v0.1.19"
	// Process-run L2 entry-bundle pin (contract.json + process-card entry schema only).
	// Derived with the same digest inputs/order as data-artifact against Crucible v0.1.19.
	ProcessRunBaselineBundleSHA256 = "sha256:4589befc1d0d3485744c7eea3dfb569ff79457f99996f2ee8313595489a7091b"
	// Process-run event-schema pin (sibling of the L2 entry; not part of the entry-bundle hash).
	// Derived with the same path|bytes digest discipline against Crucible v0.1.19.
	ProcessRunEventSchemaSHA256 = "sha256:7138fba72fea862d7964d6c235b1b93da0047e9eb76862be4d111701f887b12d"

	dataArtifactLogicalDir = "schemas/data-artifact/v0"
	processRunLogicalDir   = "schemas/process-run/v0"

	// ProcessEventSchemaFile is the sibling event-line schema under a process-run contract base.
	// It is not part of the resolved-bundle pin (entry schema is the process card only) but is
	// integrity-bound via ProcessRunEventSchemaSHA256 before event validation.
	ProcessEventSchemaFile = "process-event.schema.json"
)

type Manifest struct {
	Capability  string `json:"capability"`
	EntrySchema string `json:"entry_schema"`
}

type ResolvedContract struct {
	BaseDir              string
	Capability           string
	EntrySchema          string
	EntrySchemaPath      string
	EntrySchemaBytes     []byte
	BundleSHA256         string
	LogicalDir           string
	ContractManifest     Manifest
	ContractManifestPath string
}

type ValidationResult struct {
	Valid  bool              `json:"valid"`
	Errors []ValidationError `json:"errors,omitempty"`
	File   string            `json:"file,omitempty"`
	Schema string            `json:"schema,omitempty"`
}

type ValidationError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Resolve resolves a known host-less contract capability from an explicit local base.
// Logical bundle paths used in the digest match Crucible's schemas/<family>/v0/ layout.
func Resolve(baseDir, capability string) (*ResolvedContract, error) {
	logicalDir, err := logicalDirForCapability(capability)
	if err != nil {
		return nil, err
	}
	return resolveContract(baseDir, capability, logicalDir)
}

// ResolveContract is the shared host-less resolution primitive. Callers that already
// know the Crucible logical directory may use it directly; capability-specific
// wrappers should prefer Resolve / ResolveBaseline / ResolveProcessRunBaseline.
func ResolveContract(baseDir, capability, logicalDir string) (*ResolvedContract, error) {
	logicalDir = strings.Trim(strings.TrimSpace(logicalDir), "/")
	if logicalDir == "" {
		return nil, errors.New("contract logical directory is required")
	}
	if strings.Contains(logicalDir, "..") {
		return nil, errors.New("contract logical directory must not contain parent path segments")
	}
	return resolveContract(baseDir, capability, logicalDir)
}

func resolveContract(baseDir, capability, logicalDir string) (*ResolvedContract, error) {
	if strings.TrimSpace(baseDir) == "" {
		return nil, errors.New("contract base is required")
	}
	if strings.TrimSpace(capability) == "" {
		return nil, errors.New("contract capability is required")
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, fmt.Errorf("resolve contract base: %w", err)
	}
	manifestPath := filepath.Join(absBase, "contract.json")
	manifestBytes, err := os.ReadFile(manifestPath) // #nosec G304 - user-selected contract base, fail-closed below
	if err != nil {
		return nil, fmt.Errorf("read contract manifest: %w", err)
	}

	var manifest Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return nil, fmt.Errorf("parse contract manifest: %w", err)
	}
	if manifest.Capability != capability {
		return nil, fmt.Errorf("contract capability mismatch: manifest has %q, want %q", manifest.Capability, capability)
	}
	entryRel, err := cleanRelativeEntry(manifest.EntrySchema)
	if err != nil {
		return nil, fmt.Errorf("invalid entry schema: %w", err)
	}
	entryPath, err := resolveContainedEntryPath(absBase, entryRel)
	if err != nil {
		return nil, fmt.Errorf("invalid entry schema: %w", err)
	}
	entryBytes, err := os.ReadFile(entryPath) // #nosec G304 - entry schema is constrained to the contract base
	if err != nil {
		return nil, fmt.Errorf("read entry schema %q: %w", manifest.EntrySchema, err)
	}

	bundleSHA := bundleHash([]bundleFile{
		{LogicalPath: logicalDir + "/contract.json", Bytes: manifestBytes},
		{LogicalPath: logicalDir + "/" + entryRel, Bytes: entryBytes},
	})

	return &ResolvedContract{
		BaseDir:              absBase,
		Capability:           capability,
		EntrySchema:          entryRel,
		EntrySchemaPath:      entryPath,
		EntrySchemaBytes:     entryBytes,
		BundleSHA256:         bundleSHA,
		LogicalDir:           logicalDir,
		ContractManifest:     manifest,
		ContractManifestPath: manifestPath,
	}, nil
}

// ResolveBaseline resolves and pins the data-artifact/v0 Crucible baseline.
func ResolveBaseline(baseDir string) (*ResolvedContract, error) {
	resolved, err := Resolve(baseDir, DataArtifactCapability)
	if err != nil {
		return nil, err
	}
	if resolved.BundleSHA256 != BaselineBundleSHA256 {
		return nil, fmt.Errorf("contract baseline hash mismatch: got %s, want %s", resolved.BundleSHA256, BaselineBundleSHA256)
	}
	return resolved, nil
}

// ResolveProcessRunBaseline resolves and pins the process-run/v0 Crucible baseline.
func ResolveProcessRunBaseline(baseDir string) (*ResolvedContract, error) {
	resolved, err := Resolve(baseDir, ProcessRunCapability)
	if err != nil {
		return nil, err
	}
	if resolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
		return nil, fmt.Errorf("contract baseline hash mismatch: got %s, want %s", resolved.BundleSHA256, ProcessRunBaselineBundleSHA256)
	}
	return resolved, nil
}

func ValidateDescriptorFile(contractBase, descriptorPath string) (*ValidationResult, *ResolvedContract, error) {
	resolved, err := ResolveBaseline(contractBase)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(descriptorPath) // #nosec G304 - explicit user-selected descriptor path
	if err != nil {
		return nil, resolved, fmt.Errorf("read descriptor: %w", err)
	}
	result, err := ValidateDescriptorBytes(resolved, data, descriptorPath)
	return result, resolved, err
}

func ValidateDescriptorBytes(resolved *ResolvedContract, data []byte, descriptorName string) (*ValidationResult, error) {
	if resolved == nil {
		return nil, errors.New("resolved contract is required")
	}
	return validateAgainstSchema(resolved.EntrySchemaBytes, resolved.EntrySchema, data, descriptorName)
}

func ValidateFieldCatalogBytes(resolved *ResolvedContract, data []byte, catalogName string) (*ValidationResult, error) {
	if resolved == nil {
		return nil, errors.New("resolved contract is required")
	}
	var catalog interface{}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("parse field catalog JSON: %w", err)
	}
	wrapped := map[string]interface{}{
		"capabilities": []interface{}{DataArtifactCapability},
		"artifact_id":  "urn:uuid:00000000-0000-7000-8000-000000000000",
		"lifecycle":    "complete",
		"producer": map[string]interface{}{
			"name":    "sumpter",
			"version": "validation",
			"profile": "sumpter.extract-artifact/v0",
		},
		"grains": []interface{}{
			map[string]interface{}{
				"id":                "records",
				"kind":              "record_stream",
				"record_kind":       "extract_record",
				"field_catalog_ref": catalogName,
			},
		},
		"representations": []interface{}{
			map[string]interface{}{
				"id":                                 "records_ndjson_1",
				"role":                               "audit_stream",
				"format":                             "ndjson",
				"uri":                                "records.jsonl",
				"read_path":                          map[string]interface{}{},
				"protection_enforceable_granularity": "row",
			},
		},
		"field_catalogs": []interface{}{catalog},
		"protection": map[string]interface{}{
			"default_action": "block_export",
		},
	}
	wrappedData, err := json.Marshal(wrapped)
	if err != nil {
		return nil, fmt.Errorf("marshal field catalog validation wrapper: %w", err)
	}
	result, err := ValidateDescriptorBytes(resolved, wrappedData, catalogName)
	if result != nil {
		result.File = catalogName
	}
	return result, err
}

// ValidateProcessCardFile resolves the process-run baseline and validates a card document.
func ValidateProcessCardFile(contractBase, cardPath string) (*ValidationResult, *ResolvedContract, error) {
	resolved, err := ResolveProcessRunBaseline(contractBase)
	if err != nil {
		return nil, nil, err
	}
	data, err := os.ReadFile(cardPath) // #nosec G304 - explicit user-selected card path
	if err != nil {
		return nil, resolved, fmt.Errorf("read process card: %w", err)
	}
	result, err := ValidateProcessCardBytes(resolved, data, cardPath)
	return result, resolved, err
}

// ValidateProcessCardBytes validates a process card against the resolved entry schema.
func ValidateProcessCardBytes(resolved *ResolvedContract, data []byte, cardName string) (*ValidationResult, error) {
	if resolved == nil {
		return nil, errors.New("resolved contract is required")
	}
	if resolved.Capability != ProcessRunCapability {
		return nil, fmt.Errorf("process card validation requires %q, got %q", ProcessRunCapability, resolved.Capability)
	}
	return validateAgainstSchema(resolved.EntrySchemaBytes, resolved.EntrySchema, data, cardName)
}

// ValidateProcessEventStreamFile validates every non-blank NDJSON line against the pinned
// process-event.schema.json. Card validation alone does not prove event conformance.
func ValidateProcessEventStreamFile(contractBase, streamPath string) ([]*ValidationResult, *ResolvedContract, error) {
	resolved, err := ResolveProcessRunBaseline(contractBase)
	if err != nil {
		return nil, nil, err
	}
	eventSchema, err := LoadPinnedProcessEventSchema(resolved)
	if err != nil {
		return nil, resolved, err
	}
	data, err := os.ReadFile(streamPath) // #nosec G304 - explicit user-selected stream path
	if err != nil {
		return nil, resolved, fmt.Errorf("read process event stream: %w", err)
	}
	results, err := ValidateProcessEventStreamBytes(eventSchema, data, streamPath)
	return results, resolved, err
}

// LoadPinnedProcessEventSchema reads process-event.schema.json from the resolved base and
// fail-closes when its digest does not match ProcessRunEventSchemaSHA256.
func LoadPinnedProcessEventSchema(resolved *ResolvedContract) ([]byte, error) {
	if resolved == nil {
		return nil, errors.New("resolved contract is required")
	}
	if resolved.Capability != ProcessRunCapability {
		return nil, fmt.Errorf("process event schema requires %q, got %q", ProcessRunCapability, resolved.Capability)
	}
	eventSchema, err := ReadContainedFile(resolved.BaseDir, ProcessEventSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("load process event schema: %w", err)
	}
	got := bundleHash([]bundleFile{
		{LogicalPath: processRunLogicalDir + "/" + ProcessEventSchemaFile, Bytes: eventSchema},
	})
	if got != ProcessRunEventSchemaSHA256 {
		return nil, fmt.Errorf("process event schema hash mismatch: got %s, want %s", got, ProcessRunEventSchemaSHA256)
	}
	return eventSchema, nil
}

// ValidateProcessEventStreamBytes validates every non-blank NDJSON line.
// Returns one ValidationResult per non-blank line (in order). On the first invalid line
// (parse failure or schema Valid=false), returns the results collected so far and a non-nil error.
func ValidateProcessEventStreamBytes(eventSchema []byte, stream []byte, streamName string) ([]*ValidationResult, error) {
	if len(eventSchema) == 0 {
		return nil, errors.New("process event schema is required")
	}
	lines := strings.Split(string(stream), "\n")
	var results []*ValidationResult
	lineNo := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lineNo++
		name := fmt.Sprintf("%s:%d", streamName, lineNo)
		result, err := validateAgainstSchema(eventSchema, ProcessEventSchemaFile, []byte(line), name)
		if err != nil {
			return results, fmt.Errorf("%s: %w", name, err)
		}
		results = append(results, result)
		if !result.Valid {
			return results, fmt.Errorf("%s: event failed process-event schema validation", name)
		}
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("%s: no non-blank event lines", streamName)
	}
	return results, nil
}

// ReadContainedFile reads a relative path that must stay inside the contract base (symlink-safe).
func ReadContainedFile(baseDir, relativePath string) ([]byte, error) {
	rel, err := cleanRelativeEntry(relativePath)
	if err != nil {
		return nil, err
	}
	path, err := resolveContainedEntryPath(baseDir, rel)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path) // #nosec G304 - path constrained to contract base
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", rel, err)
	}
	return data, nil
}

func validateAgainstSchema(schemaBytes []byte, schemaName string, data []byte, fileName string) (*ValidationResult, error) {
	var document interface{}
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse JSON: %w", err)
	}
	var schemaDoc interface{}
	if err := json.Unmarshal(schemaBytes, &schemaDoc); err != nil {
		return nil, fmt.Errorf("parse schema JSON: %w", err)
	}
	normalized, err := json.Marshal(schemaDoc)
	if err != nil {
		return nil, fmt.Errorf("normalize schema: %w", err)
	}
	schemaResult, err := schema.ValidateFromBytes(normalized, document)
	if err != nil {
		return nil, fmt.Errorf("validate against schema: %w", err)
	}

	result := &ValidationResult{
		Valid:  schemaResult.Valid,
		File:   fileName,
		Schema: schemaName,
	}
	for _, validationErr := range schemaResult.Errors {
		result.Errors = append(result.Errors, ValidationError{
			Path:    validationErr.Path,
			Message: validationErr.Message,
		})
	}
	return result, nil
}

func logicalDirForCapability(capability string) (string, error) {
	switch capability {
	case DataArtifactCapability:
		return dataArtifactLogicalDir, nil
	case ProcessRunCapability:
		return processRunLogicalDir, nil
	default:
		return "", fmt.Errorf("unsupported contract capability %q", capability)
	}
}

func cleanRelativeEntry(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return "", errors.New("entry_schema is required")
	}
	if filepath.IsAbs(entry) || strings.HasPrefix(entry, "/") {
		return "", errors.New("entry_schema must be relative")
	}
	clean := pathClean(entry)
	if clean == "." || clean == "" || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("entry_schema must stay inside the contract base")
	}
	return clean, nil
}

func resolveContainedEntryPath(absBase, entryRel string) (string, error) {
	realBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", fmt.Errorf("resolve contract base real path: %w", err)
	}
	entryPath := filepath.Join(absBase, filepath.FromSlash(entryRel))
	realEntry, err := filepath.EvalSymlinks(entryPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return entryPath, nil
		}
		return "", fmt.Errorf("resolve entry schema real path: %w", err)
	}
	rel, err := filepath.Rel(realBase, realEntry)
	if err != nil {
		return "", fmt.Errorf("compare entry schema real path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
		return "", errors.New("entry_schema must stay inside the contract base")
	}
	return realEntry, nil
}

func pathClean(p string) string {
	return strings.TrimPrefix(filepath.ToSlash(filepath.Clean(filepath.FromSlash(p))), "./")
}

type bundleFile struct {
	LogicalPath string
	Bytes       []byte
}

func bundleHash(files []bundleFile) string {
	h := sha256.New()
	for _, file := range files {
		h.Write([]byte(file.LogicalPath))
		h.Write([]byte{0})
		h.Write(file.Bytes)
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}
