package dataartifact

import (
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

func TestLifecycleFromManifest(t *testing.T) {
	zero := 0
	one := 1

	tests := []struct {
		name     string
		manifest provenance.Manifest
		want     string
	}{
		{
			name: "clean single-file run without accounting",
			manifest: provenance.Manifest{
				Inputs: []provenance.Input{{Path: "a.xml", Disposition: "applied"}},
			},
			want: LifecycleComplete,
		},
		{
			name: "aggregate accounting all applied including explicit zero failed",
			manifest: provenance.Manifest{
				InputsFailed: &zero,
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "applied"},
					{Path: "b.xml", Disposition: "applied"},
				},
			},
			want: LifecycleComplete,
		},
		{
			name: "not_applicable only is complete not partial",
			manifest: provenance.Manifest{
				InputsFailed: &zero,
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "not_applicable"},
					{Path: "b.xml", Disposition: "not_applicable"},
				},
			},
			want: LifecycleComplete,
		},
		{
			name: "mixed applied and not_applicable is complete",
			manifest: provenance.Manifest{
				InputsFailed: &zero,
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "applied"},
					{Path: "b.xml", Disposition: "not_applicable"},
				},
			},
			want: LifecycleComplete,
		},
		{
			name: "InputsFailed count drives partial",
			manifest: provenance.Manifest{
				InputsFailed: &one,
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "applied"},
					{Path: "b.xml", Disposition: "failed"},
				},
			},
			want: LifecyclePartial,
		},
		{
			name: "per-input disposition failed without accounting integers",
			manifest: provenance.Manifest{
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "applied"},
					{Path: "b.xml", Disposition: "failed"},
				},
			},
			want: LifecyclePartial,
		},
		{
			name: "incomplete flag is hard failure",
			manifest: provenance.Manifest{
				Incomplete: true,
				Inputs:     []provenance.Input{{Path: "a.xml", Disposition: "applied"}},
			},
			want: LifecycleIncomplete,
		},
		{
			name: "incomplete wins over failed inputs",
			manifest: provenance.Manifest{
				Incomplete:   true,
				InputsFailed: &one,
				Inputs: []provenance.Input{
					{Path: "a.xml", Disposition: "failed"},
				},
			},
			want: LifecycleIncomplete,
		},
		{
			name:     "empty inventory defaults to complete",
			manifest: provenance.Manifest{},
			want:     LifecycleComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := LifecycleFromManifest(tt.manifest); got != tt.want {
				t.Fatalf("LifecycleFromManifest = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildRecordStreamDescriptorLifecycleMatrix(t *testing.T) {
	failed := 1
	baseOutputs := []provenance.Output{{Path: "records.jsonl", Format: "json", RecordCount: 1}}
	baseCounts := map[string]int{"sample_record": 1}
	artifactID := "0190a3f4-1c2d-7abc-9def-111111111111"

	cases := []struct {
		name     string
		manifest provenance.Manifest
		want     string
	}{
		{
			name: "complete",
			manifest: provenance.Manifest{
				RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
				CountsByRecordType: baseCounts,
				Outputs:            baseOutputs,
			},
			want: LifecycleComplete,
		},
		{
			name: "partial",
			manifest: provenance.Manifest{
				RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
				InputsFailed:       &failed,
				CountsByRecordType: baseCounts,
				Outputs:            baseOutputs,
			},
			want: LifecyclePartial,
		},
		{
			name: "incomplete",
			manifest: provenance.Manifest{
				RunID:              "0190a3f4-1c2d-7abc-9def-0123456789ab",
				Incomplete:         true,
				CountsByRecordType: baseCounts,
				Outputs:            baseOutputs,
			},
			want: LifecycleIncomplete,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			descriptor, err := BuildRecordStreamDescriptor(tt.manifest, artifactID)
			if err != nil {
				t.Fatalf("BuildRecordStreamDescriptor: %v", err)
			}
			if got := descriptor.Lifecycle; got != tt.want {
				t.Fatalf("lifecycle = %q, want %q", got, tt.want)
			}
		})
	}
}
