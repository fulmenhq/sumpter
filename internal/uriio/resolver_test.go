package uriio_test

import (
	"context"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

func TestResolverDefaultHandleImplicit(t *testing.T) {
	r := uriio.NewResolver(nil, nil)
	// Implicit "default" resolves even with no config (AWS default chain).
	hc, err := r.Resolve("")
	if err != nil {
		t.Fatalf("Resolve(\"\"): %v", err)
	}
	if hc.HasLiteralKeys() || hc.Profile != "" {
		t.Errorf("default handle = %+v, want empty (default chain)", hc)
	}
	if _, err := r.Resolve(uriio.DefaultHandleName); err != nil {
		t.Errorf("Resolve(default): %v", err)
	}
}

func TestResolverMissingHandleFailsLoud(t *testing.T) {
	r := uriio.NewResolver(nil, nil)
	_, err := r.Resolve("nope")
	if err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("Resolve(nope) = %v, want loud not-defined error", err)
	}
}

func TestResolverConfigHandle(t *testing.T) {
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{
		"src": {Profile: "prod-readonly", Region: "us-east-1"},
	}}
	r := uriio.NewResolver(cfg, nil)
	hc, err := r.Resolve("src")
	if err != nil {
		t.Fatalf("Resolve(src): %v", err)
	}
	if hc.Profile != "prod-readonly" || hc.Region != "us-east-1" {
		t.Errorf("resolved = %+v, want prod-readonly/us-east-1", hc)
	}
}

func TestResolverCLIOverrideSupersedesLiteralKeys(t *testing.T) {
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{
		"src": {AccessKeyID: uriio.Secret("AKIAIOSFODNN7EXAMPLE"), SecretAccessKey: uriio.Secret("literal-secret-value")},
	}}
	r := uriio.NewResolver(cfg, map[string]string{"src": "override-profile"})
	hc, err := r.Resolve("src")
	if err != nil {
		t.Fatalf("Resolve(src): %v", err)
	}
	if hc.Profile != "override-profile" {
		t.Errorf("Profile = %q, want override-profile", hc.Profile)
	}
	if hc.HasLiteralKeys() {
		t.Error("CLI override did not clear literal keys")
	}
}

func TestResolverCLIOverrideDefinesUndeclaredHandle(t *testing.T) {
	// An override alone defines a profile-only handle (no config entry needed).
	r := uriio.NewResolver(nil, map[string]string{"adhoc": "some-profile"})
	hc, err := r.Resolve("adhoc")
	if err != nil {
		t.Fatalf("Resolve(adhoc): %v", err)
	}
	if hc.Profile != "some-profile" {
		t.Errorf("Profile = %q, want some-profile", hc.Profile)
	}
}

// TestProviderPoolPoolsPerHandleBucket verifies one provider per unique
// (handle, bucket), reuse on repeat, and clean Close. Construction uses explicit
// credentials so no network or shared AWS config is required.
func TestProviderPoolPoolsPerHandleBucket(t *testing.T) {
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{
		"a": {Region: "us-east-1", AccessKeyID: uriio.Secret("AKIAIOSFODNN7EXAMPLE"), SecretAccessKey: uriio.Secret("secret-a"), Endpoint: "https://s3.example.com", ForcePathStyle: true},
		"b": {Region: "us-west-2", AccessKeyID: uriio.Secret("AKIAIOSFODNN7EXAMPLE"), SecretAccessKey: uriio.Secret("secret-b"), Endpoint: "https://s3.other.com", ForcePathStyle: true},
	}}
	pool := uriio.NewProviderPool(uriio.NewResolver(cfg, nil))
	t.Cleanup(func() { _ = pool.Close() })

	ctx := context.Background()
	p1, err := pool.Provider(ctx, "a", "bucket-1")
	if err != nil {
		t.Fatalf("Provider(a, bucket-1): %v", err)
	}
	// Same handle+bucket -> same instance, no new construction.
	p1again, err := pool.Provider(ctx, "a", "bucket-1")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p1again {
		t.Error("pool returned a new provider for the same handle+bucket")
	}
	// Different bucket and different handle -> distinct instances.
	if _, err := pool.Provider(ctx, "a", "bucket-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Provider(ctx, "b", "bucket-1"); err != nil {
		t.Fatal(err)
	}
	if got := pool.Len(); got != 3 {
		t.Errorf("pool size = %d, want 3 (a/bucket-1, a/bucket-2, b/bucket-1)", got)
	}

	if _, err := pool.Provider(ctx, "a", ""); err == nil {
		t.Error("Provider with empty bucket should error")
	}
	if _, err := pool.Provider(ctx, "undeclared", "bucket-1"); err == nil {
		t.Error("Provider with undeclared handle should error")
	}
}
