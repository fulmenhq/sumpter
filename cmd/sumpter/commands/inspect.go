package commands

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
	"github.com/fulmenhq/sumpter/internal/inspect/configgen"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/utils"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/html/charset"
	"gopkg.in/yaml.v3"
)

type InspectOptions struct {
	File              string
	Output            string
	Format            string
	MaxPaths          int
	SamplesPerPath    int
	ForceEncoding     string
	Progress          bool
	IncludeAttrs      bool
	ValidateOutput    bool
	GenerateConfig    bool
	MinOccurrence     int
	OptionalThreshold float64
	// NEW: Record analysis flags
	AnalyzeRecords bool   `json:"analyze_records"`
	RecordSelector string `json:"record_selector"`
	OOMThresholdMB int    `json:"oom_threshold_mb"`
	MaxCandidates  int    `json:"max_candidates"`
	MaxRecords     int    `json:"max_records"`
}

// InspectReportV0 matches the v0.1.1 schema
type InspectReportV0 struct {
	Version           string            `json:"version"`
	Input             InspectInput      `json:"input"`
	Metrics           InspectMetrics    `json:"metrics"`
	Paths             []InspectPath     `json:"paths"`
	Caps              InspectCaps       `json:"caps"`
	RecordCandidates  []RecordCandidate `json:"record_candidates,omitempty"`
	StreamingAnalysis StreamingAnalysis `json:"streaming_analysis,omitempty"`
	OOMSummary        *OOMSummary       `json:"oom_summary,omitempty"`
	AnalysisMetadata  *AnalysisMetadata `json:"analysis_metadata,omitempty"`
	Metadata          *InspectMetadata  `json:"metadata,omitempty"`
}

type InspectInput struct {
	Path             string  `json:"path"`
	SizeBytes        int64   `json:"size_bytes"`
	EncodingDetected string  `json:"encoding_detected"`
	EncodingForced   *string `json:"encoding_forced,omitempty"`
	Compressed       bool    `json:"compressed"`
	Compression      string  `json:"compression"`
}

type InspectMetrics struct {
	BytesProcessed        int64    `json:"bytes_processed"`
	ElapsedMs             int64    `json:"elapsed_ms"`
	ThroughputBytesPerSec float64  `json:"throughput_bytes_per_sec"`
	ReplacementCount      int      `json:"replacement_count"`
	RssPeakMb             *float64 `json:"rss_peak_mb,omitempty"`
}

type InspectPath struct {
	Path       string             `json:"path"`
	Count      int                `json:"count"`
	Attributes []InspectAttribute `json:"attributes,omitempty"`
	Samples    []string           `json:"samples,omitempty"`
}

type InspectAttribute struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type InspectCaps struct {
	PathsTruncated      bool `json:"paths_truncated"`
	AttributesTruncated bool `json:"attributes_truncated"`
	SamplesTruncated    bool `json:"samples_truncated"`
}

type InspectMetadata struct {
	Generator string            `json:"generator"`
	Timestamp string            `json:"timestamp"`
	Tags      map[string]string `json:"tags,omitempty"`
}

type InspectResult struct {
	FileInfo    FileInfo            `json:"file_info"`
	Encoding    EncodingInfo        `json:"encoding"`
	Structure   StructureInfo       `json:"structure"`
	Paths       map[string]PathInfo `json:"paths"`
	Attributes  map[string]AttrInfo `json:"attributes"`
	Performance PerformanceInfo     `json:"performance"`
	Samples     map[string][]string `json:"samples,omitempty"`
}

type FileInfo struct {
	Path    string `json:"path"`
	Size    int64  `json:"size_bytes"`
	SizeMB  string `json:"size_mb"`
	IsStdin bool   `json:"is_stdin"`
}

type EncodingInfo struct {
	Detected    string `json:"detected"`
	Confidence  string `json:"confidence"`
	BOM         bool   `json:"bom_present"`
	Declaration string `json:"xml_declaration,omitempty"`
	Forced      bool   `json:"forced,omitempty"`
}

type StructureInfo struct {
	TotalElements int    `json:"total_elements"`
	UniquePaths   int    `json:"unique_paths"`
	MaxDepth      int    `json:"max_depth"`
	HasNamespaces bool   `json:"has_namespaces"`
	RootElement   string `json:"root_element,omitempty"`
}

type PathInfo struct {
	Count     int   `json:"count"`
	Depth     int   `json:"depth"`
	HasText   bool  `json:"has_text"`
	HasAttrs  bool  `json:"has_attrs"`
	FirstSeen int64 `json:"first_seen_offset,omitempty"`
}

type AttrInfo struct {
	Path      string         `json:"path"`
	Attribute string         `json:"attribute"`
	Count     int            `json:"count"`
	Types     map[string]int `json:"value_types,omitempty"`
	Samples   []string       `json:"samples,omitempty"`
}

type PerformanceInfo struct {
	Duration       time.Duration `json:"duration_ms"`
	BytesProcessed int64         `json:"bytes_processed"`
	MemoryPeak     string        `json:"memory_peak_mb"`
	ThroughputMBps string        `json:"throughput_mbps"`
}

// countingReader wraps an io.Reader to count bytes read for progress/metrics
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) Bytes() int64 { return c.n }

// NEW: Phase 1.2 Types for Record Analysis

// RecordCandidate represents a potential record element for streaming
type RecordCandidate struct {
	Element       string    `json:"element"`
	XPath         string    `json:"xpath"`
	Count         int       `json:"count"`
	Depth         int       `json:"depth"`
	SizeStats     SizeStats `json:"size_stats"`
	FirstOffset   int64     `json:"first_seen_offset"`
	SampleOffsets []int64   `json:"sample_offsets"`
}

// SizeStats provides statistical information about record sizes
type SizeStats struct {
	AvgKB float64 `json:"avg_kb"`
	P50KB int64   `json:"p50_kb"`
	P95KB int64   `json:"p95_kb"`
	P99KB int64   `json:"p99_kb"`
	MaxKB int64   `json:"max_kb"`
	MinKB int64   `json:"min_kb"`
}

// StreamingAnalysis provides assessment of streaming suitability
type StreamingAnalysis struct {
	SuitableForStreaming bool                 `json:"suitable_for_streaming"`
	RecommendedSelector  string               `json:"recommended_selector"`
	Confidence           string               `json:"confidence"`
	Reasoning            string               `json:"reasoning"`
	Warnings             []Warning            `json:"warnings"`
	MemoryEstimates      MemoryEstimates      `json:"memory_estimates"`
	PerformanceEstimates PerformanceEstimates `json:"performance_estimates"`
}

// Warning represents a warning message with recommendation
type Warning struct {
	Severity       string `json:"severity"`
	Message        string `json:"message"`
	Recommendation string `json:"recommendation"`
}

// MemoryEstimates provides memory usage estimates
type MemoryEstimates struct {
	StreamingTypicalMB int `json:"streaming_typical_mb"`
	StreamingWorstMB   int `json:"streaming_worst_mb"`
	NonStreamingGB     int `json:"non_streaming_gb"`
}

