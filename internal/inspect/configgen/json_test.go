package configgen

import (
	stdjson "encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/antchfx/xpath"

	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
)

func TestLiteral(t *testing.T) {
	for in, want := range map[string]string{
		"":          `''`,
		"k":         `'k'`,
		"it's":      `"it's"`,
		`say "hi"`:  `'say "hi"'`,
		`both'and"`: `concat('both',"'",'and"')`,
		`'x'"`:      `concat("'",'x',"'",'"')`,
	} {
		if got := Literal(in); got != want {
			t.Errorf("Literal(%q) = %s, want %s", in, got, want)
		}
	}
}

// oddKeys covers every key shape Step must select verbatim.
var oddKeys = []string{
	"Order", "a.b", "a-b", "_x", "é", "and", "text", "node",
	"first name", "123", "1st", "$ref", "@id", "a:b", "xs:string", `a\b`, "",
	"it's", `say "hi"`, `both'and"`, "k—dash", "a/b", "tab\there",
}

func TestStepSelectsKeyExactly(t *testing.T) {
	obj := map[string]string{}
	for i, k := range oddKeys {
		obj[k] = "v" + string(rune('A'+i))
	}
	raw, err := stdjson.Marshal(map[string]any{"R": obj, "N": map[string]any{"a": map[string]string{"b": "nested"}}})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := docjson.Format{}.Parse(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range oddKeys {
		expr, err := xpath.Compile("/R/" + Step(k))
		if err != nil {
			t.Errorf("key %q: step %s does not compile: %v", k, Step(k), err)
			continue
		}
		it := expr.Select(doc.Root().Navigator())
		var got []string
		for it.MoveNext() {
			got = append(got, it.Current().Value())
		}
		if len(got) != 1 || got[0] != obj[k] {
			t.Errorf("key %q: step %s selected %q, want [%q]", k, Step(k), got, obj[k])
		}
	}
	// A dotted key and nested keys are different selectors.
	if Step("a.b") != "a.b" || relativeXPath([]string{"a", "b"}) != "a/b" {
		t.Errorf("dotted %q vs nested %q", Step("a.b"), relativeXPath([]string{"a", "b"}))
	}
}

// TestStandardKeyStepTable keeps the node-model standard's table of key steps
// in step with Step.
func TestStandardKeyStepTable(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/standards/document-node-model.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(doc)
	start := strings.Index(text, "<!-- key-steps:begin -->")
	end := strings.Index(text, "<!-- key-steps:end -->")
	if start < 0 || end < start {
		t.Fatal("standard lacks the key-steps block")
	}
	row := regexp.MustCompile("^\\| (`(.*)`|\\(empty key\\)) \\| `(.*)` \\|$")
	rows := 0
	for _, line := range strings.Split(text[start:end], "\n") {
		m := row.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows++
		key := m[2]
		if m[1] == "(empty key)" {
			key = ""
		}
		if got := Step(key); got != m[3] {
			t.Errorf("standard says key %q steps as %s; Step gives %s", key, m[3], got)
		}
	}
	if rows < 10 {
		t.Fatalf("parsed %d key-step rows, want at least 10", rows)
	}
}

func generateJSON(t *testing.T, doc string, opts Options) *Result {
	t.Helper()
	opts.GeneratedAt = time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	res, err := GenerateJSON(strings.NewReader(doc), opts)
	if err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	return res
}

func TestGenerateJSONRecordSelector(t *testing.T) {
	cases := []struct {
		name, doc, override, want, warning string
	}{
		{"top-level array", `[{"a":1},{"a":2}]`, "", "/item", ""},
		{"most-repeated at the shallowest depth", `{"D":{"Order":[{"id":1},{"id":2},{"id":3}],"Note":[{"x":1},{"x":2}]}}`, "", "/D/Order", ""},
		{"non-NCName record key", `{"my rows":[{"a":1},{"a":2}]}`, "", "/*[local-name()='my rows']", ""},
		{"scalar arrays are never records", `{"D":{"tags":["a","b","c"],"x":1}}`, "", "/D", "using the top-level element"},
		{"no repeat: single top-level object", `{"D":{"id":1}}`, "", "/D", "using the top-level element"},
		{"no repeat: several top-level keys", `{"a":{"x":1},"b":{"y":2}}`, "", "/*", "using every top-level element"},
		{"override", `{"D":{"Order":[{"id":1},{"id":2}]}}`, "//Order[id=2]", "//Order[id=2]", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := generateJSON(t, tc.doc, Options{RecordSelector: tc.override})
			if res.RecordSelector != tc.want {
				t.Fatalf("selector = %q, want %q", res.RecordSelector, tc.want)
			}
			if !strings.Contains(string(res.YAML), "  - xpath: "+quote(tc.want)+"\n") {
				t.Errorf("config does not match on %s:\n%s", tc.want, res.YAML)
			}
			if tc.warning != "" && !strings.Contains(strings.Join(res.Warnings, "|"), tc.warning) {
				t.Errorf("warnings = %q, want one containing %q", res.Warnings, tc.warning)
			}
		})
	}
	if _, err := GenerateJSON(strings.NewReader(`{"D":{"id":1}}`), Options{RecordSelector: "//Missing"}); err == nil || !strings.Contains(err.Error(), "did not match any element") {
		t.Errorf("override matching nothing: %v", err)
	}
	if _, err := GenerateJSON(strings.NewReader(`{"a":1,"a":2}`), Options{}); err == nil || err.Error() != `json: duplicate key "a" at byte offset 7` {
		t.Errorf("parse error: %v", err)
	}
}

