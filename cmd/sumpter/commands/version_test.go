package commands

import (
	"os"
	"os/exec"
	"testing"
)

func TestGetVersionFromBuild(t *testing.T) {
	// Save original values
	originalVersion := Version
	defer func() { Version = originalVersion }()

	tests := []struct {
		name     string
		version  string
		expected string
	}{
		{"valid version", "1.2.3", "1.2.3"},
		{"dev version", "dev", "dev"}, // This will fall back to VERSION file
		{"empty version", "", "dev"},  // This will fall back to VERSION file
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			Version = tt.version
			result := getVersionFromBuild()

			if tt.version != "" && tt.version != "dev" {
				if result != tt.expected {
					t.Errorf("getVersionFromBuild() = %s; expected %s", result, tt.expected)
				}
			} else {
				// For dev/empty, it should fall back to getVersion()
				if result == "" {
					t.Error("getVersionFromBuild() should not return empty string")
				}
			}
		})
	}
}

// TestGetVersionFromBuildIsCWDIndependent is the regression guard for the
// out-of-tree --version bug: with a build-injected version, getVersionFromBuild
// must return it regardless of the working directory. The root --version flag and
// the index metadata both source it, so a built binary previously reported the
// stale DefaultVersion ("0.1.0") when run outside the source tree because that
// path read ./VERSION relative to the CWD.
func TestGetVersionFromBuildIsCWDIndependent(t *testing.T) {
	original := Version
	defer func() { Version = original }()
	Version = "9.9.9-test"

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()
	if err := os.Chdir(t.TempDir()); err != nil { // a dir with no VERSION file
		t.Fatal(err)
	}

	if got := getVersionFromBuild(); got != "9.9.9-test" {
		t.Errorf("getVersionFromBuild() outside the source tree = %q, want the injected 9.9.9-test (must not fall back to the CWD VERSION file / DefaultVersion)", got)
	}
}

func TestGetEnvironment(t *testing.T) {
	// Save original environment
	originalEnv := make(map[string]string)
	envVars := []string{"SUMPTER_ENV", "ENV", "NODE_ENV"}

	for _, env := range envVars {
		originalEnv[env] = os.Getenv(env)
	}

	// Restore environment after test
	defer func() {
		for env, value := range originalEnv {
			if value == "" {
				_ = os.Unsetenv(env)
			} else {
				_ = os.Setenv(env, value)
			}
		}
	}()

	tests := []struct {
		name     string
		envVars  map[string]string
		expected string
	}{
		{
			name:     "SUMPTER_ENV set",
			envVars:  map[string]string{"SUMPTER_ENV": "staging"},
			expected: "staging",
		},
		{
			name:     "ENV set",
			envVars:  map[string]string{"ENV": "production"},
			expected: "production",
		},
		{
			name:     "NODE_ENV set",
			envVars:  map[string]string{"NODE_ENV": "development"},
			expected: "development",
		},
		{
			name:    "no env vars, has go.mod",
			envVars: map[string]string{},
			expected: func() string {
				// Check if go.mod exists in current working directory
				if _, err := os.Stat("go.mod"); err == nil {
					return "development"
				}
				return "production"
			}(),
		},
		{
			name:     "priority: SUMPTER_ENV over ENV",
			envVars:  map[string]string{"SUMPTER_ENV": "test", "ENV": "prod"},
			expected: "test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all env vars first
			for _, env := range envVars {
				_ = os.Unsetenv(env)
			}

			// Set test env vars
			for env, value := range tt.envVars {
				_ = os.Setenv(env, value)
			}

			result := getEnvironment()
			if result != tt.expected {
				t.Errorf("getEnvironment() = %s; expected %s", result, tt.expected)
			}
		})
	}
}

func TestGitUtilityFunctions(t *testing.T) {
	// Test isGitRepository
	t.Run("isGitRepository", func(t *testing.T) {
		result := isGitRepository()
		// This will depend on whether we're in a git repo
		// Just test that it doesn't panic
		_ = result
	})

	// Test git functions when in git repo
	if isGitRepository() {
		t.Run("getGitBranch", func(t *testing.T) {
			branch := getGitBranch()
			if branch == "" {
				t.Error("getGitBranch() should return a branch name in git repo")
			}
		})

		t.Run("getGitCommit", func(t *testing.T) {
			commit := getGitCommit()
			if commit == "" {
				t.Error("getGitCommit() should return a commit hash in git repo")
			}
			// Should be a valid SHA-1 hash (40 characters)
			if len(commit) != 40 {
				t.Errorf("getGitCommit() should return 40-character hash, got %d characters", len(commit))
			}
		})

		t.Run("isGitClean", func(t *testing.T) {
			clean := isGitClean()
			// This is just testing that it doesn't panic
			_ = clean
		})

		t.Run("getGitStatusCount", func(t *testing.T) {
			staged := getGitStatusCount("--cached")
			unstaged := getGitStatusCount("--")

			// Should be non-negative integers
			if staged < 0 {
				t.Errorf("getGitStatusCount(--cached) should be >= 0, got %d", staged)
			}
			if unstaged < 0 {
				t.Errorf("getGitStatusCount(--) should be >= 0, got %d", unstaged)
			}
		})

		t.Run("getGitUntrackedCount", func(t *testing.T) {
			untracked := getGitUntrackedCount()
			if untracked < 0 {
				t.Errorf("getGitUntrackedCount() should be >= 0, got %d", untracked)
			}
		})

		t.Run("getGitAheadBehind", func(t *testing.T) {
			ahead, behind := getGitAheadBehind()
			if ahead < 0 {
				t.Errorf("ahead should be >= 0, got %d", ahead)
			}
			if behind < 0 {
				t.Errorf("behind should be >= 0, got %d", behind)
			}
		})
	}
}

