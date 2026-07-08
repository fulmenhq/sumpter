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
	BaselineSource         = "3leaps/crucible"
	BaselineReleasedTag    = "v0.1.18"
	BaselineBundleSHA256   = "sha256:ef6de3f16bd988555a4e063d32e4f15478b56bf6f45121e890d89504ee468a01"
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

func Resolve(baseDir, capability string) (*ResolvedContract, error) {
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
		{LogicalPath: "schemas/data-artifact/v0/contract.json", Bytes: manifestBytes},
		{LogicalPath: "schemas/data-artifact/v0/" + entryRel, Bytes: entryBytes},
	})

	return &ResolvedContract{
		BaseDir:              absBase,
		Capability:           capability,
		EntrySchema:          entryRel,
		EntrySchemaPath:      entryPath,
		EntrySchemaBytes:     entryBytes,
		BundleSHA256:         bundleSHA,
		ContractManifest:     manifest,
		ContractManifestPath: manifestPath,
	}, nil
}

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
	var descriptor interface{}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, fmt.Errorf("parse descriptor JSON: %w", err)
	}
	var schemaDoc interface{}
	if err := json.Unmarshal(resolved.EntrySchemaBytes, &schemaDoc); err != nil {
		return nil, fmt.Errorf("parse entry schema JSON: %w", err)
	}
	schemaBytes, err := json.Marshal(schemaDoc)
	if err != nil {
		return nil, fmt.Errorf("normalize entry schema: %w", err)
	}
	schemaResult, err := schema.ValidateFromBytes(schemaBytes, descriptor)
	if err != nil {
		return nil, fmt.Errorf("validate descriptor: %w", err)
	}

	result := &ValidationResult{
		Valid:  schemaResult.Valid,
		File:   descriptorName,
		Schema: resolved.EntrySchema,
	}
	for _, validationErr := range schemaResult.Errors {
		result.Errors = append(result.Errors, ValidationError{
			Path:    validationErr.Path,
			Message: validationErr.Message,
		})
	}
	return result, nil
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
