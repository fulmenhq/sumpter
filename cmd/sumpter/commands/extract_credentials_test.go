package commands

import (
	"os"
	"path/filepath"
	"testing"
)

// TestValidateCredentialOptions covers the up-front credential-option validation
// wired into the extract command: malformed CLI overrides and bad credentials
// configs fail fast, while a run with no credential options does no credential
// work.
func TestValidateCredentialOptions(t *testing.T) {
	t.Run("no credential options is a no-op", func(t *testing.T) {
		if err := validateCredentialOptions(&ExtractOptions{}); err != nil {
			t.Fatalf("validateCredentialOptions(empty) = %v, want nil", err)
		}
	})

	t.Run("raw key on CLI is rejected", func(t *testing.T) {
		err := validateCredentialOptions(&ExtractOptions{CredentialOverrides: []string{"src=AKIAIOSFODNN7EXAMPLE"}})
		if err == nil {
			t.Fatal("expected rejection of a raw key on the CLI")
		}
	})

	t.Run("valid override accepted", func(t *testing.T) {
		if err := validateCredentialOptions(&ExtractOptions{CredentialOverrides: []string{"src=prod-readonly"}}); err != nil {
			t.Fatalf("valid override rejected: %v", err)
		}
	})

	t.Run("bad credentials config fails fast", func(t *testing.T) {
		dir := t.TempDir()
		// Typo'd security field must fail-closed.
		path := filepath.Join(dir, "creds.yaml")
		if err := os.WriteFile(path, []byte("handles:\n  h:\n    endpoint: http://x:9000\n    insecur: true\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateCredentialOptions(&ExtractOptions{CredentialsPath: path}); err == nil {
			t.Fatal("expected fail-closed error for typo'd config field")
		}
	})

	t.Run("valid credentials config accepted", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "creds.yaml")
		if err := os.WriteFile(path, []byte("handles:\n  src:\n    profile: prod-readonly\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateCredentialOptions(&ExtractOptions{CredentialsPath: path}); err != nil {
			t.Fatalf("valid credentials config rejected: %v", err)
		}
	})
}

// TestExtractCredentialFlagsRegistered confirms the credential flags are wired
// onto the extract files command.
func TestExtractCredentialFlagsRegistered(t *testing.T) {
	cmd := newExtractFilesCommand()
	for _, name := range []string{"credentials", "credential"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("extract files command missing --%s flag", name)
		}
	}
}

// TestExtractEndToEndIgnoresCredentialsWhenLocal proves a local run with a
// credentials file supplied still succeeds (the file is validated, not required).
func TestExtractEndToEndIgnoresCredentialsWhenLocal(t *testing.T) {
	dir := createExtractManifestFixture(t)
	credsPath := filepath.Join(dir, "creds.yaml")
	if err := os.WriteFile(credsPath, []byte("handles:\n  src:\n    profile: prod-readonly\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}

	opts := newMatrixExtractOptions(dir, filepath.Join(dir, "input.xml"), outDir)
	opts.CredentialsPath = credsPath
	opts.CredentialOverrides = []string{"src=other-profile"}
	if err := runExtract(opts); err != nil {
		t.Fatalf("local run with credentials supplied failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outDir, "manifest.json")); err != nil {
		t.Errorf("expected local output despite credentials supplied: %v", err)
	}
}
