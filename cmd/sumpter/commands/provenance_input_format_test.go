package commands

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

func manifestForInputFormat(t *testing.T, format string) map[string]interface{} {
	t.Helper()
	opts := &ExtractOptions{effectiveInputFormat: format}
	inputs := []provenance.Input{
		{Path: "a.in", SHA256: "sha256:" + strings.Repeat("a", 64), SizeBytes: 1},
		{Path: "b.in", SHA256: "sha256:" + strings.Repeat("b", 64), SizeBytes: 2},
	}
	runtime := provenance.RuntimeOptions{RunID: "0192f3a4-5b6c-7d8e-9f01-23456789abcd", SumpterVersion: "0.4.0"}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	manifest := buildProvenanceManifest(opts, runtime, now, now, inputs, []provenance.Output{}, map[string]int{}, nil)
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
}

func validateProvenanceDoc(t *testing.T, doc map[string]interface{}) *goneatschema.Result {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../../schemas/provenance/v1.json")
	if err != nil {
		t.Fatalf("read provenance schema: %v", err)
	}
	result, err := goneatschema.ValidateFromBytes(schemaBytes, doc)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	return result
}

func TestProvenanceInputsCarryFormat(t *testing.T) {
	for _, tc := range []struct{ effective, want string }{
		{effective: "", want: "xml"},
		{effective: "xml", want: "xml"},
		{effective: "json", want: "json"},
	} {
		doc := manifestForInputFormat(t, tc.effective)
		inputs, _ := doc["inputs"].([]interface{})
		if len(inputs) != 2 {
			t.Fatalf("inputs = %#v", doc["inputs"])
		}
		for i, in := range inputs {
			entry, _ := in.(map[string]interface{})
			if entry["format"] != tc.want {
				t.Errorf("effective %q: inputs[%d].format = %#v, want %q", tc.effective, i, entry["format"], tc.want)
			}
		}
		if result := validateProvenanceDoc(t, doc); !result.Valid {
			t.Errorf("effective %q: manifest failed schema validation: %v", tc.effective, result.Errors)
		}
	}
}

func TestProvenanceInputFormatOutOfEnumFails(t *testing.T) {
	doc := manifestForInputFormat(t, "json")
	inputs := doc["inputs"].([]interface{})
	inputs[0].(map[string]interface{})["format"] = "yaml"
	if result := validateProvenanceDoc(t, doc); result.Valid {
		t.Fatal("manifest with inputs[].format \"yaml\" validated")
	}
}

func TestProvenanceInputFormatOptionalForEarlierManifests(t *testing.T) {
	doc := manifestForInputFormat(t, "xml")
	for _, in := range doc["inputs"].([]interface{}) {
		delete(in.(map[string]interface{}), "format")
	}
	if result := validateProvenanceDoc(t, doc); !result.Valid {
		t.Fatalf("manifest without inputs[].format failed validation: %v", result.Errors)
	}
}

func TestProvenanceInputFormatNeverOmitted(t *testing.T) {
	raw, err := json.Marshal(provenance.Input{Path: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"format":""`) {
		t.Fatalf("an unstamped input must marshal an empty format that fails the enum, got %s", raw)
	}
	doc := manifestForInputFormat(t, "xml")
	doc["inputs"].([]interface{})[0].(map[string]interface{})["format"] = ""
	if result := validateProvenanceDoc(t, doc); result.Valid {
		t.Fatal("manifest with an empty inputs[].format validated")
	}
}

func TestFailureReasonForRouteUnsupported(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", docnode.ErrRouteUnsupported)
	if got := failureReasonForError(err); got != extract.DispositionReasonRouteUnsupported {
		t.Fatalf("failureReasonForError = %q, want %q", got, extract.DispositionReasonRouteUnsupported)
	}
	if got := failureReasonForError(errors.New("failed to parse JSON: json: duplicate key")); got != extract.DispositionReasonParseError {
		t.Fatalf("json parse failure reason = %q, want parse_error", got)
	}
}
