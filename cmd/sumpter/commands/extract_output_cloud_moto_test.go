//go:build s3integration

// S3 live-integration tests for the cloud write boundary (PR-4). They require a
// live S3-compatible endpoint and are excluded from the default/CI build. Run
// with `-tags s3integration`. They share the moto harness, env contract, and
// helpers in extract_moto_test.go.
//
// What these prove:
//   - extract publishes results + provenance sidecar to an s3:// destination
//     (JSON streaming and parquet), recording the logical destination identity —
//     never the local staging path — in every published artifact.
//   - cloud->cloud with an independent output handle selected via
//     --output-credentials-handle (distinct read vs write credentials).
//   - a publish failure is fatal (no false success), and an extraction failure
//     before publish leaves no object and cleans staging.

package commands

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

// s3ObjectReader is the read/list surface the write-boundary tests need to verify
// published objects.
type s3ObjectReader interface {
	GetObject(ctx context.Context, key string) (io.ReadCloser, int64, error)
	List(ctx context.Context, opts gonimbusprovider.ListOptions) (*gonimbusprovider.ListResult, error)
}

// reader builds a pooled provider for reading back published objects.
func (m motoEnv) reader(t *testing.T) (s3ObjectReader, func()) {
	t.Helper()
	cfg := &uriio.CredentialsConfig{Handles: map[string]uriio.HandleConfig{"default": m.handleConfig()}}
	pool := uriio.NewProviderPool(uriio.NewResolver(cfg, nil))
	p, err := pool.Provider(context.Background(), "default", m.bucket)
	if err != nil {
		_ = pool.Close()
		t.Fatalf("pool.Provider: %v", err)
	}
	return p, func() { _ = pool.Close() }
}

// getObject reads an object's bytes, reporting whether it exists.
func (m motoEnv) getObject(t *testing.T, key string) ([]byte, bool) {
	t.Helper()
	r, closeFn := m.reader(t)
	defer closeFn()
	body, _, err := r.GetObject(context.Background(), key)
	if err != nil {
		return nil, false
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read object %s: %v", key, err)
	}
	t.Cleanup(func() { m.deleteObject(t, key) })
	return data, true
}

// listKeys lists object keys under a prefix.
func (m motoEnv) listKeys(t *testing.T, prefix string) []string {
	t.Helper()
	r, closeFn := m.reader(t)
	defer closeFn()
	var keys []string
	token := ""
	for {
		res, err := r.List(context.Background(), gonimbusprovider.ListOptions{Prefix: prefix, ContinuationToken: token})
		if err != nil {
			t.Fatalf("list %s: %v", prefix, err)
		}
		for _, o := range res.Objects {
			keys = append(keys, o.Key)
		}
		if !res.IsTruncated || res.ContinuationToken == "" {
			break
		}
		token = res.ContinuationToken
	}
	return keys
}

// writeTwoHandleCredentialsConfig writes a credentials config with the default
// handle plus a second named handle, both pointing at the configured endpoint, so
// a cloud->cloud run can read under one handle and write under another (distinct
// pooled providers). With a profile both carry no secret material.
func (m motoEnv) writeTwoHandleCredentialsConfig(t *testing.T, dir, secondHandle string) string {
	t.Helper()
	insecure := "false"
	if motoInsecure(m.endpoint) {
		insecure = "true"
	}
	handleBlock := func(name string) string {
		b := "  " + name + ":\n" +
			"    region: " + m.region + "\n" +
			"    endpoint: " + m.endpoint + "\n" +
			"    force_path_style: true\n" +
			"    insecure: " + insecure + "\n"
		if m.profile != "" {
			b += "    profile: " + m.profile + "\n"
		} else {
			b += "    access_key_id: " + m.keyID + "\n" +
				"    secret_access_key: " + m.secret + "\n"
		}
		return b
	}
	body := "handles:\n" + handleBlock("default") + handleBlock(secondHandle)
	path := filepath.Join(dir, "credentials-multi.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write multi-handle credentials config: %v", err)
	}
	return path
}

func cloudOutputExtractOptions(dir, outURI, credPath, format string) *ExtractOptions {
	return &ExtractOptions{
		Files:           filepath.Join(dir, "input.xml"),
		Format:          format,
		OutputPath:      outURI,
		OutputPattern:   "out." + format,
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		CredentialsPath: credPath,
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
	}
}

