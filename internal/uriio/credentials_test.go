package uriio_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
	"gopkg.in/yaml.v3"
)

// Recognizable credential literals used to prove neither escapes redaction.
const (
	theSecret      = "wJalrXUtnFEMI-K7MDENG-bPxRfiCYEXAMPLEKEY"
	theAccessKeyID = "AKIAIOSFODNN7EXAMPLE"
)

// TestSecretRedactionAcrossSurfaces asserts neither the secret access key nor the
// access key id ever appears in cleartext across fmt verbs, %#v, error wrapping,
// structured logging, JSON, or YAML.
func TestSecretRedactionAcrossSurfaces(t *testing.T) {
	hc := uriio.HandleConfig{
		Profile:         "prod-readonly",
		Region:          "us-east-1",
		AccessKeyID:     uriio.Secret(theAccessKeyID),
		SecretAccessKey: uriio.Secret(theSecret),
	}

	surfaces := map[string]string{
		`fmt %v`:           fmt.Sprintf("%v", hc),
		`fmt %+v`:          fmt.Sprintf("%+v", hc),
		`fmt %#v`:          fmt.Sprintf("%#v", hc),
		`fmt %s on secret`: fmt.Sprintf("secret=[%s]", hc.SecretAccessKey),
		`fmt %s on key id`: fmt.Sprintf("keyid=[%s]", hc.AccessKeyID),
		`error wrap`:       fmt.Errorf("constructing provider with %v: boom", hc).Error(),
	}

	// Structured logging surface.
	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, nil))
	logger.Info("provider", slog.Any("key_id", hc.AccessKeyID), slog.Any("secret", hc.SecretAccessKey), slog.Any("handle", hc))
	surfaces["slog"] = logBuf.String()

	// JSON serialization surface.
	jsonBytes, err := json.Marshal(hc)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	surfaces["json"] = string(jsonBytes)

	// YAML serialization surface (the surface devrev flagged: a string alias
	// would otherwise emit cleartext).
	yamlBytes, err := yaml.Marshal(hc)
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	surfaces["yaml"] = string(yamlBytes)

	for name, out := range surfaces {
		if strings.Contains(out, theSecret) {
			t.Errorf("secret access key leaked through %s surface: %q", name, out)
		}
		if strings.Contains(out, theAccessKeyID) {
			t.Errorf("access key id leaked through %s surface: %q", name, out)
		}
		if !strings.Contains(out, "[redacted]") {
			t.Errorf("%s surface missing redaction marker: %q", name, out)
		}
	}

	// Reveal is the one cleartext path, used only at the provider boundary.
	if hc.SecretAccessKey.Reveal() != theSecret {
		t.Errorf("SecretAccessKey.Reveal() = %q, want the cleartext secret", hc.SecretAccessKey.Reveal())
	}
	if hc.AccessKeyID.Reveal() != theAccessKeyID {
		t.Errorf("AccessKeyID.Reveal() = %q, want the cleartext key id", hc.AccessKeyID.Reveal())
	}
}

func TestSecretEmptyIsBlankNotRedacted(t *testing.T) {
	var s uriio.Secret
	if s.String() != "" {
		t.Errorf("empty Secret String() = %q, want empty", s.String())
	}
	if !s.IsZero() {
		t.Error("empty Secret IsZero() = false, want true")
	}
	b, _ := json.Marshal(s)
	if string(b) != `""` {
		t.Errorf("empty Secret JSON = %s, want \"\"", b)
	}
}

func writeConfig(t *testing.T, dir, name, body string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	// WriteFile honors umask; force the exact mode for the permission tests.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestLoadCredentialsConfigLiteralKeyPermissions asserts a config carrying
// literal keys must be owner-only; group/world access is rejected. Profile-only
// configs are exempt.
func TestLoadCredentialsConfigLiteralKeyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission semantics not applicable on Windows")
	}
	dir := t.TempDir()

	literal := `handles:
  prod:
    region: us-east-1
    access_key_id: AKIAIOSFODNN7EXAMPLE
    secret_access_key: ` + theSecret + "\n"

	// World-readable literal-key config -> rejected.
	world := writeConfig(t, dir, "world.yaml", literal, 0o644)
	if _, err := uriio.LoadCredentialsConfig(world); err == nil {
		t.Error("world-readable literal-key config loaded, want permission error")
	} else if !strings.Contains(err.Error(), "owner-only") {
		t.Errorf("error = %v, want owner-only guidance", err)
	}

	// Owner-only literal-key config -> accepted.
	owner := writeConfig(t, dir, "owner.yaml", literal, 0o600)
	if _, err := uriio.LoadCredentialsConfig(owner); err != nil {
		t.Errorf("owner-only literal-key config failed: %v", err)
	}

	// Profile-only config is exempt from the permission check.
	profileOnly := writeConfig(t, dir, "profile.yaml", "handles:\n  prod:\n    profile: prod-readonly\n", 0o644)
	if _, err := uriio.LoadCredentialsConfig(profileOnly); err != nil {
		t.Errorf("profile-only world-readable config failed: %v", err)
	}
}

// TestLoadCredentialsConfigFailClosed asserts a typo'd or unknown field must
// error, never silently no-op into an insecure default.
func TestLoadCredentialsConfigFailClosed(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"typo'd insecure":   "handles:\n  h:\n    endpoint: http://minio:9000\n    insecur: true\n",
		"unknown field":     "handles:\n  h:\n    profile: p\n    regionx: us-east-1\n",
		"top-level unknown": "handlez:\n  h:\n    profile: p\n",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, dir, "c.yaml", body, 0o600)
			if _, err := uriio.LoadCredentialsConfig(path); err == nil {
				t.Errorf("%s loaded, want fail-closed error", name)
			}
		})
	}
}

// TestLoadCredentialsConfigValidation covers handle-name, both-or-neither key,
// and the static TLS endpoint posture.
func TestLoadCredentialsConfigValidation(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{"valid profile handle", "handles:\n  prod-ro:\n    profile: p\n", false},
		{"invalid handle name", "handles:\n  \"bad name\":\n    profile: p\n", true},
		{"only access key", "handles:\n  h:\n    access_key_id: AKIAIOSFODNN7EXAMPLE\n", true},
		{"https endpoint ok", "handles:\n  h:\n    profile: p\n    endpoint: https://s3.example.com\n", false},
		{"http endpoint rejected", "handles:\n  h:\n    profile: p\n    endpoint: http://minio:9000\n", true},
		{"http endpoint with insecure opt-in", "handles:\n  h:\n    profile: p\n    endpoint: http://minio:9000\n    insecure: true\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, dir, "c.yaml", tc.body, 0o600)
			_, err := uriio.LoadCredentialsConfig(path)
			if tc.wantErr != (err != nil) {
				t.Fatalf("LoadCredentialsConfig err = %v, wantErr = %v", err, tc.wantErr)
			}
		})
	}
}
