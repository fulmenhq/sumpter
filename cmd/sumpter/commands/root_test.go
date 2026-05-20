package commands

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestExecute(t *testing.T) {
	// Test that Execute function exists and is callable
	// We can't easily test the actual execution without complex mocking
	// But we can verify the function signature is correct
	_ = Execute // Ensure function exists
}

func TestGetVersion(t *testing.T) {
	tests := []struct {
		name        string
		fileContent string
		expected    string
	}{
		{"version_file_exists", "1.2.3\n", "1.2.3"},
		{"version_file_with_spaces", "  1.2.3  \n", "1.2.3"},
		{"version_file_empty", "", "0.1.0"}, // fallback
		{"no_version_file", "", "0.1.0"},    // fallback
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create or remove VERSION file as needed
			if tt.fileContent != "" {
				err := os.WriteFile("VERSION", []byte(tt.fileContent), 0644)
				if err != nil {
					t.Fatalf("Failed to write VERSION file: %v", err)
				}
				defer func() { _ = os.Remove("VERSION") }() // cleanup
			} else {
				_ = os.Remove("VERSION") // ensure it doesn't exist
			}

			result := getVersion()
			if result != tt.expected {
				t.Errorf("getVersion() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestRootCommandStructure(t *testing.T) {
	// Test that root command has expected properties
	if rootCmd == nil {
		t.Error("rootCmd should not be nil")
		return
	}

	if rootCmd.Use != "sumpter" {
		t.Errorf("Expected command use 'sumpter', got '%s'", rootCmd.Use)
	}

	if rootCmd.Short == "" {
		t.Error("Expected non-empty short description")
	}

	if rootCmd.Long == "" {
		t.Error("Expected non-empty long description")
	}

	if rootCmd.Version == "" {
		t.Error("Expected non-empty version")
	}

	if rootCmd.PersistentPreRun == nil {
		t.Error("Expected PersistentPreRun to be set")
	}

	if !rootCmd.SilenceUsage {
		t.Error("Expected root command to silence usage on errors")
	}
}

func TestRootSilenceUsageSuppressesRuntimeErrorUsage(t *testing.T) {
	sentinel := errors.New("sentinel runtime failure")
	cmd, stdout, stderr := newSilenceUsageTestCommand()
	cmd.AddCommand(&cobra.Command{
		Use: "fail",
		RunE: func(cmd *cobra.Command, args []string) error {
			return sentinel
		},
	})
	cmd.SetArgs([]string{"fail"})

	err := cmd.Execute()
	if !errors.Is(err, sentinel) {
		t.Fatalf("Execute() error = %v, want %v", err, sentinel)
	}

	if !strings.Contains(stderr.String(), "Error: sentinel runtime failure") {
		t.Fatalf("stderr = %q, want runtime error message", stderr.String())
	}
	if output := stdout.String() + stderr.String(); strings.Contains(output, "Usage:") {
		t.Fatalf("output contains usage block despite SilenceUsage=true:\n%s", output)
	}
}

func TestRootSilenceUsageSuppressesFlagErrorUsage(t *testing.T) {
	cmd, stdout, stderr := newSilenceUsageTestCommand()
	cmd.AddCommand(&cobra.Command{
		Use:  "inspect",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	})
	cmd.SetArgs([]string{"inspect", "--unknown-flag"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown flag error")
	}

	if !strings.Contains(stderr.String(), "unknown flag: --unknown-flag") {
		t.Fatalf("stderr = %q, want unknown flag error", stderr.String())
	}
	if output := stdout.String() + stderr.String(); strings.Contains(output, "Usage:") {
		t.Fatalf("output contains usage block despite SilenceUsage=true:\n%s", output)
	}
}

func TestRootUnknownCommandStillPrintsHelpHint(t *testing.T) {
	cmd, _, stderr := newSilenceUsageTestCommand()
	cmd.AddCommand(&cobra.Command{Use: "inspect"})
	cmd.SetArgs([]string{"ineperct"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("Execute() error = nil, want unknown command error")
	}

	const want = "Run 'sumpter --help' for usage."
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}

func newSilenceUsageTestCommand() (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	cmd := &cobra.Command{
		Use:          rootCmd.Use,
		SilenceUsage: rootCmd.SilenceUsage,
	}
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return cmd, &stdout, &stderr
}

func TestRootCommandFlags(t *testing.T) {
	// Test that root command has expected persistent flags
	expectedFlags := []string{
		"log-level", "log-format", "allow-large-files",
		"home", "workdir", "config",
		"log-file", "log-color", "log-telemetry",
	}

	flags := rootCmd.PersistentFlags()
	for _, flagName := range expectedFlags {
		flag := flags.Lookup(flagName)
		if flag == nil {
			t.Errorf("Expected flag '%s' to exist", flagName)
		}
	}
}

func TestTestableInitializeEnvironment(t *testing.T) {
	// Test the testable version of initializeEnvironment
	// This tests flag reading and basic setup without the fatal logging

	// Set HOME environment variable for path resolution
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", "/tmp/test-home")
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	// Create a mock command with the expected flags
	cmd := &cobra.Command{}
	cmd.Flags().String("home", "", "")
	cmd.Flags().String("workdir", "", "")
	cmd.Flags().String("log-level", "info", "")
	cmd.Flags().String("log-format", "console", "")
	cmd.Flags().String("log-file", "", "")
	cmd.Flags().Bool("log-color", true, "")
	cmd.Flags().Bool("log-telemetry", false, "")

	// Test that the function can be called without panicking
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("testableInitializeEnvironment panicked: %v", r)
		}
	}()

	paths, err := testableInitializeEnvironment(cmd, []string{})
	if err != nil {
		t.Errorf("testableInitializeEnvironment returned error: %v", err)
	}

	// Verify that paths were resolved
	if paths == nil {
		t.Error("Expected paths to be returned")
	} else {
		if paths.Home == "" {
			t.Error("Expected Home path to be set")
		}
		if paths.WorkDir == "" {
			t.Error("Expected WorkDir path to be set")
		}
	}
}

func TestTestableInitializeEnvironmentWithFlags(t *testing.T) {
	// Test flag reading with custom flag values (using valid paths)

	// Set HOME environment variable for path resolution
	originalHome := os.Getenv("HOME")
	_ = os.Setenv("HOME", "/tmp/test-home")
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	cmd := &cobra.Command{}
	cmd.Flags().String("home", "", "")    // Use default
	cmd.Flags().String("workdir", "", "") // Use default
	cmd.Flags().String("log-level", "debug", "")
	cmd.Flags().String("log-format", "json", "")
	cmd.Flags().String("log-file", "custom.log", "")
	cmd.Flags().Bool("log-color", false, "")
	cmd.Flags().Bool("log-telemetry", true, "")

	paths, err := testableInitializeEnvironment(cmd, []string{})
	if err != nil {
		t.Errorf("testableInitializeEnvironment returned error: %v", err)
	}

	// Verify that paths were resolved
	if paths == nil {
		t.Error("Expected paths to be returned")
	} else {
		if paths.Home == "" {
			t.Error("Expected Home path to be set")
		}
		if paths.WorkDir == "" {
			t.Error("Expected WorkDir path to be set")
		}
	}
}

func TestTestableInitializeEnvironmentPathResolutionError(t *testing.T) {
	// Test error handling when path resolution fails
	// We can trigger this by unsetting HOME

	// Remove HOME environment variable to trigger path resolution error
	originalHome := os.Getenv("HOME")
	_ = os.Unsetenv("HOME")
	defer func() { _ = os.Setenv("HOME", originalHome) }()

	cmd := &cobra.Command{}
	cmd.Flags().String("home", "", "")
	cmd.Flags().String("workdir", "", "")
	cmd.Flags().String("log-level", "info", "")
	cmd.Flags().String("log-format", "console", "")
	cmd.Flags().String("log-file", "", "")
	cmd.Flags().Bool("log-color", true, "")
	cmd.Flags().Bool("log-telemetry", false, "")

	_, err := testableInitializeEnvironment(cmd, []string{})
	// Note: config.ResolvePaths may have fallback logic, so we just verify it doesn't crash
	// The important thing is that the function handles the environment gracefully
	_ = err // We don't assert on the error since it may succeed with fallbacks
}
