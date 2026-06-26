package provenance

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/validation"
)

func inputWithDisposition(disposition string) Input {
	rc := 0
	return Input{
		Path:        "input.xml",
		SHA256:      "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		SizeBytes:   42,
		Disposition: disposition,
		RecordCount: &rc,
	}
}

func TestSetInputAccountingCountsByDisposition(t *testing.T) {
	m := testManifest(t)
	m.Inputs = []Input{
		inputWithDisposition(dispositionApplied),
		inputWithDisposition(dispositionApplied),
		inputWithDisposition(dispositionNotApplicable),
		inputWithDisposition(dispositionFailed),
	}

	if err := m.SetInputAccounting(); err != nil {
		t.Fatalf("SetInputAccounting: %v", err)
	}

	for name, got := range map[string]*int{
		"inputs_total":          m.InputsTotal,
		"inputs_applied":        m.InputsApplied,
		"inputs_not_applicable": m.InputsNotApplicable,
		"inputs_failed":         m.InputsFailed,
	} {
		if got == nil {
			t.Fatalf("%s was not set", name)
		}
	}

	if *m.InputsTotal != 4 || *m.InputsApplied != 2 || *m.InputsNotApplicable != 1 || *m.InputsFailed != 1 {
		t.Fatalf("counts = total %d applied %d not_applicable %d failed %d; want 4/2/1/1",
			*m.InputsTotal, *m.InputsApplied, *m.InputsNotApplicable, *m.InputsFailed)
	}

	// Reconciliation invariant.
	if *m.InputsApplied+*m.InputsFailed+*m.InputsNotApplicable != *m.InputsTotal {
		t.Fatal("invariant applied+failed+not_applicable == total violated")
	}
	if *m.InputsTotal != len(m.Inputs) {
		t.Fatalf("inputs_total %d != len(inputs) %d", *m.InputsTotal, len(m.Inputs))
	}
}

// An all-applied run must still emit explicit zeros for the other counts (the
// pointer fields guarantee a zero is not dropped by omitempty).
func TestSetInputAccountingPreservesExplicitZeros(t *testing.T) {
	m := testManifest(t)
	m.Inputs = []Input{inputWithDisposition(dispositionApplied)}

	if err := m.SetInputAccounting(); err != nil {
		t.Fatalf("SetInputAccounting: %v", err)
	}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	for _, key := range []string{"inputs_total", "inputs_applied", "inputs_not_applicable", "inputs_failed"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected %s present in marshaled manifest (explicit zero must not be dropped)", key)
		}
	}
	if string(raw["inputs_failed"]) != "0" || string(raw["inputs_not_applicable"]) != "0" {
		t.Fatalf("expected explicit zero for inputs_failed/inputs_not_applicable, got failed=%s not_applicable=%s",
			raw["inputs_failed"], raw["inputs_not_applicable"])
	}
}

func TestSetInputAccountingRejectsUnaccountedDisposition(t *testing.T) {
	for _, disposition := range []string{"", "skipped", "unknown", "Applied"} {
		t.Run(disposition, func(t *testing.T) {
			m := testManifest(t)
			m.Inputs = []Input{
				inputWithDisposition(dispositionApplied),
				inputWithDisposition(disposition),
			}
			if err := m.SetInputAccounting(); err == nil {
				t.Fatalf("expected error for unaccounted disposition %q", disposition)
			}
			// On error the fields must be left unset (no partial/misleading counts).
			if m.InputsTotal != nil || m.InputsApplied != nil || m.InputsNotApplicable != nil || m.InputsFailed != nil {
				t.Fatal("input-accounting fields must remain unset when SetInputAccounting fails")
			}
		})
	}
}

// The schema accepts the four optional integers and still rejects unknown
// top-level fields and negative counts.
func TestManifestSchemaAllowsInputAccounting(t *testing.T) {
	m := testManifest(t)
	m.OutputMode = "aggregate"
	m.Inputs = []Input{
		inputWithDisposition(dispositionApplied),
		inputWithDisposition(dispositionNotApplicable),
		inputWithDisposition(dispositionFailed),
	}
	if err := m.SetInputAccounting(); err != nil {
		t.Fatalf("SetInputAccounting: %v", err)
	}
	assertValidManifest(t, m)
}

func TestManifestSchemaRejectsNegativeInputCount(t *testing.T) {
	m := testManifest(t)
	neg := -1
	m.InputsTotal = &neg
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "schemas"))
	result, err := validator.ValidateProvenanceManifest(data, "manifest.json")
	if err != nil {
		t.Fatalf("ValidateProvenanceManifest: %v", err)
	}
	if result.IsValid() {
		t.Fatal("manifest with negative inputs_total unexpectedly validated")
	}
}

// A default (per-input) manifest carries none of the input-accounting fields, so
// existing manifests stay byte-identical.
func TestInputAccountingOmittedByDefault(t *testing.T) {
	m := testManifest(t)
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	for _, key := range []string{"inputs_total", "inputs_applied", "inputs_not_applicable", "inputs_failed"} {
		if _, ok := raw[key]; ok {
			t.Fatalf("unexpected %s on a default manifest (must be omitted)", key)
		}
	}
}
