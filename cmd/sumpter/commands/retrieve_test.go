package commands

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRetrieveCommand(t *testing.T) {
	cmd := NewRetrieveCommand()

	if cmd == nil {
		t.Fatal("expected retrieve command, got nil")
	}

	if cmd.Use != "retrieve" {
		t.Errorf("Use = %q, want 'retrieve'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Short description is empty")
	}

	if cmd.Long == "" {
		t.Error("Long description is empty")
	}

	// Check that subcommands exist
	subCommands := cmd.Commands()
	if len(subCommands) == 0 {
		t.Error("expected subcommands, got none")
	}
}

func TestRetrieveCommandFlags(t *testing.T) {
	cmd := NewRetrieveCommand()

	// Check persistent flags
	outputBaseFlag := cmd.PersistentFlags().Lookup("output-base")
	if outputBaseFlag == nil {
		t.Error("expected 'output-base' persistent flag to be defined")
	}

	configPathFlag := cmd.PersistentFlags().Lookup("config-path")
	if configPathFlag == nil {
		t.Error("expected 'config-path' persistent flag to be defined")
	}
}

func TestRetrieveSubcommands(t *testing.T) {
	tests := []struct {
		name string
		use  string
	}{
		{"copy subcommand", "copy"},
		{"find subcommand", "find"},
	}

	retrieveCmd := NewRetrieveCommand()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, subCmd := range retrieveCmd.Commands() {
				if subCmd.Use == tt.use || (len(subCmd.Use) > len(tt.use) && subCmd.Use[:len(tt.use)] == tt.use) {
					found = true
					if subCmd.Short == "" {
						t.Errorf("subcommand %q has empty Short description", tt.use)
					}
					break
				}
			}
			if !found {
				t.Errorf("subcommand %q not found", tt.use)
			}
		})
	}
}

func TestValidateReadablePath(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	tests := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{
			name:    "valid file path",
			path:    testFile,
			wantErr: false,
		},
		{
			name:    "empty path",
			path:    "",
			wantErr: true,
		},
		{
			name:    "nonexistent path",
			path:    filepath.Join(tmpDir, "nonexistent.txt"),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReadablePath(tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateReadablePath() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateReadableDir(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file (not a directory)
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test"), 0600); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	// Create test subdirectory
	testSubdir := filepath.Join(tmpDir, "subdir")
	if err := os.Mkdir(testSubdir, 0755); err != nil {
		t.Fatalf("failed to create test subdirectory: %v", err)
	}

	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{
			name:    "valid directory",
			dir:     testSubdir,
			wantErr: false,
		},
		{
			name:    "empty path",
			dir:     "",
			wantErr: true,
		},
		{
			name:    "nonexistent directory",
			dir:     filepath.Join(tmpDir, "nonexistent"),
			wantErr: true,
		},
		{
			name:    "file not directory",
			dir:     testFile,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateReadableDir(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateReadableDir() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateWritableDir(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name    string
		dir     string
		wantErr bool
	}{
		{
			name:    "valid writable directory",
			dir:     filepath.Join(tmpDir, "writable"),
			wantErr: false,
		},
		{
			name:    "nested directory creation",
			dir:     filepath.Join(tmpDir, "nested", "deep", "dir"),
			wantErr: false,
		},
		{
			name:    "empty path",
			dir:     "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWritableDir(tt.dir)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWritableDir() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Clean up created directories
			if err == nil && tt.dir != "" {
				_ = os.RemoveAll(tt.dir)
			}
		})
	}
}

func TestValidateWritableFile(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name     string
		filePath string
		wantErr  bool
	}{
		{
			name:     "valid file path",
			filePath: filepath.Join(tmpDir, "test.txt"),
			wantErr:  false,
		},
		{
			name:     "nested file path",
			filePath: filepath.Join(tmpDir, "nested", "file.txt"),
			wantErr:  false,
		},
		{
			name:     "empty path",
			filePath: "",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWritableFile(tt.filePath)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWritableFile() error = %v, wantErr %v", err, tt.wantErr)
			}

			// Clean up created files
			if err == nil && tt.filePath != "" {
				_ = os.Remove(tt.filePath)
				_ = os.RemoveAll(filepath.Dir(tt.filePath))
			}
		})
	}
}

func TestCopyCommandStructure(t *testing.T) {
	cmd := NewRetrieveCommand()

	var copyCmd *testing.T
	for _, subCmd := range cmd.Commands() {
		if subCmd.Use == "copy <source> <destination>" || len(subCmd.Use) >= 4 && subCmd.Use[:4] == "copy" {
			// Found copy command
			if subCmd.Short == "" {
				t.Error("copy command has empty Short description")
			}
			if subCmd.Long == "" {
				t.Error("copy command has empty Long description")
			}
			return
		}
	}

	if copyCmd == nil {
		t.Error("copy subcommand not found")
	}
}

func TestFindCommandStructure(t *testing.T) {
	cmd := NewRetrieveCommand()

	var findCmd *testing.T
	for _, subCmd := range cmd.Commands() {
		if subCmd.Use == "find" {
			// Found find command
			if subCmd.Short == "" {
				t.Error("find command has empty Short description")
			}
			if subCmd.Long == "" {
				t.Error("find command has empty Long description")
			}

			// Check that required flags exist
			flags := []string{"input-path", "include-pattern", "exclude-pattern", "max-depth", "follow-symlinks", "format", "output-path", "progress", "flatten"}
			for _, flagName := range flags {
				if subCmd.Flags().Lookup(flagName) == nil {
					t.Errorf("find command missing flag %q", flagName)
				}
			}
			return
		}
	}

	if findCmd == nil {
		t.Error("find subcommand not found")
	}
}

func TestRetrieveOptions(t *testing.T) {
	opts := &RetrieveOptions{
		OutputBase: "/tmp/test",
		Flatten:    true,
		ConfigPath: "/etc/config.yaml",
	}

	if opts.OutputBase != "/tmp/test" {
		t.Errorf("OutputBase = %q, want '/tmp/test'", opts.OutputBase)
	}

	if !opts.Flatten {
		t.Error("Flatten should be true")
	}

	if opts.ConfigPath != "/etc/config.yaml" {
		t.Errorf("ConfigPath = %q, want '/etc/config.yaml'", opts.ConfigPath)
	}
}
