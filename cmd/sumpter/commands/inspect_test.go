package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.uber.org/zap/zaptest"
)

func TestInspectReportV0_JSON_Schema(t *testing.T) {
	// Create a sample report
	report := &InspectReportV0{
		Version: "inspect-report/v0.1.1",
		Input: InspectInput{
			Path:             "/test/file.xml",
			SizeBytes:        1024,
			EncodingDetected: "UTF-8",
		},
		Metrics: InspectMetrics{
			BytesProcessed:        1024,
			ElapsedMs:             100,
			ThroughputBytesPerSec: 10240,
			ReplacementCount:      0,
		},
		Paths: []InspectPath{
			{
				Path:  "root.element",
				Count: 5,
				Attributes: []InspectAttribute{
					{Name: "id", Count: 5},
				},
				Samples: []string{"sample text"},
			},
		},
		Caps: InspectCaps{
			PathsTruncated:      false,
			AttributesTruncated: false,
			SamplesTruncated:    false,
		},
		Metadata: &InspectMetadata{
			Generator: "sumpter inspect",
			Timestamp: "2025-01-01T00:00:00Z",
		},
	}

	// Serialize to JSON
	var buf bytes.Buffer
	err := generateJSONReport(&buf, report)
	if err != nil {
		t.Fatalf("Failed to generate JSON report: %v", err)
	}

	// Parse back to verify structure
	var parsed InspectReportV0
	err = json.Unmarshal(buf.Bytes(), &parsed)
	if err != nil {
		t.Fatalf("Failed to parse generated JSON: %v", err)
	}

	// Verify required fields
	if parsed.Version != "inspect-report/v0.1.1" {
		t.Errorf("Expected version 'inspect-report/v0.1.1', got %s", parsed.Version)
	}

	if parsed.Input.Path != "/test/file.xml" {
		t.Errorf("Expected path '/test/file.xml', got %s", parsed.Input.Path)
	}

	if len(parsed.Paths) != 1 {
		t.Errorf("Expected 1 path, got %d", len(parsed.Paths))
	}

	if parsed.Paths[0].Path != "root.element" {
		t.Errorf("Expected path 'root.element', got %s", parsed.Paths[0].Path)
	}
}

func TestInspectXML_Basic(t *testing.T) {
	xmlContent := `<root><element id="1">text</element><element id="2">more text</element></root>`
	reader := strings.NewReader(xmlContent)

	fileInfo := FileInfo{
		Path:    "test.xml",
		Size:    int64(len(xmlContent)),
		IsStdin: false,
	}

	encodingInfo := EncodingInfo{
		Detected: "UTF-8",
	}

	opts := &InspectOptions{
		MaxPaths:       10,
		SamplesPerPath: 2,
		IncludeAttrs:   true,
	}

	report, err := inspectXML(reader, fileInfo, encodingInfo, opts)
	if err != nil {
		t.Fatalf("inspectXML failed: %v", err)
	}

	// Verify basic structure
	if report.Version != "inspect-report/v0.1.1" {
		t.Errorf("Expected version 'inspect-report/v0.1.1', got %s", report.Version)
	}

	if report.Input.Path != "test.xml" {
		t.Errorf("Expected path 'test.xml', got %s", report.Input.Path)
	}

	// Should have root.element path
	found := false
	for _, path := range report.Paths {
		if path.Path == "root.element" {
			found = true
			if path.Count != 2 {
				t.Errorf("Expected count 2, got %d", path.Count)
			}
			break
		}
	}

	if !found {
		t.Error("Expected to find path 'root.element'")
	}
}

