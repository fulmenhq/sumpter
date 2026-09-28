package commands

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/assets"
	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
	"github.com/fulmenhq/sumpter/internal/extract"
)

const (
	inspectXMLFixtureDir   = "../../../tests/fixtures/inspect/xml"
	inspectGuardFixtureDir = "../../../tests/fixtures/inspect/guard"
)

// runInspect runs the inspect command in-process with the report written to a
// file under a temp dir. It returns the report bytes (nil when no report file
// was written), stdout, and the command error.
func runInspect(t *testing.T, args ...string) (report []byte, stdout string, err error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "report.out")
	cmd := NewInspectCommand()
	var so, se bytes.Buffer
	cmd.SetOut(&so)
	cmd.SetErr(&se)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(append([]string{"--output", out}, args...))
	err = cmd.Execute()
	data, rerr := os.ReadFile(out) // #nosec G304 - test temp path
	if rerr == nil {
		report = data
	} else if !os.IsNotExist(rerr) {
		t.Fatalf("read report: %v", rerr)
	}
	return report, so.String(), err
}

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// inspectJSONDoc runs the JSON profile on content and returns the decoded
// report and its raw map form.
func inspectJSONDoc(t *testing.T, content string, extra ...string) (InspectReportV0, map[string]any) {
	t.Helper()
	p := writeTemp(t, "doc.json", content)
	args := append([]string{"--input-format", "json", "--format", "json"}, extra...)
	data, _, err := runInspect(t, append(args, p)...)
	if err != nil {
		t.Fatalf("inspect --input-format json %q: %v", content, err)
	}
	var rep InspectReportV0
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatalf("decode report: %v\n%s", err, data)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode raw report: %v", err)
	}
	return rep, raw
}

func findPath(rep InspectReportV0, path string) *InspectPath {
	for i := range rep.Paths {
		if rep.Paths[i].Path == path {
			return &rep.Paths[i]
		}
	}
	return nil
}

func kinds(p *InspectPath) InspectValueKinds {
	if p.ValueKinds == nil {
		return InspectValueKinds{}
	}
	return *p.ValueKinds
}

func sumKinds(k InspectValueKinds) int {
	return k.String + k.Number + k.Bool + k.Null + k.Object + k.Array
}

