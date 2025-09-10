package commands

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInspectReportV0_JSON_Schema(t *testing.T) {
	// Create a sample report
	report := &InspectReportV0{
		Version: "inspect-report/v0.1.0",
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
	if parsed.Version != "inspect-report/v0.1.0" {
		t.Errorf("Expected version 'inspect-report/v0.1.0', got %s", parsed.Version)
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
	if report.Version != "inspect-report/v0.1.0" {
		t.Errorf("Expected version 'inspect-report/v0.1.0', got %s", report.Version)
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
