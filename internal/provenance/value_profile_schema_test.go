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

func TestValueProfileSchemaForbidsCappedEnumeration(t *testing.T) {
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
      "id": {
        "tier": "enumeration",
        "status": "high_cardinality_capped",
        "count": 20,
        "null_count": 0,
        "distinct_count": 10,
        "distinct": {"a": 1}
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("enumeration + high_cardinality_capped must fail schema validation")
	}
}

func TestValueProfileSchemaForbidsCappedStatusWithExactInteger(t *testing.T) {
	// status high_cardinality_capped must not allow a precise distinct_count integer.
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
    "max_distinct": 100,
    "small_cell_threshold": 5,
    "fields": {
      "id": {
        "tier": "aggregates",
        "status": "high_cardinality_capped",
        "count": 200,
        "null_count": 0,
        "distinct_count": 1234567
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("capped status with exact integer distinct_count must fail")
	}
}

func TestValueProfileSchemaForbidsCompleteStatusWithGteForm(t *testing.T) {
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
    "max_distinct": 100,
    "small_cell_threshold": 5,
    "fields": {
      "id": {
        "tier": "aggregates",
        "status": "complete",
        "count": 200,
        "null_count": 0,
        "distinct_count": ">=100"
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("complete status with >=N distinct_count must fail")
	}
}

func TestValueProfileSchemaForbidsCappedStatusWithLtForm(t *testing.T) {
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
    "max_distinct": 100,
    "small_cell_threshold": 5,
    "fields": {
      "id": {
        "tier": "aggregates",
        "status": "high_cardinality_capped",
        "count": 3,
        "null_count": 0,
        "distinct_count": "<5"
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("capped status with <N distinct_count must fail")
	}
}

func TestValueProfileSchemaForbidsZeroFrequencyDistinctKey(t *testing.T) {
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
        "count": 1,
        "null_count": 0,
        "distinct_count": 1,
        "distinct": {"ghost": 0}
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("zero-frequency concrete distinct key must fail")
	}
}

func TestValueProfileSchemaAllowsCappedGteForm(t *testing.T) {
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
    "max_distinct": 100,
    "small_cell_threshold": 5,
    "fields": {
      "id": {
        "tier": "aggregates",
        "status": "high_cardinality_capped",
        "count": 200,
        "null_count": 0,
        "distinct_count": ">=100",
        "shape": "freeform"
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if !result.Valid {
		t.Fatalf("valid capped >=N form rejected: %+v", result.Errors)
	}
}

func TestValueProfileSchemaForbidsOverHardCapMaxDistinct(t *testing.T) {
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
    "max_distinct": 10001,
    "small_cell_threshold": 5,
    "fields": {
      "status": {
        "tier": "aggregates",
        "status": "complete",
        "count": 0,
        "null_count": 0,
        "distinct_count": 0
      }
    }
  }
}`)
	result, err := validator.ValidateProvenanceManifest(raw, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.Valid {
		t.Fatal("max_distinct above 10000 must fail schema validation")
	}
}
