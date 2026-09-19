package commands

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// declaredInput is one synthetic input with its true content identity.
type declaredInput struct {
	Path string
	Body []byte
	Size int64
	SHA  string // sha256:<hex>
}

func makeDeclaredInputs(t *testing.T, n int) []declaredInput {
	t.Helper()
	dir := t.TempDir()
	inputs := make([]declaredInput, 0, n)
	for i := 0; i < n; i++ {
		letter := string(rune('A' + i))
		body := []byte(`<root><TargetElement><Name>val` + letter + `</Name></TargetElement></root>`)
		p := filepath.Join(dir, "in"+letter+".xml")
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatalf("write input: %v", err)
		}
		inputs = append(inputs, declaredInput{
			Path: p,
			Body: body,
			Size: int64(len(body)),
			SHA:  fmt.Sprintf("sha256:%x", sha256.Sum256(body)),
		})
	}
	return inputs
}

func writeListLines(t *testing.T, dir string, lines []string) string {
	t.Helper()
	p := filepath.Join(dir, "declared.list")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write list: %v", err)
	}
	return p
}

func objectLine(t *testing.T, uri string, size int64, sha string) string {
	t.Helper()
	line, err := json.Marshal(map[string]any{"uri": uri, "size": size, "sha256": sha})
	if err != nil {
		t.Fatalf("marshal object line: %v", err)
	}
	return string(line)
}

