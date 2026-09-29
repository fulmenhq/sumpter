package commands

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/fulmenhq/sumpter/internal/extract"
)

// oddKeyDoc has two records whose keys are a dotted key, a nested pair with
// the same names, a space, a leading digit, a colon, the empty key, and both
// quote kinds.
const oddKeyDoc = `{"Rows":[` +
	`{"id":1,"a.b":"dot1","a":{"b":"nest1"},"first name":"sp1","1st":true,"a:b":"col1","":"empty1","it's \"q\"":"quote1"},` +
	`{"id":2,"a.b":"dot2","a":{"b":"nest2"},"first name":"sp2","1st":false,"a:b":"col2","":"empty2","it's \"q\"":"quote2"}]}`

// oddKeyWant is the extract.data each record must carry.
var oddKeyWant = []map[string]any{
	{"id": float64(1), "a_b": "dot1", "b": "nest1", "first_name": "sp1", "1st": true, "a_b_2": "col1", "field": "empty1", "it_s_q": "quote1"},
	{"id": float64(2), "a_b": "dot2", "b": "nest2", "first_name": "sp2", "1st": false, "a_b_2": "col2", "field": "empty2", "it_s_q": "quote2"},
}

// commentBlock returns the lines between "# --- <name> ---" and
// "# --- end <name> ---" with the leading "# " removed.
func commentBlock(t *testing.T, config, name string) string {
	t.Helper()
	start := "# --- " + name + " ---\n"
	i := strings.Index(config, start)
	j := strings.Index(config, "# --- end "+strings.Fields(name)[0])
	if i < 0 || j < i {
		t.Fatalf("config has no %q block:\n%s", name, config)
	}
	var out strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(config[i+len(start):j], "\n"), "\n") {
		out.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "#"), " ") + "\n")
	}
	return out.String()
}

// shellFields splits a command line on spaces, honoring single quotes.
func shellFields(line string) []string {
	var fields []string
	var cur strings.Builder
	inQuote, have := false, false
	for _, r := range line {
		switch {
		case r == '\'':
			inQuote, have = !inQuote, true
		case r == ' ' && !inQuote:
			if have {
				fields = append(fields, cur.String())
				cur.Reset()
				have = false
			}
		default:
			cur.WriteRune(r)
			have = true
		}
	}
	if have {
		fields = append(fields, cur.String())
	}
	return fields
}

// runSumpter runs args against a root command carrying the persistent flags
// extraction reads.
func runSumpter(t *testing.T, args []string) error {
	t.Helper()
	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(NewExtractCommand(), NewRecipesCommand())
	var so, se bytes.Buffer
	root.SetOut(&so)
	root.SetErr(&se)
	root.SilenceUsage = true
	root.SilenceErrors = true
	root.SetArgs(args)
	return root.Execute()
}

