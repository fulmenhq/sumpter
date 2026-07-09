package dataartifact

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

const (
	DescriptorFileName = "artifact-descriptor.json"
	ProducerProfile    = "sumpter.extract-artifact/v0"
	FieldCatalogRef    = "fields/records.fields.json"
	ProvenanceRef      = provenance.ManifestFileName
)

type Descriptor struct {
	Capabilities []string         `json:"capabilities"`
	ArtifactID   string           `json:"artifact_id"`
	Lifecycle    string           `json:"lifecycle"`
	Producer     Producer         `json:"producer"`
	Grains       []Grain          `json:"grains"`
	Reps         []Representation `json:"representations"`
	Provenance   *Provenance      `json:"provenance,omitempty"`
	Protection   Protection       `json:"protection"`
}

type FieldCatalog struct {
	ID                 string         `json:"id"`
	Grain              string         `json:"grain"`
	Fields             []CatalogField `json:"fields"`
	WithheldFieldCount int            `json:"withheld_field_count,omitempty"`
}

type CatalogField struct {
	Name           string   `json:"name"`
	Type           string   `json:"type"`
	Required       *bool    `json:"required,omitempty"`
	SemanticRole   string   `json:"semantic_role,omitempty"`
	Sensitivity    string   `json:"sensitivity"`
	ProtectionTags []string `json:"protection_tags,omitempty"`
	ExportAction   string   `json:"export_action,omitempty"`
	SafeToProfile  *bool    `json:"safe_to_profile,omitempty"`
}

type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Profile string `json:"profile"`
	RunID   string `json:"run_id,omitempty"`
}

type Grain struct {
	ID              string `json:"id"`
	Kind            string `json:"kind"`
	RecordKind      string `json:"record_kind"`
	RowCount        int    `json:"row_count"`
	SemanticOrder   string `json:"semantic_order,omitempty"`
	FieldCatalogRef string `json:"field_catalog_ref"`
	ProvenanceRef   string `json:"provenance_ref,omitempty"`
}

type Representation struct {
	ID                               string     `json:"id"`
	Grain                            string     `json:"grain"`
	Role                             string     `json:"role"`
	Format                           string     `json:"format"`
	URI                              string     `json:"uri"`
	RowCount                         int        `json:"row_count"`
	FieldCatalogRef                  string     `json:"field_catalog_ref,omitempty"`
	ReadPath                         ReadPath   `json:"read_path"`
	ProtectionEnforceableGranularity string     `json:"protection_enforceable_granularity"`
	Integrity                        *Integrity `json:"integrity,omitempty"`
}

type ReadPath struct {
	RangeReadable           bool     `json:"range_readable"`
	Partitioned             bool     `json:"partitioned"`
	Sharded                 bool     `json:"sharded"`
	Appendable              bool     `json:"appendable"`
	ScanCapabilities        []string `json:"scan_capabilities"`
	ReadPathGranularity     string   `json:"read_path_granularity"`
	GateableUnitGranularity string   `json:"gateable_unit_granularity"`
	SidecarRequired         bool     `json:"sidecar_required"`
	PhysicalOrdering        string   `json:"physical_ordering,omitempty"`
}

type Integrity struct {
	Mode      string `json:"mode"`
	Algorithm string `json:"algorithm"`
	Value     string `json:"value"`
}

type Provenance struct {
	JobRef string `json:"job_ref,omitempty"`
}

type Protection struct {
	DefaultAction      string `json:"default_action"`
	DefaultExportClass string `json:"default_export_class,omitempty"`
	ProfileRef         string `json:"profile_ref,omitempty"`
}

func BuildRecordFieldCatalog(fields []provenance.FieldProvenance) FieldCatalog {
	catalog := FieldCatalog{
		ID:     FieldCatalogRef,
		Grain:  "records",
		Fields: []CatalogField{},
	}
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		name := strings.TrimSpace(field.OutputField)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		if fieldCatalogKeyWithheld(field) {
			catalog.WithheldFieldCount++
			continue
		}
		catalog.Fields = append(catalog.Fields, CatalogField{
			Name:         name,
			Type:         catalogType(field.Type),
			SemanticRole: "derived_field",
			Sensitivity:  "unknown",
			ExportAction: "block_export",
		})
	}
	return catalog
}

