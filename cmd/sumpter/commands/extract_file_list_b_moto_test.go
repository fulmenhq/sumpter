//go:build s3integration

// S3 live-integration tests for integrity-bound file-list object lines
// ({uri,size,sha256}) over cloud (s3://) inputs: matching declarations extract,
// size and digest mismatches fail before any records, and staged bytes are
// reaped on every path. Run with `-tags s3integration`; shares the moto harness
// in extract_moto_test.go.

package commands

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func motoDeclaredSHA(b []byte) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256(b))
}

func countFilesUnder(t *testing.T, dir string) int {
	t.Helper()
	count := 0
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			count++
		}
		return nil
	})
	return count
}

// motoDeclaredWorkspace builds the single-recipe workspace used by the declared
// cloud tests and returns the workspace path.
func motoDeclaredWorkspace(t *testing.T, recipeID string) string {
	t.Helper()
	ws := createWorkingTempDir(t)
	for _, d := range []string{"signature", "extract", "outputs"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	mustWriteFile(t, filepath.Join(ws, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(ws, "extract", "extract.yaml"), `record_type: rec
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	mustWriteFile(t, filepath.Join(ws, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: `+recipeID+`
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    credentials_handle: reader
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  progress: false
`)
	return ws
}

// TestMotoFileListDeclaredMatchEager proves declared object lines work on the
// eager cloud path: exact decls extract, and the manifest ledger equals them.
func TestMotoFileListDeclaredMatchEager(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aBody := []byte("<root><item><Name>A</Name></item></root>")
	bBody := []byte("<root><item><Name>B</Name></item></root>")
	aKey := runKeyPrefix() + "declared/a.xml"
	bKey := runKeyPrefix() + "declared/b.xml"
	m.putObject(t, aKey, aBody)
	m.putObject(t, bKey, bBody)
	aURI := "s3://" + m.bucket + "/" + aKey
	bURI := "s3://" + m.bucket + "/" + bKey
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := motoDeclaredWorkspace(t, "declared_cloud")
	list := writeListLines(t, ws, []string{
		objectLine(t, aURI, int64(len(aBody)), motoDeclaredSHA(aBody)),
		objectLine(t, bURI, int64(len(bBody)), motoDeclaredSHA(bBody)),
	})

	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:           "recipe.yaml",
		FileList:               filepath.Base(list),
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
		Progress:               false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (declared cloud --file-list): %v", err)
	}

	outs, err := filepath.Glob(filepath.Join(ws, "outputs", "extract-*.json"))
	if err != nil {
		t.Fatalf("glob outputs: %v", err)
	}
	if len(outs) != 2 {
		t.Fatalf("got %d outputs %v, want 2", len(outs), outs)
	}
	mf := readManifest(t, filepath.Join(ws, "outputs", "manifest.json"))
	if len(mf.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2", len(mf.Inputs))
	}
	want := map[string]string{
		filepath.Base(aKey): motoDeclaredSHA(aBody),
		filepath.Base(bKey): motoDeclaredSHA(bBody),
	}
	for _, in := range mf.Inputs {
		if got, ok := want[filepath.Base(in.Path)]; !ok {
			t.Errorf("unexpected ledger input %q", in.Path)
		} else if in.SHA256 != got {
			t.Errorf("ledger digest for %s = %q, want declared %q", in.Path, in.SHA256, got)
		}
	}
	// Eager staging is removed on session close.
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files remain under SUMPTER_HOME/work after a clean eager run", files)
	}
}

// TestMotoFileListDeclaredDigestMismatchContinueOnError proves a digest mismatch
// emits no records from that input, attributes it, and reaps staging.
func TestMotoFileListDeclaredDigestMismatchContinueOnError(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aBody := []byte("<root><item><Name>A</Name></item></root>")
	bBody := []byte("<root><item><Name>B</Name></item></root>")
	aKey := runKeyPrefix() + "declared-mismatch/a.xml"
	bKey := runKeyPrefix() + "declared-mismatch/b.xml"
	m.putObject(t, aKey, aBody)
	m.putObject(t, bKey, bBody)
	aURI := "s3://" + m.bucket + "/" + aKey
	bURI := "s3://" + m.bucket + "/" + bKey
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := motoDeclaredWorkspace(t, "declared_cloud")
	wrong := "sha256:" + strings.Repeat("d", 64)
	list := writeListLines(t, ws, []string{
		objectLine(t, aURI, int64(len(aBody)), motoDeclaredSHA(aBody)),
		objectLine(t, bURI, int64(len(bBody)), wrong),
	})

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:           "recipe.yaml",
		FileList:               filepath.Base(list),
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
		ContinueOnError:        true,
		Progress:               false,
	})
	if err == nil {
		t.Fatal("continue-on-error run with a digest mismatch must report partial failure")
	}
	if _, statErr := os.Stat(filepath.Join(ws, "outputs", "extract-a.xml.json")); statErr != nil {
		t.Errorf("healthy input A must produce output: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "outputs", "extract-b.xml.json")); statErr == nil {
		t.Error("mismatched input B must not produce output")
	}
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files remain after the mismatch run", files)
	}
}

