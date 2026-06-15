package uriio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestPublishSizeWithinLimit covers the single-PUT ceiling guard: an object at or
// under the limit passes; over it fails clearly and points at the deferral.
func TestPublishSizeWithinLimit(t *testing.T) {
	if err := publishSizeWithinLimit("s3://b/k", maxSinglePutBytes); err != nil {
		t.Errorf("at-limit size = %v, want nil", err)
	}
	err := publishSizeWithinLimit("s3://b/k", maxSinglePutBytes+1)
	if err == nil {
		t.Fatal("over-limit size = nil, want a single-PUT-limit error")
	}
	if !strings.Contains(err.Error(), "single-PUT limit") || !strings.Contains(err.Error(), "SUM-006") {
		t.Errorf("error = %v, want a fail-clear single-PUT/SUM-006 message", err)
	}
}

// TestRedactSecrets proves credential cleartext is scrubbed from a string while
// non-secret content is preserved, and that empty/zero secrets are ignored.
func TestRedactSecrets(t *testing.T) {
	msg := "InvalidAccessKeyId: the key AKIAEXAMPLE123 was rejected"
	got := redactSecrets(msg, []string{"AKIAEXAMPLE123", ""})
	if strings.Contains(got, "AKIAEXAMPLE123") {
		t.Errorf("redacted string still contains the secret: %q", got)
	}
	if !strings.Contains(got, "[redacted]") {
		t.Errorf("redacted string missing placeholder: %q", got)
	}
	if !strings.Contains(got, "InvalidAccessKeyId") {
		t.Errorf("redaction dropped non-secret context: %q", got)
	}
	if redactSecrets("no secrets here", nil) != "no secrets here" {
		t.Error("redaction with no secrets altered the string")
	}
}

// TestRedactSecretsScrubsAKIDWithoutConfiguredSecret is the profile/default-chain
// case: sumpter holds no literal cleartext, but an SDK auth error may echo the
// resolved access key id. The last-defense pattern scrub must catch it even when
// secrets[] is empty.
func TestRedactSecretsScrubsAKIDWithoutConfiguredSecret(t *testing.T) {
	raw := "operation error S3: PutObject, InvalidAccessKeyId: the key AKIAIOSFODNN7EXAMPLE is not valid"
	got := redactSecrets(raw, nil) // no configured literal secrets (profile handle)
	if strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("AKID leaked despite no configured secret: %q", got)
	}
	if !strings.Contains(got, "[redacted-key-id]") {
		t.Errorf("AKID not replaced by placeholder: %q", got)
	}
	// Temporary (STS) and role key-id prefixes are also covered.
	for _, akid := range []string{"ASIAABCDEFGHIJKLMNOP", "AROAXYZ234567ABCDEFG"} {
		if redactAWSKeyIDs("id="+akid) != "id=[redacted-key-id]" {
			t.Errorf("AKID prefix not scrubbed: %s", akid)
		}
	}
}

// TestResolverRedactionSecrets proves a literal-key handle exposes its cleartext
// for scrubbing while a profile handle exposes nothing (the SDK holds its creds).
func TestResolverRedactionSecrets(t *testing.T) {
	cfg := &CredentialsConfig{Handles: map[string]HandleConfig{
		"litkey": {Region: "us-east-1", Endpoint: "https://s3.example.com", AccessKeyID: Secret("AKIALITERAL"), SecretAccessKey: Secret("supersecret")},
		"prof":   {Region: "us-east-1", Endpoint: "https://s3.example.com", Profile: "writer"},
	}}
	r := NewResolver(cfg, nil)

	lit := r.redactionSecrets("litkey")
	if len(lit) != 2 {
		t.Fatalf("literal-key handle secrets = %v, want 2 entries", lit)
	}
	joined := strings.Join(lit, " ")
	if !strings.Contains(joined, "AKIALITERAL") || !strings.Contains(joined, "supersecret") {
		t.Errorf("literal-key handle secrets missing expected values: %v", lit)
	}
	if prof := r.redactionSecrets("prof"); len(prof) != 0 {
		t.Errorf("profile handle secrets = %v, want none", prof)
	}
}

// TestSessionOpenOutputPublishFailurePropagates proves a cloud publish failure
// returns an error. This is the mechanism that makes a sidecar (provenance
// manifest) publish failure fatal after the primary output is already PUT: the
// write boundary publishes the primary first, then the sidecar through the same
// Publish path, and a non-nil Publish error propagates to fail the run. It points
// at a refused endpoint with a bounded context so the SDK fails fast.
func TestSessionOpenOutputPublishFailurePropagates(t *testing.T) {
	dir := t.TempDir()
	cfg := &CredentialsConfig{Handles: map[string]HandleConfig{
		"default": {Region: "us-east-1", Endpoint: "http://127.0.0.1:1", ForcePathStyle: true, Insecure: true},
	}}
	s := NewSession(NewResolver(cfg, nil), dir, "testrun")
	defer func() { _ = s.Close() }()

	tgt, err := s.OpenOutput(context.Background(), "s3://bucket/out/result.json", DefaultHandleName)
	if err != nil {
		t.Fatalf("OpenOutput(cloud) error = %v", err)
	}
	// Write a complete local artifact at the staging path, then publish it.
	if err := os.MkdirAll(filepath.Dir(tgt.LocalPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tgt.LocalPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := tgt.Publish(ctx); err == nil {
		t.Fatal("Publish to a refused endpoint = nil, want a fatal publish error")
	}
}

// TestValidateHandleName covers the shared handle-name slug rule: portable slugs
// are accepted; whitespace, key-shaped values, and empty are rejected with an
// actionable message.
func TestValidateHandleName(t *testing.T) {
	for _, name := range []string{"default", "reader", "writer-2", "dst.account", "A1"} {
		if err := ValidateHandleName(name); err != nil {
			t.Errorf("ValidateHandleName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range []string{"bad handle", "", "-leading", "has/slash", "key+like=value"} {
		if err := ValidateHandleName(name); err == nil {
			t.Errorf("ValidateHandleName(%q) = nil, want an invalid-handle-name error", name)
		}
	}
}

// TestOpenOutputLocalIsNoopPublish proves a local destination resolves to its own
// path with a no-op Publish (zero-drift), with or without a session.
func TestOpenOutputLocalIsNoopPublish(t *testing.T) {
	tgt, err := OpenOutput(context.Background(), OutputRequest{Reference: "/tmp/out/result.json"})
	if err != nil {
		t.Fatalf("OpenOutput(local) error = %v", err)
	}
	if tgt.Scheme != SchemeLocal || tgt.LocalPath != "/tmp/out/result.json" {
		t.Errorf("local target = %+v, want scheme=local path unchanged", tgt)
	}
	if err := tgt.Publish(context.Background()); err != nil {
		t.Errorf("local Publish = %v, want no-op nil", err)
	}
}
