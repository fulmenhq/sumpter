package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	goneatschema "github.com/fulmenhq/goneat/pkg/schema"

	"github.com/fulmenhq/sumpter/internal/extract"
)

const jsonCaseDir = "../../../examples/cases/14-json-basic-extraction"

// availabilityFixture is a scratch layout for input-availability tests: an
// empty input root, one good JSON input, and an output directory holding a
// manifest from an earlier run.
type availabilityFixture struct {
	dir, empty, good, out, sig, ext string
	priorManifest                   []byte
}

func newAvailabilityFixture(t *testing.T) availabilityFixture {
	t.Helper()
	// Discovery refuses a root with a symlinked parent, which a temp dir can have.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := availabilityFixture{
		dir:           dir,
		empty:         filepath.Join(dir, "empty"),
		good:          filepath.Join(dir, "good.json"),
		out:           filepath.Join(dir, "out"),
		sig:           filepath.Join(jsonCaseDir, "recipe", "signature", "widget-signature.yaml"),
		ext:           filepath.Join(jsonCaseDir, "recipe", "extract", "widget-extract.yaml"),
		priorManifest: []byte(`{"from":"an earlier run"}` + "\n"),
	}
	doc, err := os.ReadFile(filepath.Join(jsonCaseDir, "input.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{f.empty, f.out} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(f.good, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.out, "manifest.json"), f.priorManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	return f
}

// assertOutputUntouched requires the output directory to hold exactly the
// earlier run's manifest, byte for byte.
func (f availabilityFixture) assertOutputUntouched(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(f.out)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("output directory changed: %v", entries)
	}
	got, err := os.ReadFile(filepath.Join(f.out, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, f.priorManifest) {
		t.Fatalf("earlier manifest was rewritten: %s", got)
	}
}

// extractOptions is a direct extract files run over the fixture's recipe with
// JSON output, for tests that set per-invocation hooks.
func (f availabilityFixture) extractOptions(out string) *ExtractOptions {
	return &ExtractOptions{
		SignatureConfig: f.sig,
		ExtractConfig:   f.ext,
		OutputPath:      out,
		Format:          "json",
		OutputPattern:   "extract-{}.json",
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
	}
}

func (f availabilityFixture) filesArgs(extra ...string) []string {
	return append([]string{"extract", "files", "--signature-config-path", f.sig, "--extract-config-path", f.ext}, extra...)
}

func TestZeroDiscoveryFailsBeforeOutput(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra []string
	}{
		{"run", nil},
		{"continue-on-error", []string{"--continue-on-error"}},
		{"include mismatch", []string{"--include-pattern", "*.nomatch"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newAvailabilityFixture(t)
			root := f.empty
			if tc.name == "include mismatch" {
				root = f.dir // holds good.json, which the pattern excludes
			}
			args := f.filesArgs(append([]string{"--input-path", root, "--output-path", f.out}, tc.extra...)...)
			err := runSumpter(t, args)
			if err == nil || !strings.Contains(err.Error(), "no input files matched under "+root) {
				t.Fatalf("error = %v, want a no-match failure naming %s", err, root)
			}
			f.assertOutputUntouched(t)
		})
	}
}

func TestZeroDiscoveryCreatesNoOutputDirectory(t *testing.T) {
	f := newAvailabilityFixture(t)
	fresh := filepath.Join(f.dir, "fresh")
	err := runSumpter(t, f.filesArgs("--input-path", f.empty, "--output-path", fresh))
	if err == nil {
		t.Fatal("zero discovery succeeded")
	}
	if _, serr := os.Stat(fresh); !os.IsNotExist(serr) {
		t.Fatalf("output directory was created: %v", serr)
	}
}

func TestZeroDiscoveryDryRunFails(t *testing.T) {
	f := newAvailabilityFixture(t)
	err := runSumpter(t, f.filesArgs("--input-path", f.empty, "--dry-run"))
	if err == nil || !strings.Contains(err.Error(), "no input files matched") {
		t.Fatalf("dry run error = %v, want a no-match failure", err)
	}
}