// PerformanceEstimates provides performance estimates
type PerformanceEstimates struct {
	StreamingSequential string `json:"streaming_sequential"`
	ManifestParallel32x string `json:"manifest_parallel_32x"`
}

// OOMSummary provides out-of-memory risk analysis
type OOMSummary struct {
	ThresholdMB      int           `json:"threshold_mb"`
	LargeRecordCount int           `json:"large_record_count"`
	MaxSizeKB        int64         `json:"max_size_kb"`
	LargestRecords   []LargeRecord `json:"largest_records"`
}

// LargeRecord represents a record that exceeds OOM threshold
type LargeRecord struct {
	RecordNum   int   `json:"record_num"`
	SizeKB      int64 `json:"size_kb"`
	OffsetStart int64 `json:"offset_start"`
}

// RecordBoundaryAnalysis contains the complete record analysis results
type RecordBoundaryAnalysis struct {
	Candidates        []RecordCandidate `json:"record_candidates"`
	StreamingAnalysis StreamingAnalysis `json:"streaming_analysis"`
	OOMSummary        *OOMSummary       `json:"oom_summary,omitempty"`
}

// AnalysisOptions contains options for record analysis
type AnalysisOptions struct {
	AnalyzeRecords bool   `json:"analyze_records"`
	RecordSelector string `json:"record_selector"`
	OOMThresholdMB int    `json:"oom_threshold_mb"`
	MaxCandidates  int    `json:"max_candidates"`
	MaxRecords     int    `json:"max_records"`
	Compressed     bool   `json:"compressed"`
	Compression    string `json:"compression"`
}

// AnalysisMetadata contains metadata about the analysis process
type AnalysisMetadata struct {
	AnalyzedAt      string          `json:"analyzed_at"`
	DurationMs      int64           `json:"duration_ms"`
	RecordsAnalyzed int64           `json:"records_analyzed"`
	AnalysisOptions AnalysisOptions `json:"analysis_options"`
}

// Histogram for streaming percentile calculation
type Histogram struct {
	buckets []int64 // Count per bucket
	bounds  []int64 // Upper bounds for each bucket
	total   int64   // Total count
	sum     int64   // Sum of all values
}

// NewHistogram creates a new histogram with exponential buckets
func NewHistogram() *Histogram {
	bounds := []int64{}
	for i := 10; i <= 30; i++ { // 2^10 to 2^30 bytes (1KB to 1GB)
		bounds = append(bounds, 1<<i)
	}
	return &Histogram{
		buckets: make([]int64, len(bounds)),
		bounds:  bounds,
	}
}

// Add adds a value to the histogram
func (h *Histogram) Add(value int64) {
	// Find appropriate bucket
	for i, bound := range h.bounds {
		if value <= bound {
			h.buckets[i]++
			break
		}
	}
	h.total++
	h.sum += value
}

// Percentile calculates approximate percentile from histogram
func (h *Histogram) Percentile(p float64) int64 {
	if h.total == 0 {
		return 0
	}

	target := int64(float64(h.total) * p / 100.0)
	cumulative := int64(0)

	for i, count := range h.buckets {
		cumulative += count
		if cumulative >= target {
			return h.bounds[i]
		}
	}

	return h.bounds[len(h.bounds)-1]
}

// Average returns the average value
func (h *Histogram) Average() float64 {
	if h.total == 0 {
		return 0
	}
	return float64(h.sum) / float64(h.total)
}

// Max returns the maximum value seen
func (h *Histogram) Max() int64 {
	for i := len(h.buckets) - 1; i >= 0; i-- {
		if h.buckets[i] > 0 {
			return h.bounds[i]
		}
	}
	return 0
}

// Min returns the minimum value seen
func (h *Histogram) Min() int64 {
	for i, count := range h.buckets {
		if count > 0 {
			return h.bounds[i]
		}
	}
	return 0
}