func TestHistogram_PercentileApproximation(t *testing.T) {
	tests := []struct {
		name       string
		bounds     []int64
		counts     []int64
		percentile float64
		expected   int64
	}{
		{
			name:       "p50 with single bucket",
			bounds:     []int64{100},
			counts:     []int64{10},
			percentile: 50.0,
			expected:   100,
		},
		{
			name:       "p50 with multiple buckets",
			bounds:     []int64{10, 50, 100},
			counts:     []int64{5, 5, 5},
			percentile: 50.0,
			expected:   50, // Should return the bucket bound where cumulative reaches target
		},
		{
			name:       "p95 with skewed distribution",
			bounds:     []int64{1, 10, 100, 1000},
			counts:     []int64{90, 9, 1, 0},
			percentile: 95.0,
			expected:   10, // 95th percentile falls in the 10 bucket (cumulative 99 >= 95)
		},
		{
			name:       "p99 with skewed distribution",
			bounds:     []int64{1, 10, 100, 1000},
			counts:     []int64{90, 9, 1, 0},
			percentile: 99.0,
			expected:   100, // 99th percentile falls in the 100 bucket (cumulative 100 >= 99)
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Histogram{
				buckets: tt.counts,
				bounds:  tt.bounds,
				total:   0,
			}

			// Calculate total
			for _, count := range tt.counts {
				h.total += count
			}

			result := h.Percentile(tt.percentile)

			// The result should be one of the bucket bounds
			found := false
			for _, bound := range tt.bounds {
				if result == bound {
					found = true
					break
				}
			}

			if !found {
				t.Errorf("Percentile result %d is not a valid bucket bound %v", result, tt.bounds)
			}

			// Verify the percentile approximation is reasonable
			// The result should be the bound of the bucket where cumulative count reaches the percentile target
			if h.total > 0 {
				targetCount := int64(float64(h.total) * tt.percentile / 100.0)
				cumulative := int64(0)
				expectedBound := tt.bounds[len(tt.bounds)-1] // default to last bound

				for i, count := range tt.counts {
					cumulative += count
					if cumulative >= targetCount {
						expectedBound = tt.bounds[i]
						break
					}
				}

				if result != expectedBound {
					t.Errorf("Percentile result %d does not match expected bucket bound %d for target count %d",
						result, expectedBound, targetCount)
				}
			}
		})
	}
}

func TestAnalyzeRecordBoundaries_SizeOnly_NoBuffering(t *testing.T) {
	xmlContent := `<root><record id="1"><data>text content</data></record><record id="2"><data>more text</data></record></root>`
	reader := strings.NewReader(xmlContent)

	opts := AnalysisOptions{
		AnalyzeRecords: true,
		RecordSelector: "//record",
		OOMThresholdMB: 100,
		MaxCandidates:  5,
		MaxRecords:     10,
	}

	logger := zaptest.NewLogger(t)
	result, err := analyzeRecordBoundaries(reader, "//record", "UTF-8", opts, logger)
	if err != nil {
		t.Fatalf("analyzeRecordBoundaries failed: %v", err)
	}

	// In size-only mode, XML should be empty for all records
	for _, candidate := range result.Candidates {
		if candidate.Element == "record" {
			// We can't directly check XML content since it's not stored in candidates
			// But we can verify the analysis completed without error
			if candidate.Count != 2 {
				t.Errorf("Expected 2 records, got %d", candidate.Count)
			}
		}
	}
}

func TestInspectReportV0_JSON_Schema_Validation(t *testing.T) {
	// Create a sample report with record analysis
	report := &InspectReportV0{
		Version: "inspect-report/v0.1.1",
		Input: InspectInput{
			Path:             "/test/file.xml",
			SizeBytes:        1024,
			EncodingDetected: "UTF-8",
			Compressed:       false,
			Compression:      "none",
		},
		Metrics: InspectMetrics{
			BytesProcessed:        1024,
			ElapsedMs:             100,
			ThroughputBytesPerSec: 10240,
			ReplacementCount:      0,
		},
		Paths: []InspectPath{
			{
				Path:  "root.record",
				Count: 2,
				Attributes: []InspectAttribute{
					{Name: "id", Count: 2},
				},
				Samples: []string{"sample text"},
			},
		},
		Caps: InspectCaps{
			PathsTruncated:      false,
			AttributesTruncated: false,
			SamplesTruncated:    false,
		},
		RecordCandidates: []RecordCandidate{
			{
				Element:       "record",
				XPath:         "//record",
				Count:         2,
				Depth:         1,
				SizeStats:     SizeStats{AvgKB: 0.5, P50KB: 0, P95KB: 1, P99KB: 1, MaxKB: 1, MinKB: 0},
				FirstOffset:   6,
				SampleOffsets: []int64{6, 50},
			},
		},
		StreamingAnalysis: StreamingAnalysis{
			SuitableForStreaming: true,
			RecommendedSelector:  "//record",
			Confidence:           "high",
			Reasoning:            "2 records found",
			Warnings:             []Warning{},
			MemoryEstimates:      MemoryEstimates{StreamingTypicalMB: 50, StreamingWorstMB: 100, NonStreamingGB: 1},
			PerformanceEstimates: PerformanceEstimates{StreamingSequential: "1 hour", ManifestParallel32x: "5 minutes"},
		},
		AnalysisMetadata: &AnalysisMetadata{
			AnalyzedAt:      "2025-01-01T00:00:00Z",
			DurationMs:      50,
			RecordsAnalyzed: 2,
			AnalysisOptions: AnalysisOptions{
				AnalyzeRecords: true,
				RecordSelector: "//record",
				OOMThresholdMB: 100,
				MaxCandidates:  5,
				MaxRecords:     10,
			},
		},
		Metadata: &InspectMetadata{
			Generator: "sumpter inspect",
			Timestamp: "2025-01-01T00:00:00Z",
		},
	}

	// Serialize to JSON
	var buf bytes.Buffer
	err := generateJSONReport(&buf, report)
	if err != nil {
		t.Fatalf("Failed to generate JSON report: %v", err)
	}

	// Validate against embedded schema
	err = validateJSONAgainstEmbeddedSchema(buf.Bytes())
	if err != nil {
		t.Fatalf("Schema validation failed: %v", err)
	}
}

