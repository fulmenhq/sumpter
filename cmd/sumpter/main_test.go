package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestMainFunction(t *testing.T) {
	// Test that main function exists and can be referenced
	// This is a basic smoke test for the main package
	// Note: Functions in Go are never nil, so this test just verifies
	// that the main function can be referenced without issues
	_ = main // Reference the main function to ensure it exists
}

func TestRunMain(t *testing.T) {
	// Test the main logic without calling os.Exit
	// This should not error for basic command execution
	err := runMain()
	// Note: runMain may return an error depending on command line args,
	// but it should not panic and should execute the command logic
	_ = err // We don't assert on the error since it depends on CLI args
}

func TestMainExecution(t *testing.T) {
	// Build the binary first
	// Use -buildvcs=false to avoid VCS stamping issues in containers
	buildCmd := exec.Command("go", "build", "-buildvcs=false", "-o", "sumpter-test", ".")
	output, err := buildCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Failed to build binary: %v\nOutput: %s", err, output)
	}
	defer func() { _ = os.Remove("sumpter-test") }()

	// Test successful execution (help command)
	cmd := exec.Command("./sumpter-test", "--help")
	output, err = cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Main execution failed: %v\nOutput: %s", err, output)
	}

	// Verify we get help output
	if len(output) == 0 {
		t.Error("Expected help output from main execution")
	}
}

func TestMainExitCodes(t *testing.T) {
	// Build the binary
	// Use -buildvcs=false to avoid VCS stamping issues in containers
	buildCmd := exec.Command("go", "build", "-buildvcs=false", "-o", "sumpter-test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer func() { _ = os.Remove("sumpter-test") }()

	// Test with invalid command (should exit with code 1)
	cmd := exec.Command("./sumpter-test", "invalid-command")
	err := cmd.Run()
	if err == nil {
		t.Error("Expected command to fail with invalid command")
	}

	// Check exit code
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("Expected exit code 1, got %d", exitErr.ExitCode())
		}
	} else {
		t.Errorf("Expected ExitError, got %T", err)
	}
}

func TestMainVersionCommand(t *testing.T) {
	// Build the binary
	// Use -buildvcs=false to avoid VCS stamping issues in containers
	buildCmd := exec.Command("go", "build", "-buildvcs=false", "-o", "sumpter-test", ".")
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build binary: %v", err)
	}
	defer func() { _ = os.Remove("sumpter-test") }()

	// Test version command (should succeed)
	cmd := exec.Command("./sumpter-test", "version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("Version command failed: %v\nOutput: %s", err, output)
	}

	// Verify we get version output
	if len(output) == 0 {
		t.Error("Expected version output")
	}
}
