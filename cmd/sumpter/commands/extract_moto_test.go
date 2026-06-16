//go:build s3integration

// S3 live-integration tests: require a live S3-compatible endpoint, excluded from
// the default/CI build. Run with `-tags s3integration` (see
// `make test-integration-s3` and docs/sop/cicd-and-local-gates.md).

package commands

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// motoInsecure reports whether the endpoint is plaintext http:// (local moto or
// MinIO). A BYO https endpoint keeps TLS enforced.
func motoInsecure(endpoint string) bool {
	return strings.HasPrefix(strings.ToLower(endpoint), "http://")
}

// Cloud source-in integration tests (S3-compatible). These drive the extract
// command end-to-end against a live moto/MinIO endpoint and assert the cloud
// read-boundary contract: the logical s3:// URI is what lands in provenance,
// manifests, output naming, and _runtime.source_file — never the local staged
// working path. They run only when the moto harness is configured (same env
// contract as internal/uriio/moto_test.go), so default CI stays hermetic.
//
//	SUMPTER_TEST_S3_ENDPOINT  e.g. http://127.0.0.1:9000
//	SUMPTER_TEST_S3_BUCKET    a pre-created bucket
//	SUMPTER_TEST_S3_PROFILE   AWS shared-config profile (preferred; resolves
//	                          creds from ~/.aws/credentials, no secret in env)
//	SUMPTER_TEST_S3_KEY_ID / SUMPTER_TEST_S3_SECRET   literal creds (if no profile)
//	SUMPTER_TEST_S3_REGION    optional, defaults to us-east-1

const motoSourceXML = `<root><item><name>A</name></item><item><name>B</name></item></root>`

type motoEnv struct {
	endpoint string
	bucket   string
	region   string
	profile  string
	keyID    string
	secret   string
}

func motoEnvOrSkip(t *testing.T) motoEnv {
	t.Helper()
	endpoint := os.Getenv("SUMPTER_TEST_S3_ENDPOINT")
	bucket := os.Getenv("SUMPTER_TEST_S3_BUCKET")
	if endpoint == "" || bucket == "" {
		t.Skip("SUMPTER_TEST_S3_ENDPOINT/SUMPTER_TEST_S3_BUCKET not set; skipping S3-compatible source-in test")
	}
	region := os.Getenv("SUMPTER_TEST_S3_REGION")
	if region == "" {
		region = "us-east-1"
	}
	m := motoEnv{
		endpoint: endpoint,
		bucket:   bucket,
		region:   region,
		profile:  os.Getenv("SUMPTER_TEST_S3_PROFILE"),
		keyID:    os.Getenv("SUMPTER_TEST_S3_KEY_ID"),
		secret:   os.Getenv("SUMPTER_TEST_S3_SECRET"),
	}
	// Fail closed: a BYO endpoint is configured, so require an explicit namespaced
	// credential reference. Skipping or falling back to the AWS default chain here
	// could hit the configured bucket with the developer's ambient identity.
	if ok, msg := byoCredsValid(m.profile, m.keyID, m.secret); !ok {
		t.Fatalf("S3 integration harness misconfigured: %s", msg)
	}
	return m
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

// handleConfig builds the HandleConfig for the configured endpoint, preferring a
// shared-config profile (no secret in env/process) over a literal key/secret pair.
func (m motoEnv) handleConfig() uriio.HandleConfig {
	hc := uriio.HandleConfig{
		Region:         m.region,
		Endpoint:       m.endpoint,
		ForcePathStyle: true,
		Insecure:       motoInsecure(m.endpoint),
	}
	if m.profile != "" {
		hc.Profile = m.profile
	} else {
		hc.AccessKeyID = uriio.Secret(m.keyID)
		hc.SecretAccessKey = uriio.Secret(m.secret)
	}
	return hc
}

// writeCredentialsConfig writes the credentials config the extract command loads:
// the default handle points at the configured endpoint. With a profile it carries
// no secret material (the SDK resolves the profile from ~/.aws/credentials).
func (m motoEnv) writeCredentialsConfig(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "credentials.yaml")
	insecure := "false"
	if motoInsecure(m.endpoint) {
		insecure = "true"
	}
	body := "handles:\n" +
		"  default:\n" +
		"    region: " + m.region + "\n" +
		"    endpoint: " + m.endpoint + "\n" +
		"    force_path_style: true\n" +
		"    insecure: " + insecure + "\n"
	if m.profile != "" {
		body += "    profile: " + m.profile + "\n"
	} else {
		body += "    access_key_id: " + m.keyID + "\n" +
			"    secret_access_key: " + m.secret + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write credentials config: %v", err)
	}
	return path
}