func TestInspectPathString(t *testing.T) {
	cases := []struct {
		segs []string
		want string
	}{
		{[]string{"a", "b"}, "a.b"},
		{[]string{"a.b"}, `a\.b`},
		{[]string{`a\b`}, `a\\b`},
		{[]string{`x.\`, "y"}, `x\.\\.y`},
		{[]string{""}, ""},
		{[]string{"WidgetCoData", "Orders"}, "WidgetCoData.Orders"},
	}
	for _, tc := range cases {
		if got := inspectPathString(tc.segs); got != tc.want {
			t.Errorf("inspectPathString(%q) = %q, want %q", tc.segs, got, tc.want)
		}
	}
}

// TestInspectXMLThreeFieldNonRegression asserts that on the pass fixtures a
// v0.1.2 XML report differs from the v0.1.1 baseline (captured from the
// pre-change binary) in exactly version, input.format, and paths[].segments.
func TestInspectXMLThreeFieldNonRegression(t *testing.T) {
	dir, err := filepath.Abs(inspectXMLFixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir) // reports record the argument as given; baselines used the basename
	for _, name := range []string{"plain", "bom8", "u16le-bom", "u16be-bom", "w1252", "comment", "doctype"} {
		t.Run(name, func(t *testing.T) {
			got, _, err := runInspect(t, "--format", "json", name+".xml")
			if err != nil {
				t.Fatalf("inspect %s.xml: %v", name, err)
			}
			base, err := os.ReadFile(name + ".v011.json") // #nosec G304 - fixture
			if err != nil {
				t.Fatal(err)
			}
			gotMap := decodeNumbers(t, got)
			baseMap := decodeNumbers(t, base)
			stripVolatile(gotMap)
			stripVolatile(baseMap)

			if v := baseMap["version"]; v != "inspect-report/v0.1.1" {
				t.Fatalf("baseline version = %v", v)
			}
			if v := gotMap["version"]; v != InspectReportVersion {
				t.Fatalf("version = %v, want %s", v, InspectReportVersion)
			}
			delete(gotMap, "version")
			delete(baseMap, "version")

			input := gotMap["input"].(map[string]any)
			if f := input["format"]; f != "xml" {
				t.Fatalf("input.format = %v, want xml", f)
			}
			delete(input, "format")

			for _, p := range gotMap["paths"].([]any) {
				pm := p.(map[string]any)
				raw, ok := pm["segments"].([]any)
				if !ok {
					t.Fatalf("path %v has no segments", pm["path"])
				}
				segs := make([]string, len(raw))
				for i, s := range raw {
					segs[i] = s.(string)
				}
				if !reflect.DeepEqual(segs, strings.Split(pm["path"].(string), ".")) || inspectPathString(segs) != pm["path"] {
					t.Fatalf("segments %q do not match path %v", segs, pm["path"])
				}
				if _, has := pm["value_kinds"]; has {
					t.Fatalf("XML path %v carries value_kinds", pm["path"])
				}
				delete(pm, "segments")
			}

			gotCanon, _ := json.Marshal(gotMap)
			baseCanon, _ := json.Marshal(baseMap)
			if !bytes.Equal(gotCanon, baseCanon) {
				t.Fatalf("report differs from baseline beyond the three fields:\n got: %s\nbase: %s", gotCanon, baseCanon)
			}
		})
	}
}

func decodeNumbers(t *testing.T, data []byte) map[string]any {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	return m
}

func stripVolatile(m map[string]any) {
	if metrics, ok := m["metrics"].(map[string]any); ok {
		delete(metrics, "elapsed_ms")
		delete(metrics, "throughput_bytes_per_sec")
		delete(metrics, "rss_peak_mb")
	}
	if md, ok := m["metadata"].(map[string]any); ok {
		delete(md, "timestamp")
	}
}

func TestInspectXMLMarkdownHeaderUnchanged(t *testing.T) {
	_, stdout, err := runInspectStdout(t, filepath.Join(inspectXMLFixtureDir, "plain.xml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "# XML Inspection Report\n") {
		t.Fatalf("markdown header changed: %q", firstLine(stdout))
	}
}

func runInspectStdout(t *testing.T, args ...string) (InspectReportV0, string, error) {
	t.Helper()
	cmd := NewInspectCommand()
	var so bytes.Buffer
	cmd.SetOut(&so)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs(args)
	err := cmd.Execute()
	return InspectReportV0{}, so.String(), err
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func TestInspectXMLDottedElementName(t *testing.T) {
	data, _, err := runInspect(t, "--format", "json", filepath.Join(inspectXMLFixtureDir, "dotted.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var rep InspectReportV0
	if err := json.Unmarshal(data, &rep); err != nil {
		t.Fatal(err)
	}
	p := findPath(rep, `r.a\.b`)
	if p == nil {
		t.Fatalf("no escaped path r.a\\.b in %+v", rep.Paths)
	}
	if !reflect.DeepEqual(p.Segments, []string{"r", "a.b"}) {
		t.Fatalf("segments = %q, want raw element names", p.Segments)
	}
	if !reflect.DeepEqual(p.Samples, []string{"1"}) {
		t.Fatalf("samples = %q", p.Samples)
	}
	if findPath(rep, "r.a.b") != nil {
		t.Fatal("dotted element name produced an unescaped path")
	}
}

func TestInspectJSONValueKinds(t *testing.T) {
	type row struct {
		path  string
		segs  []string
		count int
		kinds InspectValueKinds
	}
	cases := []struct {
		name string
		doc  string
		rows []row
	}{
		{"scalar member", `{"k":"x"}`, []row{{"k", []string{"k"}, 1, InspectValueKinds{String: 1}}}},
		{"scalar array", `{"tags":["a","b"]}`, []row{{"tags", []string{"tags"}, 2, InspectValueKinds{String: 2}}}},
		{"object array", `{"Order":[{"id":1},{"id":2}]}`, []row{
			{"Order", []string{"Order"}, 2, InspectValueKinds{Object: 2}},
			{"Order.id", []string{"Order", "id"}, 2, InspectValueKinds{Number: 2}},
		}},
		{"nested array", `{"m":[[1,2],[3]]}`, []row{
			{"m", []string{"m"}, 2, InspectValueKinds{Array: 2}},
			{"m.m", []string{"m", "m"}, 3, InspectValueKinds{Number: 3}},
		}},
		{"null", `{"n":null}`, []row{{"n", []string{"n"}, 1, InspectValueKinds{Null: 1}}}},
		{"string and null", `{"t":["a",null]}`, []row{{"t", []string{"t"}, 2, InspectValueKinds{String: 1, Null: 1}}}},
		{"empty array", `{"e":[]}`, nil},
		{"top-level array", `[{"id":1}]`, []row{
			{"item", []string{"item"}, 1, InspectValueKinds{Object: 1}},
			{"item.id", []string{"item", "id"}, 1, InspectValueKinds{Number: 1}},
		}},
		{"bool", `{"b":true}`, []row{{"b", []string{"b"}, 1, InspectValueKinds{Bool: 1}}}},
		{"dotted key", `{"a.b":1}`, []row{{`a\.b`, []string{"a.b"}, 1, InspectValueKinds{Number: 1}}}},
		{"nested keys", `{"a":{"b":1}}`, []row{
			{"a", []string{"a"}, 1, InspectValueKinds{Object: 1}},
			{"a.b", []string{"a", "b"}, 1, InspectValueKinds{Number: 1}},
		}},
		{"backslash key", `{"a\\b":1}`, []row{{`a\\b`, []string{`a\b`}, 1, InspectValueKinds{Number: 1}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep, _ := inspectJSONDoc(t, tc.doc)
			if len(rep.Paths) != len(tc.rows) {
				t.Fatalf("got %d paths %+v, want %d", len(rep.Paths), rep.Paths, len(tc.rows))
			}
			for _, want := range tc.rows {
				p := findPath(rep, want.path)
				if p == nil {
					t.Fatalf("missing path %q in %+v", want.path, rep.Paths)
				}
				if !reflect.DeepEqual(p.Segments, want.segs) {
					t.Errorf("%s segments = %q, want %q", want.path, p.Segments, want.segs)
				}
				if p.Count != want.count {
					t.Errorf("%s count = %d, want %d", want.path, p.Count, want.count)
				}
				if kinds(p) != want.kinds {
					t.Errorf("%s value_kinds = %+v, want %+v", want.path, kinds(p), want.kinds)
				}
			}
			for _, p := range rep.Paths {
				if sumKinds(kinds(&p)) != p.Count {
					t.Errorf("%s: sum(value_kinds)=%d != count %d", p.Path, sumKinds(kinds(&p)), p.Count)
				}
				if inspectPathString(p.Segments) != p.Path {
					t.Errorf("path %q is not its escaped segments %q", p.Path, p.Segments)
				}
			}
		})
	}
}

func TestInspectJSONNullHasNoSample(t *testing.T) {
	rep, _ := inspectJSONDoc(t, `{"t":[null,"a"],"n":null}`)
	if p := findPath(rep, "n"); p == nil || len(p.Samples) != 0 {
		t.Fatalf("null path samples: %+v", p)
	}
	if p := findPath(rep, "t"); p == nil || !reflect.DeepEqual(p.Samples, []string{"a"}) {
		t.Fatalf("t samples: %+v", p)
	}
}

func TestInspectJSONReportShape(t *testing.T) {
	rep, raw := inspectJSONDoc(t, `{"Order":[{"id":1,"name":"x"}],"ok":true}`)
	if rep.Version != InspectReportVersion {
		t.Fatalf("version = %q", rep.Version)
	}
	if rep.Input.Format != "json" || rep.Input.EncodingDetected != "UTF-8" || rep.Input.EncodingForced != nil {
		t.Fatalf("input = %+v", rep.Input)
	}
	if rep.Caps.AttributesTruncated {
		t.Fatal("attributes_truncated must be false for json")
	}
	for _, key := range []string{"streaming_analysis", "record_candidates", "oom_summary", "analysis_metadata", "record_analysis"} {
		if _, ok := raw[key]; ok {
			t.Errorf("JSON report carries XML-only section %q", key)
		}
	}
	for _, p := range raw["paths"].([]any) {
		pm := p.(map[string]any)
		attrs, ok := pm["attributes"].([]any)
		if !ok || len(attrs) != 0 {
			t.Errorf("path %v attributes = %#v, want []", pm["path"], pm["attributes"])
		}
		if _, ok := pm["segments"]; !ok {
			t.Errorf("path %v has no segments", pm["path"])
		}
		if _, ok := pm["value_kinds"]; !ok {
			t.Errorf("path %v has no value_kinds", pm["path"])
		}
	}
	caps := raw["caps"].(map[string]any)
	if v, ok := caps["attributes_truncated"]; !ok || v != false {
		t.Errorf("caps.attributes_truncated = %v", v)
	}
	if rep.Metrics.BytesProcessed <= 0 {
		t.Errorf("bytes_processed = %d", rep.Metrics.BytesProcessed)
	}
}

func TestInspectJSONMarkdown(t *testing.T) {
	p := writeTemp(t, "doc.json", `{"a":{"b":"v"}}`)
	_, stdout, err := runInspectStdout(t, "--input-format", "json", p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout, "# JSON Inspection Report\n") {
		t.Fatalf("header = %q", firstLine(stdout))
	}
	if !strings.Contains(stdout, "| a.b | 1 | 0 | 1 |") {
		t.Fatalf("markdown row missing:\n%s", stdout)
	}
	if strings.Contains(stdout, "Record Boundary Analysis") {
		t.Fatal("JSON markdown carries record analysis")
	}
}

func TestInspectJSONSamplesCapped(t *testing.T) {
	long := strings.Repeat("x", 150)
	rep, _ := inspectJSONDoc(t, `{"s":["`+long+`","b","c"]}`, "--samples-per-path", "1")
	p := findPath(rep, "s")
	if p == nil || len(p.Samples) != 1 || p.Samples[0] != strings.Repeat("x", 97)+"..." {
		t.Fatalf("samples = %+v", p)
	}
	if !rep.Caps.SamplesTruncated {
		t.Fatal("samples_truncated not set")
	}
}

func TestInspectJSONMaxPaths(t *testing.T) {
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 20; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k` + string(rune('a'+i)) + `":1`)
	}
	b.WriteString("}")
	rep, _ := inspectJSONDoc(t, b.String(), "--max-paths", "5")
	if !rep.Caps.PathsTruncated {
		t.Fatal("paths_truncated not set")
	}
	if len(rep.Paths) > 5 {
		t.Fatalf("len(paths) = %d exceeds cap", len(rep.Paths))
	}

	rep, _ = inspectJSONDoc(t, `{"a":1,"b":2}`, "--max-paths", "5")
	if rep.Caps.PathsTruncated {
		t.Fatal("paths_truncated set under the cap")
	}
}

func TestInspectJSONDepthLimit(t *testing.T) {
	doc := strings.Repeat("[", docjson.MaxDepth+1) + strings.Repeat("]", docjson.MaxDepth+1)
	p := writeTemp(t, "deep.json", doc)
	report, stdout, err := runInspect(t, "--input-format", "json", "--format", "json", p)
	if err == nil {
		t.Fatal("depth 1025 accepted")
	}
	if !strings.Contains(err.Error(), "json: nesting depth exceeds 1024 at byte offset 1024") {
		t.Fatalf("error = %q", err)
	}
	if report != nil || stdout != "" {
		t.Fatal("a report was written for a rejected input")
	}

	// Depth 1024 is accepted.
	ok := strings.Repeat("[", docjson.MaxDepth) + strings.Repeat("]", docjson.MaxDepth)
	if _, _, err := runInspect(t, "--input-format", "json", "--format", "json", writeTemp(t, "ok.json", ok)); err != nil {
		t.Fatalf("depth 1024 rejected: %v", err)
	}
}

func TestInspectJSONErrorsCarryNoExcerpts(t *testing.T) {
	const secret = "SECRETVALUE"
	cases := []struct {
		doc        string
		wantOffset bool
	}{
		{`{"a":"` + secret + `"} {"b":"` + secret + `"}`, true},
		{strings.Repeat(`{"a":"`+secret+`","b":`, docjson.MaxDepth+1) + "1" + strings.Repeat("}", docjson.MaxDepth+1), true},
		{`{"a":"` + secret + `",}`, false},
		{`{"a":"` + secret, false},
	}
	for _, tc := range cases {
		_, _, err := runInspect(t, "--input-format", "json", "--format", "json", writeTemp(t, "bad.json", tc.doc))
		if err == nil {
			t.Fatalf("accepted %q", tc.doc)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error carries an input excerpt: %q", err)
		}
		if tc.wantOffset && !strings.Contains(err.Error(), "byte offset") {
			t.Fatalf("error has no byte offset: %q", err)
		}
	}
}

// TestInspectJSONAcceptanceParity runs the parse negatives (and a positive)
// through inspect and through extraction and asserts the same accept/reject
// decision and the same error text.
func TestInspectJSONAcceptanceParity(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		ok   bool
	}{
		{"valid object", `{"a":[1,{"b":null}]}`, true},
		{"valid array", `[1,2]`, true},
		{"malformed", `{"a" 1}`, false},
		{"trailing comma", `[1,]`, false},
		{"duplicate key", `{"b":0,"a":1,"a":2}`, false},
		{"invalid UTF-8", "{\"s\":\"ab\xffcd\"}", false},
		{"second top-level value", `{"a":1} {"b":2}`, false},
		{"depth over cap", strings.Repeat("[", docjson.MaxDepth+1) + strings.Repeat("]", docjson.MaxDepth+1), false},
		{"truncated", `{"a":[1,2`, false},
		{"top-level scalar", `42`, false},
		{"empty", ``, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTemp(t, "doc.json", tc.doc)

			_, _, inspectErr := runInspect(t, "--input-format", "json", "--format", "json", path)

			sig := &extract.FileSignature{
				FormatType:          "json",
				MatchPatterns:       []extract.MatchPattern{{PatternID: "any", Selector: "/*", Weight: 1}},
				ConfidenceThreshold: 1,
			}
			res := extract.ProcessFile(path, sig,
				&extract.ExtractRecordMatch{MatchSelectors: []extract.MatchSelector{{XPath: "/*"}}}, nil, true)
			extractErr := res.Error

			_, parseErr := docjson.Format{}.Parse(strings.NewReader(tc.doc))

			if tc.ok {
				if inspectErr != nil || extractErr != nil || parseErr != nil {
					t.Fatalf("valid input rejected: inspect=%v extract=%v parse=%v", inspectErr, extractErr, parseErr)
				}
				return
			}
			if inspectErr == nil || extractErr == nil || parseErr == nil {
				t.Fatalf("accept/reject differ: inspect=%v extract=%v parse=%v", inspectErr, extractErr, parseErr)
			}
			want := parseErr.Error()
			if !strings.Contains(inspectErr.Error(), want) {
				t.Errorf("inspect error %q does not carry parse text %q", inspectErr, want)
			}
			if !strings.Contains(extractErr.Error(), want) {
				t.Errorf("extract error %q does not carry parse text %q", extractErr, want)
			}
			if got := strings.TrimPrefix(inspectErr.Error(), "inspection failed: "); got != want {
				t.Errorf("inspect text %q != parse text %q", got, want)
			}
		})
	}
}

