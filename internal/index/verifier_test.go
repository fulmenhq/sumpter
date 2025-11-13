package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifier_Verify_Valid(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test XML file
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?>
<root>
	<Record>Data1</Record>
	<Record>Data2</Record>
</root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	// Build index
	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}
	builder := NewBuilder(buildOpts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Write index to file
	indexPath := filepath.Join(tmpDir, "test.index.json")
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Verify index
	verifyOpts := VerifyOptions{
		InputPath:     xmlPath,
		IndexPath:     indexPath,
		VerifyRecords: false, // Don't verify individual records for this basic test
	}
	verifier := NewVerifier(verifyOpts)
	result, err := verifier.Verify()
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	// Check results
	if !result.Valid {
		t.Errorf("Expected valid index, got invalid: %s", result.ErrorMessage)
	}

	if !result.SourceSizeMatch {
		t.Error("Expected source size to match")
	}

	if !result.SourceHashMatch {
		t.Error("Expected source hash to match")
	}
}

func TestVerifier_Verify_TamperedFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test XML file
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?><root><Record>Data</Record></root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	// Build index
	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}
	builder := NewBuilder(buildOpts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Write index
	indexPath := filepath.Join(tmpDir, "test.index.json")
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Tamper with XML file
	tamperedContent := `<?xml version="1.0"?><root><Record>Tampered Data</Record></root>`
	if err := os.WriteFile(xmlPath, []byte(tamperedContent), 0644); err != nil {
		t.Fatalf("Failed to tamper with XML: %v", err)
	}

	// Verify index should fail
	verifyOpts := VerifyOptions{
		InputPath:     xmlPath,
		IndexPath:     indexPath,
		VerifyRecords: false,
	}
	verifier := NewVerifier(verifyOpts)
	result, err := verifier.Verify()
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	// Should detect tampering
	if result.Valid {
		t.Error("Expected verification to fail for tampered file")
	}

	if result.SourceHashMatch {
		t.Error("Expected source hash mismatch for tampered file")
	}

	if result.ErrorMessage == "" {
		t.Error("Expected error message for failed verification")
	}
}

func TestVerifier_Verify_SizeMismatch(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test XML file
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?><root><Record>Data</Record></root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	// Build index
	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}
	builder := NewBuilder(buildOpts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Write index
	indexPath := filepath.Join(tmpDir, "test.index.json")
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Modify file size
	longerContent := xmlContent + "\n<!-- Extra comment -->"
	if err := os.WriteFile(xmlPath, []byte(longerContent), 0644); err != nil {
		t.Fatalf("Failed to modify XML: %v", err)
	}

	// Verify index should fail on size mismatch
	verifyOpts := VerifyOptions{
		InputPath:     xmlPath,
		IndexPath:     indexPath,
		VerifyRecords: false,
	}
	verifier := NewVerifier(verifyOpts)
	result, err := verifier.Verify()
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	if result.Valid {
		t.Error("Expected verification to fail for size mismatch")
	}

	if result.SourceSizeMatch {
		t.Error("Expected source size mismatch")
	}
}

