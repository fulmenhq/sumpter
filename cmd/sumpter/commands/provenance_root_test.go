package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

func TestProvenanceRootSelectionUsesFlagPrecedenceAndRejectsEmptyValues(t *testing.T) {
	envRoot := t.TempDir()
	flagRoot := t.TempDir()
	t.Setenv(provenanceRootEnvVar, envRoot)

	value, selected, fromFlag, root, err := resolveProvenanceRootSelection(flagRoot, true)
	if err != nil {
		t.Fatalf("flag selection: %v", err)
	}
	if !selected || !fromFlag || root == nil || value != flagRoot {
		t.Fatalf("flag selection = value %q, selected %v, fromFlag %v, root %v", value, selected, fromFlag, root)
	}

	value, selected, fromFlag, root, err = resolveProvenanceRootSelection("", false)
	if err != nil {
		t.Fatalf("environment selection: %v", err)
	}
	if !selected || fromFlag || root == nil || value != envRoot {
		t.Fatalf("environment selection = value %q, selected %v, fromFlag %v, root %v", value, selected, fromFlag, root)
	}

	if _, _, _, _, err := resolveProvenanceRootSelection("", true); err == nil || !strings.Contains(err.Error(), "cannot be empty") {
		t.Fatalf("empty flag error = %v, want explicit empty-value error", err)
	}

	t.Setenv(provenanceRootEnvVar, "")
	if _, _, _, _, err := resolveProvenanceRootSelection("", false); err == nil || !strings.Contains(err.Error(), provenanceRootEnvVar) {
		t.Fatalf("empty environment error = %v, want %s", err, provenanceRootEnvVar)
	}

	t.Setenv(provenanceRootEnvVar, "")
	if _, _, _, _, err := resolveProvenanceRootSelection("", false); err == nil {
		t.Fatal("unset-mode selection unexpectedly rejected an empty environment value")
	}
	// The explicit empty environment case above is intentionally distinct from
	// an unset environment; make the latter assertion without leaking the value.
	t.Setenv(provenanceRootEnvVar, "")
	if err := os.Unsetenv(provenanceRootEnvVar); err != nil {
		t.Fatalf("unset environment: %v", err)
	}
	value, selected, fromFlag, root, err = resolveProvenanceRootSelection("", false)
	if err != nil || selected || fromFlag || root != nil || value != "" {
		t.Fatalf("unset selection = value %q, selected %v, fromFlag %v, root %v, err %v", value, selected, fromFlag, root, err)
	}
}

func TestEnsureProvenanceRootReadsEnvironmentForDirectRuns(t *testing.T) {
	rootPath := t.TempDir()
	t.Setenv(provenanceRootEnvVar, rootPath)
	opts := &ExtractOptions{}
	if err := ensureProvenanceRoot(opts); err != nil {
		t.Fatalf("ensureProvenanceRoot: %v", err)
	}
	if !opts.ProvenanceRootSet || opts.ProvenanceRoot != rootPath || opts.provenanceRoot == nil {
		t.Fatalf("resolved options = %+v", opts)
	}
}

