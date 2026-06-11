package uriio_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

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
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{
		"moto": {
			Region:          region,
			Endpoint:        endpoint,
			AccessKeyID:     uriio.Secret(os.Getenv("SUMPTER_TEST_S3_KEY_ID")),
			SecretAccessKey: uriio.Secret(os.Getenv("SUMPTER_TEST_S3_SECRET")),
			ForcePathStyle:  true,
			Insecure:        true, // local moto/MinIO is typically http://
		},
	}}
	pool := uriio.NewProviderPool(uriio.NewResolver(cfg, nil))
	t.Cleanup(func() { _ = pool.Close() })
	return pool, bucket
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

	key := "uriio-scaffold/roundtrip.txt"
	payload := []byte("cloudready scaffold round-trip")
	if err := prov.PutObject(ctx, key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("PutObject: %v", err)
	}

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
