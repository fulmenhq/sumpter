//go:build s3integration

package commands

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMotoDryRunHeadsNamedObjects proves a dry run checks each named s3://
// object with a metadata read: a missing object fails as not found and a
// present one passes.
func TestMotoDryRunHeadsNamedObjects(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	key := runKeyPrefix() + "dry/doc.xml"
	m.putObject(t, key, []byte(motoSourceXML))
	present := "s3://" + m.bucket + "/" + key
	missing := "s3://" + m.bucket + "/" + runKeyPrefix() + "dry/missing.xml"

	opts := newMatrixExtractOptions(dir, missing, "")
	opts.CredentialsPath = m.writeCredentialsConfig(t, dir)
	opts.DryRun = true
	err := runExtract(opts)
	var unavailable *inputUnavailableError
	if !errors.As(err, &unavailable) || !strings.HasSuffix(err.Error(), missing+": not found") {
		t.Fatalf("dry run of a missing object: error = %v, want input unavailable not found", err)
	}

	opts = newMatrixExtractOptions(dir, present, "")
	opts.CredentialsPath = m.writeCredentialsConfig(t, dir)
	opts.DryRun = true
	if err := runExtract(opts); err != nil {
		t.Fatalf("dry run of a present object failed: %v", err)
	}
}

// TestMotoEmptyPrefixFailsBeforeOutput proves a zero-object s3:// prefix fails
// on a real run and a dry run, leaving an earlier manifest untouched.
func TestMotoEmptyPrefixFailsBeforeOutput(t *testing.T) {
	m := motoEnvOrSkip(t)
	dir := createExtractManifestFixture(t)
	prefix := "s3://" + m.bucket + "/" + runKeyPrefix() + "empty/"
	out := filepath.Join(dir, "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	prior := []byte(`{"from":"an earlier run"}`)
	if err := os.WriteFile(filepath.Join(out, "manifest.json"), prior, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dry := range []bool{false, true} {
		opts := newMatrixExtractOptions(dir, "", out)
		opts.Files = ""
		opts.InputPath = prefix
		opts.IncludePattern = "*.xml"
		opts.CredentialsPath = m.writeCredentialsConfig(t, dir)
		opts.DryRun = dry
		if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "no input files matched under "+prefix) {
			t.Fatalf("dry=%v: error = %v, want a no-match failure", dry, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(out, "manifest.json"))
	if err != nil || string(got) != string(prior) {
		t.Fatalf("earlier manifest changed: %q %v", got, err)
	}
}

// TestMotoMultiBoundedMissingObjectAccounted proves extract-multi in bounded
// mode records a missing s3:// object as input_unavailable and keeps the good
// sibling, and that a zero-object prefix fails without touching the output.
func TestMotoMultiBoundedMissingObjectAccounted(t *testing.T) {
	m := motoEnvOrSkip(t)
	f := newMultiAvailabilityFixture(t)
	prefix := runKeyPrefix()
	good := prefix + "multi/good.xml"
	m.putObject(t, good, []byte(multiTargetXML))
	list := filepath.Join(f.dir, "cloud.txt")
	body := "s3://" + m.bucket + "/" + good + "\ns3://" + m.bucket + "/" + prefix + "multi/missing.xml\n"
	if err := os.WriteFile(list, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	creds := m.writeCredentialsConfig(t, f.dir)
	fresh := filepath.Join(f.dir, "fresh")
	shared := multiSharedOptions{
		FileList: list, ContinueOnError: true, CredentialsPath: creds, OutputPath: fresh, RunID: testMultiRunID,
		CloudInputMode: cloudInputModeBounded, CloudStagingMaxBytes: 1 << 20, CloudStagingMaxFiles: 4, CloudObjectMaxBytes: 1 << 20,
	}
	if err := runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now()); err == nil || !strings.Contains(err.Error(), "partial failure: applied=1 failed=1") {
		t.Fatalf("error = %v, want a partial failure with the good sibling applied", err)
	}
	raw, err := os.ReadFile(filepath.Join(fresh, "r1", "failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"reason": "input_unavailable"`) || !strings.Contains(string(raw), "input unavailable: not found") {
		t.Fatalf("failures.json = %s", raw)
	}

	err = f.run(multiSharedOptions{InputPath: "s3://" + m.bucket + "/" + prefix + "none/", CredentialsPath: creds})
	if err == nil || !strings.Contains(err.Error(), "no input files matched") {
		t.Fatalf("zero-object prefix: error = %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.recipeOut, "manifest.json")); string(got) != string(f.prior) {
		t.Fatalf("earlier manifest rewritten: %s", got)
	}
}
