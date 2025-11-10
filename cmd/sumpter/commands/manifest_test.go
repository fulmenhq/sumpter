package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManifestBuildCommand(t *testing.T) {
	t.Parallel()

	// Create temporary directory with test assets
	tempDir := createWorkingTempDir(t)

	// Create test signature config
	sigContent := `signature_id: "test-sig"
name: "Test Signature"
description: "Test signature for manifest build"
status: "active"
match_patterns:
  - pattern_id: "test"
    selector: "//test"
    weight: 1.0
confidence_threshold: 0.8
format_type: "xml"`

	sigPath := filepath.Join(tempDir, "test-signature.yaml")
	if err := os.WriteFile(sigPath, []byte(sigContent), 0o644); err != nil {
		t.Fatalf("failed to write signature config: %v", err)
	}

	// Create test extract config
	extContent := `record_type: "test_record"
match_selectors:
  - xpath: "//record"
field_mappings:
  - output_field: "id"
    xpath: "@id"
    type: "string"
output_schema:
  type: "object"
  properties:
    id:
      type: "string"
  required:
    - "id"`

	extPath := filepath.Join(tempDir, "test-extract.yaml")
	if err := os.WriteFile(extPath, []byte(extContent), 0o644); err != nil {
		t.Fatalf("failed to write extract config: %v", err)
	}

	// Test build command
	manifestPath := filepath.Join(tempDir, "test-manifest.yaml")
	err := runManifestBuild(manifestPath, "test-recipe", "extract", false)
	if err != nil {
		t.Fatalf("runManifestBuild failed: %v", err)
	}

	// Verify manifest was created
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		t.Fatalf("manifest file was not created")
	}

	// Test verify command
	err = runManifestVerify(manifestPath, false)
	if err != nil {
		t.Fatalf("runManifestVerify failed: %v", err)
	}

	// Test verify with strict mode
	err = runManifestVerify(manifestPath, true)
	if err != nil {
		t.Fatalf("runManifestVerify (strict) failed: %v", err)
	}
}

func TestManifestBuildOverwriteProtection(t *testing.T) {
	t.Parallel()

	tempDir := createWorkingTempDir(t)

	// Create initial manifest
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	err := runManifestBuild(manifestPath, "test-recipe", "extract", false)
	if err != nil {
		t.Fatalf("initial build failed: %v", err)
	}

	// Try to build again without force - should fail
	err = runManifestBuild(manifestPath, "test-recipe-2", "extract", false)
	if err == nil {
		t.Fatalf("expected error when overwriting without force")
	}

	// Try to build again with force - should succeed
	err = runManifestBuild(manifestPath, "test-recipe-2", "extract", true)
	if err != nil {
		t.Fatalf("build with force failed: %v", err)
	}
}

func TestManifestVerifyNonexistentFile(t *testing.T) {
	t.Parallel()

	tempDir := createWorkingTempDir(t)

	// Try to verify nonexistent manifest
	manifestPath := filepath.Join(tempDir, "nonexistent.yaml")
	err := runManifestVerify(manifestPath, false)
	if err == nil {
		t.Fatalf("expected error for nonexistent manifest")
	}
}

func TestManifestBuildUnsupportedKind(t *testing.T) {
	t.Parallel()

	tempDir := createWorkingTempDir(t)

	// Try to build with unsupported kind
	manifestPath := filepath.Join(tempDir, "manifest.yaml")
	err := runManifestBuild(manifestPath, "test-recipe", "unsupported", false)
	if err == nil {
		t.Fatalf("expected error for unsupported kind")
	}
}
