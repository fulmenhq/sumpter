package extract

import (
	"sync"

	"github.com/antchfx/xpath"
	"github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/validation/dsl"
)

// FileSignature represents a file signature configuration
type FileSignature struct {
	SignatureID         string         `yaml:"signature_id" json:"signature_id"`
	Name                string         `yaml:"name" json:"name"`
	Description         string         `yaml:"description" json:"description"`
	Status              string         `yaml:"status" json:"status"`
	Priority            string         `yaml:"priority" json:"priority"`
	Realm               string         `yaml:"realm" json:"realm"`
	Dialects            []Dialect      `yaml:"dialects" json:"dialects"`
	MatchPatterns       []MatchPattern `yaml:"match_patterns" json:"match_patterns"`
	ConfidenceThreshold float64        `yaml:"confidence_threshold" json:"confidence_threshold"`
	FormatType          string         `yaml:"format_type" json:"format_type"`
	Tags                []string       `yaml:"tags" json:"tags"`
	UseCases            []string       `yaml:"use_cases" json:"use_cases"`
}

// MatchPattern represents a pattern for matching files
type MatchPattern struct {
	PatternID string  `yaml:"pattern_id" json:"pattern_id"`
	Name      string  `yaml:"name" json:"name"`
	Selector  string  `yaml:"selector" json:"selector"`
	Weight    float64 `yaml:"weight" json:"weight"`
	Ecosystem string  `yaml:"ecosystem" json:"ecosystem"`
	Dialect   string  `yaml:"dialect" json:"dialect"`
}

// Dialect represents a vendor-specific dialect configuration.
//
// Dialects describe variants of the same format family (for example,
// different vendor implementations of a shared industry XML standard).
// The OSS engine carries the dialect *shape* — identifiers, vendor,
// version — and recipes bind the shape to concrete dialect instances.
//
// Per-dialect redaction / blinding rules are intentionally NOT a field
// on this struct in the OSS engine. The v0.1.4+ redaction story (see
// the design doc at .plans/active/v0.1.3/03-blindingconfig-refactor.md)
// puts the redaction *rule shape* in the OSS schema layer and lets
// recipes declare *category names* — so the engine type system never
// names a specific vertical's redaction categories.
type Dialect struct {
	ID          string `yaml:"id" json:"id"`
	Name        string `yaml:"name" json:"name"`
	Description string `yaml:"description" json:"description"`
	Vendor      string `yaml:"vendor" json:"vendor"`
	Version     string `yaml:"version" json:"version"`
}

// ExtractRecordMatch represents an extract configuration
type ExtractRecordMatch struct {
	RecordType         string                  `yaml:"record_type" json:"record_type"`
	MatchSelectors     []MatchSelector         `yaml:"match_selectors" json:"match_selectors"`
	OutputSchema       map[string]interface{}  `yaml:"output_schema" json:"output_schema"`
	FieldMappings      []FieldMapping          `yaml:"field_mappings" json:"field_mappings"`
	Filters            map[string]interface{}  `yaml:"filters" json:"filters"`
	ValidationMetadata *dsl.ValidationMetadata `yaml:"validation_metadata,omitempty" json:"validation_metadata,omitempty"`
	OutputOptions      *OutputOptions          `yaml:"output_options,omitempty" json:"output_options,omitempty"`
	Summaries          []SummaryConfig         `yaml:"summaries,omitempty" json:"summaries,omitempty"`
	OutputValidator    *schema.Validator       `yaml:"-" json:"-"`
	prepareOnce        sync.Once               `yaml:"-" json:"-"`
	prepareErr         error                   `yaml:"-" json:"-"`
}

// MatchSelector represents a selector for matching records
type MatchSelector struct {
	XPath          string                 `yaml:"xpath" json:"xpath"`
	Attributes     map[string]interface{} `yaml:"attributes" json:"attributes"`
	MinOccurrences int                    `yaml:"min_occurrences" json:"min_occurrences"`
	CompiledXPath  *xpath.Expr            `yaml:"-" json:"-"`
}

