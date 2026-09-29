package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const ndjsonSignature = `signature_id: events
name: Events
match_patterns:
  - pattern_id: event-id
    name: event has an id
    selector: /Event/id
    weight: 1
confidence_threshold: 1
format_type: ndjson
match_scope: record
`

const ndjsonExtract = `record_type: event
match_selectors:
  - xpath: Event
field_mappings:
  - output_field: id
    xpath: id
    type: string
output_schema:
  type: object
  properties:
    id:
      type: string
  required: [id]
`

// ndjsonFixture is a directory holding an ndjson recipe and inputs.
type ndjsonFixture struct {
	dir, sig, ext string
}

func newNDJSONFixture(t *testing.T) ndjsonFixture {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := ndjsonFixture{dir: dir, sig: filepath.Join(dir, "sig.yaml"), ext: filepath.Join(dir, "ext.yaml")}
	f.write(t, "sig.yaml", ndjsonSignature)
	f.write(t, "ext.yaml", ndjsonExtract)
	return f
}

func (f ndjsonFixture) write(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f ndjsonFixture) args(extra ...string) []string {
	return append([]string{"extract", "files", "--signature-config-path", f.sig, "--extract-config-path", f.ext}, extra...)
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
}

func TestNDJSONInputExtractsEveryLine(t *testing.T) {
	f := newNDJSONFixture(t)
	f.write(t, "a.ndjson", "{\"id\":\"1\"}\n\n{\"id\":\"2\"}\r\n")
	f.write(t, "b.jsonl", "{\"id\":\"x\"}\n") // not matched by the *.ndjson default
	out := filepath.Join(f.dir, "out")
	if err := runSumpter(t, f.args("--input-path", f.dir, "--output-path", out)); err != nil {
		t.Fatal(err)
	}
	lines := readLines(t, filepath.Join(out, "extract-a.ndjson.json"))
	if len(lines) != 2 || !strings.Contains(lines[0], `"id":"1"`) || !strings.Contains(lines[1], `"id":"2"`) {
		t.Fatalf("records %q", lines)
	}
	if _, err := os.Stat(filepath.Join(out, "extract-b.jsonl.json")); !os.IsNotExist(err) {
		t.Fatalf("a .jsonl file was discovered by the ndjson default: %v", err)
	}
	m := readManifest(t, filepath.Join(out, "manifest.json"))
	if len(m.Inputs) != 1 || m.Inputs[0].Format != "ndjson" {
		t.Fatalf("manifest inputs %+v", m.Inputs)
	}
}

// TestNDJSONLateFaultDiscardsTheInput runs an input whose first record is
// good and whose later record fails, beside a good sibling, on both output
// modes and both failure branches: the failed input never publishes rows or a
// success marker.
func TestNDJSONLateFaultDiscardsTheInput(t *testing.T) {
	for _, fault := range []struct {
		name, line, reason string
	}{
		{"malformed record", `{"id":`, "parse_error"},
		{"non-object record", `[1]`, "parse_error"},
		{"signature below threshold", `{"other":"z"}`, "signature_mismatch"},
	} {
		for _, mode := range []string{"per-input", "aggregate"} {
			t.Run(fault.name+"/"+mode, func(t *testing.T) {
				f := newNDJSONFixture(t)
				bad := f.write(t, "bad.ndjson", "{\"id\":\"b1\"}\n"+fault.line+"\n")
				good := f.write(t, "good.ndjson", "{\"id\":\"g1\"}\n")
				list := f.write(t, "list.txt", bad+"\n"+good+"\n")
				modeArgs := []string{}
				if mode == "aggregate" {
					modeArgs = []string{"--output-mode", "aggregate", "--output-pattern", "records.jsonl"}
				}

				// Fail-fast: the run stops non-zero and publishes no manifest.
				out := filepath.Join(f.dir, "ff-out")
				err := runSumpter(t, f.args(append([]string{"--file-list", list, "--output-path", out}, modeArgs...)...))
				if err == nil {
					t.Fatal("fail-fast run succeeded")
				}
				if _, serr := os.Stat(filepath.Join(out, "manifest.json")); !os.IsNotExist(serr) {
					t.Fatalf("fail-fast run published a manifest: %v", serr)
				}
				assertNoRowsFrom(t, out, "b1")

				// Continue-on-error: the sibling completes, the failed input is
				// recorded, and none of its rows are published.
				out = filepath.Join(f.dir, "coe-out")
				err = runSumpter(t, f.args(append([]string{"--file-list", list, "--output-path", out, "--continue-on-error"}, modeArgs...)...))
				if err == nil || !strings.Contains(err.Error(), "partial extraction failure: applied=1 failed=1") {
					t.Fatalf("error = %v, want a partial failure", err)
				}
				failures := readFailureManifest(t, filepath.Join(out, "failures.json"))
				if len(failures.Failures) != 1 || failures.Failures[0].Reason != fault.reason {
					t.Fatalf("failures %+v", failures.Failures)
				}
				assertNoRowsFrom(t, out, "b1")
				if !anyOutputContains(t, out, "g1") {
					t.Fatal("the good sibling's record is missing")
				}
				m := readManifest(t, filepath.Join(out, "manifest.json"))
				for _, in := range m.Inputs {
					if strings.HasSuffix(in.Path, "bad.ndjson") && in.RecordCount != nil && *in.RecordCount != 0 {
						t.Fatalf("failed input reports %d records", *in.RecordCount)
					}
				}
			})
		}
	}
}

