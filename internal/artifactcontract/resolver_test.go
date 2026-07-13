package artifactcontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// processRunContractBaseEnv selects the host-less process-run contract base under test.
// make process-run-contract-check exports PROCESS_RUN_CONTRACT_BASE so the advertised
// override is the base actually hashed and validated (not only existence-checked).
const processRunContractBaseEnv = "PROCESS_RUN_CONTRACT_BASE"

func processRunContractBase() string {
	if base := strings.TrimSpace(os.Getenv(processRunContractBaseEnv)); base != "" {
		return base
	}
	return filepath.Join("..", "..", "tests", "fixtures", "process-run-contract", "v0")
}

func processRunCardFixturesDir() string {
	return filepath.Join("..", "..", "tests", "fixtures", "process-run-card")
}

func processRunEventFixturesDir() string {
	return filepath.Join("..", "..", "tests", "fixtures", "process-run-events")
}

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

func TestResolveProcessRunContractFixture(t *testing.T) {
	base := processRunContractBase()
	resolved, err := Resolve(base, ProcessRunCapability)
	if err != nil {
		t.Fatalf("Resolve returned error: %v", err)
	}
	if resolved.Capability != ProcessRunCapability {
		t.Fatalf("capability = %q, want %q", resolved.Capability, ProcessRunCapability)
	}
	if resolved.EntrySchema != "process-card.schema.json" {
		t.Fatalf("entry schema = %q", resolved.EntrySchema)
	}
	if resolved.LogicalDir != processRunLogicalDir {
		t.Fatalf("logical dir = %q, want %q", resolved.LogicalDir, processRunLogicalDir)
	}
	if resolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
		t.Fatalf("bundle sha = %q, want %q", resolved.BundleSHA256, ProcessRunBaselineBundleSHA256)
	}
}

func TestRecordedProcessRunBaselineMetadata(t *testing.T) {
	path := filepath.Join("..", "..", "config", "process-run-contract-baseline.json")
	raw, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read process-run baseline config: %v", err)
	}
	var baseline map[string]string
	if err := json.Unmarshal(raw, &baseline); err != nil {
		t.Fatalf("unmarshal process-run baseline config: %v", err)
	}
	expected := map[string]string{
		"capability":             ProcessRunCapability,
		"source":                 ProcessRunBaselineSource,
		"released_tag":           ProcessRunBaselineReleasedTag,
		"resolved_bundle_sha256": ProcessRunBaselineBundleSHA256,
		"event_schema_sha256":    ProcessRunEventSchemaSHA256,
	}
	for key, want := range expected {
		if got := baseline[key]; got != want {
			t.Fatalf("baseline[%s] = %q, want %q", key, got, want)
		}
	}
	// Process-run metadata must stay independently addressable from data-artifact constants.
	if ProcessRunBaselineSource == "" || ProcessRunBaselineReleasedTag == "" {
		t.Fatal("process-run source/tag constants must be set")
	}
}