// runKeyPrefix returns a unique, per-run key prefix so each test run is isolated
// from leftover or concurrent objects in a shared bucket, and so listing
// assertions see exactly the objects this run created.
func runKeyPrefix() string {
	return "sumpter-itest/" + strconv.FormatInt(time.Now().UnixNano(), 36) + "/"
}

// putObject uploads payload to bucket/key through the uriio provider and registers
// a best-effort delete so the test leaves no artifact behind in the bucket.
func (m motoEnv) putObject(t *testing.T, key string, payload []byte) {
	t.Helper()
	prov, closePool := m.provider(t)
	defer closePool()
	if err := prov.PutObject(context.Background(), key, bytes.NewReader(payload), int64(len(payload))); err != nil {
		t.Fatalf("PutObject(%s): %v", key, err)
	}
	t.Cleanup(func() { m.deleteObject(t, key) })
}

// deleteObject removes a test object. Cleanup is best-effort: a failure is logged,
// not fatal, so it never masks the test outcome.
func (m motoEnv) deleteObject(t *testing.T, key string) {
	prov, closePool := m.provider(t)
	defer closePool()
	if err := prov.DeleteObject(context.Background(), key); err != nil {
		t.Logf("cleanup: delete %s: %v", key, err)
	}
}

// provider builds a pooled provider for the configured endpoint/bucket and returns
// it with a close function.
func (m motoEnv) provider(t *testing.T) (prov interface {
	PutObject(ctx context.Context, key string, r io.Reader, size int64) error
	DeleteObject(ctx context.Context, key string) error
}, closePool func()) {
	t.Helper()
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{
		"default": m.handleConfig(),
	}}
	pool := uriio.NewProviderPool(uriio.NewResolver(cfg, nil))
	p, err := pool.Provider(context.Background(), "default", m.bucket)
	if err != nil {
		_ = pool.Close()
		t.Fatalf("pool.Provider: %v", err)
	}
	return p, func() { _ = pool.Close() }
}

// TestBYOCredsValid locks the fail-closed BYO credential-mode matrix: exactly one
// explicit namespaced reference (a profile, or both literal keys) is accepted;
// half-pairs, mixing, and an empty credential (which would silently use the AWS
// default chain) are rejected.
func TestBYOCredsValid(t *testing.T) {
	cases := []struct {
		name              string
		profile, kid, sec string
		want              bool
	}{
		{"profile only", "p", "", "", true},
		{"both keys", "", "k", "s", true},
		{"key only (half pair)", "", "k", "", false},
		{"secret only (half pair)", "", "", "s", false},
		{"neither (would use ambient)", "", "", "", false},
		{"profile + key (mixing)", "p", "k", "", false},
		{"profile + both keys (mixing)", "p", "k", "s", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if ok, _ := byoCredsValid(tc.profile, tc.kid, tc.sec); ok != tc.want {
				t.Errorf("byoCredsValid(%q,%q,%q) = %v, want %v", tc.profile, tc.kid, tc.sec, ok, tc.want)
			}
		})
	}
}

