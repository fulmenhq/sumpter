//go:build s3integration

// S3 live-integration tests for anonymous (unsigned, public-bucket) reads (PR-4).
// They require a live S3-compatible endpoint and are excluded from the default/CI
// build. Run with `-tags s3integration` (see `make test-integration-s3`). They
// share the moto harness and helpers in extract_moto_test.go.
//
// What these prove:
//   - An `anonymous: true` handle reads an object with unsigned requests (no
//     credential material) and extracts normally.
//   - An anonymous handle is rejected on the OUTPUT side before any staging — PA1
//     fail-closed, not relying on the provider's PutObject-time read-only error.

package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// setBucketPublicRead grants anonymous s3:GetObject on the whole bucket via a
// bucket policy, so unsigned reads succeed against the test double (moto denies
// anonymous access to private objects by default). It uses the AWS SDK directly —
// gonimbus exposes no policy API — with the harness's credentials. Test objects
// use unique key prefixes, so a bucket-wide read policy is harmless.
func (m motoEnv) setBucketPublicRead(t *testing.T, bucket string) {
	t.Helper()
	client := s3.New(s3.Options{
		Region:       m.region,
		BaseEndpoint: aws.String(m.endpoint),
		UsePathStyle: true,
		Credentials:  credentials.NewStaticCredentialsProvider(m.keyID, m.secret, ""),
	})
	policy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":"*","Action":"s3:GetObject","Resource":"arn:aws:s3:::` + bucket + `/*"}]}`
	if _, err := client.PutBucketPolicy(context.Background(), &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(policy),
	}); err != nil {
		t.Fatalf("set bucket public-read policy: %v", err)
	}
}

// writeAnonymousCredentialsConfig writes a credentials config whose handle reads
// anonymously (unsigned) from the configured endpoint. No credential material is
// present — the handle is read-only by construction.
func (m motoEnv) writeAnonymousCredentialsConfig(t *testing.T, dir, handleName string) string {
	t.Helper()
	insecure := "false"
	if motoInsecure(m.endpoint) {
		insecure = "true"
	}
	body := "handles:\n" +
		"  " + handleName + ":\n" +
		"    region: " + m.region + "\n" +
		"    endpoint: " + m.endpoint + "\n" +
		"    force_path_style: true\n" +
		"    insecure: " + insecure + "\n" +
		"    anonymous: true\n"
	path := filepath.Join(dir, "credentials-anon.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write anonymous credentials config: %v", err)
	}
	return path
}

// TestMotoExtractAnonymousRead proves an anonymous handle reads an s3:// source
// with unsigned requests and extracts normally — the records and provenance are
// produced exactly as for a credentialed read, and the provenance records the
// anonymous handle name (truthful posture, no creds implied).
func TestMotoExtractAnonymousRead(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "anon/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	m.setBucketPublicRead(t, m.bucket)
	logicalURI := "s3://" + m.bucket + "/" + key

	outDir := filepath.Join(dir, "out-anon")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	opts := newMatrixExtractOptions(dir, logicalURI, outDir)
	opts.CredentialsPath = m.writeAnonymousCredentialsConfig(t, dir, "public")
	opts.InputCredentialsHandle = "public"

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(anonymous read) error = %v", err)
	}

	// The extraction produced records, and provenance records the anonymous handle
	// name as the input identity — never the staging path or any credential.
	assertLogicalIdentityNoLeak(t, outDir, home, logicalURI)
	manifest := readMotoArtifact(t, filepath.Join(outDir, "manifest.json"))
	if !strings.Contains(manifest, `"credentials_handle": "public"`) {
		t.Errorf("manifest does not record the anonymous input handle name:\n%s", manifest)
	}
}

// TestMotoExtractAnonymousWriteRejected proves an anonymous handle is rejected on
// the OUTPUT side (PA1), and the rejection happens before any object is published
// or staging directory created.
func TestMotoExtractAnonymousWriteRejected(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	outPrefix := runKeyPrefix() + "anon-out/"
	outURI := "s3://" + m.bucket + "/" + outPrefix

	opts := cloudOutputExtractOptions(dir, outURI, m.writeAnonymousCredentialsConfig(t, dir, "public"), "json")
	opts.OutputCredentialsHandle = "public"

	err := runExtract(opts)
	if err == nil {
		t.Fatalf("runExtract with an anonymous output handle should fail (read-only)")
	}
	if !strings.Contains(err.Error(), "anonymous") {
		t.Errorf("error = %v, want an anonymous read-only rejection", err)
	}

	// No object was published, and no staging directory was created.
	if _, ok := m.getObject(t, outPrefix+"out.json"); ok {
		t.Errorf("anonymous output handle published an object %sout.json", outPrefix)
	}
	if _, statErr := os.Stat(filepath.Join(home, "work", "cloud")); !os.IsNotExist(statErr) {
		t.Errorf("anonymous output handle created a staging directory (err=%v); PA1 must fail before staging", statErr)
	}
}
