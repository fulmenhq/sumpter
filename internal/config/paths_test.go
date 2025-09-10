package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolvePaths(t *testing.T) {
	// Create a temporary directory for testing
	tempDir := t.TempDir()

	tests := []struct {
		name            string
		homeOverride    string
		workdirOverride string
		setupEnv        func()
		cleanupEnv      func()
		expectError     bool
	}{
		{
			name:        "No overrides, use defaults",
			expectError: false,
		},
		{
			name:         "Home override provided",
			homeOverride: tempDir,
			expectError:  false,
		},
		{
			name:            "Workdir override provided",
			workdirOverride: filepath.Join(tempDir, "custom-work"),
			expectError:     false,
		},
		{
			name:            "Both overrides provided",
			homeOverride:    tempDir,
			workdirOverride: filepath.Join(tempDir, "custom-work"),
			expectError:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment if needed
			if tt.setupEnv != nil {
				tt.setupEnv()
			}

			// Run the test
			paths, err := ResolvePaths(tt.homeOverride, tt.workdirOverride)

			// Cleanup environment if needed
			if tt.cleanupEnv != nil {
				tt.cleanupEnv()
			}

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			// Verify paths are set
			if paths.Home == "" {
				t.Error("Home path is empty")
			}
			if paths.WorkDir == "" {
				t.Error("WorkDir path is empty")
			}
			if paths.Cache == "" {
				t.Error("Cache path is empty")
			}
			if paths.Logs == "" {
				t.Error("Logs path is empty")
			}
			if paths.Configs == "" {
				t.Error("Configs path is empty")
			}
			if paths.Temp == "" {
				t.Error("Temp path is empty")
			}

			// Verify subdirectory structure
			if !strings.HasPrefix(paths.Cache, paths.Home) {
				t.Errorf("Cache path %s should be under home %s", paths.Cache, paths.Home)
			}
			if !strings.HasPrefix(paths.Logs, paths.Home) {
				t.Errorf("Logs path %s should be under home %s", paths.Logs, paths.Home)
			}
			if !strings.HasPrefix(paths.Configs, paths.Home) {
				t.Errorf("Configs path %s should be under home %s", paths.Configs, paths.Home)
			}
			if !strings.HasPrefix(paths.Temp, paths.WorkDir) {
				t.Errorf("Temp path %s should be under workdir %s", paths.Temp, paths.WorkDir)
			}

			// Verify directories exist
			dirs := []string{paths.Home, paths.WorkDir, paths.Cache, paths.Logs, paths.Configs, paths.Temp}
			for _, dir := range dirs {
				if _, err := os.Stat(dir); os.IsNotExist(err) {
					t.Errorf("Directory %s was not created", dir)
				}
			}
		})
	}
}

func TestResolveHomeDir(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		override     string
		envValue     string
		expectError  bool
		expectedPath string
	}{
		{
			name:         "Override provided",
			override:     tempDir,
			expectError:  false,
			expectedPath: tempDir,
		},
		{
			name:         "No override, use env var",
			envValue:     tempDir,
			expectError:  false,
			expectedPath: tempDir,
		},
		{
			name:        "No override, no env var, use OS default",
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			if tt.envValue != "" {
				oldValue := os.Getenv("SUMPTER_HOME")
				os.Setenv("SUMPTER_HOME", tt.envValue)
				defer func() {
					if oldValue == "" {
						os.Unsetenv("SUMPTER_HOME")
					} else {
						os.Setenv("SUMPTER_HOME", oldValue)
					}
				}()
			} else {
				os.Unsetenv("SUMPTER_HOME")
			}

			// Run the test
			path, err := resolveHomeDir(tt.override)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.expectedPath != "" && path != tt.expectedPath {
				t.Errorf("Expected path %s, got %s", tt.expectedPath, path)
			}

			// Verify path is absolute
			if !filepath.IsAbs(path) {
				t.Errorf("Path %s is not absolute", path)
			}
		})
	}
}