// recordData decodes the extract.data of every record in the files of dir.
func recordData(t *testing.T, dir, pattern string) []map[string]any {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil || len(paths) != 1 {
		t.Fatalf("records under %s matching %s: %v %v", dir, pattern, paths, err)
	}
	raw, err := os.ReadFile(paths[0]) // #nosec G304 - test temp path
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var rec struct {
			Extract struct {
				Data map[string]any `json:"data"`
			} `json:"extract"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode record %q: %v", line, err)
		}
		out = append(out, rec.Extract.Data)
	}
	return out
}

func sameData(got, want []map[string]any) bool {
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	return bytes.Equal(g, w)
}

// TestInspectJSONGenerateConfigRoundTrip runs inspect → --generate-config →
// extract on keys that are not XML names: the generated file loads as it is,
// the header's first-run steps run verbatim and yield the records, and the
// signature and manifest blocks, uncommented, load with the real commands.
func TestInspectJSONGenerateConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })
	if err := os.WriteFile("odd.json", []byte(oddKeyDoc), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := NewInspectCommand()
	cmd.SetOut(io.Discard)
	cmd.SetArgs([]string{"--input-format", "json", "--generate-config", "--output", "gen.yaml", "odd.json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("inspect --generate-config: %v", err)
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("inspect wrote more than the one config file: %v", entries)
	}
	raw, err := os.ReadFile("gen.yaml")
	if err != nil {
		t.Fatal(err)
	}
	config := string(raw)

	// One YAML document, loadable as-is.
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var first, second any
	if err := dec.Decode(&first); err != nil {
		t.Fatalf("decode config: %v", err)
	}
	if err := dec.Decode(&second); !errors.Is(err, io.EOF) {
		t.Fatalf("config has more than one YAML document: %v", err)
	}
	ext, err := extract.LoadExtractConfig("gen.yaml")
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if len(ext.MatchSelectors) != 1 || ext.MatchSelectors[0].XPath != "//Rows" {
		t.Fatalf("match selectors = %+v", ext.MatchSelectors)
	}
	for _, want := range []string{
		`xpath: "a.b"`, `xpath: "a/b"`, `xpath: "*[local-name()='first name']"`, `xpath: "*[local-name()='1st']"`,
		`xpath: "*[local-name()='a:b']"`, `xpath: "*[local-name()='']"`, `xpath: "*[local-name()=concat('it',\"'\",'s \"q\"')]"`,
	} {
		if !strings.Contains(config, want) {
			t.Errorf("config lacks %s", want)
		}
	}

	// First run, step 1: copy the signature block.
	step1 := regexp.MustCompile(`(?m)^#   1\. Copy the signature block below to (\S+), removing "# " from each line\.$`).FindStringSubmatch(config)
	if step1 == nil || step1[1] != "gen-signature.yaml" {
		t.Fatalf("header step 1 missing or unexpected: %v\n%s", step1, config)
	}
	if err := os.WriteFile(step1[1], []byte(commentBlock(t, config, "signature")), 0o600); err != nil {
		t.Fatal(err)
	}
	sig, err := extract.LoadSignatureConfig(step1[1])
	if err != nil {
		t.Fatalf("copied signature does not load: %v", err)
	}
	if sig.FormatType != "json" {
		t.Fatalf("copied signature format_type = %q", sig.FormatType)
	}

	// First run, step 2: the stated command, verbatim.
	step2 := regexp.MustCompile(`(?m)^#   2\. (.+)$`).FindStringSubmatch(config)
	if step2 == nil {
		t.Fatalf("header step 2 missing:\n%s", config)
	}
	args := shellFields(step2[1])
	want := []string{"sumpter", "extract", "files", "--signature-config-path", "gen-signature.yaml",
		"--extract-config-path", "gen.yaml", "--files", "odd.json", "--output-path", "extract-out"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("header step 2 = %q, want %q", args, want)
	}
	if err := runSumpter(t, args[1:]); err != nil {
		t.Fatalf("first-run command failed: %v", err)
	}
	if got := recordData(t, "extract-out", "extract-*.json"); !sameData(got, oddKeyWant) {
		t.Fatalf("first-run records = %v, want %v", got, oddKeyWant)
	}

	// The manifest fragment, uncommented, drives a recipe run to the same data.
	fragment := commentBlock(t, config, "recipe manifest fragment (recipe.yaml)")
	if err := os.MkdirAll(filepath.Join("recipe", "signature"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join("recipe", "extract"), 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := "version: \"recipe/v0.1.0\"\nkind: \"extract\"\nid: generated_json\n" +
		"display_name: \"Generated JSON\"\ncreated_at: \"2026-09-28T00:00:00Z\"\ncontent_version: \"0.0.1\"\n" +
		"assets:\n  signature: signature/signature.yaml\n  extract: extract/extract.yaml\n" + fragment
	for path, content := range map[string]string{
		filepath.Join("recipe", "recipe.yaml"):                 manifest,
		filepath.Join("recipe", "signature", "signature.yaml"): commentBlock(t, config, "signature"),
		filepath.Join("recipe", "extract", "extract.yaml"):     config,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !strings.Contains(manifest, "defaults:\n  input:\n    format: json\n") {
		t.Fatalf("fragment did not uncomment to defaults.input.format: json:\n%s", manifest)
	}
	// Recipe runs resolve relative paths against the recipe directory.
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := runSumpter(t, []string{"recipes", "run", "extract", "recipe", "--files", filepath.Join(here, "odd.json"),
		"--output-path", filepath.Join(here, "recipe-out"), "--output-pattern", "records.jsonl", "--no-manifest"}); err != nil {
		t.Fatalf("recipe run with the uncommented fragment failed: %v", err)
	}
	if got := recordData(t, "recipe-out", "records.jsonl"); !sameData(got, oddKeyWant) {
		t.Fatalf("recipe records = %v, want %v", got, oddKeyWant)
	}
}

// TestInspectXMLGenerateConfigUnchanged pins XML --generate-config output on
// the seven XML fixtures to the output of the release before JSON inspection.
func TestInspectXMLGenerateConfigUnchanged(t *testing.T) {
	stamp := regexp.MustCompile(` on [0-9TZ:-]+\.\n`)
	for _, name := range []string{"plain", "bom8", "u16le-bom", "u16be-bom", "w1252", "comment", "doctype"} {
		t.Run(name, func(t *testing.T) {
			cmd := NewInspectCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{"--generate-config", filepath.Join(inspectXMLFixtureDir, name+".xml")})
			if err := cmd.Execute(); err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(inspectXMLFixtureDir, name+".genconfig.yaml")) // #nosec G304 - test fixture path
			if err != nil {
				t.Fatal(err)
			}
			got := stamp.ReplaceAllString(out.String(), " on <generated-at>.\n")
			if got != string(want) {
				t.Errorf("XML generate-config output changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}
