package extract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// jsonTwinCase pairs a worked XML example with its JSON twin in the example
// tree: examples/cases/<case>/variants/json, or a standalone twin case (case 14
// is the twin of case 01). The twin's recipe differs by format_type: json on the
// signature and @k -> k in XPaths.
type jsonTwinCase struct {
	id       string
	caseDir  string
	twinCase string // standalone twin case dir; empty means variants/json
}

var jsonTwinCases = []jsonTwinCase{
	{id: "01", caseDir: "01-basic-extraction", twinCase: "14-json-basic-extraction"},
	{id: "02", caseDir: "02-multi-record-line-items"},
	{id: "08", caseDir: "08-polymorphic-line-items"},
	{id: "09", caseDir: "09-predicate-match-selector"},
	{id: "10", caseDir: "10-optional-fields"},
}

func examplesCasesDir() string {
	return filepath.Join("..", "..", "examples", "cases")
}

func (c jsonTwinCase) xmlDir() string {
	return filepath.Join(examplesCasesDir(), c.caseDir)
}

func (c jsonTwinCase) twinDir() string {
	if c.twinCase != "" {
		return filepath.Join(examplesCasesDir(), c.twinCase)
	}
	return filepath.Join(c.xmlDir(), "variants", "json")
}

// singleYAML returns the only *.yaml file in dir.
func singleYAML(t *testing.T, dir string) string {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		t.Fatalf("glob %s: %v", dir, err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one yaml in %s, got %v", dir, matches)
	}
	return matches[0]
}

func (c jsonTwinCase) xmlConfigPaths(t *testing.T) (sigPath, extPath string) {
	t.Helper()
	return singleYAML(t, filepath.Join(c.xmlDir(), "recipe", "signature")),
		singleYAML(t, filepath.Join(c.xmlDir(), "recipe", "extract"))
}

func (c jsonTwinCase) twinConfigPaths(t *testing.T) (sigPath, extPath string) {
	t.Helper()
	return singleYAML(t, filepath.Join(c.twinDir(), "recipe", "signature")),
		singleYAML(t, filepath.Join(c.twinDir(), "recipe", "extract"))
}

// runTwin loads the configs, resolves the input format, extracts inputPath and
// returns the canonical bytes of each record's extract block (the same
// portion the example goldens pin).
func runTwin(t *testing.T, sigPath, extPath, inputPath, recipeFormat string, recipeMode bool, wantFormat string) [][]byte {
	t.Helper()
	raw := runTwinRaw(t, sigPath, extPath, inputPath, recipeFormat, recipeMode, wantFormat)
	out := make([][]byte, 0, len(raw))
	for _, b := range raw {
		out = append(out, canonicalJSON(t, b))
	}
	return out
}

// runTwinRaw is runTwin without float64 canonicalization: it returns each
// record's extract block exactly as marshaled, for typed comparison.
func runTwinRaw(t *testing.T, sigPath, extPath, inputPath, recipeFormat string, recipeMode bool, wantFormat string) [][]byte {
	t.Helper()
	sig, err := extract.LoadSignatureConfig(sigPath)
	if err != nil {
		t.Fatalf("LoadSignatureConfig(%s): %v", sigPath, err)
	}
	ext, err := extract.LoadExtractConfig(extPath)
	if err != nil {
		t.Fatalf("LoadExtractConfig(%s): %v", extPath, err)
	}
	got, err := extract.ResolveInputFormat(recipeFormat, recipeMode, sig, ext, nil)
	if err != nil {
		t.Fatalf("ResolveInputFormat(%q, %v): %v", recipeFormat, recipeMode, err)
	}
	if got != wantFormat {
		t.Fatalf("ResolveInputFormat = %q, want %q", got, wantFormat)
	}
	result := extract.ProcessFile(inputPath, sig, ext, nil, false)
	if result.Error != nil {
		t.Fatalf("ProcessFile(%s): %v", inputPath, result.Error)
	}
	if len(result.Records) == 0 {
		t.Fatalf("ProcessFile(%s): no records", inputPath)
	}
	out := make([][]byte, 0, len(result.Records))
	for i, rec := range result.Records {
		b, err := json.Marshal(rec["extract"])
		if err != nil {
			t.Fatalf("record %d: marshal extract: %v", i, err)
		}
		out = append(out, b)
	}
	return out
}

// canonicalJSON round-trips through interface{} so map ordering and number
// representation match the example canonicalizer.
func canonicalJSON(t *testing.T, b []byte) []byte {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("unmarshal %s: %v", b, err)
	}
	out, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return out
}