// aggregateRecordNames returns extract.data.name values in committed order.
func aggregateRecordNames(t *testing.T, path string) []string {
	t.Helper()
	raw := strings.TrimRight(readFileOrFail(t, path), "\n")
	if raw == "" {
		return nil
	}
	names := make([]string, 0, 4)
	for _, line := range strings.Split(raw, "\n") {
		var rec struct {
			Extract struct {
				Data struct {
					Name string `json:"name"`
				} `json:"data"`
			} `json:"extract"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode record: %v", err)
		}
		names = append(names, rec.Extract.Data.Name)
	}
	return names
}

// TestExtractMultiFileListObjectLineLedgerMatch drives an aggregate run whose
// inputs are integrity-bound object lines: rows extract normally and the manifest
// ledger records exactly the declared digest and size.
func TestExtractMultiFileListObjectLineLedgerMatch(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 2)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		objectLine(t, inputs[1].Path, inputs[1].Size, inputs[1].SHA),
	})
	outRoot := filepath.Join(t.TempDir(), "out")

	if err := runExtractMulti(&multiSharedOptions{FileList: list, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate"}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("declared aggregate run: %v", err)
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 2 || names[0] != "valA" || names[1] != "valB" {
		t.Fatalf("names = %v, want [valA valB] (ordinal order)", names)
	}
	m := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if len(m.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2", len(m.Inputs))
	}
	for i, in := range m.Inputs {
		if filepath.Base(in.Path) != filepath.Base(inputs[i].Path) {
			t.Errorf("input[%d].path = %q, want suffix match %q", i, in.Path, inputs[i].Path)
		}
		if in.SHA256 != inputs[i].SHA {
			t.Errorf("input[%d].sha256 = %q, want declared %q", i, in.SHA256, inputs[i].SHA)
		}
		if in.SizeBytes != inputs[i].Size {
			t.Errorf("input[%d].size_bytes = %d, want declared %d", i, in.SizeBytes, inputs[i].Size)
		}
	}
}

// TestExtractMultiFileListObjectLineSizeMismatchFailsFast pins the loud fail
// before any records: a declared size that does not match the bytes aborts the
// run and leaves no committed aggregate output.
func TestExtractMultiFileListObjectLineSizeMismatchFailsFast(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 2)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		objectLine(t, inputs[1].Path, inputs[1].Size+1, inputs[1].SHA), // tampered size
	})
	outRoot := filepath.Join(t.TempDir(), "out")

	err := runExtractMulti(&multiSharedOptions{FileList: list, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate"}, []string{ws}, io.Discard, time.Now())
	if err == nil || !strings.Contains(err.Error(), "declared size") {
		t.Fatalf("err = %v, want declared-size failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(outRoot, "summary", "records.jsonl")); statErr == nil {
		t.Error("a failed run must not leave committed records.jsonl")
	}
}

// TestExtractMultiFileListObjectLineDigestMismatchContinueOnError pins per-input
// attribution: the mismatched input contributes no rows, the healthy input
// commits, and the failure is recorded (aggregate inventory keeps the failed
// input with record_count 0, per the existing failed-input contract).
func TestExtractMultiFileListObjectLineDigestMismatchContinueOnError(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 2)
	wrong := "sha256:" + strings.Repeat("b", 64)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		objectLine(t, inputs[1].Path, inputs[1].Size, wrong), // tampered digest
	})
	outRoot := filepath.Join(t.TempDir(), "out")

	err := runExtractMulti(&multiSharedOptions{FileList: list, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("continue-on-error run with a failed input must report partial failure")
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 1 || names[0] != "valA" {
		t.Fatalf("names = %v, want only [valA] (no rows from the failed input)", names)
	}
	m := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if len(m.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2 (failed input keeps its inventory entry)", len(m.Inputs))
	}
	if m.Inputs[0].SHA256 != inputs[0].SHA || m.Inputs[0].SizeBytes != inputs[0].Size {
		t.Errorf("good input ledger = (%s, %d), want declared (%s, %d)", m.Inputs[0].SHA256, m.Inputs[0].SizeBytes, inputs[0].SHA, inputs[0].Size)
	}
	if m.Inputs[1].RecordCount == nil || *m.Inputs[1].RecordCount != 0 {
		t.Errorf("failed input record_count = %v, want 0", m.Inputs[1].RecordCount)
	}
}

// TestExtractMultiFileListMixedOrderPreserved pins that URI-only and object lines
// mix freely in one list and listed order drives the aggregate record order.
func TestExtractMultiFileListMixedOrderPreserved(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 3)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		inputs[1].Path, // URI-only, unbound
		objectLine(t, inputs[2].Path, inputs[2].Size, inputs[2].SHA),
	})
	outRoot := filepath.Join(t.TempDir(), "out")

	if err := runExtractMulti(&multiSharedOptions{FileList: list, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate"}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("mixed declared run: %v", err)
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 3 || names[0] != "valA" || names[1] != "valB" || names[2] != "valC" {
		t.Fatalf("names = %v, want [valA valB valC] in listed order", names)
	}
	m := readManifest(t, filepath.Join(outRoot, "summary", "manifest.json"))
	if len(m.Inputs) != 3 {
		t.Fatalf("manifest inputs = %d, want 3", len(m.Inputs))
	}
	if m.Inputs[1].SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(inputs[1].Body)) {
		t.Errorf("URI-only input digest = %q, want the ledger hash of its bytes", m.Inputs[1].SHA256)
	}
}

// TestExtractMultiFileListDuplicateRefsOrdinalIsolation proves declarations ride
// the ordinal: the same path listed twice cannot cross-apply a digest. The first
// listing (correct declaration) succeeds; the second (wrong digest) fails.
func TestExtractMultiFileListDuplicateRefsOrdinalIsolation(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 1)
	wrong := "sha256:" + strings.Repeat("c", 64)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		objectLine(t, inputs[0].Path, inputs[0].Size, wrong),
	})
	outRoot := filepath.Join(t.TempDir(), "out")

	err := runExtractMulti(&multiSharedOptions{FileList: list, OutputPath: outRoot, RunID: testMultiRunID, OutputMode: "aggregate", ContinueOnError: true}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("the second (mismatched) duplicate must fail the run partially")
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 1 || names[0] != "valA" {
		t.Fatalf("names = %v, want exactly [valA] (duplicate refs verified independently)", names)
	}
}

// TestRecipeFileListObjectLineUnknownFieldFailsClosed pins the CLI-level
// fail-closed decode: an unknown field (including version_id) aborts with line
// context before any acquisition.
func TestRecipeFileListObjectLineUnknownFieldFailsClosed(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    mode: files\n    files: []\n")
	list := writeListLines(t, ws, []string{
		`{"uri":"testdata/a.xml","size":1,"sha256":"` + testHex64 + `","version_id":"v1"}`,
	})
	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", FileList: filepath.Base(list), Progress: false})
	if err == nil || !strings.Contains(err.Error(), `unknown field "version_id"`) {
		t.Fatalf("err = %v, want unknown-field rejection", err)
	}
	if !strings.Contains(err.Error(), "line 1") {
		t.Fatalf("err = %v, want line context", err)
	}
}

// TestRecipeFileListObjectLinePerInputMode pins the per-input (non-aggregate)
// path: a matching declared input extracts to its own output file and the ledger
// matches the declaration.
func TestRecipeFileListObjectLinePerInputMode(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    mode: files\n    files: []\n")
	aBody := readFileOrFail(t, filepath.Join(ws, "testdata", "a.xml"))
	list := writeListLines(t, ws, []string{
		objectLine(t, "testdata/a.xml", int64(len(aBody)), fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(aBody)))),
	})
	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", FileList: filepath.Base(list), Progress: false}); err != nil {
		t.Fatalf("executeExtractRecipe (declared per-input): %v", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "outputs", "extract-a.xml.json")); err != nil {
		t.Fatalf("expected per-input output: %v", err)
	}
	m := readManifest(t, filepath.Join(ws, "outputs", "manifest.json"))
	if len(m.Inputs) != 1 || m.Inputs[0].SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(aBody))) {
		t.Fatalf("manifest inputs = %+v, want the declared digest", m.Inputs)
	}
}

// TestRecipeFileListObjectLineAggregateMatch pins the single-command aggregate
// path: declared inputs commit in listed order and the ledger equals the
// declarations.
func TestRecipeFileListObjectLineAggregateMatch(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    mode: files\n    files: []\n")
	aBody := []byte(readFileOrFail(t, filepath.Join(ws, "testdata", "a.xml")))
	bBody := []byte(readFileOrFail(t, filepath.Join(ws, "testdata", "b.xml")))
	list := writeListLines(t, ws, []string{
		objectLine(t, "testdata/a.xml", int64(len(aBody)), fmt.Sprintf("sha256:%x", sha256.Sum256(aBody))),
		objectLine(t, "testdata/b.xml", int64(len(bBody)), fmt.Sprintf("sha256:%x", sha256.Sum256(bBody))),
	})
	cmd := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		FileList:     filepath.Base(list),
		OutputMode:   "aggregate",
		Progress:     false,
	}); err != nil {
		t.Fatalf("executeExtractRecipe (declared aggregate): %v", err)
	}
	names := aggregateRecordNames(t, filepath.Join(ws, "outputs", "records.jsonl"))
	if len(names) != 2 || names[0] != "A" || names[1] != "B" {
		t.Fatalf("names = %v, want [A B] in listed order", names)
	}
	m := readManifest(t, filepath.Join(ws, "outputs", "manifest.json"))
	if len(m.Inputs) != 2 {
		t.Fatalf("manifest inputs = %d, want 2", len(m.Inputs))
	}
	if m.Inputs[0].SHA256 != fmt.Sprintf("sha256:%x", sha256.Sum256(aBody)) {
		t.Errorf("input[0] digest = %q, want declared", m.Inputs[0].SHA256)
	}
}

// TestRecipeFileListObjectLineAggregateMismatchFailsFast pins the loud failure on
// the single-command aggregate path: a digest mismatch aborts with no committed
// records.jsonl.
func TestRecipeFileListObjectLineAggregateMismatchFailsFast(t *testing.T) {
	ws := fileListWorkspace(t)
	fileListRecipe(t, ws, "  input:\n    mode: files\n    files: []\n")
	aBody := []byte(readFileOrFail(t, filepath.Join(ws, "testdata", "a.xml")))
	wrong := "sha256:" + strings.Repeat("e", 64)
	list := writeListLines(t, ws, []string{
		objectLine(t, "testdata/a.xml", int64(len(aBody)), fmt.Sprintf("sha256:%x", sha256.Sum256(aBody))),
		objectLine(t, "testdata/b.xml", 1, wrong),
	})
	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, ws, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		FileList:     filepath.Base(list),
		OutputMode:   "aggregate",
		Progress:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "declared ") {
		t.Fatalf("err = %v, want a declared-identity failure", err)
	}
	if _, statErr := os.Stat(filepath.Join(ws, "outputs", "records.jsonl")); statErr == nil {
		t.Error("a failed aggregate run must not leave committed records.jsonl")
	}
}

// TestExtractMultiFileListObjectLineBoundedLocalMatch pins that declared local
// inputs verify identically under bounded mode: bounded budgets are cloud-only,
// but the local path still rides the same declared verify and ledger.
func TestExtractMultiFileListObjectLineBoundedLocalMatch(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	inputs := makeDeclaredInputs(t, 2)
	list := writeListLines(t, t.TempDir(), []string{
		objectLine(t, inputs[0].Path, inputs[0].Size, inputs[0].SHA),
		objectLine(t, inputs[1].Path, inputs[1].Size, inputs[1].SHA),
	})
	outRoot := filepath.Join(t.TempDir(), "out")
	shared := &multiSharedOptions{
		FileList:             list,
		OutputPath:           outRoot,
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		CloudInputMode:       "bounded",
		CloudStagingMaxBytes: 10 << 20,
		CloudStagingMaxFiles: 10,
		CloudObjectMaxBytes:  5 << 20,
	}
	if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("bounded local declared run: %v", err)
	}
	names := aggregateRecordNames(t, filepath.Join(outRoot, "summary", "records.jsonl"))
	if len(names) != 2 || names[0] != "valA" || names[1] != "valB" {
		t.Fatalf("names = %v, want [valA valB]", names)
	}
}