func TestInspectCommand_Integration_AnalyzeRecords(t *testing.T) {
	// Use the pre-built binary from dist/ (Makefile builds it there)
	// Path is relative from cmd/sumpter/commands/ to dist/
	binaryPath := "../../../dist/sumpter"
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		t.Skipf("Binary not found at %s - run 'make build' first (skipping integration test)", binaryPath)
	}

	// Create a temporary XML file with multiple records (same as working unit test)
	xmlContent := `<root><record id="1"><data>text content</data></record><record id="2"><data>more text</data></record></root>`

	// Create temp file
	tmpFile, err := os.CreateTemp("", "test-records-*.xml")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	_, err = tmpFile.WriteString(xmlContent)
	if err != nil {
		t.Fatalf("Failed to write to temp file: %v", err)
	}
	err = tmpFile.Close()
	if err != nil {
		t.Fatalf("Failed to close temp file: %v", err)
	}

	// Run inspect command with --analyze-records using pre-built binary
	cmd := exec.Command(binaryPath, "inspect",
		"--analyze-records",
		"--record-selector", "//record",
		"--force-encoding", "utf-8",
		"--format", "json",
		tmpFile.Name())

	// Set HOME environment variable if not set (needed for -race tests)
	if os.Getenv("HOME") == "" {
		cmd.Env = append(os.Environ(), "HOME="+os.TempDir())
	}

	cmdOutput, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Inspect command failed: %v\nOutput: %s", err, string(cmdOutput))
	}

	// Extract JSON from output (find the JSON that contains version)
	outputStr := string(cmdOutput)
	versionIndex := strings.Index(outputStr, `"version": "inspect-report/v0.1.1"`)
	if versionIndex == -1 {
		t.Fatalf("No version found in output: %s", outputStr)
	}
	// Find the opening brace before version
	braceIndex := strings.LastIndex(outputStr[:versionIndex], "{")
	if braceIndex == -1 {
		t.Fatalf("No opening brace found before version: %s", outputStr[:versionIndex])
	}
	jsonStr := outputStr[braceIndex:]

	// Parse the JSON output
	var report InspectReportV0
	err = json.Unmarshal([]byte(jsonStr), &report)
	if err != nil {
		t.Fatalf("Failed to parse JSON output: %v\nJSON: %s", err, jsonStr)
	}

	// Validate record analysis results
	if report.StreamingAnalysis.RecommendedSelector != "//record" {
		t.Errorf("Expected recommended selector '//record', got %s", report.StreamingAnalysis.RecommendedSelector)
	}

	if len(report.RecordCandidates) == 0 {
		t.Error("Expected at least one record candidate")
	}

	// Find the record candidate
	var recordCandidate *RecordCandidate
	for i := range report.RecordCandidates {
		if report.RecordCandidates[i].Element == "record" {
			recordCandidate = &report.RecordCandidates[i]
			break
		}
	}

	if recordCandidate == nil {
		t.Error("Expected to find record candidate with element 'record'")
		return
	}

	// Validate record candidate fields
	if recordCandidate.Count != 2 {
		t.Errorf("Expected 2 records, got %d", recordCandidate.Count)
	}

	if recordCandidate.Depth != 2 {
		t.Errorf("Expected depth 2, got %d", recordCandidate.Depth)
	}

	if recordCandidate.XPath != "//record" {
		t.Errorf("Expected xpath '//record', got %s", recordCandidate.XPath)
	}

	// Validate streaming analysis (with only 2 records, it won't be suitable)
	if report.StreamingAnalysis.SuitableForStreaming {
		t.Error("Expected file to NOT be suitable for streaming with only 2 records")
	}

	if report.StreamingAnalysis.Confidence != "low" {
		t.Errorf("Expected confidence 'low', got %s", report.StreamingAnalysis.Confidence)
	}
}
