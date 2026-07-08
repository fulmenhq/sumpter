package artifactcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDataArtifactContractFixture(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	resolved, err := Resolve(base, DataArtifactCapability)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Capability != DataArtifactCapability {
		t.Fatalf("capability = %q, want %q", resolved.Capability, DataArtifactCapability)
	}
	if resolved.EntrySchema != "artifact-descriptor.schema.json" {
		t.Fatalf("entry schema = %q", resolved.EntrySchema)
	}
	if resolved.BundleSHA256 != BaselineBundleSHA256 {
		t.Fatalf("bundle sha = %q, want %q", resolved.BundleSHA256, BaselineBundleSHA256)
	}
}

func TestRecordedBaselineMetadata(t *testing.T) {
	path := filepath.Join("..", "..", "config", "data-artifact-contract-baseline.json")
	raw, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read baseline config: %v", err)
	}
	var baseline map[string]string
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("unmarshal baseline config: %v", err)
	}
	expected := map[string]string{
		"capability":             DataArtifactCapability,
		"source":                 BaselineSource,
		"released_tag":           BaselineReleasedTag,
		"resolved_bundle_sha256": BaselineBundleSHA256,
	}
	for key, want := range expected {
		if got := baseline[key]; got != want {
			t.Fatalf("baseline[%s] = %q, want %q", key, got, want)
		}
	}
}

