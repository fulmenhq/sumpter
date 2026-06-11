package commands

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

// volatileRuntimeFields matches the per-run, inherently nondeterministic runtime
// fields (run id, generation timestamp) so two runs can be compared for real
// drift. Per the zero-drift contract these are the only fields permitted to vary.
var volatileRuntimeFields = regexp.MustCompile(`"(run_id|generated_at)":"[^"]*"`)

func normalizeVolatile(b []byte) string {
	return volatileRuntimeFields.ReplaceAllString(string(b), `"$1":"<normalized>"`)
}

// readSoleRecordOutput returns the contents of the single record-output file in
// dir (the one file that is not the provenance manifest).
func readSoleRecordOutput(t *testing.T, dir string) []byte {
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
	data, err := os.ReadFile(filepath.Join(dir, found))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", found, err)
	}
	return data
}

func newMatrixExtractOptions(dir, inputRef, outputDir string) *ExtractOptions {
	return &ExtractOptions{
		Files:           inputRef,
		Format:          "json",
		OutputPath:      outputDir,
		OutputPattern:   "extract-{}.json",
		SignatureConfig: filepath.Join(dir, "signature.yaml"),
		ExtractConfig:   filepath.Join(dir, "extract.yaml"),
		CommandName:     "sumpter extract files",
		Argv:            []string{"extract", "files"},
	}
}

