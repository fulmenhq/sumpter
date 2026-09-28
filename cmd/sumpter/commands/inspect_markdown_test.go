package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestMarkdownEscapes(t *testing.T) {
	for _, tc := range []struct{ fn, in, want string }{
		{"text", "Envelope.Header_Id", "Envelope.Header_Id"},
		{"text", "a|b", `a\|b`},
		{"text", "line\nbreak", `line\\nbreak`},
		{"text", "x`y", "x\\`y"},
		{"text", "<img src=x onerror=alert(1)>", "&lt;img src=x onerror=alert(1)&gt;"},
		{"text", "[c](javascript:alert(1))", `\[c\](javascript:alert(1))`},
		{"text", "a&b", "a&amp;b"},
		{"text", "evil\u202etxt", `evil\\u202Etxt`},
		{"text", "\x01\x7f\u0085\u2028\ufeff", `\\x01\\x7F\\u0085\\u2028\\uFEFF`},
		{"text", `a\.b`, `a\\.b`},
		{"code", "Café", "`Café`"},
		{"code", "v`w", "``v`w``"},
		{"code", "``x", "``` ``x ```"},
		{"code", " padded ", "`  padded  `"},
		{"code", "   ", "`   `"},
		{"code", "has | pipe\nand newline", "`has | pipe\\nand newline`"},
		{"code", "<script>", "`<script>`"},
	} {
		got := mdText(tc.in)
		if tc.fn == "code" {
			got = mdCode(tc.in)
		}
		if got != tc.want {
			t.Errorf("md%s(%q) = %q, want %q", tc.fn, tc.in, got, tc.want)
		}
	}
}

func TestTruncateInspectSampleOnRuneBoundary(t *testing.T) {
	s := strings.Repeat("a", 96) + "語語"
	got := truncateInspectSample(s)
	if !utf8.ValidString(got) || got != strings.Repeat("a", 96)+"..." {
		t.Fatalf("truncated to %q", got)
	}
	if got := truncateInspectSample(strings.Repeat("b", 100)); got != strings.Repeat("b", 100) {
		t.Fatalf("100 bytes truncated: %q", got)
	}
}

// codeSpan matches one Markdown code span: a backtick fence, content without a
// run of the same length, and the same fence.
func stripCodeSpans(t *testing.T, line string) string {
	t.Helper()
	var out strings.Builder
	for i := 0; i < len(line); {
		if line[i] != '`' || i > 0 && line[i-1] == '\\' {
			out.WriteByte(line[i])
			i++
			continue
		}
		n := 0
		for i+n < len(line) && line[i+n] == '`' {
			n++
		}
		fence := strings.Repeat("`", n)
		end := -1
		for j := i + n; j <= len(line)-n; j++ {
			if line[j:j+n] == fence && (j+n == len(line) || line[j+n] != '`') && line[j-1] != '`' {
				end = j
				break
			}
		}
		if end < 0 {
			t.Fatalf("unclosed code span in %q", line)
		}
		i = end + n
	}
	return out.String()
}

// TestInspectMarkdownIsInert runs the Markdown report on keys and samples
// that carry table, line, code-span, HTML, link, bidi and control payloads.
func TestInspectMarkdownIsInert(t *testing.T) {
	keys := []string{"a|b", "line\nbreak", "x`y", "<img src=x onerror=alert(1)>", "<script>alert(1)</script>",
		"[c](javascript:alert(1))", "evil\u202etxt", "ctl\x01\x7f", "tab\there"}
	rec := map[string]string{}
	for _, k := range keys {
		rec[k] = "has | pipe\nand newline `tick` <script>x</script> \u202e \x02"
	}
	doc, err := json.Marshal(map[string]any{"r": rec})
	if err != nil {
		t.Fatal(err)
	}
	path := writeTemp(t, "doc.json", string(doc))
	report, _, err := runInspect(t, "--input-format", "json", "--format", "markdown", path)
	if err != nil {
		t.Fatal(err)
	}
	md := string(report)

	for _, r := range md {
		if r != '\n' && needsVisible(r) {
			t.Fatalf("raw control or bidi character %U in the report:\n%s", r, md)
		}
	}
	unescapedPipe := regexp.MustCompile(`(^|[^\\])\|`)
	inTable := false
	rows := 0
	for _, line := range strings.Split(md, "\n") {
		switch {
		case line == "| Path | Count | Attributes | Samples |":
			inTable = true
		case inTable && line == "":
			inTable = false
		case inTable && !strings.HasPrefix(line, "|---"):
			rows++
			if n := len(unescapedPipe.FindAllStringIndex(line, -1)); n != 5 {
				t.Errorf("table row has %d cell borders, want 5: %q", n, line)
			}
		}
		if strings.HasPrefix(line, "### ") && strings.ContainsAny(line, "<>") {
			t.Errorf("heading carries raw HTML: %q", line)
		}
		if outside := stripCodeSpans(t, line); strings.Contains(outside, "<") {
			t.Errorf("raw < outside a code span: %q", line)
		}
	}
	if rows != len(keys)+1 {
		t.Fatalf("table has %d rows, want %d:\n%s", rows, len(keys)+1, md)
	}
	if got := strings.Count(md, "\n### Samples for "); got != len(keys) {
		t.Fatalf("%d sample headings, want %d (each on one line)", got, len(keys))
	}
}

// TestInspectXMLMarkdownUnchanged pins the Markdown report on the seven XML
// fixtures, whose names and samples carry no special characters, to the
// output of the release before escaping.
func TestInspectXMLMarkdownUnchanged(t *testing.T) {
	volatile := regexp.MustCompile(`(?m)^- \*\*(Duration|Throughput|Memory Peak|Timestamp):\*\* .*$`)
	for _, name := range []string{"plain", "bom8", "u16le-bom", "u16be-bom", "w1252", "comment", "doctype"} {
		t.Run(name, func(t *testing.T) {
			report, _, err := runInspect(t, "--format", "markdown", filepath.Join(inspectXMLFixtureDir, name+".xml"))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(inspectXMLFixtureDir, name+".md")) // #nosec G304 - test fixture path
			if err != nil {
				t.Fatal(err)
			}
			got := volatile.ReplaceAllString(string(report), "- **$1:** <volatile>")
			if got != string(want) {
				t.Errorf("XML Markdown report changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}