// TestInspectGuardMatrix is the 13-file refusal matrix plus --input-format
// json on each kind of input.
func TestInspectGuardMatrix(t *testing.T) {
	const (
		notXML  = "input does not look like XML; use --input-format json for JSON input"
		looksXM = "input looks like XML; drop --input-format json"
		gzipped = "inspect does not read gzip input; decompress it first (for example: gunzip -k <file>)"
	)
	x := func(n string) string { return filepath.Join(inspectXMLFixtureDir, n) }
	g := func(n string) string { return filepath.Join(inspectGuardFixtureDir, n) }
	gz := gzipFixture(t, x("plain.xml"))
	cases := []struct {
		name    string
		file    string
		json    bool
		wantErr string // "" = report written
	}{
		{"plain", x("plain.xml"), false, ""},
		{"utf-8 bom", x("bom8.xml"), false, ""},
		{"utf-16le bom", x("u16le-bom.xml"), false, ""},
		{"utf-16be bom", x("u16be-bom.xml"), false, ""},
		{"windows-1252", x("w1252.xml"), false, ""},
		{"comment first", x("comment.xml"), false, ""},
		{"doctype first", x("doctype.xml"), false, ""},
		{"utf-16le no bom", g("u16le-nobom.xml"), false, "inspection failed: XML parsing error: XML syntax error on line 1: expected element name after <"},
		{"utf-16be no bom", g("u16be-nobom.xml"), false, "inspection failed: XML parsing error: XML syntax error on line 1: illegal character code U+0000"},
		{"json object", g("t.json"), false, notXML},
		{"json array", g("arr.json"), false, notXML},
		{"gzip", gz, false, gzipped},
		{"empty", g("empty.xml"), false, ""},

		{"json flag on xml", x("plain.xml"), true, looksXM},
		{"json flag on bom xml", x("bom8.xml"), true, looksXM},
		{"json flag on comment xml", x("comment.xml"), true, looksXM},
		{"json flag on gzip", gz, true, gzipped},
		{"json flag on json object", g("t.json"), true, ""},
		{"json flag on json array", g("arr.json"), true, ""},
		{"json flag on empty", g("empty.xml"), true, "inspection failed: json: empty input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := []string{"--format", "json"}
			if tc.json {
				args = append(args, "--input-format", "json")
			}
			report, _, err := runInspect(t, append(args, tc.file)...)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if report == nil {
					t.Fatal("no report written")
				}
				return
			}
			if err == nil || err.Error() != tc.wantErr {
				t.Fatalf("error = %v, want %q", err, tc.wantErr)
			}
			if report != nil {
				t.Fatal("a report was written for a refused input")
			}
		})
	}
}