// TestMotoFileListDeclaredMalformedAggregateContinueOnError proves an eager
// cloud input that passes declaration verification but fails XML parsing stays
// in the aggregate manifest with the established failed/zero-record shape.
func TestMotoFileListDeclaredMalformedAggregateContinueOnError(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	goodBody := []byte("<root><item><Name>A</Name></item></root>")
	badBody := []byte("<root><item><Name>B</Name>")
	goodKey := runKeyPrefix() + "declared-malformed/good.xml"
	badKey := runKeyPrefix() + "declared-malformed/bad.xml"
	m.putObject(t, goodKey, goodBody)
	m.putObject(t, badKey, badBody)
	goodURI := "s3://" + m.bucket + "/" + goodKey
	badURI := "s3://" + m.bucket + "/" + badKey
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := motoDeclaredWorkspace(t, "declared_cloud")
	list := writeListLines(t, ws, []string{
		objectLine(t, goodURI, int64(len(goodBody)), motoDeclaredSHA(goodBody)),
		objectLine(t, badURI, int64(len(badBody)), motoDeclaredSHA(badBody)),
	})

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath:           "recipe.yaml",
		FileList:               filepath.Base(list),
		OutputMode:             "aggregate",
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
		ContinueOnError:        true,
		Progress:               false,
	})
	if err == nil {
		t.Fatal("malformed declared input must report partial failure")
	}
	names := aggregateRecordNames(t, filepath.Join(ws, "outputs", "records.jsonl"))
	if len(names) != 1 || names[0] != "A" {
		t.Fatalf("names = %v, want only [A]", names)
	}
	mf := readManifest(t, filepath.Join(ws, "outputs", "manifest.json"))
	if len(mf.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2 (failed input remains inventoried)", len(mf.Inputs))
	}
	failed := mf.Inputs[1]
	if failed.Path != badURI {
		t.Errorf("failed input path = %q, want logical URI %q", failed.Path, badURI)
	}
	if failed.RecordCount == nil || *failed.RecordCount != 0 {
		t.Errorf("failed input record_count = %v, want 0", failed.RecordCount)
	}
	if failed.Disposition != "failed" {
		t.Errorf("failed input disposition = %q, want failed", failed.Disposition)
	}
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files remain after malformed eager-cloud input", files)
	}
}

// TestMotoFileListDeclaredMatchBounded proves the bounded cloud path verifies
// declarations, commits rows, and reaps every staged byte.
func TestMotoFileListDeclaredMatchBounded(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aBody := []byte("<root><TargetElement><Name>valA</Name></TargetElement></root>")
	bBody := []byte("<root><TargetElement><Name>valB</Name></TargetElement></root>")
	aKey := runKeyPrefix() + "declared-bounded/a.xml"
	bKey := runKeyPrefix() + "declared-bounded/b.xml"
	m.putObject(t, aKey, aBody)
	m.putObject(t, bKey, bBody)
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := writeMultiRecipeWorkspace(t, "summary")
	outRoot := filepath.Join(t.TempDir(), "out")
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, "s3://"+m.bucket+"/"+aKey, int64(len(aBody)), motoDeclaredSHA(aBody)),
		objectLine(t, "s3://"+m.bucket+"/"+bKey, int64(len(bBody)), motoDeclaredSHA(bBody)),
	})

	shared := &multiSharedOptions{
		FileList:               list,
		OutputPath:             outRoot,
		RunID:                  testMultiRunID,
		OutputMode:             "aggregate",
		EmitInputIdentity:      true,
		CloudInputMode:         "bounded",
		CloudStagingMaxBytes:   10 << 20,
		CloudStagingMaxFiles:   10,
		CloudObjectMaxBytes:    5 << 20,
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
	}
	if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("bounded declared run: %v", err)
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 2 || names[0] != "valA" || names[1] != "valB" {
		t.Fatalf("names = %v, want [valA valB]", names)
	}
	mf := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if len(mf.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2", len(mf.Inputs))
	}
	declared := map[string]string{
		filepath.Base(aKey): motoDeclaredSHA(aBody),
		filepath.Base(bKey): motoDeclaredSHA(bBody),
	}
	for _, in := range mf.Inputs {
		if got, ok := declared[filepath.Base(in.Path)]; !ok || in.SHA256 != got {
			t.Errorf("ledger input %q digest = %q, want declared %q", in.Path, in.SHA256, got)
		}
	}
	rows := readNDJSONLines(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	for i, row := range rows {
		wantOrdinal := i + 1
		if !strings.Contains(row, fmt.Sprintf(`"input_ordinal":%d`, wantOrdinal)) || !strings.Contains(row, `"input_sha256":"`+mf.Inputs[i].SHA256+`"`) {
			t.Errorf("row %d does not join to cloud-input ledger entry %d: %s", i, wantOrdinal, row)
		}
	}
	// Bounded reaping: no staged files survive the run.
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files remain under SUMPTER_HOME/work after a bounded run", files)
	}
}

