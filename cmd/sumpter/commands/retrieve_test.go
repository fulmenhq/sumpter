package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
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

func TestEnsureWritableTargetRejectsDirectory(t *testing.T) {
	tmpDir := t.TempDir()

	err := ensureWritableTarget(tmpDir)
	if err == nil {
		t.Fatal("ensureWritableTarget() expected error for directory target, got nil")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Fatalf("ensureWritableTarget() error = %v, want directory message", err)
	}
}

func TestEnsureWritableTargetRejectsMissingParent(t *testing.T) {
	target := filepath.Join(t.TempDir(), "missing", "results.txt")

	err := ensureWritableTarget(target)
	if err == nil {
		t.Fatal("ensureWritableTarget() expected error for missing parent, got nil")
	}
	if !strings.Contains(err.Error(), "parent directory does not exist") {
		t.Fatalf("ensureWritableTarget() error = %v, want missing parent message", err)
	}
}

func TestEnsureWritableTargetDoesNotTruncateExistingFile(t *testing.T) {
	target := filepath.Join(t.TempDir(), "results.txt")
	const sentinel = "existing content\n"
	if err := os.WriteFile(target, []byte(sentinel), 0600); err != nil {
		t.Fatalf("failed to write target file: %v", err)
	}

	if err := ensureWritableTarget(target); err != nil {
		t.Fatalf("ensureWritableTarget() unexpected error = %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("failed to read target file: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("target content = %q, want %q", string(got), sentinel)
	}
}

func TestRetrieveFind_Stdout_NoOutputPath(t *testing.T) {
	searchRoot := makeRetrieveFindTree(t)

	output := captureStdout(t, func() {
		err := runFind(&RetrieveOptions{Flatten: true}, searchRoot, "*.xml", "", 0, false, "text", "", false)
		if err != nil {
			t.Fatalf("runFind() unexpected error = %v", err)
		}
	})

	if !strings.Contains(output, "a.xml\n") {
		t.Fatalf("stdout output = %q, want a.xml match", output)
	}
	if strings.Contains(output, "b.txt") {
		t.Fatalf("stdout output = %q, did not expect b.txt", output)
	}
}

func TestRetrieveFind_OutputPath_Text(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)
	outputPath := filepath.Join(workspace, "results.txt")

	err := runFind(&RetrieveOptions{Flatten: true}, searchRoot, "*.xml", "", 0, false, "text", outputPath, false)
	if err != nil {
		t.Fatalf("runFind() unexpected error = %v", err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if !strings.Contains(string(got), "a.xml\n") || !strings.Contains(string(got), filepath.Join("nested", "c.xml")+"\n") {
		t.Fatalf("output file = %q, want XML matches", string(got))
	}
	if strings.Contains(string(got), "b.txt") {
		t.Fatalf("output file = %q, did not expect b.txt", string(got))
	}
}

func TestRetrieveFind_OutputPath_JSON(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)
	outputPath := filepath.Join(workspace, "results.json")

	err := runFind(&RetrieveOptions{Flatten: true}, searchRoot, "*.xml", "", 0, false, "json", outputPath, false)
	if err != nil {
		t.Fatalf("runFind() unexpected error = %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}

	var matches []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(data, &matches); err != nil {
		t.Fatalf("output file is not a valid JSON document: %v; data=%q", err, string(data))
	}
	if len(matches) != 2 {
		t.Fatalf("matches len = %d, want 2; matches=%v", len(matches), matches)
	}
	if matches[0].Path == "" || matches[1].Path == "" {
		t.Fatalf("matches = %v, want populated paths", matches)
	}
}

func TestRetrieveFind_NoMatches(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)
	outputPath := filepath.Join(workspace, "results.txt")

	err := runFind(&RetrieveOptions{Flatten: true}, searchRoot, "*.csv", "", 0, false, "text", outputPath, false)
	if err != nil {
		t.Fatalf("runFind() unexpected error = %v", err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("output file = %q, want empty file for no text matches", string(got))
	}
}

func TestRetrieveFind_NoMatches_JSON(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)
	outputPath := filepath.Join(workspace, "results.json")

	err := runFind(&RetrieveOptions{Flatten: true}, searchRoot, "*.csv", "", 0, false, "json", outputPath, false)
	if err != nil {
		t.Fatalf("runFind() unexpected error = %v", err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	if strings.TrimSpace(string(got)) != "[]" {
		t.Fatalf("output file = %q, want empty JSON array", string(got))
	}

	var matches []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(got, &matches); err != nil {
		t.Fatalf("output file is not a valid JSON array: %v; data=%q", err, string(got))
	}
	if len(matches) != 0 {
		t.Fatalf("matches len = %d, want 0; matches=%v", len(matches), matches)
	}
}

func TestRetrieveFind_OutputPath_RejectsDirectory(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)

	err := runFind(&RetrieveOptions{}, searchRoot, "*.xml", "", 0, false, "text", workspace, false)
	if err == nil {
		t.Fatal("runFind() expected directory target error, got nil")
	}
	if !strings.Contains(err.Error(), "output path validation failed") {
		t.Fatalf("runFind() error = %v, want output path validation failure", err)
	}
}

func TestRetrieveFind_OutputPath_RejectsUnwritableParent(t *testing.T) {
	workspace := t.TempDir()
	t.Chdir(workspace)
	searchRoot := makeRetrieveFindTree(t)
	outputPath := filepath.Join(workspace, "missing", "results.txt")

	err := runFind(&RetrieveOptions{}, searchRoot, "*.xml", "", 0, false, "text", outputPath, false)
	if err == nil {
		t.Fatal("runFind() expected missing parent error, got nil")
	}
	if !strings.Contains(err.Error(), "parent directory does not exist") {
		t.Fatalf("runFind() error = %v, want missing parent message", err)
	}
}

func makeRetrieveFindTree(t *testing.T) string {
	t.Helper()

	root := t.TempDir()
	files := map[string]string{
		"a.xml":                          "<root/>",
		"b.txt":                          "not xml",
		filepath.Join("nested", "c.xml"): "<root/>",
	}
	for name, content := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0750); err != nil {
			t.Fatalf("failed to create parent for %s: %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatalf("failed to write %s: %v", path, err)
		}
	}
	return root
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	originalStdout := os.Stdout
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to create pipe: %v", err)
	}
	os.Stdout = writer
	defer func() {
		os.Stdout = originalStdout
	}()

	fn()

	if err := writer.Close(); err != nil {
		t.Fatalf("failed to close pipe writer: %v", err)
	}
	output, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read captured stdout: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("failed to close pipe reader: %v", err)
	}
	return string(output)
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
