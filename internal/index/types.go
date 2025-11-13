package index

import "time"

// RecordIndex represents the complete XML record index structure
// conforming to the record-index/v0.1.0 JSON schema.
type RecordIndex struct {
	Version  string           `json:"version"`
	Source   SourceInfo       `json:"source"`
	Selector SelectorInfo     `json:"selector"`
	Records  []RecordMetadata `json:"records"`
	Summary  SummaryStats     `json:"summary"`
	Metadata IndexMetadata    `json:"metadata"`
}

// SourceInfo contains source XML file information and integrity metadata
type SourceInfo struct {
	Path              string    `json:"path"`
	SizeBytes         int64     `json:"size_bytes"`
	SHA256            string    `json:"sha256"`
	Compressed        bool      `json:"compressed"`
	CompressionFormat string    `json:"compression_format,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	Encoding          string    `json:"encoding,omitempty"`
}

// SelectorInfo defines how records were identified
type SelectorInfo struct {
	XPath       string `json:"xpath"`
	ElementName string `json:"element_name"`
}

// RecordMetadata contains boundary and integrity metadata for a single record
type RecordMetadata struct {
	RecordNum   int    `json:"record_num"`
	StartOffset int64  `json:"start_offset"`
	EndOffset   int64  `json:"end_offset"`
	SizeBytes   int64  `json:"size_bytes"`
	SHA256      string `json:"sha256"`
	ElementName string `json:"element_name"`
	Depth       int    `json:"depth"`
}

// SummaryStats contains aggregate statistics across all records
type SummaryStats struct {
	TotalRecords       int     `json:"total_records"`
	TotalBytes         int64   `json:"total_bytes"`
	AvgRecordSizeBytes float64 `json:"avg_record_size_bytes"`
	MinRecordSizeBytes int64   `json:"min_record_size_bytes"`
	MaxRecordSizeBytes int64   `json:"max_record_size_bytes"`
	P50RecordSizeBytes int64   `json:"p50_record_size_bytes,omitempty"`
	P95RecordSizeBytes int64   `json:"p95_record_size_bytes,omitempty"`
	P99RecordSizeBytes int64   `json:"p99_record_size_bytes,omitempty"`
}

// IndexMetadata contains metadata about the index build process
type IndexMetadata struct {
	Generator       string `json:"generator"`
	BuildDurationMs int64  `json:"build_duration_ms,omitempty"`
	SumpterVersion  string `json:"sumpter_version,omitempty"`
}

// BuildOptions configures index building behavior
type BuildOptions struct {
	InputPath      string // Path to source XML file
	OutputPath     string // Path for output index file (optional, defaults to SUMPTER_HOME/indexes/<hash>.recordindex.json)
	Selector       string // XPath selector for record boundaries (e.g., "//VariationArchive")
	IncludeP50     bool   // Include p50 (median) in summary stats
	IncludeP95     bool   // Include p95 in summary stats
	IncludeP99     bool   // Include p99 in summary stats
	SumpterVersion string // Sumpter version for metadata
}

// VerifyOptions configures index verification behavior
type VerifyOptions struct {
	InputPath     string // Path to source XML file
	IndexPath     string // Path to index file to verify
	VerifyRecords bool   // If true, verify individual record checksums (slower)
	FailFast      bool   // Stop on first verification error
}

// VerifyResult contains the results of index verification
type VerifyResult struct {
	Valid           bool     // Overall verification result
	SourceSizeMatch bool     // Source file size matches index
	SourceHashMatch bool     // Source file SHA256 matches index
	RecordsVerified int      // Number of records verified (if VerifyRecords enabled)
	RecordErrors    []string // List of record verification errors
	ErrorMessage    string   // Overall error message if not valid
}