func TestResolveProcessRunBaselineRejectsWrongBundleHash(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(`{"capability":"contract: process-run/v0","entry_schema":"process-card.schema.json"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "process-card.schema.json"), []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}

	_, err := ResolveProcessRunBaseline(base)
	if err == nil {
		t.Fatal("ResolveProcessRunBaseline succeeded; want baseline mismatch")
	}
	if !strings.Contains(err.Error(), "contract baseline hash mismatch") {
		t.Fatalf("error = %q, want baseline mismatch", err.Error())
	}
}

func TestResolveRejectsUnsupportedCapability(t *testing.T) {
	base := processRunContractBase()
	_, err := Resolve(base, "contract: other/v0")
	if err == nil {
		t.Fatal("Resolve succeeded for unsupported capability")
	}
	if !strings.Contains(err.Error(), "unsupported contract capability") {
		t.Fatalf("error = %q, want unsupported capability", err.Error())
	}
}

func TestDataArtifactAndProcessRunHashesRemainDistinct(t *testing.T) {
	if BaselineBundleSHA256 == ProcessRunBaselineBundleSHA256 {
		t.Fatal("data-artifact and process-run baseline hashes must be distinct")
	}
	dataBase := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	processBase := processRunContractBase()
	dataResolved, err := ResolveBaseline(dataBase)
	if err != nil {
		t.Fatalf("data-artifact ResolveBaseline: %v", err)
	}
	processResolved, err := ResolveProcessRunBaseline(processBase)
	if err != nil {
		t.Fatalf("process-run ResolveProcessRunBaseline: %v", err)
	}
	if dataResolved.BundleSHA256 != BaselineBundleSHA256 {
		t.Fatalf("data-artifact pin drifted: %s", dataResolved.BundleSHA256)
	}
	if processResolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
		t.Fatalf("process-run pin drifted: %s", processResolved.BundleSHA256)
	}
}

func TestValidateProcessCardFixtures(t *testing.T) {
	base := processRunContractBase()
	cards, err := filepath.Glob(filepath.Join(processRunCardFixturesDir(), "*.json"))
	if err != nil {
		t.Fatalf("glob process-run cards: %v", err)
	}
	if len(cards) < 2 {
		t.Fatalf("expected ≥2 process-run card fixtures, found %d", len(cards))
	}
	for _, card := range cards {
		result, resolved, err := ValidateProcessCardFile(base, card)
		if err != nil {
			t.Fatalf("ValidateProcessCardFile(%s): %v", card, err)
		}
		if resolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
			t.Fatalf("bundle sha = %q", resolved.BundleSHA256)
		}
		if result == nil || !result.Valid {
			t.Fatalf("card %s did not validate: %#v", card, result)
		}
	}
}

func TestValidateProcessCardRejectsMissingCapability(t *testing.T) {
	base := processRunContractBase()
	resolved, err := ResolveProcessRunBaseline(base)
	if err != nil {
		t.Fatalf("ResolveProcessRunBaseline: %v", err)
	}
	cardPath := filepath.Join(processRunCardFixturesDir(), "telemetry-only.card.json")
	raw, err := os.ReadFile(cardPath) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read card: %v", err)
	}
	var card map[string]interface{}
	if err := json.Unmarshal(raw, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}
	card["capabilities"] = []interface{}{"contract: other/v0"}
	mutated, err := json.Marshal(card)
	if err != nil {
		t.Fatalf("marshal mutated card: %v", err)
	}
	result, err := ValidateProcessCardBytes(resolved, mutated, "mutated.json")
	if err != nil {
		t.Fatalf("ValidateProcessCardBytes: %v", err)
	}
	if result.Valid {
		t.Fatal("card without process-run capability validated")
	}
}

func TestValidateProcessEventStreamFixtures(t *testing.T) {
	base := processRunContractBase()
	streams, err := filepath.Glob(filepath.Join(processRunEventFixturesDir(), "*.ndjson"))
	if err != nil {
		t.Fatalf("glob process-run event streams: %v", err)
	}
	if len(streams) < 2 {
		t.Fatalf("expected ≥2 process-run event fixtures, found %d", len(streams))
	}
	for _, stream := range streams {
		results, resolved, err := ValidateProcessEventStreamFile(base, stream)
		if err != nil {
			t.Fatalf("ValidateProcessEventStreamFile(%s): %v", stream, err)
		}
		if resolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
			t.Fatalf("bundle sha = %q", resolved.BundleSHA256)
		}
		if len(results) == 0 {
			t.Fatalf("stream %s produced no line results", stream)
		}
		for _, result := range results {
			if result == nil || !result.Valid {
				t.Fatalf("stream %s line failed: %#v", stream, result)
			}
		}
	}
}

func TestValidateProcessEventStreamRejectsInvalidLine(t *testing.T) {
	base := processRunContractBase()
	resolved, err := ResolveProcessRunBaseline(base)
	if err != nil {
		t.Fatalf("ResolveProcessRunBaseline: %v", err)
	}
	eventSchema, err := LoadPinnedProcessEventSchema(resolved)
	if err != nil {
		t.Fatalf("LoadPinnedProcessEventSchema: %v", err)
	}
	// Missing required envelope fields.
	stream := []byte(`{"event":"started","seq":0}` + "\n")
	results, err := ValidateProcessEventStreamBytes(eventSchema, stream, "bad.ndjson")
	if err == nil {
		t.Fatal("invalid event stream validated")
	}
	if len(results) != 1 || results[0] == nil || results[0].Valid {
		t.Fatalf("want one invalid result with Valid=false, got %#v", results)
	}
}

func TestLoadPinnedProcessEventSchemaRejectsMutatedSibling(t *testing.T) {
	// Mutate only process-event.schema.json while keeping the L2 entry pin (manifest+card) intact.
	src := processRunContractBase()
	base := t.TempDir()
	for _, name := range []string{"contract.json", "process-card.schema.json", "process-event.schema.json"} {
		raw, err := os.ReadFile(filepath.Join(src, name)) // #nosec G304 - test fixture path
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(base, name), raw, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// Permissive replacement would accept any event object if pin were not checked.
	if err := os.WriteFile(filepath.Join(base, ProcessEventSchemaFile), []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("mutate event schema: %v", err)
	}

	resolved, err := ResolveProcessRunBaseline(base)
	if err != nil {
		t.Fatalf("ResolveProcessRunBaseline should still pass with pinned entry bundle: %v", err)
	}
	if resolved.BundleSHA256 != ProcessRunBaselineBundleSHA256 {
		t.Fatalf("entry bundle sha = %q", resolved.BundleSHA256)
	}
	if _, err := LoadPinnedProcessEventSchema(resolved); err == nil {
		t.Fatal("LoadPinnedProcessEventSchema succeeded with mutated event schema")
	} else if !strings.Contains(err.Error(), "process event schema hash mismatch") {
		t.Fatalf("error = %q, want event schema hash mismatch", err.Error())
	}

	// Stream validation must fail closed via the pin, not accept invalid lines under a permissive schema.
	streamPath := filepath.Join(processRunEventFixturesDir(), "telemetry-lifecycle.ndjson")
	if _, _, err := ValidateProcessEventStreamFile(base, streamPath); err == nil {
		t.Fatal("ValidateProcessEventStreamFile succeeded with mutated event schema")
	} else if !strings.Contains(err.Error(), "process event schema hash mismatch") {
		t.Fatalf("error = %q, want event schema hash mismatch", err.Error())
	}
}

func TestProcessRunContractBaseEnvSelectsTrustRoot(t *testing.T) {
	// Complete-but-wrong base: valid layout, wrong entry-schema bytes → pin mismatch.
	src := processRunContractBase()
	wrong := t.TempDir()
	for _, name := range []string{"contract.json", "process-card.schema.json", "process-event.schema.json"} {
		raw, err := os.ReadFile(filepath.Join(src, name)) // #nosec G304 - test fixture path
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(wrong, name), raw, 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(wrong, "process-card.schema.json"), []byte(`{"type":"object","title":"tampered"}`), 0o600); err != nil {
		t.Fatalf("tamper card schema: %v", err)
	}

	t.Setenv(processRunContractBaseEnv, wrong)
	if got := processRunContractBase(); got != wrong {
		t.Fatalf("processRunContractBase() = %q, want env override %q", got, wrong)
	}

	// Selected override must be the trust root: pin checks fail closed on the wrong base.
	if _, err := ResolveProcessRunBaseline(processRunContractBase()); err == nil {
		t.Fatal("ResolveProcessRunBaseline accepted complete-but-wrong PROCESS_RUN_CONTRACT_BASE")
	} else if !strings.Contains(err.Error(), "contract baseline hash mismatch") {
		t.Fatalf("error = %q, want baseline hash mismatch", err.Error())
	}

	card := filepath.Join(processRunCardFixturesDir(), "telemetry-only.card.json")
	if _, _, err := ValidateProcessCardFile(processRunContractBase(), card); err == nil {
		t.Fatal("ValidateProcessCardFile accepted complete-but-wrong PROCESS_RUN_CONTRACT_BASE")
	}
	stream := filepath.Join(processRunEventFixturesDir(), "telemetry-lifecycle.ndjson")
	if _, _, err := ValidateProcessEventStreamFile(processRunContractBase(), stream); err == nil {
		t.Fatal("ValidateProcessEventStreamFile accepted complete-but-wrong PROCESS_RUN_CONTRACT_BASE")
	}
}

func TestResolveContractPrimitivePreservesDataArtifactHash(t *testing.T) {
	base := filepath.Join("..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	viaResolve, err := Resolve(base, DataArtifactCapability)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	viaPrimitive, err := ResolveContract(base, DataArtifactCapability, dataArtifactLogicalDir)
	if err != nil {
		t.Fatalf("ResolveContract: %v", err)
	}
	if viaResolve.BundleSHA256 != viaPrimitive.BundleSHA256 {
		t.Fatalf("hash mismatch Resolve=%s ResolveContract=%s", viaResolve.BundleSHA256, viaPrimitive.BundleSHA256)
	}
	if viaResolve.BundleSHA256 != BaselineBundleSHA256 {
		t.Fatalf("data-artifact hash changed: %s", viaResolve.BundleSHA256)
	}
}