func TestZeroDiscoveryMessageNamesBaseKind(t *testing.T) {
	for _, tc := range []struct {
		opts ExtractOptions
		want string
	}{
		{ExtractOptions{InputPath: "data", IncludePattern: "*.json"}, `no input files matched under data (include "*.json") relative to the working directory`},
		{ExtractOptions{InputPath: "/abs/data", IncludePattern: "*.xml", ExcludePattern: "tmp/*"}, `no input files matched under /abs/data (include "*.xml", exclude "tmp/*")`},
		{ExtractOptions{InputPath: "/ws/in", inputDisplay: "in", inputBaseKind: baseKindRecipeDir}, `no input files matched under in relative to the recipe directory`},
		{ExtractOptions{InputPath: "s3://bucket/prefix/"}, `no input files matched under s3://bucket/prefix/`},
	} {
		if got := noInputsMatchedError(&tc.opts).Error(); got != tc.want {
			t.Errorf("got  %q\nwant %q", got, tc.want)
		}
	}
}

// TestRecipeZeroDiscoveryFails covers default recipe discovery: a recipe whose
// input path exists but holds nothing fails and leaves the output untouched.
func TestRecipeZeroDiscoveryFails(t *testing.T) {
	f := newAvailabilityFixture(t)
	ws := filepath.Join(f.dir, "recipe")
	for _, sub := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, f.sig, filepath.Join(ws, "signature", "signature.yaml"))
	copyFile(t, f.ext, filepath.Join(ws, "extract", "extract.yaml"))
	manifest := "version: \"recipe/v0.1.0\"\nkind: \"extract\"\nid: empty_root\n" +
		"display_name: \"Empty root\"\ncreated_at: \"2026-09-28T00:00:00Z\"\ncontent_version: \"0.0.1\"\n" +
		"assets:\n  signature: signature/signature.yaml\n  extract: extract/extract.yaml\n" +
		"defaults:\n  input:\n    format: json\n    path: in\n"
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, "in"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, extra := range [][]string{{"--output-path", f.out}, {"--dry-run"}} {
		err := runSumpter(t, append([]string{"recipes", "run", "extract", ws}, extra...))
		if err == nil || !strings.Contains(err.Error(), "no input files matched under in") || !strings.Contains(err.Error(), "relative to the recipe directory") {
			t.Fatalf("%v: error = %v", extra, err)
		}
	}
	f.assertOutputUntouched(t)
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f availabilityFixture) writeList(t *testing.T, name string, entries ...string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte(strings.Join(entries, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingInputFailFastAbortsBeforeSibling(t *testing.T) {
	f := newAvailabilityFixture(t)
	list := f.writeList(t, "list.txt", f.good, filepath.Join(f.dir, "missing.json"))
	err := runSumpter(t, f.filesArgs("--file-list", list, "--output-path", f.out))
	var unavailable *inputUnavailableError
	if !errors.As(err, &unavailable) || !strings.HasSuffix(err.Error(), ": not found") {
		t.Fatalf("error = %v, want an input-unavailable not-found failure", err)
	}
	if strings.Contains(err.Error(), "no such file") {
		t.Fatalf("error carries raw OS text: %v", err)
	}
	f.assertOutputUntouched(t) // the good sibling wrote nothing
}

func TestMissingInputContinueOnErrorRecordsInputUnavailable(t *testing.T) {
	for _, mode := range []string{"per-input", "aggregate"} {
		t.Run(mode, func(t *testing.T) {
			f := newAvailabilityFixture(t)
			out := filepath.Join(f.dir, "fresh-out")
			list := f.writeList(t, "list.txt", f.good, filepath.Join(f.dir, "missing.json"))
			args := f.filesArgs("--file-list", list, "--output-path", out, "--continue-on-error")
			if mode == "aggregate" {
				args = append(args, "--output-mode", "aggregate", "--output-pattern", "records.jsonl")
			}
			if err := runSumpter(t, args); err == nil || !strings.Contains(err.Error(), "partial extraction failure: applied=1 failed=1") {
				t.Fatalf("error = %v, want a non-zero partial failure", err)
			}
			raw, err := os.ReadFile(filepath.Join(out, "failures.json"))
			if err != nil {
				t.Fatal(err)
			}
			var failures struct {
				Failures []struct{ File, Reason, Detail string } `json:"failures"`
			}
			if err := json.Unmarshal(raw, &failures); err != nil {
				t.Fatal(err)
			}
			if len(failures.Failures) != 1 || failures.Failures[0].Reason != "input_unavailable" || failures.Failures[0].Detail != "input unavailable: not found" {
				t.Fatalf("failures = %+v", failures.Failures)
			}
			validateAgainstSchema(t, "../../../schemas/extract/v0.1.0/failures.schema.json", raw)
		})
	}
}

func TestUnreadableAfterPreflightFailsAtRead(t *testing.T) {
	f := newAvailabilityFixture(t)
	second := filepath.Join(f.dir, "second.json")
	copyFile(t, f.good, second)
	opts := f.extractOptions(filepath.Join(f.dir, "fresh-out"))
	opts.FileList = f.writeList(t, "list.txt", f.good, second)
	opts.afterLocalInputPreflight = func() { _ = os.Remove(second) }
	err := runExtract(opts)
	var unavailable *inputUnavailableError
	if !errors.As(err, &unavailable) || !strings.HasSuffix(err.Error(), ": not found") {
		t.Fatalf("error = %v, want an input-unavailable failure at read", err)
	}
}

func TestFailureReasonForUnavailableInput(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("failed to read file: %w", &fs.PathError{Op: "stat", Path: "x", Err: fs.ErrNotExist}),
		fmt.Errorf("failed to read file: %w", &fs.PathError{Op: "open", Path: "x", Err: fs.ErrPermission}),
		&inputUnavailableError{display: "x", class: "not found", err: fs.ErrNotExist},
	} {
		if got := failureReasonForError(err); got != extract.DispositionReasonInputUnavailable {
			t.Errorf("failureReasonForError(%v) = %q", err, got)
		}
	}
	if got := failureDetail(extract.DispositionReasonInputUnavailable, fmt.Errorf("x: %w", fs.ErrPermission)); got != "input unavailable: permission denied" {
		t.Errorf("detail = %q", got)
	}
}

func validateAgainstSchema(t *testing.T, schemaPath string, doc []byte) {
	t.Helper()
	schema, err := os.ReadFile(schemaPath) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	res, err := goneatschema.ValidateFromBytes(schema, v)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Valid {
		t.Fatalf("document fails %s: %v", schemaPath, res.Errors)
	}
}

func TestDryRunChecksNamedLocalInputs(t *testing.T) {
	f := newAvailabilityFixture(t)
	list := f.writeList(t, "list.txt", f.good, filepath.Join(f.dir, "missing.json"))
	for _, args := range [][]string{
		f.filesArgs("--file-list", list, "--dry-run"),
		f.filesArgs("--files", f.good+","+filepath.Join(f.dir, "missing.json"), "--dry-run"),
	} {
		err := runSumpter(t, args)
		var unavailable *inputUnavailableError
		if !errors.As(err, &unavailable) || !strings.HasSuffix(err.Error(), ": not found") {
			t.Fatalf("%v: error = %v, want an input-unavailable dry-run failure", args, err)
		}
	}
	if err := runSumpter(t, f.filesArgs("--files", f.good, "--dry-run")); err != nil {
		t.Fatalf("dry run of an available input failed: %v", err)
	}
}

// failIfCalledCredentials writes a credentials config whose default handle
// points at a server that fails the test on any request, proving a code path
// makes no cloud call. It also gives the run a private SUMPTER_HOME, since a
// cloud session needs a work directory.
func failIfCalledCredentials(t *testing.T, dir string) string {
	t.Helper()
	t.Setenv("SUMPTER_HOME", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected cloud request: %s %s", r.Method, r.URL)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(dir, "credentials.yaml")
	body := "handles:\n  default:\n    region: us-east-1\n    endpoint: " + srv.URL + "\n" +
		"    force_path_style: true\n    insecure: true\n    access_key_id: test\n    secret_access_key: test\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// recipeWorkspace copies the JSON example recipe into dir/recipe with the given
// defaults block and returns the workspace path.
func recipeWorkspace(t *testing.T, f availabilityFixture, defaults string) string {
	t.Helper()
	ws := filepath.Join(f.dir, "recipe")
	for _, sub := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, sub), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, f.sig, filepath.Join(ws, "signature", "signature.yaml"))
	copyFile(t, f.ext, filepath.Join(ws, "extract", "extract.yaml"))
	manifest := "version: \"recipe/v0.1.0\"\nkind: \"extract\"\nid: paths\n" +
		"display_name: \"Paths\"\ncreated_at: \"2026-09-28T00:00:00Z\"\ncontent_version: \"0.0.1\"\n" +
		"assets:\n  signature: signature/signature.yaml\n  extract: extract/extract.yaml\n" + defaults
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws
}

// TestRecipeCLIPathsRelativeToWorkingDir runs from a working directory that is
// neither the recipe directory nor a list's directory.
func TestRecipeCLIPathsRelativeToWorkingDir(t *testing.T) {
	f := newAvailabilityFixture(t)
	ws := recipeWorkspace(t, f, "defaults:\n  input:\n    format: json\n  output:\n    format: json\n    pattern: records.jsonl\n")
	cwd := filepath.Join(f.dir, "cwd")
	for _, d := range []string{filepath.Join(cwd, "in"), filepath.Join(cwd, "lists", "data")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	copyFile(t, f.good, filepath.Join(cwd, "in", "good.json"))
	copyFile(t, f.good, filepath.Join(cwd, "lists", "data", "good.json"))
	if err := os.WriteFile(filepath.Join(cwd, "lists", "l.txt"), []byte("data/good.json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	for _, tc := range []struct {
		name  string
		input []string
		out   string
	}{
		{"input-path", []string{"--input-path", "in"}, "out-a"},
		{"file-list", []string{"--file-list", "lists/l.txt"}, "out-b"},
	} {
		args := append([]string{"recipes", "run", "extract", ws}, tc.input...)
		args = append(args, "--output-path", tc.out, "--no-manifest")
		if err := runSumpter(t, args); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if _, err := os.Stat(filepath.Join(cwd, tc.out, "records.jsonl")); err != nil {
			t.Fatalf("%s: records not under the working directory: %v", tc.name, err)
		}
		if _, err := os.Stat(filepath.Join(ws, tc.out)); !os.IsNotExist(err) {
			t.Fatalf("%s: output written inside the recipe directory", tc.name)
		}
	}
}

// TestRecipeDefaultOutputPathRelativeToRecipeDir keeps a path written in
// recipe.yaml relative to the recipe directory.
func TestRecipeDefaultOutputPathRelativeToRecipeDir(t *testing.T) {
	f := newAvailabilityFixture(t)
	ws := recipeWorkspace(t, f, "defaults:\n  input:\n    format: json\n  output:\n    format: json\n    pattern: records.jsonl\n    path: recipe-out\n")
	if err := runSumpter(t, []string{"recipes", "run", "extract", ws, "--files", f.good, "--no-manifest"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(ws, "recipe-out", "records.jsonl")); err != nil {
		t.Fatalf("defaults.output.path is not relative to the recipe directory: %v", err)
	}
}
