package dataartifact

import (
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

func TestBuildRecordStreamDescriptorUsesRecordCountAsGrainRows(t *testing.T) {
	descriptor, err := BuildRecordStreamDescriptor(provenance.Manifest{
		RunID:          "0190a3f4-1c2d-7abc-9def-0123456789ab",
		SumpterVersion: "0.3.0-dev",
		CountsByRecordType: map[string]int{
			"sample_record": 2,
		},
		Outputs: []provenance.Output{
			{Path: "b-records.jsonl", Format: "json", RecordCount: 2},
			{Path: "a-records.parquet", Format: "parquet", RecordCount: 2},
		},
	}, "0190a3f4-1c2d-7abc-9def-111111111111")
	if err != nil {
		t.Fatalf("BuildRecordStreamDescriptor: %v", err)
	}
	if got := descriptor.Grains[0].RowCount; got != 2 {
		t.Fatalf("grain row_count = %d, want 2", got)
	}
	if len(descriptor.Reps) != 2 {
		t.Fatalf("representations len = %d, want 2", len(descriptor.Reps))
	}
	if got := descriptor.Reps[0].Format; got != "parquet" {
		t.Fatalf("first representation format = %q, want parquet after path sort", got)
	}
	if got := descriptor.Reps[1].Format; got != "ndjson" {
		t.Fatalf("second representation format = %q, want ndjson", got)
	}
	if got := descriptor.Reps[0].ProtectionEnforceableGranularity; got != "artifact" {
		t.Fatalf("parquet protection floor = %q, want artifact", got)
	}
}

func TestBuildRecordFieldCatalogWithholdsSourceStructureKeys(t *testing.T) {
	catalog := BuildRecordFieldCatalog([]provenance.FieldProvenance{
		{
			OutputField: "source_label",
			XPath:       "Label",
			Type:        "string",
			Description: "Label from the source record",
		},
		{
			OutputField: "derived_total",
			Expression:  "a + b",
			Type:        "integer",
		},
	})

	if catalog.ID != FieldCatalogRef {
		t.Fatalf("catalog id = %q, want %q", catalog.ID, FieldCatalogRef)
	}
	if got := catalog.WithheldFieldCount; got != 1 {
		t.Fatalf("withheld_field_count = %d, want 1", got)
	}
	if len(catalog.Fields) != 1 {
		t.Fatalf("fields len = %d, want 1", len(catalog.Fields))
	}
	field := catalog.Fields[0]
	if field.Name != "derived_total" {
		t.Fatalf("field name = %q, want derived_total", field.Name)
	}
	if field.Type != "integer" {
		t.Fatalf("field type = %q, want integer", field.Type)
	}
	if field.Sensitivity != "unknown" || field.ExportAction != "block_export" {
		t.Fatalf("field protection = sensitivity %q action %q, want unknown/block_export", field.Sensitivity, field.ExportAction)
	}
}
