package inspect

import (
	"time"
)

// DialectRegistry represents the complete registry of XML dialects
type DialectRegistry struct {
	RegistryVersion string           `json:"registry_version" yaml:"registry_version"`
	LastUpdated     time.Time        `json:"last_updated" yaml:"last_updated"`
	Dialects        []Dialect        `json:"dialects" yaml:"dialects"`
	Extensions      []Extension      `json:"extensions,omitempty" yaml:"extensions,omitempty"`
	Validation      *ValidationRules `json:"validation,omitempty" yaml:"validation,omitempty"`
}

// Dialect represents a single XML dialect definition
type Dialect struct {
	DialectID           string    `json:"dialect_id" yaml:"dialect_id"`
	Name                string    `json:"name" yaml:"name"`
	Description         string    `json:"description" yaml:"description"`
	Status              string    `json:"status" yaml:"status"`
	Priority            string    `json:"priority" yaml:"priority"`
	Realm               string    `json:"realm" yaml:"realm"`
	Patterns            []Pattern `json:"patterns" yaml:"patterns"`
	XMLStandards        []string  `json:"xml_standards,omitempty" yaml:"xml_standards,omitempty"`
	DataSensitivity     string    `json:"data_sensitivity,omitempty" yaml:"data_sensitivity,omitempty"`
	BridgeDialects      []Bridge  `json:"bridge_dialects,omitempty" yaml:"bridge_dialects,omitempty"`
	Tags                []string  `json:"tags,omitempty" yaml:"tags,omitempty"`
	UseCases            []string  `json:"use_cases,omitempty" yaml:"use_cases,omitempty"`
	Examples            []Example `json:"examples,omitempty" yaml:"examples,omitempty"`
	ConfidenceThreshold float64   `json:"confidence_threshold,omitempty" yaml:"confidence_threshold"`
	FormatType          string    `json:"format_type,omitempty" yaml:"format_type"`
}

// Pattern represents a detection pattern for a dialect
type Pattern struct {
	PatternID string  `json:"pattern_id" yaml:"pattern_id"`
	Name      string  `json:"name" yaml:"name"`
	Selector  string  `json:"selector" yaml:"selector"`
	Weight    float64 `json:"weight" yaml:"weight"`
	Ecosystem string  `json:"ecosystem" yaml:"ecosystem"`
}

// Bridge represents a connection between dialects
type Bridge struct {
	TargetDialect string   `json:"target_dialect" yaml:"target_dialect"`
	BridgeType    string   `json:"bridge_type" yaml:"bridge_type"`
	Description   string   `json:"description" yaml:"description"`
	BridgeKeys    []string `json:"bridge_keys" yaml:"bridge_keys"`
}

// Example represents a sample file for a dialect
type Example struct {
	Path        string `json:"path" yaml:"path"`
	Description string `json:"description" yaml:"description"`
}

// Extension represents a registry extension for blending/overriding
type Extension struct {
	Type      string   `json:"type" yaml:"type"`
	Source    string   `json:"source" yaml:"source"`
	MergeKeys []string `json:"merge_keys,omitempty" yaml:"merge_keys,omitempty"`
	Version   string   `json:"version" yaml:"version"`
	Priority  string   `json:"priority,omitempty" yaml:"priority,omitempty"`
}

// ValidationRules represents post-blend validation rules
type ValidationRules struct {
	RequiredFields []string `json:"required_fields,omitempty" yaml:"required_fields,omitempty"`
	UniqueItems    []string `json:"unique_items,omitempty" yaml:"unique_items,omitempty"`
	NoDuplicates   bool     `json:"no_duplicates,omitempty" yaml:"no_duplicates,omitempty"`
}

// DetectionResult represents the result of dialect detection
type DetectionResult struct {
	DialectName     string                 `json:"dialect_name"`
	Confidence      float64                `json:"confidence"`
	Score           float64                `json:"score"`
	ScoreBreakdown  map[string]float64     `json:"score_breakdown,omitempty"`
	MatchedPatterns []string               `json:"matched_patterns,omitempty"`
	Metadata        map[string]interface{} `json:"metadata,omitempty"`
}

// DetectorOptions represents options for dialect detection
type DetectorOptions struct {
	MaxTokens        int     `json:"max_tokens,omitempty"`
	MinConfidence    float64 `json:"min_confidence,omitempty"`
	IncludeBreakdown bool    `json:"include_breakdown,omitempty"`
}