func BuildRecordStreamDescriptor(manifest provenance.Manifest, artifactUUID string) (Descriptor, error) {
	artifactUUID = strings.TrimSpace(artifactUUID)
	if artifactUUID == "" {
		return Descriptor{}, fmt.Errorf("artifact UUID is required")
	}
	recordKind := recordKind(manifest.CountsByRecordType)
	rowCount := totalRecordCount(manifest.CountsByRecordType, manifest.Outputs)

	reps := make([]Representation, 0, len(manifest.Outputs))
	for i, output := range sortedOutputs(manifest.Outputs) {
		rep, ok := representationForOutput(output, i+1, manifest.AggregateOutputs)
		if !ok {
			continue
		}
		reps = append(reps, rep)
	}
	if len(reps) == 0 {
		return Descriptor{}, fmt.Errorf("record-stream descriptor requires at least one record representation")
	}

	return Descriptor{
		Capabilities: []string{artifactcontract.DataArtifactCapability},
		ArtifactID:   "urn:uuid:" + artifactUUID,
		Lifecycle:    LifecycleFromManifest(manifest),
		Producer: Producer{
			Name:    "sumpter",
			Version: valueOrUnknown(manifest.SumpterVersion),
			Profile: ProducerProfile,
			RunID:   manifest.RunID,
		},
		Grains: []Grain{{
			ID:              "records",
			Kind:            "record_stream",
			RecordKind:      recordKind,
			RowCount:        rowCount,
			SemanticOrder:   "source_order",
			FieldCatalogRef: FieldCatalogRef,
			ProvenanceRef:   ProvenanceRef,
		}},
		Reps: reps,
		Provenance: &Provenance{
			JobRef: ProvenanceRef,
		},
		Protection: Protection{
			DefaultAction:      "block_export",
			DefaultExportClass: "internal",
			ProfileRef:         "profile:" + ProducerProfile,
		},
	}, nil
}

func sortedOutputs(outputs []provenance.Output) []provenance.Output {
	out := append([]provenance.Output(nil), outputs...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Format < out[j].Format
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func representationForOutput(output provenance.Output, ordinal int, aggregateOutputs []provenance.AggregateOutput) (Representation, bool) {
	switch output.Format {
	case "json", "ndjson":
		rep := Representation{
			ID:                               fmt.Sprintf("records_ndjson_%d", ordinal),
			Grain:                            "records",
			Role:                             "audit_stream",
			Format:                           "ndjson",
			URI:                              output.Path,
			RowCount:                         output.RecordCount,
			FieldCatalogRef:                  FieldCatalogRef,
			ProtectionEnforceableGranularity: "row",
			ReadPath: ReadPath{
				RangeReadable:           true,
				Partitioned:             false,
				Sharded:                 false,
				Appendable:              false,
				ScanCapabilities:        []string{},
				ReadPathGranularity:     "row",
				GateableUnitGranularity: "row",
				SidecarRequired:         true,
				PhysicalOrdering:        "line_order",
			},
		}
		if integrity := integrityFor(output.Path, aggregateOutputs); integrity != nil {
			rep.Integrity = integrity
		}
		return rep, true
	case "parquet":
		return Representation{
			ID:                               fmt.Sprintf("records_parquet_%d", ordinal),
			Grain:                            "records",
			Role:                             "analytics_scan",
			Format:                           "parquet",
			URI:                              output.Path,
			RowCount:                         output.RecordCount,
			FieldCatalogRef:                  FieldCatalogRef,
			ProtectionEnforceableGranularity: "artifact",
			ReadPath: ReadPath{
				RangeReadable:           true,
				Partitioned:             false,
				Sharded:                 false,
				Appendable:              false,
				ScanCapabilities:        []string{"columnar_scan"},
				ReadPathGranularity:     "row_group",
				GateableUnitGranularity: "artifact",
				SidecarRequired:         true,
			},
		}, true
	default:
		return Representation{}, false
	}
}

func integrityFor(path string, aggregateOutputs []provenance.AggregateOutput) *Integrity {
	for _, output := range aggregateOutputs {
		if output.Path == path && strings.HasPrefix(output.SHA256, "sha256:") {
			return &Integrity{
				Mode:      "whole_digest",
				Algorithm: "sha256",
				Value:     output.SHA256,
			}
		}
	}
	return nil
}

func fieldCatalogKeyWithheld(field provenance.FieldProvenance) bool {
	// XPath and descriptions are source-structure content, disclosed only by count.
	// data-artifact/v0 allows a fully withheld catalog when withheld_field_count is positive.
	return strings.TrimSpace(field.XPath) != "" || strings.TrimSpace(field.Description) != ""
}

func catalogType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "integer", "int", "int32", "int64":
		return "integer"
	case "number", "float", "float32", "float64", "decimal", "double":
		return "number"
	case "boolean", "bool":
		return "boolean"
	case "date":
		return "date"
	case "datetime", "date-time", "timestamp":
		return "datetime"
	case "object", "map":
		return "object"
	case "array", "list":
		return "array"
	case "null":
		return "null"
	default:
		return "string"
	}
}

func recordKind(counts map[string]int) string {
	if len(counts) == 1 {
		for kind := range counts {
			if strings.TrimSpace(kind) != "" {
				return kind
			}
		}
	}
	return "extract_record"
}

func totalRecordCount(counts map[string]int, outputs []provenance.Output) int {
	if len(counts) > 0 {
		total := 0
		for _, count := range counts {
			total += count
		}
		return total
	}
	return maxOutputRows(outputs)
}

func maxOutputRows(outputs []provenance.Output) int {
	total := 0
	for _, output := range outputs {
		if output.RecordCount > total {
			total = output.RecordCount
		}
	}
	return total
}

func valueOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}
