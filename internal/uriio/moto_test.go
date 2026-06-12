//go:build s3integration

// This file is part of the S3 live-integration class: it requires a live
// S3-compatible endpoint and is excluded from the default/CI build. Build/run it
// with `-tags s3integration` (see `make test-integration-s3` and
// docs/sop/cicd-and-local-gates.md).

package uriio_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// Moto/MinIO scaffold for S3-compatible integration tests.
//
// These tests are the harness the cloud read/write boundaries build on. They run
// only when SUMPTER_TEST_S3_ENDPOINT points at a live S3-compatible server
// (moto or MinIO), so CI stays hermetic and needs no live AWS. Configure with:
//
//	SUMPTER_TEST_S3_ENDPOINT  e.g. http://127.0.0.1:9000
//	SUMPTER_TEST_S3_BUCKET    a pre-created bucket
//	SUMPTER_TEST_S3_KEY_ID    access key id
//	SUMPTER_TEST_S3_SECRET    secret access key
//	SUMPTER_TEST_S3_REGION    optional, defaults to us-east-1

// motoPool builds a pool against the configured S3-compatible endpoint, or skips
// the test when the harness is not configured.
func motoPool(t *testing.T) (*uriio.ProviderPool, string) {
	t.Helper()
	endpoint := os.Getenv("SUMPTER_TEST_S3_ENDPOINT")
	bucket := os.Getenv("SUMPTER_TEST_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("SUMPTER_TEST_S3_ENDPOINT/SUMPTER_TEST_S3_BUCKET not set; skipping S3-compatible integration test")
	}
	region := os.Getenv("SUMPTER_TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	profile := os.Getenv("SUMPTER_TEST_S3_PROFILE")
	keyID := os.Getenv("SUMPTER_TEST_S3_KEY_ID")
	secret := os.Getenv("SUMPTER_TEST_S3_SECRET")
	// Fail closed: a BYO endpoint is configured, so require an explicit namespaced
	// credential reference rather than letting the AWS default chain take over.
	if ok, msg := byoCredsValid(profile, keyID, secret); !ok {
		t.Fatalf("S3 integration harness misconfigured: %s", msg)
	}
	hc := uriio.HandleConfig{
		Region:         region,
		Endpoint:       endpoint,
		ForcePathStyle: true,
		// Insecure only for an http:// endpoint (local moto/MinIO); a BYO
		// https endpoint keeps TLS enforced.
		Insecure: strings.HasPrefix(strings.ToLower(endpoint), "http://"),
	}
	// Prefer a shared-config profile when given (the SDK reads ~/.aws/credentials
	// directly, so no secret enters the env/process); otherwise use the literal
	// key/secret pair.
	if profile != "" {
		hc.Profile = profile
	} else {
		hc.AccessKeyID = uriio.Secret(keyID)
		hc.SecretAccessKey = uriio.Secret(secret)
	}
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{"moto": hc}}
	pool := uriio.NewProviderPool(uriio.NewResolver(cfg, nil))
	t.Cleanup(func() { _ = pool.Close() })
	return pool, bucket
}

// byoCredsValid enforces exactly one explicit credential mode for a BYO endpoint:
// a profile, or both literal keys. Half-pairs, profile+literal mixing, and an
// empty credential (which would silently use the AWS default chain) are rejected.
func byoCredsValid(profile, keyID, secret string) (bool, string) {
	hasProfile := profile != ""
	hasKey := keyID != ""
	hasSecret := secret != ""
	switch {
	case hasProfile && (hasKey || hasSecret):
		return false, "set SUMPTER_TEST_S3_PROFILE or the KEY_ID/SECRET pair, not both"
	case hasProfile:
		return true, ""
	case hasKey && hasSecret:
		return true, ""
	case hasKey != hasSecret:
		return false, "SUMPTER_TEST_S3_KEY_ID and SUMPTER_TEST_S3_SECRET must both be set"
	default:
		return false, "set SUMPTER_TEST_S3_PROFILE (preferred) or both SUMPTER_TEST_S3_KEY_ID and SUMPTER_TEST_S3_SECRET; refusing to fall back to ambient AWS credentials"
	}
}

// TestMotoRoundTrip is the scaffold smoke test: put then get an object via the
// pooled provider. Skipped unless the S3-compatible harness is configured.
func TestMotoRoundTrip(t *testing.T) {
	pool, bucket := motoPool(t)
	ctx := context.Background()

	prov, err := pool.Provider(ctx, "moto", bucket)
	if err != nil {
		t.Fatalf("pool.Provider: %v", err)
	}

	// Unique key per run + best-effort delete so the suite leaves no artifact in a
	// shared bucket.
	key := "sumpter-itest/" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/roundtrip.txt"
	payload := []byte("cloudready scaffold round-trip")
	if err := prov.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("PutObject: %v", err)
	}
	t.Cleanup(func() {
		if err := prov.DeleteObject(ctx, key); err != nil {
			t.Logf("cleanup: delete %s: %v", key, err)
		}
	})

	rc, _, err := prov.GetObject(ctx, key)
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read object: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("round-trip mismatch: got %q, want %q", got, payload)
	}
}
