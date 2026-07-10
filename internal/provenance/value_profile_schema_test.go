package provenance

import (
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/validation"
)

func TestValueProfileSchemaForbidsDistinctOnAggregates(t *testing.T) {
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	// Minimal valid skeleton with an illegal aggregates+distinct field.
	raw := []byte(`{
  "schema_version": "sumpter.provenance/v1",
  "run_id": "00000000-0000-7000-8000-000000000001",
  "sumpter_version": "0.3.0-dev",
  "started_at": "2026-07-10T00:00:00Z",
  "completed_at": "2026-07-10T00:00:01Z",
  "cli": {"command": "sumpter extract files", "argv_sanitized": ["extract","files"]},
  "inputs": [],
  "outputs": [],
  "counts_by_record_type": {},
  "value_profile": {
    "version": "sumpter.value-profile/v0",
    "max_distinct": 10,
    "small_cell_threshold": 5,
    "fields": {
      "secret": {
        "tier": "aggregates",
        "status": "complete",
        "count": 1,
        "null_count": 0,
        "distinct_count": 1,
        "distinct": {"leaked": 1}
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("aggregates tier with distinct must fail schema validation")
	}
}

func TestValueProfileSchemaForbidsMinMaxOnAggregates(t *testing.T) {
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	raw := []byte(`{
  "schema_version": "sumpter.provenance/v1",
  "run_id": "00000000-0000-7000-8000-000000000001",
  "sumpter_version": "0.3.0-dev",
  "started_at": "2026-07-10T00:00:00Z",
  "completed_at": "2026-07-10T00:00:01Z",
  "cli": {"command": "sumpter extract files", "argv_sanitized": ["extract","files"]},
  "inputs": [],
  "outputs": [],
  "counts_by_record_type": {},
  "value_profile": {
    "version": "sumpter.value-profile/v0",
    "max_distinct": 10,
    "small_cell_threshold": 5,
    "fields": {
      "balance": {
        "tier": "aggregates",
        "status": "complete",
        "count": 3,
        "null_count": 0,
        "distinct_count": 3,
        "min": 1,
        "max": 99999
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("aggregates tier with min/max must fail schema validation")
	}
}

func TestValueProfileSchemaAllowsGuardedEnumeration(t *testing.T) {
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	raw := []byte(`{
  "schema_version": "sumpter.provenance/v1",
  "run_id": "00000000-0000-7000-8000-000000000001",
  "sumpter_version": "0.3.0-dev",
  "started_at": "2026-07-10T00:00:00Z",
  "completed_at": "2026-07-10T00:00:01Z",
  "cli": {"command": "sumpter extract files", "argv_sanitized": ["extract","files"]},
  "inputs": [],
  "outputs": [],
  "counts_by_record_type": {},
  "value_profile": {
    "version": "sumpter.value-profile/v0",
    "max_distinct": 10,
    "small_cell_threshold": 5,
    "fields": {
      "status": {
        "tier": "enumeration",
        "status": "complete",
        "count": 2,
        "null_count": 0,
        "distinct_count": 2,
        "distinct": {"a": 1, "b": 1}
      },
      "id": {
        "tier": "aggregates",
        "status": "complete",
        "count": 2,
        "null_count": 0,
        "distinct_count": 2,
        "shape": "opaque_string"
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if !result.Valid {
		t.Fatalf("valid guarded profile rejected: %+v", result.Errors)
	}
}