func goldenExtracts(t *testing.T, path string) [][]byte {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304 -- test reads repo golden.
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	var golden struct {
		Records []struct {
			Extract json.RawMessage `json:"extract"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &golden); err != nil {
		t.Fatalf("decode golden %s: %v", path, err)
	}
	out := make([][]byte, 0, len(golden.Records))
	for _, r := range golden.Records {
		out = append(out, canonicalJSON(t, r.Extract))
	}
	return out
}

func assertSameExtracts(t *testing.T, label string, want, got [][]byte) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s: record count %d, want %d", label, len(got), len(want))
	}
	for i := range want {
		if !bytes.Equal(want[i], got[i]) {
			t.Errorf("%s: record %d extract differs\n want: %s\n  got: %s", label, i, want[i], got[i])
		}
	}
}

// TestJSONTwinParity runs each worked XML example and its JSON twin and
// requires byte-identical extract blocks, and both to match the example golden.
func TestJSONTwinParity(t *testing.T) {
	for _, c := range jsonTwinCases {
		t.Run(c.id, func(t *testing.T) {
			xmlSig, xmlExt := c.xmlConfigPaths(t)
			xmlOut := runTwin(t, xmlSig, xmlExt, filepath.Join(c.xmlDir(), "input.xml"), "", false, extract.FormatXML)

			jsonSig, jsonExt := c.twinConfigPaths(t)
			jsonOut := runTwin(t, jsonSig, jsonExt, filepath.Join(c.twinDir(), "input.json"), extract.FormatJSON, true, extract.FormatJSON)

			assertSameExtracts(t, "json vs xml", xmlOut, jsonOut)

			xmlRaw := runTwinRaw(t, xmlSig, xmlExt, filepath.Join(c.xmlDir(), "input.xml"), "", false, extract.FormatXML)
			jsonRaw := runTwinRaw(t, jsonSig, jsonExt, filepath.Join(c.twinDir(), "input.json"), extract.FormatJSON, true, extract.FormatJSON)
			if err := typedRecordsEqual(xmlRaw, jsonRaw); err != nil {
				t.Errorf("json vs xml typed parity: %v", err)
			}

			golden := goldenExtracts(t, filepath.Join(c.xmlDir(), "expected", "output.json"))
			assertSameExtracts(t, "xml vs golden", golden, xmlOut)
			assertSameExtracts(t, "json vs golden", golden, jsonOut)
		})
	}
}

// TestJSONTwinRecipeContentHashDiffers pins that the JSON twin recipe is a
// distinct recipe identity from its XML source.
func TestJSONTwinRecipeContentHashDiffers(t *testing.T) {
	for _, c := range jsonTwinCases {
		t.Run(c.id, func(t *testing.T) {
			xmlSigPath, xmlExtPath := c.xmlConfigPaths(t)
			jsonSigPath, jsonExtPath := c.twinConfigPaths(t)
			read := func(p string) []byte {
				b, err := os.ReadFile(p) // #nosec G304 -- test reads repo fixtures.
				if err != nil {
					t.Fatalf("read %s: %v", p, err)
				}
				return b
			}
			xmlHash, err := provenance.RecipeContentHash(read(xmlSigPath), read(xmlExtPath))
			if err != nil {
				t.Fatalf("xml RecipeContentHash: %v", err)
			}
			jsonHash, err := provenance.RecipeContentHash(read(jsonSigPath), read(jsonExtPath))
			if err != nil {
				t.Fatalf("json RecipeContentHash: %v", err)
			}
			if xmlHash == "" || jsonHash == "" {
				t.Fatalf("empty recipe content hash: xml=%q json=%q", xmlHash, jsonHash)
			}
			if xmlHash == jsonHash {
				t.Fatalf("recipe content hash equal for xml and json twin: %s", xmlHash)
			}
		})
	}
}

// TestJSONTwinXMLRecipeRejectedUnderJSON pins that the unmodified XML recipe
// (with @k steps) fails at load when forced to json.
func TestJSONTwinXMLRecipeRejectedUnderJSON(t *testing.T) {
	for _, c := range jsonTwinCases {
		t.Run(c.id, func(t *testing.T) {
			sigPath, extPath := c.xmlConfigPaths(t)
			sig, err := extract.LoadSignatureConfig(sigPath)
			if err != nil {
				t.Fatal(err)
			}
			ext, err := extract.LoadExtractConfig(extPath)
			if err != nil {
				t.Fatal(err)
			}
			sig.FormatType = extract.FormatJSON
			if _, err := extract.ResolveInputFormat(extract.FormatJSON, true, sig, ext, nil); err == nil {
				t.Fatal("expected attribute-axis rejection under json, got nil")
			}
		})
	}
}
