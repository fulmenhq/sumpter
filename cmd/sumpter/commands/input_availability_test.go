package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
