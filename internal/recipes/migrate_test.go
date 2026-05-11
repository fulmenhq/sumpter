package recipes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// minimalManifest is the canonical pre-A.3 manifest shape used for migration
// tests: valid YAML + valid schema, but missing content_version.
const minimalManifest = `version: recipe/v0.1.0
kind: extract
id: test_recipe
created_at: "2026-05-11T00:00:00Z"
assets:
  signature: sig.yaml
  extract: ext.yaml
`

func TestMigrateBytes_StampsMissingContentVersion(t *testing.T) {
	out, action, err := MigrateBytes([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("MigrateBytes returned error: %v", err)
	}
	if action != MigrationStamped {
		t.Fatalf("action = %q, want %q", action, MigrationStamped)
	}
	if !strings.Contains(string(out), `content_version: "0.0.1"`) {
		t.Errorf("output missing stamped content_version:\n%s", out)
	}
	// Insertion must occur after `created_at:` (highest-priority anchor).
	idxCreated := strings.Index(string(out), "created_at:")
	idxVersion := strings.Index(string(out), `content_version:`)
	if idxCreated < 0 || idxVersion <= idxCreated {
		t.Errorf("content_version should appear after created_at; got created_at=%d content_version=%d",
			idxCreated, idxVersion)
	}
}

func TestMigrateBytes_Idempotent(t *testing.T) {
	first, action1, err := MigrateBytes([]byte(minimalManifest))
	if err != nil {
		t.Fatalf("first migrate returned error: %v", err)
	}
	if action1 != MigrationStamped {
		t.Fatalf("first action = %q, want %q", action1, MigrationStamped)
	}

	second, action2, err := MigrateBytes(first)
	if err != nil {
		t.Fatalf("second migrate returned error: %v", err)
	}
	if action2 != MigrationAlreadyStamped {
		t.Errorf("second action = %q, want %q", action2, MigrationAlreadyStamped)
	}
	if string(second) != string(first) {
		t.Errorf("idempotent re-migrate altered bytes:\nfirst=%s\nsecond=%s", first, second)
	}
}

func TestMigrateBytes_AlreadyStampedRespected(t *testing.T) {
	manifest := `version: recipe/v0.1.0
kind: extract
id: test_recipe
content_version: "0.4.2"
assets:
  signature: sig.yaml
  extract: ext.yaml
`
	out, action, err := MigrateBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("MigrateBytes returned error: %v", err)
	}
	if action != MigrationAlreadyStamped {
		t.Errorf("action = %q, want %q", action, MigrationAlreadyStamped)
	}
	if string(out) != manifest {
		t.Errorf("manifest mutated despite existing content_version:\nbefore=%s\nafter=%s",
			manifest, out)
	}
}

func TestMigrateBytes_NoAnchorPrependsAfterPreamble(t *testing.T) {
	manifest := `# leading comment
---
kind: extract
display_name: ""
assets:
  signature: sig.yaml
  extract: ext.yaml
`
	out, action, err := MigrateBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("MigrateBytes returned error: %v", err)
	}
	if action != MigrationStamped {
		t.Fatalf("action = %q, want %q", action, MigrationStamped)
	}
	// The stamped line should appear before `kind:` since there is no
	// version/id/created_at anchor, and after the leading comment + `---`.
	idxStamped := strings.Index(string(out), "content_version:")
	idxKind := strings.Index(string(out), "kind:")
	idxComment := strings.Index(string(out), "# leading comment")
	if idxComment >= idxStamped {
		t.Errorf("stamp must appear after leading comment; comment=%d stamp=%d",
			idxComment, idxStamped)
	}
	if idxStamped < 0 || idxStamped >= idxKind {
		t.Errorf("stamp must appear before kind anchor; stamp=%d kind=%d", idxStamped, idxKind)
	}
}

func TestMigrateBytes_PreservesCRLF(t *testing.T) {
	manifest := strings.ReplaceAll(minimalManifest, "\n", "\r\n")
	out, action, err := MigrateBytes([]byte(manifest))
	if err != nil {
		t.Fatalf("MigrateBytes returned error: %v", err)
	}
	if action != MigrationStamped {
		t.Fatalf("action = %q, want %q", action, MigrationStamped)
	}
	if !strings.Contains(string(out), "\r\n") {
		t.Errorf("CRLF line endings were not preserved")
	}
	if strings.Contains(strings.ReplaceAll(string(out), "\r\n", ""), "\n") {
		t.Errorf("LF line endings introduced into CRLF document")
	}
}

func TestMigrateFile_DryRunDoesNotWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(minimalManifest), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	action, err := MigrateFile(path, true)
	if err != nil {
		t.Fatalf("MigrateFile(dryRun=true) returned error: %v", err)
	}
	if action != MigrationStamped {
		t.Errorf("action = %q, want %q", action, MigrationStamped)
	}

	got, err := os.ReadFile(path) // #nosec G304 - test-controlled path
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != minimalManifest {
		t.Errorf("dry-run mutated file on disk:\nwant=%s\ngot=%s", minimalManifest, got)
	}
}

func TestMigrateFile_WritesAndValidates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(minimalManifest), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	action, err := MigrateFile(path, false)
	if err != nil {
		t.Fatalf("MigrateFile returned error: %v", err)
	}
	if action != MigrationStamped {
		t.Errorf("action = %q, want %q", action, MigrationStamped)
	}

	// Post-migration the file must load cleanly with content_version set.
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest after migrate failed: %v", err)
	}
	if m.ContentVersion != StarterContentVersion {
		t.Errorf("ContentVersion = %q, want %q", m.ContentVersion, StarterContentVersion)
	}
	if len(m.Warnings) != 0 {
		t.Errorf("expected no warnings after migrate, got %v", m.Warnings)
	}

	// Re-running should be idempotent.
	action2, err := MigrateFile(path, false)
	if err != nil {
		t.Fatalf("second MigrateFile returned error: %v", err)
	}
	if action2 != MigrationAlreadyStamped {
		t.Errorf("second action = %q, want %q", action2, MigrationAlreadyStamped)
	}
}

func TestMigrateBytes_RejectsInvalidYAML(t *testing.T) {
	_, _, err := MigrateBytes([]byte("not: valid: yaml: :"))
	if err == nil {
		t.Fatalf("expected error for invalid YAML, got nil")
	}
	if !strings.Contains(err.Error(), "valid YAML") {
		t.Errorf("error %q does not mention invalid YAML", err.Error())
	}
}

func TestDrainWarnings_OnceThenEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipe.yaml")
	if err := os.WriteFile(path, []byte(minimalManifest), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	m, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	first := m.DrainWarnings()
	if len(first) != 1 {
		t.Fatalf("first DrainWarnings len = %d, want 1 (warnings=%v)", len(first), first)
	}
	if !strings.Contains(first[0], "content_version") {
		t.Errorf("warning should mention content_version, got %q", first[0])
	}
	if len(m.Warnings) != 0 {
		t.Errorf("Warnings slice not cleared after drain: %v", m.Warnings)
	}
	second := m.DrainWarnings()
	if second != nil {
		t.Errorf("second DrainWarnings should return nil, got %v", second)
	}
}

func TestDrainWarnings_NilSafe(t *testing.T) {
	var m *Manifest
	if got := m.DrainWarnings(); got != nil {
		t.Errorf("nil receiver DrainWarnings should return nil, got %v", got)
	}
}