func TestResolveFailsClosed(t *testing.T) {
	tests := []struct {
		name     string
		manifest string
		wantErr  string
	}{
		{
			name:     "capability mismatch",
			manifest: `{"capability":"contract: other/v0","entry_schema":"artifact-descriptor.schema.json"}`,
			wantErr:  "capability mismatch",
		},
		{
			name:     "missing entry schema",
			manifest: `{"capability":"contract: data-artifact/v0","entry_schema":"missing.schema.json"}`,
			wantErr:  "read entry schema",
		},
		{
			name:     "entry escapes base",
			manifest: `{"capability":"contract: data-artifact/v0","entry_schema":"../artifact-descriptor.schema.json"}`,
			wantErr:  "must stay inside",
		},
		{
			name:     "absolute entry",
			manifest: `{"capability":"contract: data-artifact/v0","entry_schema":"/tmp/artifact-descriptor.schema.json"}`,
			wantErr:  "must be relative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := t.TempDir()
			if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(tt.manifest), 0o600); err != nil {
				t.Fatalf("write manifest: %v", err)
			}
			if err := os.WriteFile(filepath.Join(base, "artifact-descriptor.schema.json"), []byte(`{"type":"object"}`), 0o600); err != nil {
				t.Fatalf("write schema: %v", err)
			}
			_, err := Resolve(base, DataArtifactCapability)
			if err == nil {
				t.Fatal("Resolve succeeded; want fail-closed error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestResolveRejectsSymlinkEscapedEntrySchema(t *testing.T) {
	base := t.TempDir()
	outside := t.TempDir()
	outsideSchema := filepath.Join(outside, "artifact-descriptor.schema.json")
	if err := os.WriteFile(outsideSchema, []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("write outside schema: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(`{"capability":"contract: data-artifact/v0","entry_schema":"linked.schema.json"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.Symlink(outsideSchema, filepath.Join(base, "linked.schema.json")); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	_, err := Resolve(base, DataArtifactCapability)
	if err == nil {
		t.Fatal("Resolve succeeded; want symlink escape rejection")
	}
	if !strings.Contains(err.Error(), "must stay inside") {
		t.Fatalf("error = %q, want containment error", err.Error())
	}
}

func TestResolveBaselineRejectsWrongBundleHash(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(`{"capability":"contract: data-artifact/v0","entry_schema":"artifact-descriptor.schema.json"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "artifact-descriptor.schema.json"), []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	_, err := ResolveBaseline(base)
	if err == nil {
		t.Fatal("ResolveBaseline succeeded; want baseline mismatch")
	}
	if !strings.Contains(err.Error(), "contract baseline hash mismatch") {
		t.Fatalf("error = %q, want baseline mismatch", err.Error())
	}
}

func TestValidateDescriptorFile(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	descriptor := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-descriptor", "record-stream.descriptor.json")

	result, resolved, err := ValidateDescriptorFile(base, descriptor)
	if err != nil {
		t.Fatalf("ValidateDescriptorFile returned error: %v", err)
	}
	if resolved.BundleSHA256 != BaselineBundleSHA256 {
		t.Fatalf("bundle sha = %q, want %q", resolved.BundleSHA256, BaselineBundleSHA256)
	}
	if result == nil || !result.Valid {
		t.Fatalf("descriptor did not validate: %#v", result)
	}
}

func TestValidateDescriptorFileRejectsWrongBundleHash(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(`{"capability":"contract: data-artifact/v0","entry_schema":"artifact-descriptor.schema.json"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "artifact-descriptor.schema.json"), []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	descriptor := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-descriptor", "record-stream.descriptor.json")

	result, resolved, err := ValidateDescriptorFile(base, descriptor)
	if err == nil {
		t.Fatal("ValidateDescriptorFile succeeded; want baseline mismatch")
	}
	if result != nil || resolved != nil {
		t.Fatalf("result/resolved = %#v/%#v, want nils on baseline mismatch", result, resolved)
	}
	if !strings.Contains(err.Error(), "contract baseline hash mismatch") {
		t.Fatalf("error = %q, want baseline mismatch", err.Error())
	}
}

func TestValidateDescriptorRejectsMissingCapability(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	resolved, err := Resolve(base, DataArtifactCapability)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}

	descriptorPath := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-descriptor", "record-stream.descriptor.json")
	raw, err := os.ReadFile(descriptorPath) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read descriptor fixture: %v", err)
	}
	var descriptor map[string]interface{}
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		t.Fatalf("unmarshal descriptor: %v", err)
	}
	descriptor["capabilities"] = []interface{}{"contract: other/v0"}
	mutated, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatalf("marshal mutated descriptor: %v", err)
	}

	result, err := ValidateDescriptorBytes(resolved, mutated, "mutated.json")
	if err != nil {
		t.Fatalf("ValidateDescriptorBytes returned error: %v", err)
	}
	if result.Valid {
		t.Fatal("descriptor with missing data-artifact capability validated")
	}
}

func TestValidateFieldCatalogBytes(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	resolved, err := ResolveBaseline(base)
	if err != nil {
		t.Fatalf("ResolveBaseline returned error: %v", err)
	}

	valid := []byte(`{
	  "id": "fields/records.fields.json",
	  "grain": "records",
	  "fields": [
	    {
	      "name": "derived_total",
	      "type": "integer",
	      "sensitivity": "unknown",
	      "export_action": "block_export"
	    }
	  ],
	  "withheld_field_count": 1
	}`)
	result, err := ValidateFieldCatalogBytes(resolved, valid, "fields/records.fields.json")
	if err != nil {
		t.Fatalf("ValidateFieldCatalogBytes returned error: %v", err)
	}
	if result == nil || !result.Valid {
		t.Fatalf("field catalog did not validate: %#v", result)
	}
}

func TestValidateFieldCatalogBytesAllowsAllWithheldEmptyFields(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	resolved, err := ResolveBaseline(base)
	if err != nil {
		t.Fatalf("ResolveBaseline returned error: %v", err)
	}

	valid := []byte(`{
	  "id": "fields/records.fields.json",
	  "grain": "records",
	  "fields": [],
	  "withheld_field_count": 1
	}`)
	result, err := ValidateFieldCatalogBytes(resolved, valid, "fields/records.fields.json")
	if err != nil {
		t.Fatalf("ValidateFieldCatalogBytes returned error: %v", err)
	}
	if result == nil || !result.Valid {
		t.Fatalf("fully withheld field catalog did not validate: %#v", result)
	}
}

func TestValidateFieldCatalogBytesRejectsEmptyFieldsWithoutWithheldCount(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	resolved, err := ResolveBaseline(base)
	if err != nil {
		t.Fatalf("ResolveBaseline returned error: %v", err)
	}

	invalid := []byte(`{
	  "id": "fields/records.fields.json",
	  "grain": "records",
	  "fields": [],
	  "withheld_field_count": 0
	}`)
	result, err := ValidateFieldCatalogBytes(resolved, invalid, "fields/records.fields.json")
	if err != nil {
		t.Fatalf("ValidateFieldCatalogBytes returned error: %v", err)
	}
	if result == nil || result.Valid {
		t.Fatalf("empty field catalog with zero withheld count validated: %#v", result)
	}
}
