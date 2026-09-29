package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// deniedCloudAcquireFor stages local references in place and fails every s3://
// reference as access denied. Denied is used rather than not-found because an
// open of the placeholder path would report "not found": a recorded
// "permission denied" proves no route opened it.
func deniedCloudAcquireFor(t *testing.T) func(context.Context, *uriio.Session, string, string) (*uriio.AcquiredSource, error) {
	t.Helper()
	return func(_ context.Context, _ *uriio.Session, ref, _ string) (*uriio.AcquiredSource, error) {
		if strings.HasPrefix(ref, "s3://") {
			return nil, fmt.Errorf("uriio: get %s: %w", ref, uriio.ErrObjectAccessDenied)
		}
		return &uriio.AcquiredSource{LogicalURI: ref, LocalPath: ref, Scheme: uriio.SchemeLocal}, nil
	}
}

type failureRow struct{ File, Reason, Detail string }

func readFailureRows(t *testing.T, path string) []failureRow {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 - test temp path
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Failures []failureRow `json:"failures"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Failures
}

func requireUnavailableRows(t *testing.T, rows []failureRow, want int) {
	t.Helper()
	if len(rows) != want {
		t.Fatalf("failure rows = %+v, want %d", rows, want)
	}
	for _, r := range rows {
		if r.Reason != "input_unavailable" || r.Detail != "input unavailable: permission denied" {
			t.Fatalf("failure row = %+v, want input_unavailable permission denied (the placeholder was never opened)", r)
		}
	}
}

// TestCloudMissContinueOnErrorAccountedPerRoute covers each route that
// consumes the resolved inputs: the serial loop, sequential JSON streaming and
// aggregate streaming. The missing object is named twice, so it must yield two
// rows at its two ordinals, and the good sibling's output must be written.
func TestCloudMissContinueOnErrorAccountedPerRoute(t *testing.T) {
	for _, route := range []string{"serial", "streaming", "aggregate"} {
		t.Run(route, func(t *testing.T) {
			f := newAvailabilityFixture(t)
			out := filepath.Join(f.dir, "out-"+route)
			opts := f.extractOptions(out)
			opts.Files = f.good + ",s3://bucket/in/missing.json,s3://bucket/in/missing.json"
			opts.ContinueOnError = true
			opts.CredentialsPath = failIfCalledCredentials(t, f.dir)
			opts.acquireSource = deniedCloudAcquireFor(t)
			switch route {
			case "serial":
				opts.Format = "ndjson"
			case "aggregate":
				opts.OutputMode = outputModeAggregate
				opts.OutputPattern = "records.jsonl"
			}
			err := runExtract(opts)
			if err == nil || !strings.Contains(err.Error(), "applied=1 failed=2") {
				t.Fatalf("error = %v, want a partial failure with one applied and two failed", err)
			}
			requireUnavailableRows(t, readFailureRows(t, filepath.Join(out, "failures.json")), 2)
			entries, rerr := os.ReadDir(out)
			if rerr != nil {
				t.Fatal(rerr)
			}
			var data int
			for _, e := range entries {
				if strings.HasPrefix(e.Name(), "extract-") || e.Name() == "records.jsonl" {
					data++
				}
			}
			if data != 1 {
				t.Fatalf("data files = %v, want exactly the good sibling's", entries)
			}
			if route == "aggregate" {
				requireGapOmitsCounts(t, filepath.Join(out, "manifest.json"))
			}
		})
	}
}

func TestCloudMissFailFastStillAborts(t *testing.T) {
	f := newAvailabilityFixture(t)
	opts := f.extractOptions(f.out)
	opts.Files = f.good + ",s3://bucket/in/missing.json"
	opts.CredentialsPath = failIfCalledCredentials(t, f.dir)
	opts.acquireSource = deniedCloudAcquireFor(t)
	if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "resolve input s3://bucket/in/missing.json") {
		t.Fatalf("error = %v, want the acquisition abort", err)
	}
	f.assertOutputUntouched(t)
}

func TestCloudUnclassifiedAcquireErrorStillAborts(t *testing.T) {
	f := newAvailabilityFixture(t)
	opts := f.extractOptions(filepath.Join(f.dir, "fresh"))
	opts.Files = f.good + ",s3://bucket/in/doc.json"
	opts.ContinueOnError = true
	opts.CredentialsPath = failIfCalledCredentials(t, f.dir)
	opts.acquireSource = func(_ context.Context, _ *uriio.Session, ref, _ string) (*uriio.AcquiredSource, error) {
		if strings.HasPrefix(ref, "s3://") {
			return nil, errors.New("uriio: get " + ref + " failed: throttled")
		}
		return &uriio.AcquiredSource{LogicalURI: ref, LocalPath: ref, Scheme: uriio.SchemeLocal}, nil
	}
	if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "throttled") {
		t.Fatalf("error = %v, want an abort on an unclassified acquisition error", err)
	}
}

func TestDryRunMissingInputFailsWithContinueOnError(t *testing.T) {
	f := newAvailabilityFixture(t)
	err := runSumpter(t, f.filesArgs("--files", f.good+","+filepath.Join(f.dir, "missing.json"), "--dry-run", "--continue-on-error"))
	var unavailable *inputUnavailableError
	if !errors.As(err, &unavailable) {
		t.Fatalf("dry run with --continue-on-error: error = %v, want input unavailable", err)
	}
}

func TestMultiEagerCloudMissAccounted(t *testing.T) {
	f := newMultiAvailabilityFixture(t)
	fresh := filepath.Join(f.dir, "fresh")
	shared := multiSharedOptions{
		Files: f.good + ",s3://bucket/in/missing.xml", ContinueOnError: true, OutputPath: fresh, RunID: testMultiRunID,
		CredentialsPath: failIfCalledCredentials(t, f.dir), acquireSource: deniedCloudAcquireFor(t),
	}
	if err := runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now()); err == nil || !strings.Contains(err.Error(), "partial failure: applied=1 failed=1") {
		t.Fatalf("error = %v, want a partial failure with the good sibling applied", err)
	}
	requireUnavailableRows(t, readFailureRows(t, filepath.Join(fresh, "r1", "failures.json")), 1)
}

// requireGapOmitsCounts checks an aggregate manifest whose inventory lacks the
// unidentified failed inputs omits all four input counts, lists only the
// identified input, and is not marked incomplete.
func requireGapOmitsCounts(t *testing.T, manifestPath string) {
	t.Helper()
	m := readManifest(t, manifestPath)
	if m.Incomplete || len(m.Inputs) != 1 {
		t.Fatalf("manifest incomplete=%v inputs=%d, want not incomplete with the one identified input", m.Incomplete, len(m.Inputs))
	}
	if m.InputsTotal != nil || m.InputsApplied != nil || m.InputsNotApplicable != nil || m.InputsFailed != nil {
		t.Fatalf("gapped inventory emitted counts: total=%v applied=%v not_applicable=%v failed=%v",
			m.InputsTotal, m.InputsApplied, m.InputsNotApplicable, m.InputsFailed)
	}
}