// gzipFixture writes a gzip-compressed copy of src to a temp file; the
// repository does not track .gz files.
func gzipFixture(t *testing.T, src string) string {
	t.Helper()
	data, err := os.ReadFile(src) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return writeTemp(t, "plain.xml.gz", buf.String())
}

func TestInspectJSONFlagRefusals(t *testing.T) {
	doc := writeTemp(t, "doc.json", `{"a":1}`)
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"force-encoding", []string{"--force-encoding", "utf-8"}, "--force-encoding does not apply to --input-format json"},
		{"analyze-records", []string{"--analyze-records", "--record-selector", "//a"}, "streaming input is not supported for json in this release"},
		{"generate-config", []string{"--generate-config"}, "--generate-config does not support --input-format json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"--input-format", "json", "--format", "json"}, tc.args...)
			report, stdout, err := runInspect(t, append(args, doc)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want it to contain %q", err, tc.want)
			}
			if report != nil || stdout != "" {
				t.Fatal("output written for a refused invocation")
			}
		})
	}

	// The analyze-records refusal is the format's own route-unsupported error.
	_, want := docjson.Format{}.NewScanner(nil, "", false)
	_, _, err := runInspect(t, "--input-format", "json", "--analyze-records", doc)
	if err == nil || err.Error() != want.Error() {
		t.Fatalf("analyze-records error = %v, want %v", err, want)
	}

	// Unknown formats are rejected.
	if _, _, err := runInspect(t, "--input-format", "yaml", doc); err == nil || !strings.Contains(err.Error(), `unknown --input-format "yaml"`) {
		t.Fatalf("unknown format error = %v", err)
	}
}

