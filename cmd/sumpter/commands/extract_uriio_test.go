package commands

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
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

// TestGuardUnsupportedCloudReferences exercises the edge guard directly across
// the local-pass / cloud-reject matrix.
func TestGuardUnsupportedCloudReferences(t *testing.T) {
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
			err := guardUnsupportedCloudReferences(tc.opts)
			if tc.wantErr != (err != nil) {
				t.Fatalf("guardUnsupportedCloudReferences(%+v) err = %v, wantErr = %v", tc.opts, err, tc.wantErr)
			}
		})
	}
}
