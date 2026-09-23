package provenance

import (
	"errors"
	"strings"
	"testing"
)

func TestRuntimeOptionsDiagnosticLabelDoesNotEnterRuntimeFields(t *testing.T) {
	opts := RuntimeOptions{
		RunID:           "run",
		DiagnosticLabel: "input 3",
	}
	if got := opts.DiagnosticIdentity("/private/corpus/input.xml"); got != "input 3" {
		t.Fatalf("DiagnosticIdentity = %q, want input 3", got)
	}
	if _, ok := opts.RuntimeFields()["diagnostic_label"]; ok {
		t.Fatal("DiagnosticLabel entered runtime fields")
	}
}

func TestRuntimeOptionsDiagnosticErrorReplacesLocalPathAndUnwraps(t *testing.T) {
	original := errors.New("failed to open /private/corpus/input.xml")
	opts := RuntimeOptions{DiagnosticLabel: "input 2"}
	redacted := opts.DiagnosticError(original, "/private/corpus/input.xml")
	if redacted == original {
		t.Fatal("DiagnosticError returned the original error after replacement")
	}
	if got := redacted.Error(); strings.Contains(got, "/private/corpus/input.xml") || !strings.Contains(got, "input 2") {
		t.Fatalf("DiagnosticError = %q, want opaque input label without local path", got)
	}
	if !errors.Is(redacted, original) {
		t.Fatal("DiagnosticError did not preserve the original error through Unwrap")
	}
}
