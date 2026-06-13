package uriio

import (
	"context"
	"strings"
	"testing"
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