// FieldMapping represents a mapping from an XPath or expression to output field.
//
// Description (added per ADR-0006) is an optional human-readable note
// describing what the field captures and where it comes from. The
// extract pipeline propagates Description into the provenance sidecar's
// per-field manifest entry (manifest.recipe.field_provenance[]).
type FieldMapping struct {
	OutputField     string                 `yaml:"output_field" json:"output_field"`
	XPath           string                 `yaml:"xpath" json:"xpath"`
	Expression      string                 `yaml:"expression,omitempty" json:"expression,omitempty"`
	Type            string                 `yaml:"type" json:"type"`
	Description     string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Transform       string                 `yaml:"transform,omitempty" json:"transform,omitempty"`
	TransformParams map[string]interface{} `yaml:"transform_params,omitempty" json:"transform_params,omitempty"`
	ItemMapping     []FieldMapping         `yaml:"item_mapping,omitempty" json:"item_mapping,omitempty"`
	Polymorphic     []PolymorphicMapping   `yaml:"polymorphic_mapping,omitempty" json:"polymorphic_mapping,omitempty"`
	CompiledXPath   *xpath.Expr            `yaml:"-" json:"-"`
}

// PolymorphicMapping describes how to map heterogeneous XML line items into a
// unified array output structure.
type PolymorphicMapping struct {
	ElementType        string         `yaml:"element_type,omitempty" json:"element_type,omitempty"`
	MatchXPath         string         `yaml:"match_xpath,omitempty" json:"match_xpath,omitempty"`
	ItemType           string         `yaml:"item_type,omitempty" json:"item_type,omitempty"`
	FieldMappings      []FieldMapping `yaml:"field_mappings" json:"field_mappings"`
	CompiledMatchXPath *xpath.Expr    `yaml:"-" json:"-"`
}

// ExtractResult represents the result of processing a file
type ExtractResult struct {
	File                      string                   `json:"file"`
	Records                   []map[string]interface{} `json:"records"`
	Error                     error                    `json:"error,omitempty"`
	PerSelectorCounts         map[int]int              `json:"per_selector_counts,omitempty"`
	PerSelectorCountsComplete bool                     `json:"-"`
}

// OutputOptions controls optional output sections.
type OutputOptions struct {
	ShowSummaries          *bool `yaml:"show_summaries,omitempty" json:"show_summaries,omitempty"`
	ShowValidationMetadata *bool `yaml:"show_validation_metadata,omitempty" json:"show_validation_metadata,omitempty"`
}

// SummaryConfig describes a declarative rollup summary.
type SummaryConfig struct {
	Name       string                   `yaml:"name" json:"name"`
	Label      string                   `yaml:"label" json:"label"`
	Format     string                   `yaml:"format,omitempty" json:"format,omitempty"`
	Total      SummaryValueConfig       `yaml:"total" json:"total"`
	Components []SummaryComponentConfig `yaml:"components" json:"components"`
}

// SummaryValueConfig defines a summary total expression.
type SummaryValueConfig struct {
	Expression string `yaml:"expression" json:"expression"`
}

// SummaryComponentConfig defines an individual summary component.
type SummaryComponentConfig struct {
	Name       string `yaml:"name" json:"name"`
	Label      string `yaml:"label" json:"label"`
	Expression string `yaml:"expression,omitempty" json:"expression,omitempty"`
	Remainder  bool   `yaml:"remainder,omitempty" json:"remainder,omitempty"`
	Format     string `yaml:"format,omitempty" json:"format,omitempty"`
}

// JSONSchema represents a JSON schema
type JSONSchema struct {
	Schema     string                  `json:"$schema,omitempty"`
	Type       string                  `json:"type,omitempty"`
	Properties map[string]JSONProperty `json:"properties,omitempty"`
	Required   []string                `json:"required,omitempty"`
}

// JSONProperty represents a property in a JSON schema
type JSONProperty struct {
	Type string `json:"type"`
}
