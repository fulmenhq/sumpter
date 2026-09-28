package commands

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

const jsonCaseRecipe = "../../../examples/cases/14-json-basic-extraction/recipe"

// runJSONExtract runs `recipes run extract` on the JSON example recipe with
// input as its only file (--files splits on commas, so input must not contain
// one). It returns the bytes of every file written to the output directory
// and the command error.
func runJSONExtract(t *testing.T, input string) ([]byte, error) {
	t.Helper()
	out := t.TempDir()
	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(NewRecipesCommand())
	var so, se bytes.Buffer
	root.SetOut(&so)
	root.SetErr(&se)
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetArgs([]string{
		"recipes", "run", "extract", jsonCaseRecipe,
		"--files", input,
		"--output-path", out,
		"--output-pattern", "records.jsonl",
		"--run-id", "0196d5b2-0d00-7c00-8000-000000000006",
		"--no-manifest",
	})
	err := root.Execute()
	var written []byte
	ents, rerr := os.ReadDir(out)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, e := range ents {
		b, rerr := os.ReadFile(filepath.Join(out, e.Name())) // #nosec G304 - test temp path
		if rerr != nil {
			t.Fatal(rerr)
		}
		written = append(written, b...)
	}
	return written, err
}

// TestInspectExtractCommandParity runs the inspect and extract commands on the
// same JSON input and requires the same fault text, including its
// file-relative byte offset, with no report and no records.
func TestInspectExtractCommandParity(t *testing.T) {
	far := strings.Repeat("x", 1<<20)
	longKey := strings.Repeat("k", 70<<10)
	cases := []struct {
		name, doc, want string
	}{
		{"syntax then UTF-8 in one chunk", "{\"a\":tru, \"b\":\"\xff\"}", "json: invalid character ',' in literal true (expecting 'e')"},
		{"UTF-8 then syntax in one chunk", "{\"b\":\"\xff\", \"a\":tru}", "json: invalid UTF-8 at byte offset 6"},
		{"syntax then UTF-8 1 MiB apart", "{\"a\":tru, \"b\":\"" + far + "\xff\"}", "json: invalid character ',' in literal true (expecting 'e')"},
		{"UTF-8 then syntax 1 MiB apart", "{\"b\":\"\xff" + far + "\", \"a\":tru}", "json: invalid UTF-8 at byte offset 6"},
		{"BOM and duplicate key", "\xef\xbb\xbf{\"a\":1,\"a\":2}", `json: duplicate key "a" at byte offset 10`},
		{"BOM and invalid byte", "\xef\xbb\xbf{\"a\":\"\xff\"}", "json: invalid UTF-8 at byte offset 9"},
		{"long duplicate key", `{"` + longKey + `":1,"` + longKey + `":2}`, `json: duplicate key "` + longKey[:64] + `"…(71680 bytes) at byte offset 71686`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "doc.json", tc.doc)

			report, stdout, inspectErr := runInspect(t, "--input-format", "json", "--format", "json", path)
			if inspectErr == nil {
				t.Fatal("inspect accepted the input")
			}
			if report != nil || stdout != "" {
				t.Fatal("inspect wrote a report for a rejected input")
			}
			if got := strings.TrimPrefix(inspectErr.Error(), "inspection failed: "); got != tc.want {
				t.Errorf("inspect: got %.120q, want %.120q", got, tc.want)
			}

			written, extractErr := runJSONExtract(t, path)
			if extractErr == nil {
				t.Fatal("extract accepted the input")
			}
			if len(written) != 0 {
				t.Fatalf("extract wrote %d bytes of output for a rejected input", len(written))
			}
			prefix := "failed to process file " + path + ": failed to parse JSON: "
			if got := strings.TrimPrefix(extractErr.Error(), prefix); got != tc.want {
				t.Errorf("extract: got %.400q, want %.120q", got, tc.want)
			}
		})
	}
}

// TestInspectExtractCommandBOM requires a UTF-8 byte order mark to be accepted
// by both commands, with the same profile and the same extract.data as
// without it.
func TestInspectExtractCommandBOM(t *testing.T) {
	doc, err := os.ReadFile("../../../examples/cases/14-json-basic-extraction/input.json")
	if err != nil {
		t.Fatal(err)
	}
	plain := writeTemp(t, "plain.json", string(doc))
	bom := writeTemp(t, "bom.json", "\xef\xbb\xbf"+string(doc))

	profile := func(path string) string {
		data, _, err := runInspect(t, "--input-format", "json", "--format", "json", path)
		if err != nil {
			t.Fatalf("inspect %s: %v", filepath.Base(path), err)
		}
		var rep InspectReportV0
		if err := json.Unmarshal(data, &rep); err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(struct {
			Format string
			Paths  []InspectPath
			Caps   InspectCaps
		}{rep.Input.Format, rep.Paths, rep.Caps})
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	if p, b := profile(plain), profile(bom); p != b {
		t.Errorf("BOM changed the inspect profile:\n%s\n%s", p, b)
	}

	extractData := func(path string) string {
		out, err := runJSONExtract(t, path)
		if err != nil {
			t.Fatalf("extract %s: %v", filepath.Base(path), err)
		}
		var blocks []string
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			var rec struct {
				Extract json.RawMessage `json:"extract"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil {
				t.Fatalf("decode record: %v", err)
			}
			blocks = append(blocks, string(rec.Extract))
		}
		return strings.Join(blocks, "\n")
	}
	p, b := extractData(plain), extractData(bom)
	if p == "" || p != b {
		t.Errorf("BOM changed extract.data:\n%s\n%s", p, b)
	}
}