// TestExtractFileURIMatchesBarePath proves the uriio read-boundary routing is
// zero-drift: a file:// input URI produces byte-for-byte the same record output
// as the equivalent bare local path.
func TestExtractFileURIMatchesBarePath(t *testing.T) {
	dir := createExtractManifestFixture(t)
	barePath := filepath.Join(dir, "input.xml")

	bareOut := filepath.Join(dir, "out-bare")
	if err := os.MkdirAll(bareOut, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := runExtract(newMatrixExtractOptions(dir, barePath, bareOut)); err != nil {
		t.Fatalf("runExtract(bare): %v", err)
	}

	absInput, err := filepath.Abs(barePath)
	if err != nil {
		t.Fatal(err)
	}
	fileURI := "file://" + filepath.ToSlash(absInput)
	fileOut := filepath.Join(dir, "out-fileuri")
	if err := os.MkdirAll(fileOut, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := runExtract(newMatrixExtractOptions(dir, fileURI, fileOut)); err != nil {
		t.Fatalf("runExtract(file://): %v", err)
	}

	bareData := normalizeVolatile(readSoleRecordOutput(t, bareOut))
	fileData := normalizeVolatile(readSoleRecordOutput(t, fileOut))
	if bareData != fileData {
		t.Errorf("file:// output differs from bare-path output (beyond volatile run fields):\nbare:  %s\nfile:  %s", bareData, fileData)
	}
}

// TestExtractRejectsCloudReferences asserts the edge guard fails fast on cloud
// and unsupported references, leaving no output behind.
func TestExtractRejectsCloudReferences(t *testing.T) {
	dir := createExtractManifestFixture(t)
	localInput := filepath.Join(dir, "input.xml")

	cases := []struct {
		name        string
		mutate      func(o *ExtractOptions)
		wantNotImpl bool // true: ErrSchemeNotImplemented; false: some classification error
	}{
		{
			name:        "s3 input",
			mutate:      func(o *ExtractOptions) { o.Files = "s3://bucket/key.xml" },
			wantNotImpl: true,
		},
		{
			name:        "s3 output",
			mutate:      func(o *ExtractOptions) { o.OutputPath = "s3://bucket/out/" },
			wantNotImpl: true,
		},
		{
			name:        "s3 record-index",
			mutate:      func(o *ExtractOptions) { o.RecordIndex = "s3://bucket/idx.bin" },
			wantNotImpl: true,
		},
		{
			name:        "unsupported gcs scheme",
			mutate:      func(o *ExtractOptions) { o.Files = "gs://bucket/key.xml" },
			wantNotImpl: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			outDir := filepath.Join(dir, "out-"+tc.name)
			if err := os.MkdirAll(outDir, 0o750); err != nil {
				t.Fatal(err)
			}
			opts := newMatrixExtractOptions(dir, localInput, outDir)
			tc.mutate(opts)

			err := runExtract(opts)
			if err == nil {
				t.Fatalf("runExtract(%s) = nil, want rejection", tc.name)
			}
			if tc.wantNotImpl && !errors.Is(err, uriio.ErrSchemeNotImplemented) {
				t.Fatalf("runExtract(%s) error = %v, want ErrSchemeNotImplemented", tc.name, err)
			}

			// No record output should have been produced.
			entries, _ := os.ReadDir(outDir)
			for _, e := range entries {
				if !e.IsDir() && e.Name() != "manifest.json" {
					t.Errorf("rejected run left output artifact: %s", e.Name())
				}
			}
		})
	}
}

// TestResolveLocalReferences exercises the edge resolve/guard directly across
// the local-pass / cloud-reject matrix.
func TestResolveLocalReferences(t *testing.T) {
	cases := []struct {
		name    string
		opts    *ExtractOptions
		wantErr bool
	}{
		{"bare input + bare output", &ExtractOptions{InputPath: "data/in", OutputPath: "data/out"}, false},
		{"file uri input", &ExtractOptions{Files: "file:///abs/in.xml"}, false},
		{"comma list local", &ExtractOptions{Files: "a.xml, b.xml"}, false},
		{"s3 in one of list", &ExtractOptions{Files: "a.xml, s3://b/c.xml"}, true},
		{"s3 output", &ExtractOptions{OutputPath: "s3://b/out/"}, true},
		{"s3 record index", &ExtractOptions{RecordIndex: "s3://b/idx"}, true},
		{"empty opts", &ExtractOptions{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := resolveLocalReferences(tc.opts)
			if tc.wantErr != (err != nil) {
				t.Fatalf("resolveLocalReferences(%+v) err = %v, wantErr = %v", tc.opts, err, tc.wantErr)
			}
		})
	}
}

// TestResolveLocalReferencesNormalizesFileURIRoots proves the edge rewrites
// file:// directory/index roots to their local filesystem path in place, so
// downstream path joins and traversal never see the scheme.
func TestResolveLocalReferencesNormalizesFileURIRoots(t *testing.T) {
	opts := &ExtractOptions{
		InputPath:   "file:///abs/in",
		OutputPath:  "file:///abs/out",
		RecordIndex: "file:///abs/idx.bin",
	}
	if err := resolveLocalReferences(opts); err != nil {
		t.Fatalf("resolveLocalReferences: %v", err)
	}
	if opts.InputPath != filepath.FromSlash("/abs/in") {
		t.Errorf("InputPath = %q, want /abs/in", opts.InputPath)
	}
	if opts.OutputPath != filepath.FromSlash("/abs/out") {
		t.Errorf("OutputPath = %q, want /abs/out", opts.OutputPath)
	}
	if opts.RecordIndex != filepath.FromSlash("/abs/idx.bin") {
		t.Errorf("RecordIndex = %q, want /abs/idx.bin", opts.RecordIndex)
	}
}

// fileURI builds the canonical file:// URI for a local path.
func fileURI(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	return "file://" + filepath.ToSlash(abs)
}

// TestExtractInputPathFileURIDirectoryMatchesBare proves zero-drift for a
// file:// *directory* root driving discovery: --input-path file://<dir> produces
// the same record output (and the same discovered file set) as the bare dir.
func TestExtractInputPathFileURIDirectoryMatchesBare(t *testing.T) {
	dir := createExtractManifestFixture(t)
	inputs := filepath.Join(dir, "inputs")
	mkXML(t, filepath.Join(inputs, "a.xml"))
	mkXML(t, filepath.Join(inputs, "b.xml"))

	run := func(inputRef, outDir string) {
		if err := os.MkdirAll(outDir, 0o750); err != nil {
			t.Fatal(err)
		}
		opts := newMatrixExtractOptions(dir, "", outDir)
		opts.Files = ""
		opts.InputPath = inputRef
		if err := runExtract(opts); err != nil {
			t.Fatalf("runExtract(%s): %v", inputRef, err)
		}
	}

	bareOut := filepath.Join(dir, "dir-out-bare")
	run(inputs, bareOut)
	fileOut := filepath.Join(dir, "dir-out-fileuri")
	run(fileURI(t, inputs), fileOut)

	if bare, file := dirRecordSet(t, bareOut), dirRecordSet(t, fileOut); bare != file {
		t.Errorf("file:// dir input output differs from bare:\nbare:  %s\nfile:  %s", bare, file)
	}
}

// TestExtractOutputPathFileURIDirectory proves a file:// *output* root resolves
// to the real local destination: records and the provenance manifest land under
// the actual directory, not a literal "file:" path.
func TestExtractOutputPathFileURIDirectory(t *testing.T) {
	dir := createExtractManifestFixture(t)
	input := filepath.Join(dir, "input.xml")
	realOut := filepath.Join(dir, "uri-out")

	opts := newMatrixExtractOptions(dir, input, "")
	opts.OutputPath = fileURI(t, realOut)
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract(output file://): %v", err)
	}

	// The provenance manifest must exist at the real local destination.
	if _, err := os.Stat(filepath.Join(realOut, "manifest.json")); err != nil {
		t.Fatalf("manifest not at real local output dir: %v", err)
	}
	// At least one record-output file must exist there too.
	_ = readSoleRecordOutput(t, realOut)
	// And nothing should have leaked into a literal "file:"-prefixed directory.
	if _, err := os.Stat(filepath.Join(dir, "file:")); err == nil {
		t.Errorf("output leaked into a literal file: path")
	}
}

func mkXML(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`<root><item><name>A</name></item><item><name>B</name></item></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// dirRecordSet returns the normalized, order-independent set of record-output
// file contents in dir (excluding the manifest), for cross-run comparison.
func dirRecordSet(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	var contents []string
	for _, e := range entries {
		if e.IsDir() || e.Name() == "manifest.json" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		contents = append(contents, normalizeVolatile(data))
	}
	sort.Strings(contents)
	return strings.Join(contents, "\n---\n")
}
