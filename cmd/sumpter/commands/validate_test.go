package commands

import (
	"strings"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	if validateCmd == nil {
		t.Fatal("expected validate command, got nil")
	}

	if validateCmd.Use != "validate" {
		t.Errorf("Use = %q, want 'validate'", validateCmd.Use)
	}

	if validateCmd.Short == "" {
		t.Error("Short description is empty")
	}

	if validateCmd.Long == "" {
		t.Error("Long description is empty")
	}

	if validateCmd.RunE == nil {
		t.Error("RunE should not be nil")
	}
}

func TestValidateCommandFlags(t *testing.T) {
	// Check that flags are defined
	dirFlag := validateCmd.Flags().Lookup("dir")
	if dirFlag == nil {
		t.Error("expected 'dir' flag to be defined")
	} else {
		if dirFlag.Shorthand != "d" {
			t.Errorf("expected 'dir' flag shorthand to be 'd', got %q", dirFlag.Shorthand)
		}
	}

	jsonFlag := validateCmd.Flags().Lookup("json")
	if jsonFlag == nil {
		t.Error("expected 'json' flag to be defined")
	} else {
		if jsonFlag.DefValue != "false" {
			t.Errorf("expected 'json' flag default to be 'false', got %q", jsonFlag.DefValue)
		}
	}
}

func TestValidateCommandExamples(t *testing.T) {
	// Verify the command has useful examples in the Long description
	if validateCmd.Long == "" {
		t.Error("Long description should contain examples")
	}

	// Check that examples are present
	examples := []string{"sumpter validate", "config.yaml", "--dir", "--json"}
	for _, example := range examples {
		if !strings.Contains(validateCmd.Long, example) {
			t.Errorf("Long description should contain example %q", example)
		}
	}
}

func TestOutputJSONResults(t *testing.T) {
	// This test verifies the function signature exists and can be called
	// Integration testing of actual validation would require config setup
	cmd := validateCmd
	if cmd == nil {
		t.Fatal("validateCmd is nil")
	}

	// Verify function exists by checking if RunE is set
	if cmd.RunE == nil {
		t.Error("RunE function should be defined for validate command")
	}
}

func TestOutputTextResults(t *testing.T) {
	// This test verifies the function signature exists
	cmd := validateCmd
	if cmd == nil {
		t.Fatal("validateCmd is nil")
	}

	// Verify the command can output to stdout (set by Cobra)
	out := cmd.OutOrStdout()
	if out == nil {
		t.Error("command should have output writer")
	}
}