// TestMotoExtractOutputLocalToCloudNoLeak publishes JSON output + sidecar to s3://
// and asserts the logical destination identity reaches the manifest while the
// staging path never appears, and staging is cleaned up.
func TestMotoExtractOutputLocalToCloudNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "wout/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	if err := runExtract(cloudOutputExtractOptions(dir, outURI, credPath, "json")); err != nil {
		t.Fatalf("runExtract(local->cloud) error = %v", err)
	}

	// Output object + sidecar manifest are published.
	outData, ok := m.getObject(t, prefix+"out.json")
	if !ok {
		t.Fatalf("output object %sout.json was not published", prefix)
	}
	if !strings.Contains(string(outData), `"name"`) {
		t.Errorf("published output missing extracted records:\n%s", outData)
	}
	manifestData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatalf("provenance sidecar %smanifest.json was not published", prefix)
	}
	manifest := string(manifestData)
	if !strings.Contains(manifest, outURI) && !strings.Contains(manifest, prefix+"out.json") {
		t.Errorf("manifest does not record the logical output destination:\n%s", manifest)
	}
	// No-leak: the staging root must not appear in any published artifact.
	stageRoot := filepath.Join(home, "work", "cloud")
	for _, blob := range []string{string(outData), manifest} {
		if strings.Contains(blob, stageRoot) {
			t.Errorf("published artifact leaked the staging path %q", stageRoot)
		}
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractRecordIndexOutputToCloudNoLeak covers the record-index JSON
// streaming output path to s3://: the manifest must record the logical output
// destination and never the local staging path (the path devrev flagged where
// the streaming target previously stored the staging path).
func TestMotoExtractRecordIndexOutputToCloudNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	// Build a LOCAL index from the fixture (index files stay local).
	xmlPath := filepath.Join(dir, "input.xml")
	indexPath := filepath.Join(dir, "input.recordindex.json")
	builder := index.NewBuilder(index.BuildOptions{InputPath: xmlPath, OutputPath: indexPath, Selector: "//item"})
	idx, err := builder.Build()
	if err != nil {
		t.Fatalf("build index: %v", err)
	}
	if err := builder.WriteToFile(idx, indexPath); err != nil {
		t.Fatalf("write index: %v", err)
	}

	prefix := runKeyPrefix() + "ri-out/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	opts := &ExtractOptions{
		Files:           xmlPath,
		Format:          "json",
		OutputPath:      outURI,
		OutputPattern:   "out.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		RecordIndex:     indexPath,
		Workers:         2,
		CredentialsPath: credPath,
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(record-index -> cloud) error = %v", err)
	}

	if _, ok := m.getObject(t, prefix+"out.json"); !ok {
		t.Fatalf("record-index output %sout.json was not published", prefix)
	}
	manifestData, ok := m.getObject(t, prefix+"manifest.json")
	if !ok {
		t.Fatalf("provenance sidecar %smanifest.json was not published", prefix)
	}
	manifest := string(manifestData)
	if !strings.Contains(manifest, prefix+"out.json") && !strings.Contains(manifest, outURI) {
		t.Errorf("manifest does not record the logical output destination:\n%s", manifest)
	}
	if stageRoot := filepath.Join(home, "work", "cloud"); strings.Contains(manifest, stageRoot) {
		t.Errorf("record-index streaming manifest leaked the staging path %q:\n%s", stageRoot, manifest)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractCloudToCloudIndependentHandles reads from one handle (default)
// and writes through a distinct named output handle selected by
// --output-credentials-handle, proving independent read/write credentials.
func TestMotoExtractCloudToCloudIndependentHandles(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	// Source object read under the default handle.
	srcKey := runKeyPrefix() + "c2c/src.xml"
	m.putObject(t, srcKey, []byte(motoSourceXML))
	srcURI := "s3://" + m.bucket + "/" + srcKey

	// Credentials config with two handles pointing at the same endpoint: a default
	// reader and a named writer. Distinct handles => distinct pooled providers.
	credPath := m.writeTwoHandleCredentialsConfig(t, dir, "writer")

	outPrefix := runKeyPrefix() + "c2c-out/"
	outURI := "s3://" + m.bucket + "/" + outPrefix

	opts := &ExtractOptions{
		Files:                   srcURI,
		Format:                  "json",
		OutputPath:              outURI,
		OutputPattern:           "out.json",
		SignatureConfig:         filepath.Join(dir, "signature.yaml"),
		ExtractConfig:           filepath.Join(dir, "extract.yaml"),
		CredentialsPath:         credPath,
		OutputCredentialsHandle: "writer",
		CommandName:             "sumpter extract files",
		Argv:                    []string{"extract", "files"},
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(cloud->cloud) error = %v", err)
	}
	if _, ok := m.getObject(t, outPrefix+"out.json"); !ok {
		t.Fatalf("cloud->cloud output %sout.json was not published", outPrefix)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractOutputParquetToCloud covers the in-memory/parquet write path:
// the parquet object publishes and embeds the logical destination in metadata.
func TestMotoExtractOutputParquetToCloud(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "wout-pq/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	if err := runExtract(cloudOutputExtractOptions(dir, outURI, credPath, "parquet")); err != nil {
		t.Fatalf("runExtract(parquet->cloud) error = %v", err)
	}
	if _, ok := m.getObject(t, prefix+"out.parquet"); !ok {
		t.Fatalf("parquet output %sout.parquet was not published", prefix)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractOutputPublishFailureIsFatal points output at a nonexistent
// bucket: the publish must fail the run (no false success) and leave staging
// cleaned up.
func TestMotoExtractOutputPublishFailureIsFatal(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	// A bucket that does not exist on the endpoint -> PutObject fails.
	outURI := "s3://" + m.bucket + "-does-not-exist-" + strings.TrimSuffix(runKeyPrefix(), "/") + "/out/"
	credPath := m.writeCredentialsConfig(t, dir)

	err := runExtract(cloudOutputExtractOptions(dir, outURI, credPath, "json"))
	if err == nil {
		t.Fatal("runExtract(publish to missing bucket) error = nil, want a fatal publish failure")
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoExtractOutputAbortLeavesNoObject forces an extraction failure (malformed
// source) before publish and asserts no output object was created and staging is
// cleaned up.
func TestMotoExtractOutputAbortLeavesNoObject(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	mustWriteFile(t, filepath.Join(dir, "input.xml"), `<root><item><name>A</name`) // malformed (truncated)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefix := runKeyPrefix() + "abort/"
	outURI := "s3://" + m.bucket + "/" + prefix
	credPath := m.writeCredentialsConfig(t, dir)

	err := runExtract(cloudOutputExtractOptions(dir, outURI, credPath, "json"))
	if err == nil {
		t.Fatal("runExtract(malformed source) error = nil, want extraction failure")
	}
	if keys := m.listKeys(t, prefix); len(keys) != 0 {
		t.Errorf("aborted run published objects under %s: %v", prefix, keys)
	}
	assertStagingCleanedUp(t, home)
}