func TestNDJSONBlankOnlyInputIsParseError(t *testing.T) {
	f := newNDJSONFixture(t)
	blank := f.write(t, "blank.ndjson", "\n   \n\r\n")
	good := f.write(t, "good.ndjson", "{\"id\":\"g1\"}\n")
	list := f.write(t, "list.txt", blank+"\n"+good+"\n")
	out := filepath.Join(f.dir, "out")
	err := runSumpter(t, f.args("--file-list", list, "--output-path", out, "--continue-on-error"))
	if err == nil {
		t.Fatal("blank-only input admitted")
	}
	failures := readFailureManifest(t, filepath.Join(out, "failures.json"))
	if len(failures.Failures) != 1 || failures.Failures[0].Reason != "parse_error" || !strings.Contains(failures.Failures[0].Detail, "input contains no JSON value") {
		t.Fatalf("failures %+v", failures.Failures)
	}
}

func TestNDJSONRefusedByUnsupportedRoutes(t *testing.T) {
	f := newNDJSONFixture(t)
	in := f.write(t, "a.ndjson", "{\"id\":\"1\"}\n")
	idx := f.write(t, "a.idx.json", "{}")
	err := runSumpter(t, f.args("--files", in, "--output-path", filepath.Join(f.dir, "out"), "--record-index", idx))
	if err == nil || !strings.Contains(err.Error(), `record-index extraction supports xml and json input in this release; input format is "ndjson"`) {
		t.Fatalf("record-index error = %v", err)
	}
}

func TestNDJSONSignatureNeedsRecordScope(t *testing.T) {
	f := newNDJSONFixture(t)
	f.write(t, "sig.yaml", strings.Replace(ndjsonSignature, "match_scope: record\n", "", 1))
	in := f.write(t, "a.ndjson", "{\"id\":\"1\"}\n")
	err := runSumpter(t, f.args("--files", in, "--output-path", filepath.Join(f.dir, "out")))
	if err == nil || !strings.Contains(err.Error(), `must declare "match_scope: record"`) {
		t.Fatalf("error = %v", err)
	}
}

// assertNoRowsFrom requires no output file under out to hold a record with
// the given id.
func assertNoRowsFrom(t *testing.T, out, id string) {
	t.Helper()
	if anyOutputContains(t, out, id) {
		t.Fatalf("a failed input's record %q was published", id)
	}
}

func anyOutputContains(t *testing.T, out, id string) bool {
	t.Helper()
	found := false
	_ = filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if name := d.Name(); name == "manifest.json" || name == "failures.json" {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		for _, line := range strings.Split(string(raw), "\n") {
			var rec struct {
				Extract struct {
					Data map[string]any `json:"data"`
				} `json:"extract"`
			}
			if json.Unmarshal([]byte(line), &rec) == nil && rec.Extract.Data["id"] == id {
				found = true
			}
		}
		return nil
	})
	return found
}
