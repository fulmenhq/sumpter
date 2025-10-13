package recipes

import (
	"io/fs"
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

// TestResolvePath tests path resolution relative to workspace
func TestResolvePath(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		candidate string
		want      string
	}{
		{
			name:      "empty candidate",
			base:      "/workspace",
			candidate: "",
			want:      "",
		},
		{
			name:      "absolute path unchanged",
			base:      "/workspace",
			candidate: "/absolute/path/file.yaml",
			want:      "/absolute/path/file.yaml",
		},
		{
			name:      "relative path joined",
			base:      "/workspace",
			candidate: "signature/test.yaml",
			want:      "/workspace/signature/test.yaml",
		},
		{
			name:      "nested relative path",
			base:      "/recipes/naxml",
			candidate: "assets/extract/cpjr.yaml",
			want:      "/recipes/naxml/assets/extract/cpjr.yaml",
		},
		{
			name:      "single file",
			base:      "/workspace",
			candidate: "config.yaml",
			want:      "/workspace/config.yaml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolvePath(tt.base, tt.candidate)
			if got != tt.want {
				t.Errorf("ResolvePath(%q, %q) = %q, want %q", tt.base, tt.candidate, got, tt.want)
			}
		})
	}
}

// TestOpenRelativeFile tests secure file opening within workspace
func TestOpenRelativeFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test files
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0o600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	subdir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(subdir, 0o755); err != nil {
		t.Fatalf("failed to create subdir: %v", err)
	}
	nestedFile := filepath.Join(subdir, "nested.txt")
	if err := os.WriteFile(nestedFile, []byte("nested content"), 0o600); err != nil {
		t.Fatalf("failed to create nested file: %v", err)
	}

	tests := []struct {
		name      string
		base      string
		candidate string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "open file in workspace",
			base:      tmpDir,
			candidate: "test.txt",
			wantErr:   false,
		},
		{
			name:      "open nested file",
			base:      tmpDir,
			candidate: "subdir/nested.txt",
			wantErr:   false,
		},
		{
			name:      "empty path",
			base:      tmpDir,
			candidate: "",
			wantErr:   true,
			errMsg:    "empty path",
		},
		{
			name:      "path escape with ..",
			base:      tmpDir,
			candidate: "../../../etc/passwd",
			wantErr:   true,
			errMsg:    "escapes workspace",
		},
		{
			name:      "nonexistent file",
			base:      tmpDir,
			candidate: "does-not-exist.txt",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := OpenRelativeFile(tt.base, tt.candidate)
			if (err != nil) != tt.wantErr {
				t.Errorf("OpenRelativeFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("error message %q does not contain %q", err.Error(), tt.errMsg)
				}
			}

			if file != nil {
				_ = file.Close() // Clean up
			}
		})
	}
}

// TestListAssets tests asset path collection from manifest
func TestListAssets(t *testing.T) {
	tests := []struct {
		name     string
		manifest *Manifest
		base     string
		want     []string
	}{
		{
			name: "all assets populated",
			manifest: &Manifest{
				Assets: Assets{
					Signature:  "sig/test.yaml",
					Extract:    "extract/test.yaml",
					Validation: "validation/rules.yaml",
					Retrieve:   "retrieve/config.yaml",
					Extras:     []string{"docs/readme.md", "scripts/process.sh"},
				},
			},
			base: "/workspace",
			want: []string{
				"/workspace/sig/test.yaml",
				"/workspace/extract/test.yaml",
				"/workspace/validation/rules.yaml",
				"/workspace/retrieve/config.yaml",
				"/workspace/docs/readme.md",
				"/workspace/scripts/process.sh",
			},
		},
		{
			name: "only required assets",
			manifest: &Manifest{
				Assets: Assets{
					Signature: "signature.yaml",
					Extract:   "extract.yaml",
				},
			},
			base: "/base",
			want: []string{
				"/base/signature.yaml",
				"/base/extract.yaml",
			},
		},
		{
			name: "empty strings ignored",
			manifest: &Manifest{
				Assets: Assets{
					Signature:  "sig.yaml",
					Extract:    "ext.yaml",
					Validation: "",
					Retrieve:   "  ",
					Extras:     []string{"", "  ", "valid.txt"},
				},
			},
			base: "/test",
			want: []string{
				"/test/sig.yaml",
				"/test/ext.yaml",
				"/test/valid.txt",
			},
		},
		{
			name: "no extras",
			manifest: &Manifest{
				Assets: Assets{
					Signature: "s.yaml",
					Extract:   "e.yaml",
					Extras:    nil,
				},
			},
			base: "/workspace",
			want: []string{
				"/workspace/s.yaml",
				"/workspace/e.yaml",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.manifest.ListAssets(tt.base)

			if len(got) != len(tt.want) {
				t.Fatalf("ListAssets() returned %d assets, want %d\nGot: %v\nWant: %v",
					len(got), len(tt.want), got, tt.want)
			}

			for i, wantPath := range tt.want {
				if got[i] != wantPath {
					t.Errorf("asset[%d] = %q, want %q", i, got[i], wantPath)
				}
			}
		})
	}
}

// TestWorkspaceFilesystem tests fs.FS creation
func TestWorkspaceFilesystem(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0o600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	fsys, err := WorkspaceFilesystem(tmpDir)
	if err != nil {
		t.Fatalf("WorkspaceFilesystem() error = %v", err)
	}

	if fsys == nil {
		t.Fatal("expected fs.FS, got nil")
	}

	// Verify we can read from the filesystem
	data, err := fs.ReadFile(fsys, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	if string(data) != "test" {
		t.Errorf("file content = %q, want 'test'", string(data))
	}
}

// TestManifestValidation tests validation error cases
func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name         string
		manifestYAML string
		wantErr      bool
		errContains  string
	}{
		{
			name: "missing version",
			manifestYAML: `kind: extract
id: test_recipe
assets:
  signature: sig.yaml
  extract: ext.yaml`,
			wantErr:     true,
			errContains: "version is required",
		},
		{
			name: "invalid version",
			manifestYAML: `version: recipe/v99.99.99
kind: extract
id: test_recipe
assets:
  signature: sig.yaml
  extract: ext.yaml`,
			wantErr:     true,
			errContains: "version",
		},
		{
			name: "missing id",
			manifestYAML: `version: recipe/v0.1.0
kind: extract
assets:
  signature: sig.yaml
  extract: ext.yaml`,
			wantErr:     true,
			errContains: "id is required",
		},
		{
			name: "invalid kind",
			manifestYAML: `version: recipe/v0.1.0
kind: invalid_kind
id: test_recipe
assets:
  signature: sig.yaml
  extract: ext.yaml`,
			wantErr:     true,
			errContains: "kind",
		},
		{
			name: "extract missing signature",
			manifestYAML: `version: recipe/v0.1.0
kind: extract
id: test_recipe
assets:
  extract: ext.yaml`,
			wantErr:     true,
			errContains: "signature is required",
		},
		{
			name: "extract missing extract asset",
			manifestYAML: `version: recipe/v0.1.0
kind: extract
id: test_recipe
assets:
  signature: sig.yaml`,
			wantErr:     true,
			errContains: "extract is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			manifestPath := filepath.Join(tmpDir, "recipe.yaml")

			if err := os.WriteFile(manifestPath, []byte(tt.manifestYAML), 0o644); err != nil {
				t.Fatalf("failed to write manifest: %v", err)
			}

			_, err := LoadManifest(manifestPath)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadManifest() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if err != nil && tt.errContains != "" {
				if !contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
			}
		})
	}
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