func NewInspectCommand() *cobra.Command {
	opts := &InspectOptions{}

	cmd := &cobra.Command{
		Use:   "inspect [file]",
		Short: "Inspect XML file structure, encoding, and content",
		Long: `Inspect XML files to understand their structure, encoding, and content patterns.

This command performs a streaming analysis of XML files to:
- Detect encoding (BOM, XML declaration, charset detection)
- Analyze element structure and path frequencies
- Enumerate attributes and their usage patterns
- Sample text content for understanding data patterns
- Generate reports in Markdown or JSON format

The command is designed for large files (100MB+) with constant memory usage
and configurable sampling to balance speed and insight.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				opts.File = args[0]
			} else {
				opts.File = "-" // stdin
			}

			return runInspectCommand(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.Output, "output", "o", "", "Output file (default: stdout)")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "markdown", "Output format: markdown|json")
	cmd.Flags().IntVar(&opts.MaxPaths, "max-paths", 200, "Maximum number of unique paths to track")
	cmd.Flags().IntVar(&opts.SamplesPerPath, "samples-per-path", 2, "Number of text samples to collect per path")
	cmd.Flags().StringVar(&opts.ForceEncoding, "force-encoding", "", "Force specific encoding (e.g., windows-1252)")
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress for large files")
	cmd.Flags().BoolVar(&opts.IncludeAttrs, "include-attributes", true, "Include attribute analysis")
	cmd.Flags().BoolVar(&opts.ValidateOutput, "validate-output", false, "Validate JSON output against schema")
	cmd.Flags().BoolVar(&opts.GenerateConfig, "generate-config", false, "Emit a starter extract.yaml derived from the document structure")
	cmd.Flags().IntVar(&opts.MinOccurrence, "min-occurrence", 2, "Minimum element frequency to include in generated field_mappings")
	cmd.Flags().Float64Var(&opts.OptionalThreshold, "optional-threshold", 0.5, "Element occurrence ratio below which generated fields get optional-review TODO comments")

	// NEW: Record analysis flags
	cmd.Flags().BoolVar(&opts.AnalyzeRecords, "analyze-records", false, "Enable record boundary analysis for streaming assessment")
	cmd.Flags().StringVar(&opts.RecordSelector, "record-selector", "", "XPath selector for record detection or generated config record matching (auto-detect if empty)")
	cmd.Flags().IntVar(&opts.OOMThresholdMB, "oom-threshold-mb", 100, "OOM warning threshold in megabytes")
	cmd.Flags().IntVar(&opts.MaxCandidates, "max-candidates", 5, "Maximum number of record candidates to analyze")
	cmd.Flags().IntVar(&opts.MaxRecords, "max-records", 0, "Maximum number of records to analyze for statistics (0 = unlimited)")

	return cmd
}

func runInspectCommand(cmd *cobra.Command, opts *InspectOptions) error {
	log := logging.Component("inspect")
	startTime := time.Now()

	// Open input
	var reader io.Reader
	var fileInfo FileInfo

	if opts.File == "-" {
		reader = bufio.NewReader(os.Stdin)
		fileInfo = FileInfo{
			Path:    "<stdin>",
			Size:    -1,
			SizeMB:  "unknown",
			IsStdin: true,
		}
	} else {
		file, err := os.Open(opts.File)
		if err != nil {
			return fmt.Errorf("failed to open file: %w", err)
		}
		defer func() { _ = file.Close() }()

		stat, err := file.Stat()
		if err != nil {
			return fmt.Errorf("failed to stat file: %w", err)
		}

		reader = bufio.NewReader(file)
		fileInfo = FileInfo{
			Path:    opts.File,
			Size:    stat.Size(),
			SizeMB:  fmt.Sprintf("%.2f", float64(stat.Size())/1024/1024),
			IsStdin: false,
		}
	}

	// Detect encoding
	encodingInfo, encodedReader, err := detectEncoding(reader, opts.ForceEncoding)
	if err != nil {
		return fmt.Errorf("encoding detection failed: %w", err)
	}

	// INFO start
	log.Info("inspect start",
		zap.String("file", fileInfo.Path),
		zap.Int64("size_bytes", fileInfo.Size),
		zap.String("encoding_detected", encodingInfo.Detected),
		zap.Bool("forced", encodingInfo.Forced),
	)

	// Wrap reader to track progress
	cr := &countingReader{r: encodedReader}
	// Optional progress ticker
	var progressDone chan struct{}
	if opts.Progress {
		progressDone = make(chan struct{})
		ticker := time.NewTicker(1 * time.Second)
		go func() {
			for {
				select {
				case <-ticker.C:
					bytes := cr.Bytes()
					elapsed := time.Since(startTime)
					throughput := 0.0
					if elapsed > 0 {
						throughput = float64(bytes) / elapsed.Seconds()
					}
					log.Info("progress",
						zap.Int64("bytes_processed", bytes),
						zap.Float64("throughput_bps", throughput),
					)
				case <-progressDone:
					ticker.Stop()
					return
				}
			}
		}()
	}

	// Perform record analysis if requested
	var analysisResult *RecordBoundaryAnalysis
	if opts.AnalyzeRecords {
		if opts.File == "-" {
			log.Warn("record analysis not supported for stdin input")
		} else if opts.RecordSelector == "" {
			log.Warn("record analysis requires --record-selector to be specified")
		} else {
			// Open file again for analysis
			analysisFile, err := os.Open(opts.File)
			if err != nil {
				return fmt.Errorf("failed to open file for analysis: %w", err)
			}
			defer func() { _ = analysisFile.Close() }()

			analysisReader := bufio.NewReader(analysisFile)

			// Detect encoding for analysis
			encodingInfo, _, err := detectEncoding(analysisReader, opts.ForceEncoding)
			if err != nil {
				log.Warn("encoding detection failed for analysis, skipping", zap.Error(err))
			} else {
				// Perform analysis with raw reader and encoding info
				// Detect compression for analysis
				compressed := false
				compression := "none"
				if strings.HasSuffix(strings.ToLower(opts.File), ".gz") ||
					strings.HasSuffix(strings.ToLower(opts.File), ".gzip") {
					compressed = true
					compression = "gzip"
				} else if strings.HasSuffix(strings.ToLower(opts.File), ".bz2") ||
					strings.HasSuffix(strings.ToLower(opts.File), ".bzip2") {
					compressed = true
					compression = "bzip2"
				} else if strings.HasSuffix(strings.ToLower(opts.File), ".xz") {
					compressed = true
					compression = "xz"
				}

				analysisOpts := AnalysisOptions{
					AnalyzeRecords: opts.AnalyzeRecords,
					RecordSelector: opts.RecordSelector,
					OOMThresholdMB: opts.OOMThresholdMB,
					MaxCandidates:  opts.MaxCandidates,
					MaxRecords:     opts.MaxRecords,
					Compressed:     compressed,
					Compression:    compression,
				}

				analysisResult, err = analyzeRecordBoundaries(analysisReader, opts.RecordSelector, encodingInfo.Detected, analysisOpts, log.WithFields())
				if err != nil {
					log.Warn("record boundary analysis failed", zap.Error(err))
				}
			}
		}
	}

	// Perform inspection
	report, err := inspectXML(cr, fileInfo, encodingInfo, opts)
	if err != nil {
		return fmt.Errorf("inspection failed: %w", err)
	}

	// Attach analysis results to report
	if analysisResult != nil {
		report.RecordCandidates = analysisResult.Candidates
		report.StreamingAnalysis = analysisResult.StreamingAnalysis
		report.OOMSummary = analysisResult.OOMSummary
		report.AnalysisMetadata = &AnalysisMetadata{
			AnalyzedAt: time.Now().Format(time.RFC3339), // Use current time since analysisStart is not in scope
			DurationMs: 0,                               // TODO: track duration properly
			RecordsAnalyzed: func() int64 {
				if len(analysisResult.Candidates) > 0 {
					return int64(analysisResult.Candidates[0].Count)
				}
				return 0
			}(), // Approximate
			AnalysisOptions: AnalysisOptions{ // Reconstruct
				AnalyzeRecords: opts.AnalyzeRecords,
				RecordSelector: opts.RecordSelector,
				OOMThresholdMB: opts.OOMThresholdMB,
				MaxCandidates:  opts.MaxCandidates,
				MaxRecords:     opts.MaxRecords,
				Compressed:     report.Input.Compressed,
				Compression:    report.Input.Compression,
			},
		}
	}

	// Add performance info
	elapsed := time.Since(startTime)
	report.Metrics.ElapsedMs = elapsed.Milliseconds()
	if fileInfo.Size > 0 {
		report.Metrics.ThroughputBytesPerSec = float64(fileInfo.Size) / elapsed.Seconds()
	}
	// Use actual bytes processed if available
	if cr != nil {
		report.Metrics.BytesProcessed = cr.Bytes()
	}

	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	rssPeak := float64(m.Alloc) / 1024 / 1024
	report.Metrics.RssPeakMb = &rssPeak

	// Add metadata
	report.Metadata = &InspectMetadata{
		Generator: "sumpter inspect",
		Timestamp: time.Now().Format(time.RFC3339),
	}

	// WARN on caps
	if report.Caps.PathsTruncated || report.Caps.AttributesTruncated || report.Caps.SamplesTruncated {
		log.Warn("caps reached",
			zap.Bool("paths_truncated", report.Caps.PathsTruncated),
			zap.Bool("attributes_truncated", report.Caps.AttributesTruncated),
			zap.Bool("samples_truncated", report.Caps.SamplesTruncated),
		)
	}

	// INFO finish
	log.Info("inspect finish",
		zap.Int64("bytes_processed", report.Metrics.BytesProcessed),
		zap.Int64("elapsed_ms", report.Metrics.ElapsedMs),
		zap.Float64("throughput_bps", report.Metrics.ThroughputBytesPerSec),
		zap.Int("replacement_count", report.Metrics.ReplacementCount),
	)

	// Stop progress ticker if running
	if progressDone != nil {
		close(progressDone)
	}

	// Generate output
	if opts.GenerateConfig {
		if err := generateExtractConfig(cmd, opts); err != nil {
			return err
		}
	} else {
		if err := generateReport(cmd, report, opts); err != nil {
			return err
		}
	}

	// Optional output validation
	if opts.ValidateOutput && opts.Format == "json" {
		// Marshal to bytes and validate with embedded schema
		data, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("failed to marshal report for validation: %w", err)
		}

		// Use embedded schema validation
		if err := validateJSONAgainstEmbeddedSchema(data); err != nil {
			return fmt.Errorf("output schema validation failed: %w", err)
		}
	}

	return nil
}

func generateExtractConfig(cmd *cobra.Command, opts *InspectOptions) error {
	if opts.File == "-" {
		return fmt.Errorf("--generate-config requires a seekable file path; stdin is not supported")
	}

	readerFactory := func() (io.ReadCloser, error) {
		file, err := os.Open(opts.File) // #nosec G304 - user-selected inspect input path
		if err != nil {
			return nil, fmt.Errorf("failed to open file for config generation: %w", err)
		}
		_, encodedReader, err := detectEncoding(bufio.NewReader(file), opts.ForceEncoding)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("encoding detection failed for config generation: %w", err)
		}
		return &readCloserReader{Reader: encodedReader, close: file.Close}, nil
	}

	result, err := configgen.Generate(readerFactory, configgen.Options{
		SourcePath:        opts.File,
		RecordSelector:    opts.RecordSelector,
		MinOccurrence:     opts.MinOccurrence,
		OptionalThreshold: opts.OptionalThreshold,
		GeneratedAt:       time.Now().UTC(),
	})
	if err != nil {
		return fmt.Errorf("failed to generate extract config: %w", err)
	}

	return writeInspectBytes(cmd, opts.Output, result.YAML)
}

type readCloserReader struct {
	io.Reader
	close func() error
}

func (r *readCloserReader) Close() error {
	if r.close == nil {
		return nil
	}
	return r.close()
}

func writeInspectBytes(cmd *cobra.Command, outputPath string, data []byte) error {
	if outputPath == "" {
		_, err := cmd.OutOrStdout().Write(data)
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to resolve current directory: %w", err)
	}
	if err := utils.ValidateUserPathForCreate(outputPath, utils.RootCwd, cwd); err != nil {
		return fmt.Errorf("invalid output path: %w", err)
	}
	file, err := os.OpenFile(outputPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 - path validated by ValidateUserPathForCreate
	if err != nil {
		return fmt.Errorf("failed to open output file: %w", err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("failed to write output file: %w", err)
	}
	return nil
}

func detectEncoding(reader io.Reader, forceEncoding string) (EncodingInfo, io.Reader, error) {
	info := EncodingInfo{}

	// If forced encoding, use it
	if forceEncoding != "" {
		info.Detected = forceEncoding
		info.Forced = true
		info.Confidence = "forced"

		// Create reader with forced encoding
		encodedReader, err := charset.NewReaderLabel(forceEncoding, reader)
		if err != nil {
			return info, nil, fmt.Errorf("failed to create reader with forced encoding: %w", err)
		}
		return info, encodedReader, nil
	}

	// Read first 1024 bytes for detection
	buf := make([]byte, 1024)
	n, err := reader.Read(buf)
	if err != nil && err != io.EOF {
		return info, nil, err
	}

	data := buf[:n]

	// Check for BOM
	if len(data) >= 3 {
		if data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
			info.Detected = "UTF-8"
			info.BOM = true
			info.Confidence = "bom"
		} else if len(data) >= 4 {
			if data[0] == 0x00 && data[1] == 0x00 && data[2] == 0xFE && data[3] == 0xFF {
				info.Detected = "UTF-32BE"
				info.BOM = true
				info.Confidence = "bom"
			} else if data[0] == 0xFF && data[1] == 0xFE && data[2] == 0x00 && data[3] == 0x00 {
				info.Detected = "UTF-32LE"
				info.BOM = true
				info.Confidence = "bom"
			}
		}
	}

	// Check for UTF-16 BOM if not already detected
	if !info.BOM && len(data) >= 2 {
		if data[0] == 0xFE && data[1] == 0xFF {
			info.Detected = "UTF-16BE"
			info.BOM = true
			info.Confidence = "bom"
		} else if data[0] == 0xFF && data[1] == 0xFE {
			info.Detected = "UTF-16LE"
			info.BOM = true
			info.Confidence = "bom"
		}
	}

	// Check for XML declaration
	if !info.BOM {
		xmlDecl := string(data)
		if strings.Contains(xmlDecl, "<?xml") {
			// Extract encoding from declaration
			if encodingStart := strings.Index(xmlDecl, "encoding=\""); encodingStart != -1 {
				encodingStart += 10
				if encodingEnd := strings.Index(xmlDecl[encodingStart:], "\""); encodingEnd != -1 {
					encoding := xmlDecl[encodingStart : encodingStart+encodingEnd]
					info.Detected = strings.ToUpper(encoding)
					info.Declaration = encoding
					info.Confidence = "declaration"
				}
			}
		}
	}

	// Fallback to charset detection
	if info.Detected == "" {
		detected, name, certain := charset.DetermineEncoding(data, "")
		if detected != nil {
			info.Detected = strings.ToUpper(name)
			if certain {
				info.Confidence = "certain"
			} else {
				info.Confidence = "probable"
			}
		} else {
			info.Detected = "UTF-8"
			info.Confidence = "fallback"
		}
	}

	// Create the encoded reader
	combinedReader := io.MultiReader(strings.NewReader(string(data)), reader)
	encodedReader, err := charset.NewReaderLabel(info.Detected, combinedReader)
	if err != nil {
		return info, nil, fmt.Errorf("failed to create encoded reader: %w", err)
	}

	return info, encodedReader, nil
}

func inspectXML(reader io.Reader, fileInfo FileInfo, encodingInfo EncodingInfo, opts *InspectOptions) (*InspectReportV0, error) {
	decoder := xml.NewDecoder(reader)
	// The reader has already been transcoded to UTF-8 by detectEncoding(); use a
	// passthrough CharsetReader so the encoding/xml package's check on the XML
	// declaration is satisfied (it would otherwise error on any non-UTF-8 label
	// like "ISO-8859-1") without re-transcoding the already-decoded bytes.
	decoder.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		return r, nil
	}

	// Track element path stack
	var pathStack []string
	pathMap := make(map[string]*InspectPath)
	attrMap := make(map[string]map[string]*InspectAttribute) // path -> attrName -> attr
	sampleMap := make(map[string][]string)

	elementCount := 0
	maxPaths := opts.MaxPaths
	samplesPerPath := opts.SamplesPerPath
	replacementCount := 0
	samplesTruncatedAny := false

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parsing error: %w", err)
		}

		switch element := token.(type) {
		case xml.StartElement:
			elementCount++
			pathStack = append(pathStack, element.Name.Local)

			// Build current path
			currentPath := strings.Join(pathStack, ".")

			// Track path if we haven't hit the limit
			if len(pathMap) < maxPaths || pathMap[currentPath] != nil {
				if pathMap[currentPath] == nil {
					pathMap[currentPath] = &InspectPath{
						Path:       currentPath,
						Count:      0,
						Attributes: []InspectAttribute{},
						Samples:    []string{},
					}
				}
				pathMap[currentPath].Count++
			}

			// Process attributes
			if opts.IncludeAttrs && pathMap[currentPath] != nil {
				if attrMap[currentPath] == nil {
					attrMap[currentPath] = make(map[string]*InspectAttribute)
				}

				for _, attr := range element.Attr {
					if attrMap[currentPath][attr.Name.Local] == nil {
						attrMap[currentPath][attr.Name.Local] = &InspectAttribute{
							Name:  attr.Name.Local,
							Count: 0,
						}
					}
					attrMap[currentPath][attr.Name.Local].Count++
				}
			}

		case xml.CharData:
			if len(pathStack) > 0 {
				currentPath := strings.Join(pathStack, ".")
				text := strings.TrimSpace(string(element))

				// Only collect non-empty text samples
				if text != "" && pathMap[currentPath] != nil {
					// Collect text samples
					if len(sampleMap[currentPath]) < samplesPerPath {
						// Truncate very long text
						if len(text) > 100 {
							text = text[:97] + "..."
						}
						sampleMap[currentPath] = append(sampleMap[currentPath], text)
					} else if samplesPerPath > 0 {
						// We've hit the cap for this path and saw more content
						samplesTruncatedAny = true
					}
				}
			}

		case xml.EndElement:
			if len(pathStack) > 0 {
				pathStack = pathStack[:len(pathStack)-1]
			}
		}
	}

	// Build paths array, sorted by count desc
	var paths []InspectPath
	for _, pathInfo := range pathMap {
		// Add attributes
		if attrs, exists := attrMap[pathInfo.Path]; exists {
			for _, attr := range attrs {
				pathInfo.Attributes = append(pathInfo.Attributes, *attr)
			}
		}
		// Add samples
		if samples, exists := sampleMap[pathInfo.Path]; exists {
			pathInfo.Samples = samples
		}
		paths = append(paths, *pathInfo)
	}

	// Sort by count desc; tie-break by path asc for determinism
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Count == paths[j].Count {
			return paths[i].Path < paths[j].Path
		}
		return paths[i].Count > paths[j].Count
	})

	// Apply max paths limit
	pathsTruncated := false
	if len(paths) > maxPaths {
		paths = paths[:maxPaths]
		pathsTruncated = true
	}

	// Build input
	input := InspectInput{
		Path:             fileInfo.Path,
		SizeBytes:        fileInfo.Size,
		EncodingDetected: encodingInfo.Detected,
		Compressed:       false,
		Compression:      "none",
	}
	if opts.ForceEncoding != "" {
		input.EncodingForced = &opts.ForceEncoding
	}

	// Detect compression based on file extension
	if strings.HasSuffix(strings.ToLower(fileInfo.Path), ".gz") ||
		strings.HasSuffix(strings.ToLower(fileInfo.Path), ".gzip") {
		input.Compressed = true
		input.Compression = "gzip"
	} else if strings.HasSuffix(strings.ToLower(fileInfo.Path), ".bz2") ||
		strings.HasSuffix(strings.ToLower(fileInfo.Path), ".bzip2") {
		input.Compressed = true
		input.Compression = "bzip2"
	} else if strings.HasSuffix(strings.ToLower(fileInfo.Path), ".xz") {
		input.Compressed = true
		input.Compression = "xz"
	}

	// Build caps (sample truncation inferred)
	caps := InspectCaps{
		PathsTruncated:      pathsTruncated,
		AttributesTruncated: false,
		SamplesTruncated:    samplesTruncatedAny,
	}

	report := &InspectReportV0{
		Version: "inspect-report/v0.1.1",
		Input:   input,
		Metrics: InspectMetrics{
			BytesProcessed:        fileInfo.Size,
			ElapsedMs:             0, // Will be set by caller
			ThroughputBytesPerSec: 0, // Will be set by caller
			ReplacementCount:      replacementCount,
		},
		Paths: paths,
		Caps:  caps,
	}

	// Record analysis is now handled in runInspectCommand before inspection

	return report, nil
}

// validateJSONAgainstEmbeddedSchema validates JSON output against embedded schema
func validateJSONAgainstEmbeddedSchema(jsonData []byte) error {
	// CRITICAL: Use embedded schema via assets package
	schemaPath := "schemas/inspect/v0.1.1/inspect-report.schema.yaml"

	// Load schema from embedded assets
	schemasFS, err := assets.GetSchemasFS()
	if err != nil {
		return fmt.Errorf("failed to get schemas filesystem: %w", err)
	}

	// Read schema file
	schemaFile, err := schemasFS.Open(schemaPath)
	if err != nil {
		return fmt.Errorf("failed to open schema file %s: %w", schemaPath, err)
	}
	defer func() {
		_ = schemaFile.Close() // Best practice: handle close errors in production code
	}()

	schemaYAMLBytes, err := io.ReadAll(schemaFile)
	if err != nil {
		return fmt.Errorf("failed to read schema file %s: %w", schemaPath, err)
	}

	// Convert YAML schema to JSON for goneat validation
	var schemaInterface interface{}
	if err := yaml.Unmarshal(schemaYAMLBytes, &schemaInterface); err != nil {
		return fmt.Errorf("failed to parse schema YAML: %w", err)
	}
	schemaBytes, err := json.Marshal(schemaInterface)
	if err != nil {
		return fmt.Errorf("failed to convert schema to JSON: %w", err)
	}

	// Decode data into interface for validation
	var dataInterface interface{}
	if err := json.Unmarshal(jsonData, &dataInterface); err != nil {
		return fmt.Errorf("failed to parse report JSON: %w", err)
	}

	// Validate
	res, err := schema.ValidateFromBytes(schemaBytes, dataInterface)
	if err != nil {
		return fmt.Errorf("output schema validation failed: %w", err)
	}
	if !res.Valid {
		return fmt.Errorf("output schema validation failed: %v", res.Errors)
	}

	return nil
}

// generateReport generates the inspection report in the requested format
func generateReport(cmd *cobra.Command, report *InspectReportV0, opts *InspectOptions) error {
	var output io.Writer
	if opts.Output != "" {
		file, err := os.OpenFile(opts.Output, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			return fmt.Errorf("failed to open output file: %w", err)
		}
		defer func() { _ = file.Close() }()
		output = file
	} else {
		output = cmd.OutOrStdout()
	}

	switch opts.Format {
	case "json":
		return generateJSONReport(output, report)
	case "markdown":
		return generateMarkdownReport(output, report)
	default:
		return fmt.Errorf("unsupported format: %s", opts.Format)
	}
}

func generateJSONReport(output io.Writer, report *InspectReportV0) error {
	encoder := json.NewEncoder(output)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func generateMarkdownReport(output io.Writer, report *InspectReportV0) error {
	if _, err := fmt.Fprintf(output, "# XML Inspection Report\n\n"); err != nil {
		return fmt.Errorf("failed to write report header: %w", err)
	}
	if _, err := fmt.Fprintf(output, "**Size:** %.2f MB\n\n", float64(report.Input.SizeBytes)/1024/1024); err != nil {
		return fmt.Errorf("failed to write size info: %w", err)
	}
	if _, err := fmt.Fprintf(output, "**Encoding:** %s\n\n", report.Input.EncodingDetected); err != nil {
		return fmt.Errorf("failed to write encoding info: %w", err)
	}

	if report.Input.EncodingForced != nil {
		if _, err := fmt.Fprintf(output, "**Encoding Forced:** %s\n\n", *report.Input.EncodingForced); err != nil {
			return fmt.Errorf("failed to write encoding forced: %w", err)
		}
	}

	if report.Input.Compressed {
		if _, err := fmt.Fprintf(output, "**⚠️ Compressed Input:** %s compression detected. Manifest-based parallelization may be limited.\n\n", report.Input.Compression); err != nil {
			return fmt.Errorf("failed to write compression warning: %w", err)
		}
	}

	if _, err := fmt.Fprintf(output, "## Performance\n\n"); err != nil {
		return fmt.Errorf("failed to write performance header: %w", err)
	}
	if _, err := fmt.Fprintf(output, "- **Duration:** %d ms\n", report.Metrics.ElapsedMs); err != nil {
		return fmt.Errorf("failed to write duration: %w", err)
	}
	if report.Metrics.ThroughputBytesPerSec > 0 {
		if _, err := fmt.Fprintf(output, "- **Throughput:** %.2f MB/s\n", report.Metrics.ThroughputBytesPerSec/1024/1024); err != nil {
			return fmt.Errorf("failed to write throughput: %w", err)
		}
	}
	if report.Metrics.RssPeakMb != nil {
		if _, err := fmt.Fprintf(output, "- **Memory Peak:** %.2f MB\n\n", *report.Metrics.RssPeakMb); err != nil {
			return fmt.Errorf("failed to write memory peak: %w", err)
		}
	}

	if _, err := fmt.Fprintf(output, "## Top Paths\n\n"); err != nil {
		return fmt.Errorf("failed to write top paths header: %w", err)
	}
	if len(report.Paths) == 0 {
		if _, err := fmt.Fprintf(output, "*No paths analyzed yet*\n\n"); err != nil {
			return fmt.Errorf("failed to write no paths message: %w", err)
		}
	} else {
		if _, err := fmt.Fprintf(output, "| Path | Count | Attributes | Samples |\n"); err != nil {
			return fmt.Errorf("failed to write table header: %w", err)
		}
		if _, err := fmt.Fprintf(output, "|------|-------|------------|---------|\n"); err != nil {
			return fmt.Errorf("failed to write table separator: %w", err)
		}

		for _, path := range report.Paths {
			attrCount := len(path.Attributes)
			sampleCount := len(path.Samples)
			if _, err := fmt.Fprintf(output, "| %s | %d | %d | %d |\n",
				path.Path, path.Count, attrCount, sampleCount); err != nil {
				return fmt.Errorf("failed to write path row: %w", err)
			}
		}
		if _, err := fmt.Fprintf(output, "\n"); err != nil {
			return fmt.Errorf("failed to write newline: %w", err)
		}
	}

	// Show attributes if available
	for _, path := range report.Paths {
		if len(path.Attributes) > 0 {
			if _, err := fmt.Fprintf(output, "### Attributes for %s\n\n", path.Path); err != nil {
				return fmt.Errorf("failed to write attributes header: %w", err)
			}
			if _, err := fmt.Fprintf(output, "| Attribute | Count |\n"); err != nil {
				return fmt.Errorf("failed to write attributes table header: %w", err)
			}
			if _, err := fmt.Fprintf(output, "|-----------|-------|\n"); err != nil {
				return fmt.Errorf("failed to write attributes table separator: %w", err)
			}

			for _, attr := range path.Attributes {
				if _, err := fmt.Fprintf(output, "| %s | %d |\n", attr.Name, attr.Count); err != nil {
					return fmt.Errorf("failed to write attribute row: %w", err)
				}
			}
			if _, err := fmt.Fprintf(output, "\n"); err != nil {
				return fmt.Errorf("failed to write attributes newline: %w", err)
			}
		}
	}

	// Show text samples if available
	for _, path := range report.Paths {
		if len(path.Samples) > 0 {
			if _, err := fmt.Fprintf(output, "### Samples for %s\n\n", path.Path); err != nil {
				return fmt.Errorf("failed to write samples header: %w", err)
			}
			for _, sample := range path.Samples {
				if _, err := fmt.Fprintf(output, "- `%s`\n", sample); err != nil {
					return fmt.Errorf("failed to write sample: %w", err)
				}
			}
			if _, err := fmt.Fprintf(output, "\n"); err != nil {
				return fmt.Errorf("failed to write samples newline: %w", err)
			}
		}
	}

	// Record Boundary Analysis section
	if len(report.RecordCandidates) > 0 {
		analysis := &RecordBoundaryAnalysis{
			Candidates:        report.RecordCandidates,
			StreamingAnalysis: report.StreamingAnalysis,
			OOMSummary:        report.OOMSummary,
		}
		if err := writeRecordAnalysisSection(output, analysis); err != nil {
			return fmt.Errorf("failed to write record analysis section: %w", err)
		}
	}

	if report.Metadata != nil {
		if _, err := fmt.Fprintf(output, "## Metadata\n\n"); err != nil {
			return fmt.Errorf("failed to write metadata header: %w", err)
		}
		if _, err := fmt.Fprintf(output, "- **Generator:** %s\n", report.Metadata.Generator); err != nil {
			return fmt.Errorf("failed to write generator: %w", err)
		}
		if _, err := fmt.Fprintf(output, "- **Timestamp:** %s\n", report.Metadata.Timestamp); err != nil {
			return fmt.Errorf("failed to write timestamp: %w", err)
		}
	}

	return nil
}

// writeRecordAnalysisSection writes the record boundary analysis section to markdown
func writeRecordAnalysisSection(output io.Writer, analysis *RecordBoundaryAnalysis) error {
	if _, err := fmt.Fprintf(output, "## Record Boundary Analysis\n\n"); err != nil {
		return err
	}

	// Streaming suitability
	suitability := "Not Recommended"
	if analysis.StreamingAnalysis.SuitableForStreaming {
		suitability = "Recommended"
	}
	if _, err := fmt.Fprintf(output, "**Streaming Suitability:** %s\n\n", suitability); err != nil {
		return err
	}

	// Top candidates table
	if len(analysis.Candidates) > 0 {
		if _, err := fmt.Fprintf(output, "### Top Record Candidates\n\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "| XPath | Count | Avg Size (KB) | Max Size (KB) |\n"); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "|-------|-------|----------------|---------------|\n"); err != nil {
			return err
		}

		// Show top 5 candidates
		limit := 5
		if len(analysis.Candidates) < limit {
			limit = len(analysis.Candidates)
		}

		for i := 0; i < limit; i++ {
			candidate := analysis.Candidates[i]
			if _, err := fmt.Fprintf(output, "| `%s` | %d | %.1f | %d |\n",
				candidate.XPath,
				candidate.Count,
				candidate.SizeStats.AvgKB,
				candidate.SizeStats.MaxKB); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(output, "\n"); err != nil {
			return err
		}
	}

	// Warnings from analysis
	for _, warning := range analysis.StreamingAnalysis.Warnings {
		if _, err := fmt.Fprintf(output, "**⚠️ %s:** %s\n", warning.Severity, warning.Message); err != nil {
			return err
		}
		if warning.Recommendation != "" {
			if _, err := fmt.Fprintf(output, "**Recommendation:** %s\n", warning.Recommendation); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(output, "\n"); err != nil {
			return err
		}
	}

	// OOM warnings
	if analysis.OOMSummary != nil && analysis.OOMSummary.LargeRecordCount > 0 {
		if _, err := fmt.Fprintf(output, "**⚠️ Large Records:** %d records exceed %d MB threshold (max: %d KB).\n",
			analysis.OOMSummary.LargeRecordCount,
			analysis.OOMSummary.ThresholdMB,
			analysis.OOMSummary.MaxSizeKB); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(output, "**Recommendation:** Increase `--oom-threshold-mb` or decompress file for better parallelization.\n\n"); err != nil {
			return err
		}
	}

	return nil
}

// NEW: Phase 1.2 Record Analysis Functions

// analyzeRecordBoundaries performs record boundary analysis for streaming assessment
func analyzeRecordBoundaries(reader io.Reader, selector string, encoding string, opts AnalysisOptions, logger *zap.Logger) (*RecordBoundaryAnalysis, error) {
	logger.Info("Starting record boundary analysis",
		zap.String("selector", selector),
		zap.Int("max_records", opts.MaxRecords))

	// CRITICAL: Use size-only scanner for constant-memory analysis
	// No XML buffering or serialization - just track offsets, sizes, and counts
	scanner, err := createRecordScannerSizeOnly(reader, selector, encoding)
	if err != nil {
		return nil, fmt.Errorf("failed to create record scanner: %w", err)
	}

	// Track element statistics
	elementStats := make(map[string]*ElementStats)
	var recordNum int64
	var totalBytes int64

	// CRITICAL: Constant-memory analysis - no buffering
	// Scan records and collect statistics without storing full XML
	for {
		record, err := scanner.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("record scanning failed: %w", err)
		}

		recordNum++
		totalBytes += record.SizeBytes

		// CRITICAL: Early bail-out on very large records to avoid OOM
		if record.SizeBytes > int64(opts.OOMThresholdMB*1024*1024) {
			logger.Warn("Skipping very large record to avoid OOM",
				zap.Int64("record_num", recordNum),
				zap.Int64("size_mb", record.SizeBytes/1024/1024),
				zap.Int("threshold_mb", opts.OOMThresholdMB))
			continue
		}

		// Get element name from record (available in size-only mode)
		elementName := record.ElementName
		if elementName == "" {
			logger.Warn("Missing element name in record")
			continue
		}

		// Update statistics
		if _, exists := elementStats[elementName]; !exists {
			elementStats[elementName] = &ElementStats{
				Name:          elementName,
				XPath:         "//" + elementName,
				Count:         0,
				Depth:         record.Depth, // Use actual depth from scanner
				Histogram:     NewHistogram(),
				SampleOffsets: []int64{},
			}
		}

		stats := elementStats[elementName]
		stats.Count++
		stats.Histogram.Add(record.SizeBytes)

		// Track sample offsets (ring buffer, max 3)
		if len(stats.SampleOffsets) < 3 {
			stats.SampleOffsets = append(stats.SampleOffsets, record.StartOffset)
		} else {
			// Replace oldest sample
			stats.SampleOffsets[int(recordNum%3)] = record.StartOffset
		}

		// Track first offset
		if stats.FirstOffset == 0 {
			stats.FirstOffset = record.StartOffset
		}

		// Check OOM threshold
		if record.SizeBytes > int64(opts.OOMThresholdMB*1024*1024) {
			stats.LargeRecords = append(stats.LargeRecords, LargeRecord{
				RecordNum:   int(recordNum),
				SizeKB:      record.SizeBytes / 1024,
				OffsetStart: record.StartOffset,
			})
		}

		// Respect max records limit
		if opts.MaxRecords > 0 && recordNum >= int64(opts.MaxRecords) {
			logger.Info("Reached max records limit", zap.Int64("limit", int64(opts.MaxRecords)))
			break
		}

		// Progress reporting
		if recordNum%10000 == 0 {
			logger.Info("Analyzed records",
				zap.Int64("count", recordNum),
				zap.Int64("bytes", totalBytes))
		}
	}

	// Convert to candidates
	candidates := make([]RecordCandidate, 0, len(elementStats))
	for _, stats := range elementStats {
		candidates = append(candidates, RecordCandidate{
			Element:       stats.Name,
			XPath:         stats.XPath,
			Count:         stats.Count,
			Depth:         stats.Depth,
			SizeStats:     calculateSizeStatistics(stats.Histogram),
			FirstOffset:   stats.FirstOffset,
			SampleOffsets: stats.SampleOffsets,
		})
	}

	// Sort candidates by count (descending)
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Count > candidates[j].Count
	})

	// Limit candidates
	if len(candidates) > opts.MaxCandidates {
		candidates = candidates[:opts.MaxCandidates]
	}

	// Perform streaming suitability assessment
	streamingAnalysis := assessStreamingSuitability(candidates, opts.OOMThresholdMB, opts.Compressed, opts.Compression, logger)

	// Generate OOM summary if needed
	var oomSummary *OOMSummary
	if hasLargeRecords(elementStats) {
		oomSummary = generateOOMSummary(elementStats, opts.OOMThresholdMB)
	}

	logger.Info("Record boundary analysis completed",
		zap.Int("candidates_found", len(candidates)),
		zap.Int64("records_analyzed", recordNum))

	return &RecordBoundaryAnalysis{
		Candidates:        candidates,
		StreamingAnalysis: streamingAnalysis,
		OOMSummary:        oomSummary,
	}, nil
}

// ElementStats tracks statistics for a single element type
type ElementStats struct {
	Name          string
	XPath         string
	Count         int
	Depth         int
	Histogram     *Histogram
	SampleOffsets []int64
	FirstOffset   int64
	LargeRecords  []LargeRecord
}

// createRecordScannerSizeOnly creates a size-only record scanner for analysis
func createRecordScannerSizeOnly(reader io.Reader, selector string, encoding string) (*streaming.RecordScanner, error) {
	scanner := streaming.NewRecordScannerSizeOnlyWithEncoding(reader, selector, encoding)
	return scanner, nil
}

// calculateSizeStatistics calculates size statistics from histogram
func calculateSizeStatistics(h *Histogram) SizeStats {
	return SizeStats{
		AvgKB: h.Average() / 1024.0,
		P50KB: h.Percentile(50.0) / 1024,
		P95KB: h.Percentile(95.0) / 1024,
		P99KB: h.Percentile(99.0) / 1024,
		MaxKB: h.Max() / 1024,
		MinKB: h.Min() / 1024,
	}
}

// assessStreamingSuitability evaluates streaming suitability based on candidates
func assessStreamingSuitability(candidates []RecordCandidate, oomThresholdMB int, compressed bool, compression string, logger *zap.Logger) StreamingAnalysis {
	if logger != nil {
		logger.Info("Assessing streaming suitability", zap.Int("candidates", len(candidates)))
	}

	// Early return if no candidates
	if len(candidates) == 0 {
		return StreamingAnalysis{
			SuitableForStreaming: false,
			RecommendedSelector:  "",
			Confidence:           "low",
			Reasoning:            "No record candidates found",
			Warnings:             []Warning{},
			MemoryEstimates: MemoryEstimates{
				StreamingTypicalMB: 50,
				StreamingWorstMB:   100,
				NonStreamingGB:     1,
			},
			PerformanceEstimates: PerformanceEstimates{
				StreamingSequential: "~1 hour",
				ManifestParallel32x: "~5 minutes",
			},
		}
	}

	// Find top-level candidates (depth == 1)
	var topLevelCandidates []RecordCandidate
	for _, candidate := range candidates {
		if candidate.Depth == 1 {
			topLevelCandidates = append(topLevelCandidates, candidate)
		}
	}

	// If no top-level candidates, use all candidates
	if len(topLevelCandidates) == 0 {
		topLevelCandidates = candidates
		if logger != nil {
			logger.Warn("No top-level candidates found, using all candidates")
		}
	}

	// Select best candidate
	var bestCandidate *RecordCandidate
	if len(topLevelCandidates) > 0 {
		bestCandidate = &topLevelCandidates[0] // Already sorted by count
	}

	// Evaluate suitability
	suitable := false
	confidence := "low"
	reasoning := ""
	var warnings []Warning

	// Add compressed file warning if applicable
	// Note: compression info not available here, would need to pass from caller

	if bestCandidate != nil {
		// Apply heuristics from audit recommendations
		if bestCandidate.Count >= 10000 &&
			bestCandidate.SizeStats.P95KB <= 200 &&
			bestCandidate.SizeStats.MaxKB <= 5120 {
			suitable = true
			confidence = "high"
			reasoning = fmt.Sprintf("%d top-level records, avg %.1fKB each, p95 %dKB",
				bestCandidate.Count, bestCandidate.SizeStats.AvgKB, bestCandidate.SizeStats.P95KB)
		} else if bestCandidate.Count >= 1000 {
			suitable = true
			confidence = "medium"
			reasoning = fmt.Sprintf("%d records found, but size distribution may be challenging", bestCandidate.Count)
		} else {
			confidence = "low"
			reasoning = fmt.Sprintf("Only %d records found, may not benefit from streaming", bestCandidate.Count)
		}

		// Add warnings for large records
		if bestCandidate.SizeStats.MaxKB > 100*1024 { // > 100MB
			warnings = append(warnings, Warning{
				Severity:       "critical",
				Message:        fmt.Sprintf("Max record size %dMB exceeds safe processing threshold", bestCandidate.SizeStats.MaxKB/1024),
				Recommendation: "Increase --oom-threshold-mb or decompress file before processing",
			})
		} else if bestCandidate.SizeStats.MaxKB > 10*1024 { // > 10MB
			warnings = append(warnings, Warning{
				Severity:       "warning",
				Message:        fmt.Sprintf("Max record size %dMB exceeds recommended threshold", bestCandidate.SizeStats.MaxKB/1024),
				Recommendation: "Monitor memory usage during extraction or consider decompression",
			})
		}

		// Add warning for compressed inputs
		if compressed {
			warnings = append(warnings, Warning{
				Severity:       "info",
				Message:        fmt.Sprintf("%s compression detected", compression),
				Recommendation: "Manifest-based parallelization may be limited",
			})
		}
	} else {
		reasoning = "No suitable record candidates found"
	}

	// Memory estimates
	streamingTypicalMB := 50 // Default estimate when no candidates
	streamingWorstMB := 100  // Default estimate when no candidates
	if bestCandidate != nil {
		streamingTypicalMB = int(bestCandidate.SizeStats.AvgKB * 2) // Rough estimate
		streamingWorstMB = int(bestCandidate.SizeStats.MaxKB * 2)   // Rough estimate
	}
	nonStreamingGB := 100 // Placeholder - would be calculated from file size

	// Performance estimates
	streamingSequential := "~8 hours"    // Placeholder
	manifestParallel32x := "~15 minutes" // Placeholder

	return StreamingAnalysis{
		SuitableForStreaming: suitable,
		RecommendedSelector:  getRecommendedSelector(bestCandidate),
		Confidence:           confidence,
		Reasoning:            reasoning,
		Warnings:             warnings,
		MemoryEstimates: MemoryEstimates{
			StreamingTypicalMB: streamingTypicalMB,
			StreamingWorstMB:   streamingWorstMB,
			NonStreamingGB:     nonStreamingGB,
		},
		PerformanceEstimates: PerformanceEstimates{
			StreamingSequential: streamingSequential,
			ManifestParallel32x: manifestParallel32x,
		},
	}
}

// getRecommendedSelector returns XPath for best candidate
func getRecommendedSelector(candidate *RecordCandidate) string {
	if candidate == nil {
		return ""
	}
	return candidate.XPath
}

// hasLargeRecords checks if any element has large records
func hasLargeRecords(elementStats map[string]*ElementStats) bool {
	for _, stats := range elementStats {
		if len(stats.LargeRecords) > 0 {
			return true
		}
	}
	return false
}

// generateOOMSummary creates OOM summary from element statistics
func generateOOMSummary(elementStats map[string]*ElementStats, thresholdMB int) *OOMSummary {
	var allLargeRecords []LargeRecord
	var maxSizeKB int64
	var largeRecordCount int

	for _, stats := range elementStats {
		allLargeRecords = append(allLargeRecords, stats.LargeRecords...)
		largeRecordCount += len(stats.LargeRecords)

		for _, record := range stats.LargeRecords {
			if record.SizeKB > maxSizeKB {
				maxSizeKB = record.SizeKB
			}
		}
	}

	// Sort and limit to 5 largest records
	sort.Slice(allLargeRecords, func(i, j int) bool {
		return allLargeRecords[i].SizeKB > allLargeRecords[j].SizeKB
	})

	if len(allLargeRecords) > 5 {
		allLargeRecords = allLargeRecords[:5]
	}

	return &OOMSummary{
		ThresholdMB:      thresholdMB,
		LargeRecordCount: largeRecordCount,
		MaxSizeKB:        maxSizeKB,
		LargestRecords:   allLargeRecords,
	}
}
