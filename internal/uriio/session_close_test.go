package uriio

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSessionCloseRemovesStagedFiles proves Close removes the run's staging
// directory and everything staged in it, with no network, so an aborted run
// cannot leave staged inputs behind.
func TestSessionCloseRemovesStagedFiles(t *testing.T) {
	s := NewSession(nil, t.TempDir(), "run-close-test")
	stage := filepath.Join(t.TempDir(), "cloud", "run-close-test")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "staged.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.stageDir = stage
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("staging directory still present after Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