// TestMotoExtractSourceInNoLeak proves an s3:// --files source extracts, and the
// logical URI (not the staged local path) is what reaches every published surface.
func TestMotoExtractSourceInNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	// Stage a private SUMPTER_HOME so the run's cloud staging lives somewhere we
	// can inspect for cleanup afterward.
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "source-in/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key

	outDir := filepath.Join(dir, "out-s3-files")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	opts := newMatrixExtractOptions(dir, logicalURI, outDir)
	opts.CredentialsPath = m.writeCredentialsConfig(t, dir)

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(s3 source) error = %v", err)
	}

	assertLogicalIdentityNoLeak(t, outDir, home, logicalURI)

	// FU-2: the cloud input records the resolved input handle ("default"); the
	// local output records none. No resolved credential material reaches the sidecar.
	manifestData, err := os.ReadFile(filepath.Join(outDir, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	assertManifestHandleNames(t, manifestData, m, "default", "")
}

// TestMotoExtractInputPrefixNoLeak proves an s3:// prefix --input-path is listed
// and each staged object records its own logical URI, never the staged path.
func TestMotoExtractInputPrefixNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "prefix-in/"
	keyA := prefix + "a.xml"
	keyB := prefix + "b.xml"
	m.putObject(t, keyA, []byte(motoSourceXML))
	m.putObject(t, keyB, []byte(motoSourceXML))

	outDir := filepath.Join(dir, "out-s3-prefix")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}
	opts := newMatrixExtractOptions(dir, "", outDir)
	opts.Files = ""
	opts.InputPath = "s3://" + m.bucket + "/" + prefix
	opts.IncludePattern = "*.xml"
	opts.CredentialsPath = m.writeCredentialsConfig(t, dir)

	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(s3 prefix) error = %v", err)
	}

	// Both objects' logical URIs must appear in the manifest; the staged path must not.
	manifest := readMotoArtifact(t, filepath.Join(outDir, "manifest.json"))
	for _, key := range []string{keyA, keyB} {
		uri := "s3://" + m.bucket + "/" + key
		if !strings.Contains(manifest, uri) {
			t.Errorf("manifest missing logical URI %q", uri)
		}
	}
	assertNoStagingLeak(t, outDir, home)
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractDryRunDoesNotStage proves a cloud --dry-run is truly dry: it
// previews the logical source identity without downloading or staging any object
// (B3 / SUM-042). The single-object case is the sharp discriminator — a dry run
// of a NON-EXISTENT object still succeeds and echoes its URI, because nothing is
// fetched; the previous stage-then-list path would have tried to download it and
// failed. Staging is lazy (created on first acquire), so a true dry run also
// leaves no staging directory behind at all.
func TestMotoExtractDryRunDoesNotStage(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)
	stageRoot := filepath.Join(home, "work", "cloud")

	outDir := filepath.Join(dir, "out-dryrun")
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		t.Fatal(err)
	}

	// 1) A single cloud object that was never uploaded. A truly-dry run succeeds
	//    and echoes the logical URI without any fetch.
	missingURI := "s3://" + m.bucket + "/" + runKeyPrefix() + "dryrun/never-uploaded.xml"
	opts := newMatrixExtractOptions(dir, missingURI, outDir)
	opts.CredentialsPath = m.writeCredentialsConfig(t, dir)
	opts.DryRun = true

	var runErr error
	out := captureStdout(t, func() { runErr = runExtract(opts) })
	if runErr != nil {
		t.Fatalf("dry-run of a non-existent cloud object should succeed (no fetch), got: %v", runErr)
	}
	if !strings.Contains(out, missingURI) {
		t.Errorf("dry-run output missing logical URI %q:\n%s", missingURI, out)
	}
	if _, err := os.Stat(stageRoot); !os.IsNotExist(err) {
		t.Errorf("dry-run created a staging directory %q (err=%v); nothing should be staged", stageRoot, err)
	}

	// 2) A real prefix with two objects. A dry run lists (allowed — you must list
	//    to know what matches) and echoes both logical URIs, still staging nothing.
	prefix := runKeyPrefix() + "dryrun-prefix/"
	keyA := prefix + "a.xml"
	keyB := prefix + "b.xml"
	m.putObject(t, keyA, []byte(motoSourceXML))
	m.putObject(t, keyB, []byte(motoSourceXML))

	popts := newMatrixExtractOptions(dir, "", outDir)
	popts.Files = ""
	popts.InputPath = "s3://" + m.bucket + "/" + prefix
	popts.IncludePattern = "*.xml"
	popts.CredentialsPath = m.writeCredentialsConfig(t, dir)
	popts.DryRun = true

	pout := captureStdout(t, func() { runErr = runExtract(popts) })
	if runErr != nil {
		t.Fatalf("dry-run of a cloud prefix should succeed, got: %v", runErr)
	}
	for _, key := range []string{keyA, keyB} {
		uri := "s3://" + m.bucket + "/" + key
		if !strings.Contains(pout, uri) {
			t.Errorf("dry-run prefix output missing logical URI %q:\n%s", uri, pout)
		}
	}
	if _, err := os.Stat(stageRoot); !os.IsNotExist(err) {
		t.Errorf("dry-run (prefix) created a staging directory %q; listing must not stage", stageRoot)
	}
}

