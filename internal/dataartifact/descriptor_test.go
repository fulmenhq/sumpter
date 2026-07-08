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

func TestBuildRecordStreamDescriptorMarksPartialLifecycle(t *testing.T) {
	failed := 1
	descriptor, err := BuildRecordStreamDescriptor(provenance.Manifest{
		RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
		SumpterVersion:     "0.3.0-dev",
		InputsFailed:       &failed,
		CountsByRecordType: map[string]int{"sample_record": 2},
		Outputs:            []provenance.Output{{Path: "records.jsonl", Format: "json", RecordCount: 2}},
	}, "0190a3f4-1c2d-7abc-9def-111111111111")
	if err != nil {
		t.Fatalf("BuildRecordStreamDescriptor: %v", err)
	}
	if got := descriptor.Lifecycle; got != "partial" {
		t.Fatalf("lifecycle = %q, want partial", got)
	}
}