func TestVerifier_Verify_WithRecords(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test XML file
	xmlPath := filepath.Join(tmpDir, "test.xml")
	xmlContent := `<?xml version="1.0"?>
<root>
	<Record>First Record</Record>
	<Record>Second Record</Record>
	<Record>Third Record</Record>
</root>`
	if err := os.WriteFile(xmlPath, []byte(xmlContent), 0644); err != nil {
		t.Fatalf("Failed to create test XML: %v", err)
	}

	// Build index
	buildOpts := BuildOptions{
		InputPath:      xmlPath,
		Selector:       "//Record",
		SumpterVersion: "0.1.2-test",
	}
	builder := NewBuilder(buildOpts)
	index, err := builder.Build()
	if err != nil {
		t.Fatalf("Failed to build index: %v", err)
	}

	// Write index
	indexPath := filepath.Join(tmpDir, "test.index.json")
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Verify with record verification enabled
	verifyOpts := VerifyOptions{
		InputPath:     xmlPath,
		IndexPath:     indexPath,
		VerifyRecords: true,
		FailFast:      false,
	}
	verifier := NewVerifier(verifyOpts)
	result, err := verifier.Verify()
	if err != nil {
		t.Fatalf("Verify failed: %v", err)
	}

	if !result.Valid {
		t.Errorf("Expected valid index, got invalid: %s", result.ErrorMessage)
	}

	if result.RecordsVerified != 3 {
		t.Errorf("Expected 3 records verified, got %d", result.RecordsVerified)
	}

	if len(result.RecordErrors) > 0 {
		t.Errorf("Expected no record errors, got %d: %v",
			len(result.RecordErrors), result.RecordErrors)
	}
}

func TestVerifier_Verify_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create index pointing to non-existent file
	indexPath := filepath.Join(tmpDir, "test.index.json")
	index := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              "/nonexistent/file.xml",
			SizeBytes:         100,
			SHA256:            "abc123",
			Compressed:        false,
			CompressionFormat: "none",
		},
		Selector: SelectorInfo{
			XPath:       "//Record",
			ElementName: "Record",
		},
		Records: []RecordMetadata{},
		Summary: SummaryStats{},
		Metadata: IndexMetadata{
			Generator: "test",
		},
	}

	builder := NewBuilder(BuildOptions{})
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Verify should fail with file not found
	verifyOpts := VerifyOptions{
		InputPath:     "/nonexistent/file.xml",
		IndexPath:     indexPath,
		VerifyRecords: false,
	}
	verifier := NewVerifier(verifyOpts)
	result, err := verifier.Verify()
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}

	if result.Valid {
		t.Error("Expected verification to fail for missing file")
	}

	if result.ErrorMessage == "" {
		t.Error("Expected error message for missing file")
	}
}

func TestLoadIndex(t *testing.T) {
	tmpDir := t.TempDir()

	// Create and write a test index
	index := &RecordIndex{
		Version: SchemaVersion,
		Source: SourceInfo{
			Path:              "/test/file.xml",
			SizeBytes:         1024,
			SHA256:            "abc123",
			Compressed:        false,
			CompressionFormat: "none",
		},
		Selector: SelectorInfo{
			XPath:       "//Record",
			ElementName: "Record",
		},
		Records: []RecordMetadata{
			{
				RecordNum:   1,
				StartOffset: 0,
				EndOffset:   100,
				SizeBytes:   100,
				SHA256:      "def456",
				ElementName: "Record",
				Depth:       1,
			},
		},
		Summary: SummaryStats{
			TotalRecords:       1,
			TotalBytes:         100,
			AvgRecordSizeBytes: 100.0,
			MinRecordSizeBytes: 100,
			MaxRecordSizeBytes: 100,
		},
		Metadata: IndexMetadata{
			Generator: "test",
		},
	}

	indexPath := filepath.Join(tmpDir, "test.index.json")
	builder := NewBuilder(BuildOptions{})
	if err := builder.WriteToFile(index, indexPath); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}

	// Load index using public utility function
	loaded, err := LoadIndex(indexPath)
	if err != nil {
		t.Fatalf("Failed to load index: %v", err)
	}

	// Verify loaded content
	if loaded.Version != index.Version {
		t.Errorf("Version mismatch: expected %s, got %s", index.Version, loaded.Version)
	}

	if loaded.Summary.TotalRecords != index.Summary.TotalRecords {
		t.Errorf("TotalRecords mismatch: expected %d, got %d",
			index.Summary.TotalRecords, loaded.Summary.TotalRecords)
	}

	if len(loaded.Records) != len(index.Records) {
		t.Errorf("Records count mismatch: expected %d, got %d",
			len(index.Records), len(loaded.Records))
	}
}
