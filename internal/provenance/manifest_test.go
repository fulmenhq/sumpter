package provenance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/validation"
)

func TestManifestSchemaValidatesDirectAndRecipeBacked(t *testing.T) {
	direct := testManifest(t)
	assertValidManifest(t, direct)

	recipe := testManifest(t)
	recipe.Recipe = &Recipe{
		ID:                    "sample_extract",
		ManifestSchemaVersion: "recipe/v0.1.0",
		ContentVersion:        "1.0.0",
		ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Owners:                []Owner{{Name: "Fulmen Sumpter contributors"}},
		ManifestYAML:          "version: recipe/v0.1.0\n",
		SignatureYAML:         "signature_id: sample\n",
		ExtractYAML:           "record_type: sample\n",
		ApplicabilityYAML:     "applicability:\n  type: xpath\n  expression: count(//item) > 0\n",
		FieldProvenance: []FieldProvenance{{
			OutputField: "business_date",
			XPath:       "BusinessDate",
			Type:        "string",
			Description: "Business date from the source record",
			Transform:   "trim",
		}},
	}
	assertValidManifest(t, recipe)
}

func TestManifestSchemaRejectsExtraFields(t *testing.T) {
	manifest := testManifest(t)
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	raw["unexpected"] = true
	data, err = json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal mutated manifest: %v", err)
	}

	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.IsValid() {
		t.Fatal("manifest with extra top-level field unexpectedly validated")
	}
}

func TestManifestSchemaAllowsRecipeCadence(t *testing.T) {
	manifest := testManifest(t)
	manifest.Recipe = &Recipe{
		ID:                    "sample_extract",
		ManifestSchemaVersion: "recipe/v0.1.0",
		ContentVersion:        "1.0.0",
		ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Cadence:               "daily-rolling",
		SignatureYAML:         "signature_id: sample\n",
		ExtractYAML:           "record_type: sample\n",
	}
	assertValidManifest(t, manifest)
}

func TestManifestSchemaAllowsRecipeWithoutCadence(t *testing.T) {
	manifest := testManifest(t)
	manifest.Recipe = &Recipe{
		ID:                    "sample_extract",
		ManifestSchemaVersion: "recipe/v0.1.0",
		ContentVersion:        "1.0.0",
		ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SignatureYAML:         "signature_id: sample\n",
		ExtractYAML:           "record_type: sample\n",
	}
	assertValidManifest(t, manifest)
}

func TestManifestSchemaRejectsInvalidRecipeCadence(t *testing.T) {
	tests := []string{
		"Daily-Rolling",
		"daily rolling",
		"daily!",
		"daily-",
		"daily--rolling",
	}

	for _, cadence := range tests {
		t.Run(cadence, func(t *testing.T) {
			manifest := testManifest(t)
			manifest.Recipe = &Recipe{
				ID:                    "sample_extract",
				ManifestSchemaVersion: "recipe/v0.1.0",
				ContentVersion:        "1.0.0",
				ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				Cadence:               cadence,
				SignatureYAML:         "signature_id: sample\n",
				ExtractYAML:           "record_type: sample\n",
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}

			validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
			result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
			if err != nil {
				t.Fatalf("ValidateProvenanceManifest: %v", err)
			}
			if result.IsValid() {
				t.Fatal("manifest with invalid cadence unexpectedly validated")
			}
		})
	}
}

func TestManifestSchemaAllowsParquetWithholdColumns(t *testing.T) {
	manifest := testManifest(t)
	manifest.Outputs = []Output{{
		Path:            "records.parquet",
		Format:          "parquet",
		RecordCount:     2,
		WithholdColumns: []string{"year", "month", "site"},
	}}
	assertValidManifest(t, manifest)
}

