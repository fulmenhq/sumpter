//go:build s3integration

// S3 live-integration tests for the `inspect` cloud source-read boundary (PR-3).
// They require a live S3-compatible endpoint and are excluded from the
// default/CI build. Run with `-tags s3integration` (see `make test-integration-s3`
// and docs/sop/cicd-and-local-gates.md). They share the moto harness, env
// contract, and helpers in extract_moto_test.go.
//
// What these prove:
//   - `inspect` against an s3:// source stages to a local working copy through the
//     shared single-object read boundary, inspects the staged bytes, and records
//     the LOGICAL s3:// URI in the report (never the staging path).
//   - `inspect --generate-config` against an s3:// source records the logical URI
//     as the generated config's source identity, never the staging path.
//   - The run's staging directory is cleaned up on exit.

package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runInspectCloud drives the real `inspect` command against a cloud source,
// writing a JSON report to outPath.
func runInspectCloud(t *testing.T, logicalURI, outPath, credPath string, extraArgs ...string) {
	t.Helper()
	cmd := NewInspectCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	args := []string{
		logicalURI,
		"--format", "json",
		"--output", outPath,
		"--credentials", credPath,
	}
	cmd.SetArgs(append(args, extraArgs...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect (cloud source %s) error = %v", logicalURI, err)
	}
}

// TestMotoInspectCloudReadNoLeak proves `inspect` reads an s3:// source through
// the staged read boundary and records the logical URI (not the staging path) in
// the report.
func TestMotoInspectCloudReadNoLeak(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "inspect/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key

	outPath := filepath.Join(dir, "inspect-report.json")
	runInspectCloud(t, logicalURI, outPath, m.writeCredentialsConfig(t, dir))

	reportBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read inspect report: %v", err)
	}
	var report InspectReportV0
	if err := json.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("decode inspect report: %v", err)
	}

	// The report records the logical s3:// URI as its source identity.
	if report.Input.Path != logicalURI {
		t.Errorf("inspect report input.path = %q, want logical URI %q", report.Input.Path, logicalURI)
	}
	// Sanity: the inspection actually ran against the staged bytes.
	if report.Input.SizeBytes != int64(len(motoSourceXML)) {
		t.Errorf("inspect report size_bytes = %d, want %d", report.Input.SizeBytes, len(motoSourceXML))
	}

	// The staging path must not leak into the report.
	stageRoot := filepath.Join(home, "work", "cloud")
	if strings.Contains(string(reportBytes), stageRoot) {
		t.Errorf("inspect report leaked the staging path %q:\n%s", stageRoot, reportBytes)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoInspectGenerateConfigCloud proves `inspect --generate-config` against an
// s3:// source records the logical URI as the generated config's source identity,
// never the staging path.
func TestMotoInspectGenerateConfigCloud(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	key := runKeyPrefix() + "inspect-cfg/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	logicalURI := "s3://" + m.bucket + "/" + key

	outPath := filepath.Join(dir, "generated-extract.yaml")
	runInspectCloud(t, logicalURI, outPath, m.writeCredentialsConfig(t, dir),
		"--generate-config", "--record-selector", "//item")

	cfgBytes, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}
	cfg := string(cfgBytes)
	if !strings.Contains(cfg, logicalURI) {
		t.Errorf("generated config does not record logical URI %q:\n%s", logicalURI, cfg)
	}
	stageRoot := filepath.Join(home, "work", "cloud")
	if strings.Contains(cfg, stageRoot) {
		t.Errorf("generated config leaked the staging path %q:\n%s", stageRoot, cfg)
	}
	assertStagingCleanedUp(t, home)
}

// TestMotoInspectRejectsCloudPrefix proves a cloud prefix (not a single object) is
// rejected for inspect, before any staging.
func TestMotoInspectRejectsCloudPrefix(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)

	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	prefixURI := "s3://" + m.bucket + "/" + runKeyPrefix() + "inspect-prefix/"
	outPath := filepath.Join(dir, "should-not-exist.json")

	cmd := NewInspectCommand()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{prefixURI, "--format", "json", "--output", outPath, "--credentials", m.writeCredentialsConfig(t, dir)})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("inspect of a cloud prefix %q should fail (single object required)", prefixURI)
	}
	if !strings.Contains(err.Error(), "single object") {
		t.Errorf("error = %v, want a single-object rejection", err)
	}
}