// TestMotoFileListDeclaredSizeMismatchBoundedFailsBeforeStaging proves the HEAD
// size gate rejects a size deviant object before any bytes are fetched.
func TestMotoFileListDeclaredSizeMismatchBoundedFailsBeforeStaging(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aBody := []byte("<root><TargetElement><Name>valA</Name></TargetElement></root>")
	aKey := runKeyPrefix() + "declared-size/a.xml"
	m.putObject(t, aKey, aBody)
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := writeMultiRecipeWorkspace(t, "summary")
	outRoot := filepath.Join(t.TempDir(), "out")
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, "s3://"+m.bucket+"/"+aKey, int64(len(aBody))+1, motoDeclaredSHA(aBody)), // tampered size
	})

	shared := &multiSharedOptions{
		FileList:               list,
		OutputPath:             outRoot,
		RunID:                  testMultiRunID,
		OutputMode:             "aggregate",
		CloudInputMode:         "bounded",
		CloudStagingMaxBytes:   10 << 20,
		CloudStagingMaxFiles:   10,
		CloudObjectMaxBytes:    5 << 20,
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
	}
	err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "does not match declared size") {
		t.Fatalf("err = %v, want declared-size mismatch before staging", err)
	}
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files exist after a pre-staging size rejection", files)
	}
}

// TestMotoFileListDeclaredDigestMismatchBoundedReaps proves a bounded digest
// mismatch emits no rows from that input and leaves no staged bytes behind.
func TestMotoFileListDeclaredDigestMismatchBoundedReaps(t *testing.T) {
	m := motoEnvOrSkip(t)
	initExtractManifestTestLogger(t)
	home := t.TempDir()
	t.Setenv("SUMPTER_HOME", home)

	aBody := []byte("<root><TargetElement><Name>valA</Name></TargetElement></root>")
	bBody := []byte("<root><TargetElement><Name>valB</Name></TargetElement></root>")
	aKey := runKeyPrefix() + "declared-bounded-mismatch/a.xml"
	bKey := runKeyPrefix() + "declared-bounded-mismatch/b.xml"
	m.putObject(t, aKey, aBody)
	m.putObject(t, bKey, bBody)
	credPath := m.writeNamedCredentialsConfig(t, t.TempDir(), "reader")

	ws := writeMultiRecipeWorkspace(t, "summary")
	outRoot := filepath.Join(t.TempDir(), "out")
	wrong := "sha256:" + strings.Repeat("f", 64)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, "s3://"+m.bucket+"/"+aKey, int64(len(aBody)), motoDeclaredSHA(aBody)),
		objectLine(t, "s3://"+m.bucket+"/"+bKey, int64(len(bBody)), wrong),
	})
	shared := &multiSharedOptions{
		FileList:               list,
		OutputPath:             outRoot,
		RunID:                  testMultiRunID,
		OutputMode:             "aggregate",
		ContinueOnError:        true,
		CloudInputMode:         "bounded",
		CloudStagingMaxBytes:   10 << 20,
		CloudStagingMaxFiles:   10,
		CloudObjectMaxBytes:    5 << 20,
		CredentialsPath:        credPath,
		InputCredentialsHandle: "reader",
	}
	err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("bounded digest mismatch must report partial failure")
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 1 || names[0] != "valA" {
		t.Fatalf("names = %v, want only [valA] (no rows from the mismatched input)", names)
	}
	if files := countFilesUnder(t, filepath.Join(home, "work")); files != 0 {
		t.Errorf("%d staged files remain after a bounded digest mismatch", files)
	}
}