func TestGenerateJSONMappings(t *testing.T) {
	doc := `{"R":[
		{"i":1,"f":2.50,"e":1e3,"b":true,"s":"x","d":"2026-09-28","z":null,"m":1,"tags":["a","b"],"lines":[{"sku":"A","qty":1},{"sku":"B","qty":2}],"o":{"k":"v"}},
		{"i":2,"f":3,"e":2,"b":false,"s":"y","d":"2026-09-29","z":null,"m":"one","tags":["c"],"lines":[{"sku":"C","qty":3}],"o":{"k":"w"}}
	]}`
	got := string(generateJSON(t, doc, Options{}).YAML)
	for _, want := range []string{
		"- output_field: \"i\"\n    xpath: \"i\"\n    type: \"integer\"\n",
		"- output_field: \"f\"\n    xpath: \"f\"\n    type: \"number\"\n",
		"- output_field: \"e\"\n    xpath: \"e\"\n    type: \"number\"\n",
		"- output_field: \"b\"\n    xpath: \"b\"\n    type: \"boolean\"\n",
		"- output_field: \"s\"\n    xpath: \"s\"\n    type: \"string\"\n",
		"# date-shaped; consider transform if downstream consumers need dates\n  - output_field: \"d\"\n",
		"# only null values in the sample; review inferred string type\n  - output_field: \"z\"\n",
		"# mixed value kinds; review inferred string type\n  - output_field: \"m\"\n",
		"- output_field: \"tags\"\n    xpath: \"tags\"\n    type: \"array\"\n",
		"- output_field: \"lines\"\n    xpath: \"lines\"\n    type: \"array\"\n    item_mapping:\n      - output_field: \"qty\"\n        xpath: \"qty\"\n        type: \"integer\"\n      - output_field: \"sku\"\n        xpath: \"sku\"\n        type: \"string\"\n",
		"- output_field: \"k\"\n    xpath: \"o/k\"\n    type: \"string\"\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("config lacks:\n%s\n--- config ---\n%s", want, got)
		}
	}
}

func TestGenerateJSONHeader(t *testing.T) {
	res := generateJSON(t, `{"D":{"Order":[{"id":1},{"id":2}]}}`, Options{SourcePath: "in dir/doc.json", ConfigPath: "out/gen.yaml"})
	got := string(res.YAML)
	for _, want := range []string{
		"#   1. Copy the signature block below to out/gen-signature.yaml, removing \"# \" from each line.\n",
		"#   2. sumpter extract files --signature-config-path out/gen-signature.yaml --extract-config-path out/gen.yaml --files 'in dir/doc.json' --output-path extract-out\n",
		"# --- signature ---\n# signature_id: \"generated-json\"\n# name: \"Generated JSON signature\"\n# format_type: json\n",
		"#     selector: \"/D\"\n",
		"# --- recipe manifest fragment (recipe.yaml) ---\n# defaults:\n#   input:\n#     format: json\n# --- end recipe manifest fragment ---\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("header lacks:\n%s\n--- config ---\n%s", want, got)
		}
	}
	// Without a known output path the command keeps placeholders.
	res = generateJSON(t, `[{"a":1},{"a":2}]`, Options{})
	if !strings.Contains(string(res.YAML), "--signature-config-path <name>-signature.yaml --extract-config-path <this file> --files <input.json>") {
		t.Errorf("placeholder command missing:\n%s", res.YAML)
	}
	if !strings.Contains(string(res.YAML), "#     selector: \"/*\"\n") {
		t.Errorf("a top-level array's signature must match any top-level element:\n%s", res.YAML)
	}
}
