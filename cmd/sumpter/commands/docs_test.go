package commands

import (
	"testing"
)

func TestDocsCommand(t *testing.T) {
	// Test that docs command is properly configured
	if docsCmd == nil {
		t.Error("docsCmd should not be nil")
		return
	}

	if docsCmd.Use != "docs" {
		t.Errorf("Expected command use 'docs', got '%s'", docsCmd.Use)
	}

	if docsCmd.Short == "" {
		t.Error("Expected non-empty short description")
	}

	if docsCmd.Long == "" {
		t.Error("Expected non-empty long description")
	}

	// Check that subcommands are added
	subcommands := docsCmd.Commands()
	if len(subcommands) == 0 {
		t.Error("Expected docs command to have subcommands")
	}

	foundList := false
	foundShow := false
	for _, cmd := range subcommands {
		switch cmd.Use {
		case "list":
			foundList = true
		case "show <path>":
			foundShow = true
		}
	}

	if !foundList {
		t.Error("Expected 'list' subcommand")
	}
	if !foundShow {
		t.Error("Expected 'show <path>' subcommand")
	}
}

func TestDocsListCommand(t *testing.T) {
	// Test docs list command structure
	if docsListCmd == nil {
		t.Error("docsListCmd should not be nil")
		return
	}

	if docsListCmd.Use != "list" {
		t.Errorf("Expected command use 'list', got '%s'", docsListCmd.Use)
	}

	if docsListCmd.Short == "" {
		t.Error("Expected non-empty short description")
	}

	if docsListCmd.Run == nil {
		t.Error("Expected Run function to be set")
	}
}

func TestDocsShowCommand(t *testing.T) {
	// Test docs show command structure
	if docsShowCmd == nil {
		t.Error("docsShowCmd should not be nil")
		return
	}

	if docsShowCmd.Use != "show <path>" {
		t.Errorf("Expected command use 'show <path>', got '%s'", docsShowCmd.Use)
	}

	if docsShowCmd.Short == "" {
		t.Error("Expected non-empty short description")
	}

	if docsShowCmd.Args == nil {
		t.Error("Expected Args validation to be set")
	}

	if docsShowCmd.Run == nil {
		t.Error("Expected Run function to be set")
	}
}

func TestDocsListExecution(t *testing.T) {
	// Test docs list command execution
	cmd := docsListCmd

	// Execute the command - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("docs list command panicked: %v", r)
		}
	}()

	cmd.Run(cmd, []string{})
	// Command executed successfully if no panic
}

func TestDocsShowExecution(t *testing.T) {
	// Test docs show command with valid path
	cmd := docsShowCmd

	// Execute the command - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("docs show command panicked: %v", r)
		}
	}()

	// Try to show a known documentation file
	cmd.Run(cmd, []string{"docs/sumpter_overview"})
	// Command executed successfully if no panic
}

func TestDocsShowNotFound(t *testing.T) {
	// Test docs show command with non-existent path
	cmd := docsShowCmd

	// Execute the command - should not panic even with invalid path
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("docs show command panicked with invalid path: %v", r)
		}
	}()

	// Try to show a non-existent file
	cmd.Run(cmd, []string{"non-existent-file"})
	// Command executed successfully if no panic
}

func TestDocsShowWithDocsPrefix(t *testing.T) {
	// Test docs show command with docs/ prefix
	cmd := docsShowCmd

	// Execute the command - should not panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("docs show command panicked with docs/ prefix: %v", r)
		}
	}()

	// Try to show a file with docs/ prefix
	cmd.Run(cmd, []string{"docs/sumpter_overview"})
	// Command executed successfully if no panic
}

func TestDocsCommandInitialization(t *testing.T) {
	// Test that docs command is properly added to root
	if rootCmd.Commands() == nil {
		t.Error("rootCmd should have commands")
		return
	}

	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "docs" {
			found = true
			break
		}
	}
	if !found {
		t.Error("docs command should be added to root command")
	}
}

func TestDocsArgsValidation(t *testing.T) {
	// Test that docs show command validates arguments
	cmd := docsShowCmd

	// Should fail with no args
	err := cmd.Args(cmd, []string{})
	if err == nil {
		t.Error("Expected error when docs show is called without arguments")
	}

	// Should pass with one arg
	err = cmd.Args(cmd, []string{"some-path"})
	if err != nil {
		t.Errorf("Expected no error with one argument, got: %v", err)
	}

	// Should fail with multiple args
	err = cmd.Args(cmd, []string{"arg1", "arg2"})
	if err == nil {
		t.Error("Expected error when docs show is called with multiple arguments")
	}
}