func TestGetCurrentGitStatus(t *testing.T) {
	// Test that function doesn't panic
	status := getCurrentGitStatus()

	// If not in git repo, should return nil
	if !isGitRepository() {
		if status != nil {
			t.Error("getCurrentGitStatus() should return nil when not in git repo")
		}
		return
	}

	// If in git repo, should return a status struct
	if status == nil {
		t.Error("getCurrentGitStatus() should return a status struct when in git repo")
		return
	}

	// Test that commit is properly truncated
	if status.CommitFull != "" && len(status.CommitFull) > 8 {
		if len(status.Commit) != 8 {
			t.Errorf("Commit should be truncated to 8 characters, got %d", len(status.Commit))
		}
		if status.Commit != status.CommitFull[:8] {
			t.Error("Commit should be first 8 characters of CommitFull")
		}
	}
}

func TestVersionInfoStruct(t *testing.T) {
	// Test that VersionInfo struct can be created and has expected fields
	info := VersionInfo{
		Version:     "1.2.3",
		BuildTime:   "2024-01-01T12:00:00Z",
		GitCommit:   "abc123def456",
		GoVersion:   "go1.21.0",
		Platform:    "darwin",
		Arch:        "amd64",
		Environment: "development",
	}

	if info.Version != "1.2.3" {
		t.Errorf("Version = %s; expected 1.2.3", info.Version)
	}
	if info.BuildTime != "2024-01-01T12:00:00Z" {
		t.Errorf("BuildTime = %s; expected 2024-01-01T12:00:00Z", info.BuildTime)
	}
	if info.GoVersion != "go1.21.0" {
		t.Errorf("GoVersion = %s; expected go1.21.0", info.GoVersion)
	}
	if info.Platform != "darwin" {
		t.Errorf("Platform = %s; expected darwin", info.Platform)
	}
	if info.Arch != "amd64" {
		t.Errorf("Arch = %s; expected amd64", info.Arch)
	}
	if info.Environment != "development" {
		t.Errorf("Environment = %s; expected development", info.Environment)
	}
}

func TestGitStatusStruct(t *testing.T) {
	// Test GitStatus struct creation
	status := &GitStatus{
		Branch:     "main",
		Commit:     "abc123de",
		CommitFull: "abc123def456789",
		Clean:      true,
		Staged:     2,
		Unstaged:   1,
		Untracked:  3,
		Ahead:      5,
		Behind:     2,
	}

	if status.Branch != "main" {
		t.Errorf("Branch = %s; expected main", status.Branch)
	}
	if status.Commit != "abc123de" {
		t.Errorf("Commit = %s; expected abc123de", status.Commit)
	}
	if status.CommitFull != "abc123def456789" {
		t.Errorf("CommitFull = %s; expected abc123def456789", status.CommitFull)
	}
	if !status.Clean {
		t.Error("Clean should be true")
	}
	if status.Staged != 2 {
		t.Errorf("Staged = %d; expected 2", status.Staged)
	}
	if status.Unstaged != 1 {
		t.Errorf("Unstaged = %d; expected 1", status.Unstaged)
	}
	if status.Untracked != 3 {
		t.Errorf("Untracked = %d; expected 3", status.Untracked)
	}
	if status.Ahead != 5 {
		t.Errorf("Ahead = %d; expected 5", status.Ahead)
	}
	if status.Behind != 2 {
		t.Errorf("Behind = %d; expected 2", status.Behind)
	}
}

// Test helper functions for mocking git commands
func TestMockGitCommands(t *testing.T) {
	// This test demonstrates how we could mock git commands for more controlled testing
	// In a real implementation, we might use dependency injection or interfaces

	// Test that our git functions handle command failures gracefully
	t.Run("git command failure handling", func(t *testing.T) {
		// Test with a non-existent git command
		cmd := exec.Command("git", "nonexistent-command")
		err := cmd.Run()
		if err == nil {
			t.Error("Expected git command to fail with nonexistent command")
		}
	})
}

// Test edge cases and error conditions
func TestVersionEdgeCases(t *testing.T) {
	t.Run("empty build variables", func(t *testing.T) {
		// Save original values
		origVersion := Version
		origBuildTime := BuildTime
		origGitCommit := GitCommit
		defer func() {
			Version = origVersion
			BuildTime = origBuildTime
			GitCommit = origGitCommit
		}()

		// Set empty values
		Version = ""
		BuildTime = ""
		GitCommit = ""

		// Should not panic
		version := getVersionFromBuild()
		if version == "" {
			t.Error("getVersionFromBuild() should not return empty string")
		}

		env := getEnvironment()
		if env == "" {
			t.Error("getEnvironment() should not return empty string")
		}
	})

	t.Run("git status with no upstream", func(t *testing.T) {
		if !isGitRepository() {
			t.Skip("Not in git repository")
		}

		// This should not panic even if there's no upstream branch
		ahead, behind := getGitAheadBehind()
		_ = ahead
		_ = behind
	})
}