// assertLogicalIdentityNoLeak runs the full leak/identity assertion set for a
// single-source run.
func assertLogicalIdentityNoLeak(t *testing.T, outDir, home, logicalURI string) {
	t.Helper()

	// _runtime.source_file in the record output is the logical URI.
	record := readMotoArtifact(t, filepath.Join(outDir, soleRecordOutputName(t, outDir)))
	if !strings.Contains(record, `"source_file":"`+logicalURI+`"`) {
		t.Errorf("record _runtime.source_file is not the logical URI %q:\n%s", logicalURI, record)
	}

	// Manifest input path is the logical URI.
	manifest := readMotoArtifact(t, filepath.Join(outDir, "manifest.json"))
	if !strings.Contains(manifest, logicalURI) {
		t.Errorf("manifest does not record logical URI %q:\n%s", logicalURI, manifest)
	}

	assertNoStagingLeak(t, outDir, home)
	assertStagingCleanedUp(t, home)
}

// assertNoStagingLeak fails if the run's staging directory path appears anywhere
// in the produced artifacts.
func assertNoStagingLeak(t *testing.T, outDir, home string) {
	t.Helper()
	stageRoot := filepath.Join(home, "work", "cloud")
	entries, err := os.ReadDir(outDir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", outDir, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		content := readMotoArtifact(t, filepath.Join(outDir, e.Name()))
		if strings.Contains(content, stageRoot) {
			t.Errorf("artifact %s leaked the staging path %q", e.Name(), stageRoot)
		}
	}
}

// assertStagingCleanedUp fails if any run staging directory survived the run —
// Session.Close must remove it on every exit path.
func assertStagingCleanedUp(t *testing.T, home string) {
	t.Helper()
	stageRoot := filepath.Join(home, "work", "cloud")
	entries, err := os.ReadDir(stageRoot)
	if os.IsNotExist(err) {
		return // never created or fully removed
	}
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", stageRoot, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			t.Errorf("staging run directory survived the run (not cleaned up): %s", e.Name())
		}
	}
}

func readMotoArtifact(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(data)
}

// soleRecordOutputName returns the single record-output filename in dir (the file
// that is not the provenance manifest).
func soleRecordOutputName(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var found string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "manifest.json" {
			continue
		}
		if found != "" {
			t.Fatalf("expected one record-output file, found %q and %q", found, e.Name())
		}
		found = e.Name()
	}
	if found == "" {
		t.Fatalf("no record-output file in %s", dir)
	}
	return found
}