func TestManifestSchemaRejectsInvalidWithholdColumn(t *testing.T) {
	manifest := testManifest(t)
	manifest.Outputs = []Output{{
		Path:            "records.parquet",
		Format:          "parquet",
		RecordCount:     2,
		WithholdColumns: []string{"bad-name"},
	}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.IsValid() {
		t.Fatal("manifest with invalid withhold column unexpectedly validated")
	}
}

func TestWriteManifestPreservesRawYAML(t *testing.T) {
	dir := t.TempDir()
	manifest := testManifest(t)
	manifest.Recipe = &Recipe{
		ID:                    "sample_extract",
		ManifestSchemaVersion: "recipe/v0.1.0",
		ContentVersion:        "1.0.0",
		ContentHash:           "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SignatureYAML:         "signature_id: sample\n# keep this comment\n",
		ExtractYAML:           "record_type: sample\nxpath: ./A\n",
		ApplicabilityYAML:     "applicability:\n  type: xpath\n  expression: count(//A) > 0\n",
	}

	path := filepath.Join(dir, ManifestFileName)
	if err := WriteManifest(path, manifest); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var got Manifest
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal written manifest: %v", err)
	}
	if got.Recipe == nil {
		t.Fatal("recipe manifest missing")
	}
	if got.Recipe.SignatureYAML != manifest.Recipe.SignatureYAML {
		t.Fatalf("signature yaml = %q, want %q", got.Recipe.SignatureYAML, manifest.Recipe.SignatureYAML)
	}
	if got.Recipe.ExtractYAML != manifest.Recipe.ExtractYAML {
		t.Fatalf("extract yaml = %q, want %q", got.Recipe.ExtractYAML, manifest.Recipe.ExtractYAML)
	}
	if got.Recipe.ApplicabilityYAML != manifest.Recipe.ApplicabilityYAML {
		t.Fatalf("applicability yaml = %q, want %q", got.Recipe.ApplicabilityYAML, manifest.Recipe.ApplicabilityYAML)
	}
}

func TestSanitizePathAndArgv(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inputs", "source.xml")
	gotPath := SanitizePath(inside, root)
	if gotPath != "inputs/source.xml" {
		t.Fatalf("SanitizePath inside root = %q, want inputs/source.xml", gotPath)
	}

	outside := filepath.Join(os.TempDir(), "sumpter-secret", "source.xml")
	if got := SanitizePath(outside, root); filepath.IsAbs(got) || strings.Contains(got, os.TempDir()) {
		t.Fatalf("SanitizePath outside root leaked host path: %q", got)
	}

	args := SanitizeArgv([]string{
		"extract",
		"files",
		"--input-path=" + inside,
		"--api-token=secret",
		"--password",
		"also-secret",
	}, root)
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "secret") || strings.Contains(joined, root) {
		t.Fatalf("SanitizeArgv leaked secret or root: %q", joined)
	}
	if !strings.Contains(joined, "inputs/source.xml") {
		t.Fatalf("SanitizeArgv did not preserve relative path: %q", joined)
	}
}

func TestBuildInputLedger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input.xml")
	if err := os.WriteFile(path, []byte("<root/>\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	input, err := BuildInputLedger(path, dir)
	if err != nil {
		t.Fatalf("BuildInputLedger: %v", err)
	}
	if input.Path != "input.xml" {
		t.Fatalf("path = %q, want input.xml", input.Path)
	}
	if input.SizeBytes != 8 {
		t.Fatalf("size = %d, want 8", input.SizeBytes)
	}
	if !strings.HasPrefix(input.SHA256, "sha256:") || len(input.SHA256) != len("sha256:")+64 {
		t.Fatalf("sha256 format = %q", input.SHA256)
	}
}

func testManifest(t *testing.T) Manifest {
	t.Helper()
	return Manifest{
		SchemaVersion:  ManifestSchemaVersion,
		RunID:          "0190a3f4-1c2d-7abc-9def-0123456789ab",
		SumpterVersion: "0.1.3",
		StartedAt:      time.Date(2026, 5, 12, 20, 0, 0, 0, time.UTC),
		CompletedAt:    time.Date(2026, 5, 12, 20, 1, 0, 0, time.UTC),
		CLI: CLI{
			Command:       "sumpter extract files",
			ArgvSanitized: []string{"extract", "files", "--output-path=outputs"},
		},
		Inputs: []Input{{
			Path:      "input.xml",
			SHA256:    "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			SizeBytes: 42,
		}},
		Outputs: []Output{{
			Path:        "records.json",
			Format:      "json",
			RecordCount: 2,
		}},
		CountsByRecordType: map[string]int{"sample": 2},
	}
}

func assertValidManifest(t *testing.T, manifest Manifest) {
	t.Helper()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if !result.IsValid() {
		t.Fatalf("manifest did not validate: %s", result.ErrorSummary())
	}
}
