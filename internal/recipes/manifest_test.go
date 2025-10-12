package recipes

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadManifestApplyDefaults(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "recipe.yaml")
	content := `version: recipe/v0.1.0
kind: extract
id: test_recipe
assets:
  signature: signature/test-signature.yaml
  extract: extract/test-extract.yaml
`

	if err := os.WriteFile(manifestPath, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	manifest, err := LoadManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadManifest failed: %v", err)
	}

	if manifest.Defaults.Input.IncludePattern != "*.xml" {
		t.Fatalf("expected default include pattern '*.xml', got %q", manifest.Defaults.Input.IncludePattern)
	}
	if manifest.Defaults.Output.Pattern != "extract-{}.json" {
		t.Fatalf("expected default output pattern 'extract-{}.json', got %q", manifest.Defaults.Output.Pattern)
	}
	if manifest.Defaults.Workers != 1 {
		t.Fatalf("expected default workers = 1, got %d", manifest.Defaults.Workers)
	}
}