func TestResolveWorkDir(t *testing.T) {
	tempDir := t.TempDir()

	tests := []struct {
		name         string
		override     string
		envValue     string
		home         string
		expectError  bool
		expectedPath string
	}{
		{
			name:         "Override provided",
			override:     tempDir,
			home:         t.TempDir(),
			expectError:  false,
			expectedPath: tempDir,
		},
		{
			name:         "No override, use env var",
			envValue:     tempDir,
			home:         t.TempDir(),
			expectError:  false,
			expectedPath: tempDir,
		},
		{
			name:         "No override, no env var, use home/work",
			home:         tempDir,
			expectError:  false,
			expectedPath: filepath.Join(tempDir, "work"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup environment
			if tt.envValue != "" {
				oldValue := os.Getenv("SUMPTER_WORKDIR")
				os.Setenv("SUMPTER_WORKDIR", tt.envValue)
				defer func() {
					if oldValue == "" {
						os.Unsetenv("SUMPTER_WORKDIR")
					} else {
						os.Setenv("SUMPTER_WORKDIR", oldValue)
					}
				}()
			} else {
				os.Unsetenv("SUMPTER_WORKDIR")
			}

			// Run the test
			path, err := resolveWorkDir(tt.override, tt.home)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if tt.expectedPath != "" && path != tt.expectedPath {
				t.Errorf("Expected path %s, got %s", tt.expectedPath, path)
			}

			// Verify path is absolute
			if !filepath.IsAbs(path) {
				t.Errorf("Path %s is not absolute", path)
			}
		})
	}
}

func TestGetOSUserDataDir(t *testing.T) {
	path, err := getOSUserDataDir()

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}

	if path == "" {
		t.Error("Path is empty")
		return
	}

	// Verify path is absolute
	if !filepath.IsAbs(path) {
		t.Errorf("Path %s is not absolute", path)
	}

	// Verify path structure based on OS
	switch runtime.GOOS {
	case "darwin":
		if !strings.Contains(path, "Library/Application Support") {
			t.Errorf("macOS path %s doesn't contain expected structure", path)
		}
	case "windows":
		if !strings.Contains(path, "AppData") {
			t.Errorf("Windows path %s doesn't contain AppData", path)
		}
	case "linux":
		// Linux could use either XDG_DATA_HOME or HOME/.local/share
		if !strings.Contains(path, ".local/share") && !strings.Contains(path, "share/") {
			t.Errorf("Linux path %s doesn't contain expected structure", path)
		}
	}
}

func TestCreateDirectories(t *testing.T) {
	tempDir := t.TempDir()

	paths := &Paths{
		Home:    filepath.Join(tempDir, "home"),
		WorkDir: filepath.Join(tempDir, "work"),
		Cache:   filepath.Join(tempDir, "home", "cache"),
		Logs:    filepath.Join(tempDir, "home", "logs"),
		Configs: filepath.Join(tempDir, "home", "configs"),
		Temp:    filepath.Join(tempDir, "work", "temp"),
	}

	err := createDirectories(paths)
	if err != nil {
		t.Errorf("Unexpected error creating directories: %v", err)
		return
	}

	// Verify all directories exist
	dirs := []string{paths.Home, paths.WorkDir, paths.Cache, paths.Logs, paths.Configs, paths.Temp}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", dir)
		}
	}
}

