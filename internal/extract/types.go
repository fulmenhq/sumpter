package extract

// FileSignature represents a file signature configuration
type FileSignature struct {
	SignatureID         string         `yaml:"signature_id" json:"signature_id"`
	Name                string         `yaml:"name" json:"name"`
	Description         string         `yaml:"description" json:"description"`
	Status              string         `yaml:"status" json:"status"`
	Priority            string         `yaml:"priority" json:"priority"`
	Realm               string         `yaml:"realm" json:"realm"`
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
}

// ExtractRecordMatch represents an extract configuration
type ExtractRecordMatch struct {
	RecordType     string                 `yaml:"record_type" json:"record_type"`
	MatchSelectors []MatchSelector        `yaml:"match_selectors" json:"match_selectors"`
	OutputSchema   map[string]interface{} `yaml:"output_schema" json:"output_schema"`
	FieldMappings  []FieldMapping         `yaml:"field_mappings" json:"field_mappings"`
	Filters        map[string]interface{} `yaml:"filters" json:"filters"`
}

// MatchSelector represents a selector for matching records
type MatchSelector struct {
	XPath          string                 `yaml:"xpath" json:"xpath"`
	Attributes     map[string]interface{} `yaml:"attributes" json:"attributes"`
	MinOccurrences int                    `yaml:"min_occurrences" json:"min_occurrences"`
}

// FieldMapping represents a mapping from XPath to output field
type FieldMapping struct {
	OutputField string         `yaml:"output_field" json:"output_field"`
	XPath       string         `yaml:"xpath" json:"xpath"`
	Type        string         `yaml:"type" json:"type"`
	Transform   string         `yaml:"transform,omitempty" json:"transform,omitempty"`
	ItemMapping []FieldMapping `yaml:"item_mapping,omitempty" json:"item_mapping,omitempty"`
}

// ExtractResult represents the result of processing a file
type ExtractResult struct {
	File    string                   `json:"file"`
	Records []map[string]interface{} `json:"records"`
	Error   error                    `json:"error,omitempty"`
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
