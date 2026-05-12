package provenance

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestNewRunIDReturnsUUIDv7(t *testing.T) {
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}

	id, err := uuid.Parse(runID)
	if err != nil {
		t.Fatalf("parse run id: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("run id must not be nil")
	}
	if id.Version() != uuid.Version(7) {
		t.Fatalf("version = %s, want 7", id.Version())
	}
}

func TestValidateRunID(t *testing.T) {
	valid, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}

	tests := []struct {
		name    string
		value   string
		wantErr bool
	}{
		{name: "valid v7", value: valid},
		{name: "nil", value: uuid.Nil.String(), wantErr: true},
		{name: "v4", value: uuid.NewString(), wantErr: true},
		{name: "invalid", value: "not-a-uuid", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRunID(tt.value)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateRunID() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestResolveRunIDPrecedence(t *testing.T) {
	flagID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID flag: %v", err)
	}
	envID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID env: %v", err)
	}

	got, err := ResolveRunID(strings.ToUpper(flagID), envID)
	if err != nil {
		t.Fatalf("ResolveRunID flag: %v", err)
	}
	if got != flagID {
		t.Fatalf("ResolveRunID flag precedence = %q, want %q", got, flagID)
	}

	got, err = ResolveRunID("", envID)
	if err != nil {
		t.Fatalf("ResolveRunID env: %v", err)
	}
	if got != envID {
		t.Fatalf("ResolveRunID env = %q, want %q", got, envID)
	}
}

func TestResolveRunIDNormalizesParserAcceptedSpellings(t *testing.T) {
	runID, err := NewRunID()
	if err != nil {
		t.Fatalf("NewRunID: %v", err)
	}

	tests := []struct {
		name  string
		value string
	}{
		{name: "uppercase canonical", value: strings.ToUpper(runID)},
		{name: "urn", value: "urn:uuid:" + runID},
		{name: "braced", value: "{" + runID + "}"},
		{name: "raw hex", value: strings.ReplaceAll(runID, "-", "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveRunID(tt.value, "")
			if err != nil {
				t.Fatalf("ResolveRunID(%q): %v", tt.value, err)
			}
			if got != runID {
				t.Fatalf("ResolveRunID(%q) = %q, want %q", tt.value, got, runID)
			}
		})
	}
}
