package dataartifact

import (
	"path/filepath"
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
	if got := descriptor.Reps[0].ProtectionEnforceableGranularity; got != "column" {
		t.Fatalf("parquet protection floor = %q, want column", got)
	}
	if got := descriptor.Reps[0].ReadPath.GateableUnitGranularity; got != "column" {
		t.Fatalf("parquet gateable unit = %q, want column", got)
	}
	for _, cap := range descriptor.Reps[0].ReadPath.ScanCapabilities {
		if cap == "predicate_pushdown" {
			t.Fatalf("parquet rep must not claim predicate_pushdown without pushdown_withheld")
		}
	}
}

func TestBuildExtractDescriptorAddsObjectIndexGrainForRecordIndex(t *testing.T) {
	// URI must be a portable/sanitized ref (relative or basename), never a host-local absolute path.
	const portableIndexURI = "indexes/source.recordindex.json"
	descriptor, err := BuildExtractDescriptor(provenance.Manifest{
		RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
		SumpterVersion:     "0.3.0-dev",
		CountsByRecordType: map[string]int{"item": 3},
		Outputs:            []provenance.Output{{Path: "records.jsonl", Format: "json", RecordCount: 3}},
	}, "0190a3f4-1c2d-7abc-9def-111111111111", DescriptorOptions{
		RecordIndexPath:     portableIndexURI,
		RecordIndexRowCount: 3,
	})
	if err != nil {
		t.Fatalf("BuildExtractDescriptor: %v", err)
	}
	if len(descriptor.Grains) != 2 {
		t.Fatalf("grains len = %d, want 2", len(descriptor.Grains))
	}
	if got := descriptor.Grains[0].Kind; got != GrainKindRecordStream {
		t.Fatalf("primary grain kind = %q, want %q", got, GrainKindRecordStream)
	}
	indexGrain := descriptor.Grains[1]
	if indexGrain.ID != GrainIDRecordIndex || indexGrain.Kind != GrainKindObjectIndex {
		t.Fatalf("index grain = %#v, want object_index record_index", indexGrain)
	}
	if indexGrain.RowCount != 3 || indexGrain.RecordKind != "xml_record_boundary" {
		t.Fatalf("index grain fields = %#v", indexGrain)
	}
	found := false
	for _, rep := range descriptor.Reps {
		if rep.Grain == GrainIDRecordIndex {
			found = true
			if rep.Role != "object_index" || rep.Format != "json" || rep.URI != portableIndexURI {
				t.Fatalf("index representation = %#v", rep)
			}
			if filepath.IsAbs(rep.URI) {
				t.Fatalf("index URI must not be absolute host path: %q", rep.URI)
			}
			if rep.ProtectionEnforceableGranularity != "artifact" {
				t.Fatalf("index protection floor = %q, want artifact", rep.ProtectionEnforceableGranularity)
			}
		}
	}
	if !found {
		t.Fatal("missing object_index representation")
	}
}

func TestBuildExtractDescriptorUsesAggregationGrainForAggregateMode(t *testing.T) {
	descriptor, err := BuildExtractDescriptor(provenance.Manifest{
		RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
		SumpterVersion:     "0.3.0-dev",
		OutputMode:         "aggregate",
		CountsByRecordType: map[string]int{"item": 4},
		Outputs: []provenance.Output{
			{Path: "records.jsonl", Format: "json", RecordCount: 2},
			{Path: "records-00001.jsonl", Format: "json", RecordCount: 2},
		},
		AggregateOutputs: []provenance.AggregateOutput{
			{Path: "records.jsonl", Format: "json", RecordCount: 2, SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
			{Path: "records-00001.jsonl", Format: "json", RecordCount: 2, SHA256: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
		},
	}, "0190a3f4-1c2d-7abc-9def-111111111111", DescriptorOptions{})
	if err != nil {
		t.Fatalf("BuildExtractDescriptor: %v", err)
	}
	if got := descriptor.Grains[0].Kind; got != GrainKindAggregation {
		t.Fatalf("primary grain kind = %q, want %q", got, GrainKindAggregation)
	}
	if got := descriptor.Grains[0].SemanticOrder; got != "input_order_record_num" {
		t.Fatalf("semantic_order = %q, want input_order_record_num", got)
	}
	for _, rep := range descriptor.Reps {
		if rep.Grain != GrainIDRecords {
			continue
		}
		// Monotonicity: aggregate NDJSON stays row-gated (same as record_stream NDJSON).
		if rep.ProtectionEnforceableGranularity != "row" {
			t.Fatalf("aggregate rep protection floor = %q, want row", rep.ProtectionEnforceableGranularity)
		}
		if !rep.ReadPath.Sharded {
			t.Fatalf("aggregate multi-shard rep should be sharded: %#v", rep)
		}
		if rep.Integrity == nil || rep.Integrity.Mode != "whole_digest" {
			t.Fatalf("aggregate rep missing whole_digest integrity: %#v", rep)
		}
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
