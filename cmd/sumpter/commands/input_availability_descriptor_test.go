package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

const descriptorContractBase = "../../../tests/fixtures/data-artifact-contract/v0"

func descriptorLifecycle(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "artifact-descriptor.json")) // #nosec G304 - test temp path
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Lifecycle string `json:"lifecycle"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	return d.Lifecycle
}

func TestLifecycleForRun(t *testing.T) {
	failed := 1
	for _, tc := range []struct {
		name      string
		manifest  provenance.Manifest
		runFailed int
		want      string
	}{
		{"no failures", provenance.Manifest{}, 0, dataartifact.LifecycleComplete},
		{"unidentified failure, no row, no counts", provenance.Manifest{}, 1, dataartifact.LifecyclePartial},
		{"counted failure", provenance.Manifest{InputsFailed: &failed}, 1, dataartifact.LifecyclePartial},
		{"incomplete wins", provenance.Manifest{Incomplete: true}, 1, dataartifact.LifecycleIncomplete},
	} {
		if got := dataartifact.LifecycleForRun(tc.manifest, tc.runFailed); got != tc.want {
			t.Errorf("%s: lifecycle = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestUnidentifiedFailureDescriptorIsPartial covers the per-input routes and
// aggregate, for a missing local input and an unavailable cloud object: the run
// exits non-zero, failures.json records the failure, and the descriptor is
// partial, never complete.
func TestUnidentifiedFailureDescriptorIsPartial(t *testing.T) {
	for _, route := range []string{"serial", "streaming", "aggregate"} {
		for _, miss := range []string{"local", "cloud"} {
			t.Run(route+"/"+miss, func(t *testing.T) {
				f := newAvailabilityFixture(t)
				out := filepath.Join(f.dir, "fresh-out")
				opts := f.extractOptions(out)
				opts.ContinueOnError = true
				opts.ArtifactDescriptor = true
				opts.ArtifactContractBase = descriptorContractBase
				if miss == "local" {
					opts.Files = f.good + "," + filepath.Join(f.dir, "missing.json")
				} else {
					opts.Files = f.good + ",s3://bucket/in/missing.json"
					opts.CredentialsPath = failIfCalledCredentials(t, f.dir)
					opts.acquireSource = deniedCloudAcquireFor(t)
				}
				switch route {
				case "serial":
					opts.Format = "ndjson"
				case "aggregate":
					opts.OutputMode = outputModeAggregate
					opts.OutputPattern = "records.jsonl"
				}
				if err := runExtract(opts); err == nil || !strings.Contains(err.Error(), "applied=1 failed=1") {
					t.Fatalf("error = %v, want a non-zero partial failure", err)
				}
				rows := readFailureRows(t, filepath.Join(out, "failures.json"))
				if len(rows) != 1 || rows[0].Reason != "input_unavailable" {
					t.Fatalf("failures = %+v", rows)
				}
				if got := descriptorLifecycle(t, out); got != dataartifact.LifecyclePartial {
					t.Fatalf("descriptor lifecycle = %q, want partial", got)
				}
				if route == "aggregate" {
					requireGapOmitsCounts(t, filepath.Join(out, "manifest.json"))
				}
			})
		}
	}
}

// TestGapFreeAggregateWithNotApplicableKeepsCounts is the control row: a
// not_applicable input is ledgered, so an aggregate run with no failures keeps
// all four counts and a complete descriptor.
func TestGapFreeAggregateWithNotApplicableKeepsCounts(t *testing.T) {
	ws := writeApplicabilityRecipe(t, "summary", "count(//TargetElement) > 0")
	dir := t.TempDir()
	var paths []string
	for name, body := range map[string]string{
		"inA.xml": `<root><TargetElement><Name>valA</Name></TargetElement></root>`,
		"inB.xml": `<root><Other><Name>valB</Name></Other></root>`,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	list := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(list, []byte(strings.Join(paths, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "out")
	contract, err := filepath.Abs(descriptorContractBase)
	if err != nil {
		t.Fatal(err)
	}
	if err := runSumpter(t, []string{"recipes", "run", "extract", ws, "--file-list", list, "--output-path", out,
		"--output-mode", "aggregate", "--output-pattern", "records.jsonl", "--artifact-descriptor", "--contract-base", contract}); err != nil {
		t.Fatalf("aggregate run: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	assertInputAccounting(t, m, 2, 1, 1, 0)
	if got := descriptorLifecycle(t, out); got != dataartifact.LifecycleComplete {
		t.Fatalf("descriptor lifecycle = %q, want complete", got)
	}
}

// TestMultiUnidentifiedFailureDescriptorIsPartial covers extract-multi, per
// input and aggregate: a missing input gives a partial descriptor, and the
// aggregate manifest omits its counts.
func TestMultiUnidentifiedFailureDescriptorIsPartial(t *testing.T) {
	for _, mode := range []string{"per-input", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			f := newMultiAvailabilityFixture(t)
			fresh := filepath.Join(f.dir, "fresh")
			contract, err := filepath.Abs(descriptorContractBase)
			if err != nil {
				t.Fatal(err)
			}
			shared := multiSharedOptions{
				Files: f.good + "," + filepath.Join(f.dir, "missing.xml"), ContinueOnError: true, OutputPath: fresh,
				RunID: testMultiRunID, ArtifactDescriptor: true, ArtifactContractBase: contract,
			}
			if mode == "aggregate" {
				shared.OutputMode = outputModeAggregate
			}
			if err := runExtractMulti(&shared, []string{f.ws}, io.Discard, time.Now()); err == nil || !strings.Contains(err.Error(), "partial failure: applied=1 failed=1") {
				t.Fatalf("error = %v, want a partial failure", err)
			}
			if got := descriptorLifecycle(t, filepath.Join(fresh, "r1")); got != dataartifact.LifecyclePartial {
				t.Fatalf("descriptor lifecycle = %q, want partial", got)
			}
			if mode == "aggregate" {
				requireGapOmitsCounts(t, filepath.Join(fresh, "r1", "manifest.json"))
			}
		})
	}
}
