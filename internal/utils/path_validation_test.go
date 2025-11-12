package utils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateUserPathWithRoot_TraversalAttacks tests that directory traversal attacks are blocked
func TestValidateUserPathWithRoot_TraversalAttacks(t *testing.T) {
	// Create a temporary root directory for testing
	tmpRoot := t.TempDir()

	tests := []struct {
		name      string
		userPath  string
		root      AllowedRoot
		rootDir   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid path within root",
			userPath: filepath.Join(tmpRoot, "data", "file.xml"),
			root:     RootHome,
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "valid relative path within root",
			userPath: filepath.Join(tmpRoot, "subdir", "file.txt"),
			root:     RootHome,
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:      "absolute path outside root",
			userPath:  "/etc/passwd",
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "relative traversal attack",
			userPath:  filepath.Join(tmpRoot, "..", "..", "etc", "passwd"),
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "traversal with absolute path",
			userPath:  filepath.Join(tmpRoot, "data", "..", "..", "..", "etc", "passwd"),
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "system directory /dev",
			userPath:  "/dev/null",
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed", // System dirs are caught by path validation first
		},
		{
			name:      "system directory /proc",
			userPath:  "/proc/self/environ",
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "system directory /sys",
			userPath:  "/sys/class/net",
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "empty path",
			userPath:  "",
			root:      RootHome,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path cannot be empty",
		},
		{
			name:     "path exactly at root",
			userPath: tmpRoot,
			root:     RootHome,
			rootDir:  tmpRoot,
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserPathWithRoot(tt.userPath, tt.root, tt.rootDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUserPathWithRoot() expected error, got nil")
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateUserPathWithRoot() error = %v, want substring %q", err, tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUserPathWithRoot() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestValidateUserPathWithRoot_SymlinkEscape tests that symlink escapes are prevented
func TestValidateUserPathWithRoot_SymlinkEscape(t *testing.T) {
	// Create a temporary root directory
	tmpRoot := t.TempDir()

	// Create a subdirectory inside root
	subDir := filepath.Join(tmpRoot, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	// Create a symlink that points outside the root
	linkPath := filepath.Join(subDir, "escape_link")
	targetPath := "/etc"

	// Only run this test if we can create symlinks (may fail on Windows without admin)
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("Cannot create symlinks (may need admin on Windows): %v", err)
	}

	// Try to access a file through the symlink escape
	escapePath := filepath.Join(linkPath, "passwd")

	err := ValidateUserPathWithRoot(escapePath, RootHome, tmpRoot)
	if err == nil {
		t.Error("ValidateUserPathWithRoot() should block symlink escape, got nil error")
	} else if !strings.Contains(err.Error(), "path outside allowed") {
		t.Errorf("ValidateUserPathWithRoot() error should mention 'path outside allowed', got: %v", err)
	}
}

// TestValidateUserPathForRead tests read-specific validation
func TestValidateUserPathForRead(t *testing.T) {
	tmpRoot := t.TempDir()

	// Create a test file
	testFile := filepath.Join(tmpRoot, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a test directory
	testDir := filepath.Join(tmpRoot, "testdir")
	if err := os.Mkdir(testDir, 0755); err != nil {
		t.Fatalf("Failed to create test directory: %v", err)
	}

	tests := []struct {
		name      string
		userPath  string
		rootDir   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid file read",
			userPath: testFile,
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:      "directory not file",
			userPath:  testDir,
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "points to directory",
		},
		{
			name:      "non-existent file",
			userPath:  filepath.Join(tmpRoot, "nonexistent.txt"),
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "file access error",
		},
		{
			name:      "path outside root",
			userPath:  "/etc/passwd",
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserPathForRead(tt.userPath, RootHome, tt.rootDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUserPathForRead() expected error, got nil")
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateUserPathForRead() error = %v, want substring %q", err, tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUserPathForRead() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestValidateUserPathForWrite tests write-specific validation
func TestValidateUserPathForWrite(t *testing.T) {
	tmpRoot := t.TempDir()

	tests := []struct {
		name      string
		userPath  string
		rootDir   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid write path with existing parent",
			userPath: filepath.Join(tmpRoot, "output.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "valid write path with non-existent parent",
			userPath: filepath.Join(tmpRoot, "newdir", "output.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:      "path outside root",
			userPath:  "/etc/evil.txt",
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
		{
			name:      "traversal attack in write",
			userPath:  filepath.Join(tmpRoot, "..", "..", "tmp", "evil.txt"),
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserPathForWrite(tt.userPath, RootHome, tt.rootDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUserPathForWrite() expected error, got nil")
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateUserPathForWrite() error = %v, want substring %q", err, tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUserPathForWrite() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestValidateUserPathForCreate tests create-specific validation
func TestValidateUserPathForCreate(t *testing.T) {
	tmpRoot := t.TempDir()

	tests := []struct {
		name      string
		userPath  string
		rootDir   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "valid create path",
			userPath: filepath.Join(tmpRoot, "newfile.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "valid create path with nested dirs",
			userPath: filepath.Join(tmpRoot, "a", "b", "c", "file.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:      "path outside root",
			userPath:  "/etc/new.txt",
			rootDir:   tmpRoot,
			wantErr:   true,
			errSubstr: "path outside allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserPathForCreate(tt.userPath, RootHome, tt.rootDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUserPathForCreate() expected error, got nil")
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateUserPathForCreate() error = %v, want substring %q", err, tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUserPathForCreate() unexpected error = %v", err)
				}
			}
		})
	}
}

// TestAllowedRoot_String tests the String() method for AllowedRoot
func TestAllowedRoot_String(t *testing.T) {
	tests := []struct {
		root AllowedRoot
		want string
	}{
		{RootHome, "SUMPTER_HOME"},
		{RootWork, "SUMPTER_WORKDIR"},
		{RootCwd, "current working directory"},
		{AllowedRoot(999), "unknown root"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.root.String()
			if got != tt.want {
				t.Errorf("AllowedRoot.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateUserPathWithRoot_EdgeCases tests edge cases and boundary conditions
func TestValidateUserPathWithRoot_EdgeCases(t *testing.T) {
	tmpRoot := t.TempDir()

	tests := []struct {
		name      string
		userPath  string
		rootDir   string
		wantErr   bool
		errSubstr string
	}{
		{
			name:     "path with dots in filename",
			userPath: filepath.Join(tmpRoot, "file.with.dots.xml"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "path with spaces",
			userPath: filepath.Join(tmpRoot, "file with spaces.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "path with unicode characters",
			userPath: filepath.Join(tmpRoot, "文件.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "deeply nested path",
			userPath: filepath.Join(tmpRoot, "a", "b", "c", "d", "e", "f", "file.txt"),
			rootDir:  tmpRoot,
			wantErr:  false,
		},
		{
			name:     "path with multiple consecutive slashes",
			userPath: filepath.Join(tmpRoot, "a", "", "", "b", "file.txt"),
			rootDir:  tmpRoot,
			wantErr:  false, // filepath.Join normalizes these
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateUserPathWithRoot(tt.userPath, RootHome, tt.rootDir)

			if tt.wantErr {
				if err == nil {
					t.Errorf("ValidateUserPathWithRoot() expected error, got nil")
				} else if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Errorf("ValidateUserPathWithRoot() error = %v, want substring %q", err, tt.errSubstr)
				}
			} else {
				if err != nil {
					t.Errorf("ValidateUserPathWithRoot() unexpected error = %v", err)
				}
			}
		})
	}
}