func TestInspectReportSchemaV012(t *testing.T) {
	t.Run("json report", func(t *testing.T) {
		p := writeTemp(t, "doc.json", `{"Order":[{"id":1,"tags":["a",null],"m":[[1],[]]}],"a.b":true}`)
		data, _, err := runInspect(t, "--input-format", "json", "--format", "json", "--validate-output", p)
		if err != nil {
			t.Fatalf("inspect --validate-output: %v", err)
		}
		if err := validateJSONAgainstEmbeddedSchema(data); err != nil {
			t.Fatalf("JSON report does not validate: %v", err)
		}
	})

	t.Run("xml report", func(t *testing.T) {
		src, err := os.ReadFile(filepath.Join(inspectXMLFixtureDir, "plain.xml"))
		if err != nil {
			t.Fatal(err)
		}
		rep, err := inspectXML(bytes.NewReader(src), FileInfo{Path: "plain.xml", Size: int64(len(src))},
			EncodingInfo{Detected: "UTF-8"}, &InspectOptions{MaxPaths: 200, SamplesPerPath: 2, IncludeAttrs: true})
		if err != nil {
			t.Fatal(err)
		}
		// A plain XML report carries a zero-valued streaming_analysis block
		// that the schema's enums reject (unchanged from v0.1.1); fill the
		// record-analysis sections as a real --analyze-records run would.
		rep.StreamingAnalysis = StreamingAnalysis{
			Confidence: "low", Warnings: []Warning{},
			PerformanceEstimates: PerformanceEstimates{StreamingSequential: "-", ManifestParallel32x: "-"},
		}
		rep.RecordCandidates = []RecordCandidate{{Element: "Order", XPath: "//Order", Count: 1, Depth: 3, SampleOffsets: []int64{}}}
		rep.AnalysisMetadata = &AnalysisMetadata{
			AnalyzedAt:      "2026-01-01T00:00:00Z",
			AnalysisOptions: AnalysisOptions{AnalyzeRecords: true, MaxCandidates: 5},
		}
		var buf bytes.Buffer
		if err := generateJSONReport(&buf, rep); err != nil {
			t.Fatal(err)
		}
		if err := validateJSONAgainstEmbeddedSchema(buf.Bytes()); err != nil {
			t.Fatalf("XML report does not validate: %v", err)
		}
	})

	t.Run("segments required", func(t *testing.T) {
		bad := `{"version":"inspect-report/v0.1.2","input":{"path":"x","size_bytes":1,"encoding_detected":"UTF-8","compressed":false,"compression":"none","format":"json"},` +
			`"metrics":{"bytes_processed":1,"elapsed_ms":0,"throughput_bytes_per_sec":0,"replacement_count":0},"paths":[{"path":"a","count":1}]}`
		if err := validateJSONAgainstEmbeddedSchema([]byte(bad)); err == nil {
			t.Fatal("path without segments validated")
		}
		badKinds := strings.Replace(bad, `{"path":"a","count":1}`, `{"path":"a","segments":["a"],"count":1,"value_kinds":{"text":1}}`, 1)
		if err := validateJSONAgainstEmbeddedSchema([]byte(badKinds)); err == nil {
			t.Fatal("unknown value_kinds key validated")
		}
		noFormat := strings.Replace(bad, `,"format":"json"`, ``, 1)
		noFormat = strings.Replace(noFormat, `{"path":"a","count":1}`, `{"path":"a","segments":["a"],"count":1}`, 1)
		if err := validateJSONAgainstEmbeddedSchema([]byte(noFormat)); err == nil {
			t.Fatal("report without input.format validated")
		}
	})

	t.Run("catalog current", func(t *testing.T) {
		want := map[string]any{"version": "v0.1.2", "entry": "contract://sumpter.inspect/v0.1.2/inspect-report.schema.yaml"}
		src, err := os.ReadFile("../../../schemas/index.json")
		if err != nil {
			t.Fatal(err)
		}
		schemasFS, err := assets.GetSchemasFS()
		if err != nil {
			t.Fatal(err)
		}
		embedded, err := fs.ReadFile(schemasFS, "schemas/index.json")
		if err != nil {
			t.Fatal(err)
		}
		for name, data := range map[string][]byte{"schemas/index.json": src, "embedded index.json": embedded} {
			var idx struct {
				Current map[string]map[string]any `json:"current"`
			}
			if err := json.Unmarshal(data, &idx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(idx.Current["inspect"], want) {
				t.Errorf("%s current.inspect = %v, want %v", name, idx.Current["inspect"], want)
			}
		}
	})
}