func TestProvenanceRootPreflightAndProjectionHandleLocalFileURIAndCloud(t *testing.T) {
	rootPath := t.TempDir()
	inside := filepath.Join(rootPath, "inputs", "source.xml")
	if err := os.MkdirAll(filepath.Dir(inside), 0o750); err != nil {
		t.Fatalf("mkdir input directory: %v", err)
	}
	if err := os.WriteFile(inside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.xml")
	if err := os.WriteFile(outside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write outside input: %v", err)
	}

	root, err := provenance.ResolveRoot(rootPath)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	opts := &ExtractOptions{
		ProvenanceRoot:    rootPath,
		ProvenanceRootSet: true,
		provenanceRoot:    root,
	}
	fileURI := "file://" + filepath.ToSlash(inside)
	logicalByLocal := map[string]string{}
	if err := preflightProvenanceRootInputs(opts, []string{inside}, logicalByLocal); err != nil {
		t.Fatalf("local preflight: %v", err)
	}
	var input provenance.Input
	input, err = provenance.BuildInputLedger(inside, fileURI, "")
	if err != nil {
		t.Fatalf("BuildInputLedger(file URI): %v", err)
	}
	if err := applyProvenanceRootInputPath(opts, &input, inside, fileURI); err != nil {
		t.Fatalf("project file URI: %v", err)
	}
	if input.Path != "inputs/source.xml" {
		t.Fatalf("file URI path = %q, want inputs/source.xml", input.Path)
	}

	cloudLogical := "s3://bucket/source.xml"
	if err := preflightProvenanceRootInputs(opts, []string{outside}, map[string]string{outside: cloudLogical}); err != nil {
		t.Fatalf("cloud preflight: %v", err)
	}
	cloudInput, err := provenance.BuildInputLedgerHashed(cloudLogical, "sha256:"+strings.Repeat("a", 64), 7, "")
	if err != nil {
		t.Fatalf("BuildInputLedgerHashed(cloud): %v", err)
	}
	if err := applyProvenanceRootInputPath(opts, &cloudInput, outside, cloudLogical); err != nil {
		t.Fatalf("project cloud input: %v", err)
	}
	if cloudInput.Path != cloudLogical {
		t.Fatalf("cloud path = %q, want unchanged URI", cloudInput.Path)
	}

	err = preflightProvenanceRootInputs(opts, []string{inside, outside}, nil)
	if err == nil || !strings.Contains(err.Error(), "input 2 is outside the provenance root") {
		t.Fatalf("mixed preflight error = %v, want ordinal-only containment error", err)
	}
	if strings.Contains(err.Error(), rootPath) || strings.Contains(err.Error(), outside) {
		t.Fatalf("containment error leaked a host path: %v", err)
	}
}

func TestRunExtractProvenanceRootProjectsFileURIAndMarksManifest(t *testing.T) {
	dir := createExtractManifestFixture(t)
	rootPath := filepath.Join(dir, "inputs")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		t.Fatalf("mkdir provenance root: %v", err)
	}
	inputPath := filepath.Join(rootPath, "source.xml")
	data, err := os.ReadFile(filepath.Join(dir, "input.xml"))
	if err != nil {
		t.Fatalf("read fixture input: %v", err)
	}
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		t.Fatalf("write root input: %v", err)
	}
	outputDir := filepath.Join(dir, "outputs")

	opts := &ExtractOptions{
		Files:                  "file://" + filepath.ToSlash(inputPath),
		Format:                 "json",
		OutputPath:             outputDir,
		OutputPattern:          "records.jsonl",
		SignatureConfig:        filepath.Join(dir, "signature.yaml"),
		ExtractConfig:          filepath.Join(dir, "extract.yaml"),
		ProvenanceRoot:         rootPath,
		ProvenanceRootSet:      true,
		ProvenanceRootFromFlag: true,
		Argv: []string{
			"extract", "files", "--provenance-root=" + rootPath,
		},
	}
	if err := runExtract(opts); err != nil {
		t.Fatalf("runExtract: %v", err)
	}

	manifest := readManifest(t, filepath.Join(outputDir, provenance.ManifestFileName))
	if manifest.InputPathForm != provenance.InputPathFormRootRelative {
		t.Fatalf("input_path_form = %q, want %q", manifest.InputPathForm, provenance.InputPathFormRootRelative)
	}
	if len(manifest.Inputs) != 1 || manifest.Inputs[0].Path != "source.xml" {
		t.Fatalf("inputs = %#v, want one root-relative source.xml input", manifest.Inputs)
	}
	argv := strings.Join(manifest.CLI.ArgvSanitized, " ")
	if strings.Contains(argv, rootPath) || strings.Count(argv, "--provenance-root=<set>") != 1 {
		t.Fatalf("sanitized argv = %q, want one redacted provenance-root token", argv)
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if strings.Contains(string(manifestBytes), rootPath) {
		t.Fatalf("manifest leaked provenance root %q: %s", rootPath, manifestBytes)
	}
}

func TestRunExtractProvenanceRootFailsBeforeOutputSideEffects(t *testing.T) {
	dir := createExtractManifestFixture(t)
	rootPath := filepath.Join(dir, "root")
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		t.Fatalf("mkdir provenance root: %v", err)
	}
	outsideDir := t.TempDir()
	outsidePath := filepath.Join(outsideDir, "outside.xml")
	data, err := os.ReadFile(filepath.Join(dir, "input.xml"))
	if err != nil {
		t.Fatalf("read fixture input: %v", err)
	}
	if err := os.WriteFile(outsidePath, data, 0o600); err != nil {
		t.Fatalf("write outside input: %v", err)
	}

	tests := []struct {
		name            string
		dryRun          bool
		noManifest      bool
		continueOnError bool
	}{
		{name: "normal"},
		{name: "dry-run", dryRun: true},
		{name: "no-manifest", noManifest: true},
		{name: "continue-on-error", continueOnError: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outputDir := filepath.Join(t.TempDir(), "outputs")
			opts := &ExtractOptions{
				Files:             outsidePath,
				Format:            "json",
				OutputPath:        outputDir,
				OutputPattern:     "records.jsonl",
				SignatureConfig:   filepath.Join(dir, "signature.yaml"),
				ExtractConfig:     filepath.Join(dir, "extract.yaml"),
				DryRun:            tc.dryRun,
				NoManifest:        tc.noManifest,
				ContinueOnError:   tc.continueOnError,
				ProvenanceRoot:    rootPath,
				ProvenanceRootSet: true,
			}
			err := runExtract(opts)
			if err == nil || !strings.Contains(err.Error(), "input 1 is outside the provenance root") {
				t.Fatalf("runExtract error = %v, want shared containment plan error", err)
			}
			if strings.Contains(err.Error(), rootPath) || strings.Contains(err.Error(), outsidePath) {
				t.Fatalf("containment error leaked a host path: %v", err)
			}
			if _, statErr := os.Stat(outputDir); !os.IsNotExist(statErr) {
				t.Fatalf("output directory exists after failed preflight: %q (err=%v)", outputDir, statErr)
			}
		})
	}
}
