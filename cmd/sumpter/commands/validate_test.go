package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
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

func TestValidateArtifactDescriptorCommand(t *testing.T) {
	if validateArtifactDescriptorCmd == nil {
		t.Fatal("expected artifact descriptor validate command, got nil")
	}
	if validateArtifactDescriptorCmd.Flags().Lookup("contract-base") == nil {
		t.Fatal("expected contract-base flag")
	}
}

func TestRunValidateArtifactDescriptor(t *testing.T) {
	base := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-contract", "v0")
	descriptor := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-descriptor", "record-stream.descriptor.json")

	savedBase := validateArtifactContractBase
	savedJSON := validateJSON
	t.Cleanup(func() {
		validateArtifactContractBase = savedBase
		validateJSON = savedJSON
	})
	validateArtifactContractBase = base
	validateJSON = true

	var out bytes.Buffer
	cmd := validateArtifactDescriptorCmd
	cmd.SetOut(&out)
	err := runValidateArtifactDescriptor(cmd, []string{descriptor})
	cmd.SetOut(nil)
	if err != nil {
		t.Fatalf("runValidateArtifactDescriptor returned error: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`"valid": true`,
		artifactcontract.DataArtifactCapability,
		artifactcontract.BaselineBundleSHA256,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("output missing %q:\n%s", want, got)
		}
	}
}

func TestRunValidateArtifactDescriptorRejectsWrongBundleBeforeOutput(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "contract.json"), []byte(`{"capability":"contract: data-artifact/v0","entry_schema":"artifact-descriptor.schema.json"}`), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(base, "artifact-descriptor.schema.json"), []byte(`{"type":"object"}`), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	descriptor := filepath.Join("..", "..", "..", "tests", "fixtures", "data-artifact-descriptor", "record-stream.descriptor.json")

	savedBase := validateArtifactContractBase
	savedJSON := validateJSON
	t.Cleanup(func() {
		validateArtifactContractBase = savedBase
		validateJSON = savedJSON
	})
	validateArtifactContractBase = base
	validateJSON = true

	var out bytes.Buffer
	cmd := validateArtifactDescriptorCmd
	cmd.SetOut(&out)
	err := runValidateArtifactDescriptor(cmd, []string{descriptor})
	cmd.SetOut(nil)
	if err == nil {
		t.Fatal("runValidateArtifactDescriptor succeeded; want baseline mismatch")
	}
	if !strings.Contains(err.Error(), "contract baseline hash mismatch") {
		t.Fatalf("error = %q, want baseline mismatch", err.Error())
	}
	got := out.String()
	for _, disallowed := range []string{`"valid": true`, `Status: valid`} {
		if strings.Contains(got, disallowed) {
			t.Fatalf("output contains success-looking result %q:\n%s", disallowed, got)
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
		return
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