func TestPathsMethods(t *testing.T) {
	tempDir := t.TempDir()

	paths := &Paths{
		Home:    tempDir,
		WorkDir: filepath.Join(tempDir, "work"),
		Cache:   filepath.Join(tempDir, "cache"),
		Logs:    filepath.Join(tempDir, "logs"),
		Configs: filepath.Join(tempDir, "configs"),
		Temp:    filepath.Join(tempDir, "work", "temp"),
	}

	// Test GetDefaultConfigPath
	configPath := paths.GetDefaultConfigPath()
	expectedConfigPath := filepath.Join(paths.Configs, "sumpter.yaml")
	if configPath != expectedConfigPath {
		t.Errorf("GetDefaultConfigPath() = %s, expected %s", configPath, expectedConfigPath)
	}

	// Test GetLoggerConfigPath
	loggerPath := paths.GetLoggerConfigPath()
	expectedLoggerPath := filepath.Join(paths.Configs, "logger.yaml")
	if loggerPath != expectedLoggerPath {
		t.Errorf("GetLoggerConfigPath() = %s, expected %s", loggerPath, expectedLoggerPath)
	}

	// Test GetPIIConfigPath
	piiPath := paths.GetPIIConfigPath()
	expectedPIIPath := filepath.Join(paths.Configs, "pii.yaml")
	if piiPath != expectedPIIPath {
		t.Errorf("GetPIIConfigPath() = %s, expected %s", piiPath, expectedPIIPath)
	}

	// Test GetDefaultLogPath
	logPath := paths.GetDefaultLogPath()
	expectedLogPath := filepath.Join(paths.Logs, "sumpter.log")
	if logPath != expectedLogPath {
		t.Errorf("GetDefaultLogPath() = %s, expected %s", logPath, expectedLogPath)
	}

	// Test GetCachePath
	cacheFile := paths.GetCachePath("test.txt")
	expectedCacheFile := filepath.Join(paths.Cache, "test.txt")
	if cacheFile != expectedCacheFile {
		t.Errorf("GetCachePath('test.txt') = %s, expected %s", cacheFile, expectedCacheFile)
	}

	// Test GetTempPath
	tempFile := paths.GetTempPath("temp.txt")
	expectedTempFile := filepath.Join(paths.Temp, "temp.txt")
	if tempFile != expectedTempFile {
		t.Errorf("GetTempPath('temp.txt') = %s, expected %s", tempFile, expectedTempFile)
	}
}

func TestEnsurePaths(t *testing.T) {
	tempDir := t.TempDir()

	paths := &Paths{
		Home:    filepath.Join(tempDir, "home"),
		WorkDir: filepath.Join(tempDir, "work"),
		Cache:   filepath.Join(tempDir, "home", "cache"),
		Logs:    filepath.Join(tempDir, "home", "logs"),
		Configs: filepath.Join(tempDir, "home", "configs"),
		Temp:    filepath.Join(tempDir, "work", "temp"),
	}

	// Remove directories to test creation
	os.RemoveAll(paths.Home)
	os.RemoveAll(paths.WorkDir)

	err := paths.EnsurePaths()
	if err != nil {
		t.Errorf("EnsurePaths() returned error: %v", err)
		return
	}

	// Verify directories exist
	dirs := []string{paths.Home, paths.WorkDir, paths.Cache, paths.Logs, paths.Configs, paths.Temp}
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created by EnsurePaths", dir)
		}
	}
}

func TestPathResolutionIntegration(t *testing.T) {
	// Test the complete path resolution workflow
	tempDir := t.TempDir()

	paths, err := ResolvePaths(tempDir, "")
	if err != nil {
		t.Errorf("ResolvePaths failed: %v", err)
		return
	}

	// Verify the paths object is properly constructed
	if paths.Home != tempDir {
		t.Errorf("Home path mismatch: expected %s, got %s", tempDir, paths.Home)
	}

	// Verify all subdirectories exist
	subdirs := []string{paths.Cache, paths.Logs, paths.Configs, paths.Temp}
	for _, subdir := range subdirs {
		if _, err := os.Stat(subdir); os.IsNotExist(err) {
			t.Errorf("Subdirectory %s does not exist", subdir)
		}
	}

	// Test that methods work correctly
	configPath := paths.GetDefaultConfigPath()
	if !strings.HasPrefix(configPath, paths.Configs) {
		t.Errorf("Config path %s should be under configs directory %s", configPath, paths.Configs)
	}

	logPath := paths.GetDefaultLogPath()
	if !strings.HasPrefix(logPath, paths.Logs) {
		t.Errorf("Log path %s should be under logs directory %s", logPath, paths.Logs)
	}
}
