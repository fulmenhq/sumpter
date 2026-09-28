package commands

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const multiTargetXML = `<root><TargetElement><Name>A</Name></TargetElement></root>`

// multiAvailabilityFixture is an extract-multi recipe, one good XML input, and
// an output root whose recipe directory holds a manifest from an earlier run.
type multiAvailabilityFixture struct {
	ws, dir, good, out, recipeOut string
	prior                         []byte
}

func newMultiAvailabilityFixture(t *testing.T) multiAvailabilityFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := multiAvailabilityFixture{
		ws:    writeMultiRecipeWorkspace(t, "r1"),
		dir:   dir,
		good:  filepath.Join(dir, "good.xml"),
		out:   filepath.Join(dir, "out"),
		prior: []byte(`{"from":"an earlier run"}`),
	}
	f.recipeOut = filepath.Join(f.out, "r1")
	if err := os.WriteFile(f.good, []byte(multiTargetXML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(f.recipeOut, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.recipeOut, "manifest.json"), f.prior, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f multiAvailabilityFixture) run(shared multiSharedOptions) error {
	shared.OutputPath = f.out
	shared.RunID = testMultiRunID
	return runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now())
}

func TestMultiZeroDiscoveryFailsBeforeOutput(t *testing.T) {
	for _, continueOnError := range []bool{false, true} {
		f := newMultiAvailabilityFixture(t)
		empty := filepath.Join(f.dir, "empty")
		if err := os.MkdirAll(empty, 0o750); err != nil {
			t.Fatal(err)
		}
		err := f.run(multiSharedOptions{InputPath: empty, ContinueOnError: continueOnError})
		if err == nil || !strings.Contains(err.Error(), "no input files matched under "+empty) {
			t.Fatalf("continue=%v: error = %v, want a no-match failure", continueOnError, err)
		}
		entries, rerr := os.ReadDir(f.recipeOut)
		if rerr != nil || len(entries) != 1 {
			t.Fatalf("continue=%v: recipe output changed: %v %v", continueOnError, entries, rerr)
		}
		if got, _ := os.ReadFile(filepath.Join(f.recipeOut, "manifest.json")); string(got) != string(f.prior) {
			t.Fatalf("continue=%v: earlier manifest rewritten: %s", continueOnError, got)
		}
	}
}

func TestMultiMissingInputRecordedInputUnavailable(t *testing.T) {
	f := newMultiAvailabilityFixture(t)
	fresh := filepath.Join(f.dir, "fresh")
	list := filepath.Join(f.dir, "list.txt")
	if err := os.WriteFile(list, []byte(f.good+"\n"+filepath.Join(f.dir, "missing.xml")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	shared := multiSharedOptions{FileList: list, ContinueOnError: true, OutputPath: fresh, RunID: testMultiRunID}
	if err := runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now()); err == nil || !strings.Contains(err.Error(), "partial failure: applied=1 failed=1") {
		t.Fatalf("error = %v, want a non-zero partial failure", err)
	}
	raw, err := os.ReadFile(filepath.Join(fresh, "r1", "failures.json"))
	if err != nil {
		t.Fatal(err)
	}
	var failures struct {
		Failures []struct{ Reason, Detail string } `json:"failures"`
	}
	if err := json.Unmarshal(raw, &failures); err != nil {
		t.Fatal(err)
	}
	if len(failures.Failures) != 1 || failures.Failures[0].Reason != "input_unavailable" || failures.Failures[0].Detail != "input unavailable: not found" {
		t.Fatalf("failures = %+v", failures.Failures)
	}

	// Fail-fast reports the same bounded error.
	shared = multiSharedOptions{FileList: list, OutputPath: filepath.Join(f.dir, "fresh2"), RunID: testMultiRunID}
	err = runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now())
	var unavailable *inputUnavailableError
	if !errors.As(err, &unavailable) || !strings.HasSuffix(err.Error(), ": not found") {
		t.Fatalf("fail-fast error = %v, want input unavailable", err)
	}
}

func TestMultiEagerCloudContinueOnErrorRefused(t *testing.T) {
	f := newMultiAvailabilityFixture(t)
	creds := failIfCalledCredentials(t, f.dir)
	list := filepath.Join(f.dir, "mixed.txt")
	if err := os.WriteFile(list, []byte(f.good+"\ns3://bucket/in/doc.xml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.run(multiSharedOptions{FileList: list, ContinueOnError: true, CredentialsPath: creds})
	if err == nil || err.Error() != "--continue-on-error with s3:// inputs requires --cloud-input-mode bounded in this release" {
		t.Fatalf("error = %v, want the eager cloud refusal", err)
	}
	if got, _ := os.ReadFile(filepath.Join(f.recipeOut, "manifest.json")); string(got) != string(f.prior) {
		t.Fatalf("earlier manifest rewritten: %s", got)
	}
}

func TestBoundedCloudContinueOnErrorNotRefused(t *testing.T) {
	opts := &ExtractOptions{Files: "s3://bucket/in/doc.xml", CloudInputMode: cloudInputModeBounded}
	if err := refuseEagerCloudContinueOnError(opts, true); err != nil {
		t.Fatalf("bounded mode refused: %v", err)
	}
	opts.CloudInputMode = cloudInputModeEager
	if err := refuseEagerCloudContinueOnError(opts, true); !errors.Is(err, errEagerCloudContinueOnError) {
		t.Fatalf("eager mode not refused: %v", err)
	}
	if err := refuseEagerCloudContinueOnError(opts, false); err != nil {
		t.Fatalf("refused without --continue-on-error: %v", err)
	}
}
