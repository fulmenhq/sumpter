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
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/net/html/charset"
	"gopkg.in/yaml.v3"
)

type InspectOptions struct {
	File           string
	Output         string
	Format         string
	MaxPaths       int
	SamplesPerPath int
	ForceEncoding  string
	Progress       bool
	IncludeAttrs   bool
	ValidateOutput bool
}

// InspectReportV0 matches the v0.1.0 schema
type InspectReportV0 struct {
	Version  string           `json:"version"`
	Input    InspectInput     `json:"input"`
	Metrics  InspectMetrics   `json:"metrics"`
	Paths    []InspectPath    `json:"paths"`
	Caps     InspectCaps      `json:"caps"`
	Metadata *InspectMetadata `json:"metadata,omitempty"`
}

type InspectInput struct {
	Path             string  `json:"path"`
	SizeBytes        int64   `json:"size_bytes"`
	EncodingDetected string  `json:"encoding_detected"`
	EncodingForced   *string `json:"encoding_forced,omitempty"`
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

	// Perform inspection
	report, err := inspectXML(cr, fileInfo, encodingInfo, opts)
	if err != nil {
		return fmt.Errorf("inspection failed: %w", err)
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
	if err := generateReport(cmd, report, opts); err != nil {
		return err
	}

	// Optional output validation
	if opts.ValidateOutput && opts.Format == "json" {
		// Marshal to bytes and validate with goneat schema library
		data, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("failed to marshal report for validation: %w", err)
		}
		// Read schema file (YAML format)
		schemaYAMLBytes, err := os.ReadFile("schemas/inspect/v0.1.0/inspect-report.schema.yaml")
		if err != nil {
			return fmt.Errorf("failed to read inspect schema: %w", err)
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
		if err := json.Unmarshal(data, &dataInterface); err != nil {
			return fmt.Errorf("failed to parse report JSON: %w", err)
		}
		// Validate
		res, err := schema.ValidateFromBytes(schemaBytes, dataInterface)
		if err != nil {
			return fmt.Errorf("output schema validation failed: %w", err)
		}
		if !res.Valid {
			// Show first validation error for debugging
			if len(res.Errors) > 0 {
				return fmt.Errorf("output invalid against schema: %s", res.Errors[0].Message)
			}
			return fmt.Errorf("output invalid against schema: %d error(s)", len(res.Errors))
		}
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
	}
	if opts.ForceEncoding != "" {
		input.EncodingForced = &opts.ForceEncoding
	}

	// Build caps (sample truncation inferred)
	caps := InspectCaps{
		PathsTruncated:      pathsTruncated,
		AttributesTruncated: false,
		SamplesTruncated:    samplesTruncatedAny,
	}

	report := &InspectReportV0{
		Version: "inspect-report/v0.1.0",
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

	return report, nil
}

func generateReport(cmd *cobra.Command, report *InspectReportV0, opts *InspectOptions) error {
	var output io.Writer
	if opts.Output != "" {
		file, err := os.Create(opts.Output)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
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
	if _, err := fmt.Fprintf(output, "**File:** %s\n\n", report.Input.Path); err != nil {
		return fmt.Errorf("failed to write file info: %w", err)
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
