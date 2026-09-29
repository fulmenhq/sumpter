package extract

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/docnode"
	"github.com/fulmenhq/sumpter/internal/inspect/configgen"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// boundarySink collects emitted records and file boundaries.
type boundarySink struct {
	records    []map[string]interface{}
	boundaries []FileEmissionSummary
}

func (s *boundarySink) OnRecord(_ context.Context, r EmittedRecord) error {
	s.records = append(s.records, r.Envelope())
	return nil
}

func (s *boundarySink) OnFileBoundary(_ context.Context, summary FileEmissionSummary) error {
	s.boundaries = append(s.boundaries, summary)
	return nil
}

func (s *boundarySink) Close(context.Context) error { return nil }

// lowerStreamingThreshold makes every non-empty input a large file.
func lowerStreamingThreshold(t *testing.T) {
	t.Helper()
	old := largeFileStreamingThreshold
	largeFileStreamingThreshold = 1
	t.Cleanup(func() { largeFileStreamingThreshold = old })
}

// projection is what a route decides about an input: admission, and each
// record's number and data.
func projection(res ExtractResult, sink *boundarySink) string {
	var b strings.Builder
	if res.Error != nil {
		fmt.Fprintf(&b, "error:%v|", strings.Contains(res.Error.Error(), "signature mismatch"))
	}
	fmt.Fprintf(&b, "status:%s|", res.SignatureMatchStatus)
	for _, r := range sink.records {
		rt, _ := r["_runtime"].(map[string]interface{})
		ex, _ := r["extract"].(map[string]interface{})
		data, _ := json.Marshal(ex["data"])
		fmt.Fprintf(&b, "%v:%s;", rt["record_num"], data)
	}
	return b.String()
}

// runBothRoutes runs the same recipe over the same source on the
// whole-document route (--allow-large-files) and on the streaming route.
func runBothRoutes(t *testing.T, src string, sig *FileSignature, ext *ExtractRecordMatch) (dom, stream string, domRes, streamRes ExtractResult) {
	t.Helper()
	lowerStreamingThreshold(t)
	path := writeTempFile(t, "in.json", src)
	domSink := &boundarySink{}
	domRes = ProcessFileWithApplicabilityToSink(context.Background(), path, cloneSig(sig), CloneRecordMatch(ext), nil, nil, true, provenance.RuntimeOptions{}, domSink)
	streamSink := &boundarySink{}
	streamRes = ProcessFileWithApplicabilityToSink(context.Background(), path, cloneSig(sig), CloneRecordMatch(ext), nil, nil, false, provenance.RuntimeOptions{}, streamSink)
	return projection(domRes, domSink), projection(streamRes, streamSink), domRes, streamRes
}

func cloneSig(sig *FileSignature) *FileSignature {
	c := *sig
	c.MatchPatterns = append([]MatchPattern(nil), sig.MatchPatterns...)
	return &c
}

func recordScopeConfigs(selector string, patterns ...MatchPattern) (*FileSignature, *ExtractRecordMatch) {
	sig := &FileSignature{
		SignatureID:         "rs",
		FormatType:          FormatJSON,
		MatchScope:          MatchScopeRecord,
		ConfidenceThreshold: 0.8,
		MatchPatterns:       patterns,
	}
	ext := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: selector}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "id", Type: "string"}},
	}
	return sig, ext
}

// TestRecordScopeAdmissionIsRouteIndependent is the forced-route
// discriminator: the same record-scoped recipe and source give the same
// admission and the same records on the whole-document and streaming routes,
// including count(), not() and low-weight document-rooted patterns.
func TestRecordScopeAdmissionIsRouteIndependent(t *testing.T) {
	src := `{"Root":{"m":[{"id":"1"},{"id":"2"}],"forbidden":true}}`
	for _, tc := range []struct {
		name     string
		patterns []MatchPattern
		admitted bool
	}{
		{"count per record", []MatchPattern{{PatternID: "c", Selector: "count(//m)=1", Weight: 1}}, true},
		{"not of a document path", []MatchPattern{{PatternID: "n", Selector: "not(/Root/forbidden)", Weight: 1}}, true},
		{"document-rooted pattern", []MatchPattern{{PatternID: "d", Selector: "/Root", Weight: 1}}, false},
		{"low-weight document-rooted pattern", []MatchPattern{
			{PatternID: "m", Selector: "/m", Weight: 9},
			{PatternID: "d", Selector: "/Root", Weight: 1},
		}, true},
		{"document-rooted pattern above the margin", []MatchPattern{
			{PatternID: "m", Selector: "/m", Weight: 1},
			{PatternID: "d", Selector: "/Root", Weight: 1},
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, ext := recordScopeConfigs("//m", tc.patterns...)
			dom, stream, domRes, _ := runBothRoutes(t, src, sig, ext)
			if dom != stream {
				t.Fatalf("routes disagree:\nDOM    %s\nstream %s", dom, stream)
			}
			if admitted := domRes.Error == nil; admitted != tc.admitted {
				t.Fatalf("admitted = %v, want %v (%v)", admitted, tc.admitted, domRes.Error)
			}
		})
	}
}

func TestRecordScopeParityOnNestedAndScalarRecords(t *testing.T) {
	sig, ext := recordScopeConfigs("//m", MatchPattern{PatternID: "m", Selector: "/m", Weight: 1})
	ext.FieldMappings = []FieldMapping{{OutputField: "v", XPath: ".", Type: "string"}}
	dom, stream, domRes, _ := runBothRoutes(t, `{"m":[[1,2],[3]],"x":{"m":"s"}}`, sig, ext)
	if domRes.Error != nil {
		t.Fatal(domRes.Error)
	}
	if dom != stream {
		t.Fatalf("routes disagree:\nDOM    %s\nstream %s", dom, stream)
	}
	if !strings.Contains(dom, "6:") {
		t.Fatalf("expected six records in preorder: %s", dom)
	}
}

func TestRecordScopeMismatchNamesOrdinalAndFailsInput(t *testing.T) {
	sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "k", Selector: "/r/kind='ok'", Weight: 1})
	src := `{"r":[{"id":"1","kind":"ok"},{"id":"2","kind":"bad"},{"id":"3","kind":"ok"}]}`
	_, _, domRes, streamRes := runBothRoutes(t, src, sig, ext)
	for route, res := range map[string]ExtractResult{"dom": domRes, "stream": streamRes} {
		if res.Error == nil || !strings.Contains(res.Error.Error(), "signature mismatch: record 2") || !strings.Contains(res.Error.Error(), "evaluated per record") {
			t.Fatalf("%s: error = %v", route, res.Error)
		}
		if res.SignatureMatchStatus != SignatureMatchMismatched {
			t.Fatalf("%s: status %s", route, res.SignatureMatchStatus)
		}
		if strings.Contains(res.Error.Error(), "bad") {
			t.Fatalf("%s: mismatch detail carries source content: %v", route, res.Error)
		}
	}
	if domRes.Disposition != DispositionFailed || domRes.DispositionReason != DispositionReasonSignatureMismatch {
		t.Fatalf("dom disposition %s/%s", domRes.Disposition, domRes.DispositionReason)
	}
}

func TestRecordScopeFailsBeforeAnyStreamedBoundaryIsApplied(t *testing.T) {
	lowerStreamingThreshold(t)
	sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "k", Selector: "/r/kind='ok'", Weight: 1})
	sink := &boundarySink{}
	path := writeTempFile(t, "in.json", `{"r":[{"id":"1","kind":"ok"},{"id":"2","kind":"bad"}]}`)
	res := ProcessFileWithApplicabilityToSink(context.Background(), path, sig, ext, nil, nil, false, provenance.RuntimeOptions{}, sink)
	if res.Error == nil {
		t.Fatal("late mismatch admitted")
	}
	if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != DispositionFailed || sink.boundaries[0].DispositionReason != DispositionReasonSignatureMismatch {
		t.Fatalf("boundaries %+v", sink.boundaries)
	}
}

func TestRecordScopeWithNoRecordsLeavesStatusUnknown(t *testing.T) {
	sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "k", Selector: "/r", Weight: 1})
	dom, stream, domRes, streamRes := runBothRoutes(t, `{"other":[1,2]}`, sig, ext)
	if dom != stream {
		t.Fatalf("routes disagree:\nDOM    %s\nstream %s", dom, stream)
	}
	for _, res := range []ExtractResult{domRes, streamRes} {
		if res.Error != nil || res.SignatureMatchStatus != SignatureMatchUnknown {
			t.Fatalf("error %v, status %s", res.Error, res.SignatureMatchStatus)
		}
	}
}

func TestDocumentScopeJSONRefusesStreamingAboveThreshold(t *testing.T) {
	lowerStreamingThreshold(t)
	sig, ext := jsonProcessConfigs()
	path := writeTempFile(t, "in.json", `{"D":{"Order":[{"id":"1"}]}}`)
	sink := &boundarySink{}
	res := ProcessFileWithApplicabilityToSink(context.Background(), path, sig, ext, nil, nil, false, provenance.RuntimeOptions{}, sink)
	if !errors.Is(res.Error, docnode.ErrRouteUnsupported) || !strings.Contains(res.Error.Error(), "match_scope: document") || !strings.Contains(res.Error.Error(), "--allow-large-files") {
		t.Fatalf("error = %v", res.Error)
	}
	if len(sink.records) != 0 {
		t.Fatalf("emitted %d records", len(sink.records))
	}
	sig, ext = jsonProcessConfigs()
	sink = &boundarySink{}
	res = ProcessFileWithApplicabilityToSink(context.Background(), path, sig, ext, nil, nil, true, provenance.RuntimeOptions{}, sink)
	if res.Error != nil || len(sink.records) != 1 {
		t.Fatalf("with --allow-large-files: %v, %d records", res.Error, len(sink.records))
	}
}

func TestJSONStreamingBlockers(t *testing.T) {
	lowerStreamingThreshold(t)
	path := writeTempFile(t, "in.json", `{"r":[{"id":"1"}]}`)
	app := &ApplicabilityConfig{Applicability: ApplicabilityPredicate{Type: "xpath", Expression: "true()"}}
	for _, tc := range []struct {
		name string
		app  *ApplicabilityConfig
		sink RecordSink
		want string
	}{
		{"applicability", app, &boundarySink{}, "applicability predicate"},
		{"buffered output", nil, nil, "output is buffered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "r", Selector: "/r", Weight: 1})
			res := processFileWithProvenance(context.Background(), path, sig, ext, tc.app, nil, false, provenance.RuntimeOptions{}, tc.sink)
			if !errors.Is(res.Error, docnode.ErrRouteUnsupported) || !strings.Contains(res.Error.Error(), tc.want) {
				t.Fatalf("error = %v", res.Error)
			}
		})
	}
}

func TestJSONStreamingScanFaultIsParseError(t *testing.T) {
	lowerStreamingThreshold(t)
	sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "r", Selector: "/r", Weight: 1})
	sink := &boundarySink{}
	path := writeTempFile(t, "in.json", `{"r":[{"id":"1"},{"id":"2","id":"3"}]}`)
	res := ProcessFileWithApplicabilityToSink(context.Background(), path, sig, ext, nil, nil, false, provenance.RuntimeOptions{}, sink)
	if res.Error == nil || !strings.Contains(res.Error.Error(), "failed to parse JSON") || !strings.Contains(res.Error.Error(), `duplicate key "id"`) {
		t.Fatalf("error = %v", res.Error)
	}
	if len(sink.boundaries) != 1 || sink.boundaries[0].DispositionReason != DispositionReasonParseError {
		t.Fatalf("boundaries %+v", sink.boundaries)
	}
}

func ndjsonConfigs() (*FileSignature, *ExtractRecordMatch) {
	sig := &FileSignature{
		SignatureID:         "nd",
		FormatType:          FormatNDJSON,
		MatchScope:          MatchScopeRecord,
		ConfidenceThreshold: 1,
		MatchPatterns:       []MatchPattern{{PatternID: "id", Selector: "/Event/id", Weight: 1}},
	}
	ext := &ExtractRecordMatch{
		RecordType:     "event",
		MatchSelectors: []MatchSelector{{XPath: "Event"}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "id", Type: "string"}},
	}
	return sig, ext
}

func runNDJSON(t *testing.T, src string) (ExtractResult, *boundarySink) {
	t.Helper()
	sig, ext := ndjsonConfigs()
	sink := &boundarySink{}
	res := ProcessFileWithApplicabilityToSink(context.Background(), writeTempFile(t, "in.ndjson", src), sig, ext, nil, nil, false, provenance.RuntimeOptions{}, sink)
	return res, sink
}

func TestNDJSONStreamsEveryInput(t *testing.T) {
	res, sink := runNDJSON(t, "{\"id\":\"a\"}\n\n  \r\n{\"id\":\"b\"}\r\n{\"id\":\"c\"}")
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	if got := projection(res, sink); got != `status:matched|1:{"id":"a"};2:{"id":"b"};3:{"id":"c"};` {
		t.Fatalf("records %s", got)
	}
}

func TestNDJSONFailures(t *testing.T) {
	for _, tc := range []struct {
		name, src, want string
		reason          DispositionReason
	}{
		{"blank only", "\n  \n\r\n", "input contains no JSON value", DispositionReasonParseError},
		{"empty", "", "input contains no JSON value", DispositionReasonParseError},
		{"array line", "{\"id\":\"a\"}\n[1,2]\n", "record 2 at byte offset 11 is not a JSON object", DispositionReasonParseError},
		{"scalar line", "7\n", "record 1 at byte offset 0 is not a JSON object", DispositionReasonParseError},
		{"truncated last line", "{\"id\":\"a\"}\n{\"id\":", "record 2", DispositionReasonParseError},
		{"two values on a line", "{\"id\":\"a\"} {\"id\":\"b\"}\n", "unexpected data after top-level value at byte offset 11", DispositionReasonParseError},
		{"duplicate key", "{\"id\":\"a\"}\n{\"id\":\"b\",\"id\":\"c\"}\n", "duplicate key \"id\" at byte offset 21", DispositionReasonParseError},
		{"signature below threshold", "{\"id\":\"a\"}\n{\"other\":1}\n", "signature mismatch: record 2", DispositionReasonSignatureMismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, sink := runNDJSON(t, tc.src)
			if res.Error == nil || !strings.Contains(res.Error.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", res.Error, tc.want)
			}
			if res.SignatureMatchStatus == SignatureMatchMatched {
				t.Fatal("failed input reports matched")
			}
			if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != DispositionFailed || sink.boundaries[0].DispositionReason != tc.reason {
				t.Fatalf("boundaries %+v", sink.boundaries)
			}
		})
	}
}

func TestNDJSONHasNoWholeDocumentRoute(t *testing.T) {
	format, ok := docnode.Lookup(FormatNDJSON)
	if !ok {
		t.Fatal("ndjson not registered")
	}
	if _, err := format.Parse(strings.NewReader(`{}`)); !errors.Is(err, docnode.ErrRouteUnsupported) {
		t.Fatalf("Parse: %v", err)
	}
}

func TestMatchScopeLoadRules(t *testing.T) {
	app := &ApplicabilityConfig{Applicability: ApplicabilityPredicate{Type: "xpath", Expression: "true()"}}
	oneSel := func(xp string) *ExtractRecordMatch {
		return &ExtractRecordMatch{MatchSelectors: []MatchSelector{{XPath: xp}}, FieldMappings: []FieldMapping{{OutputField: "id", XPath: "id"}}}
	}
	for _, tc := range []struct {
		name   string
		format string
		scope  string
		ext    *ExtractRecordMatch
		app    *ApplicabilityConfig
		want   string
	}{
		{"ndjson without scope", FormatNDJSON, "", oneSel("//r"), nil, `must declare "match_scope: record"`},
		{"ndjson document scope", FormatNDJSON, "document", oneSel("//r"), nil, `must declare "match_scope: record"`},
		{"ndjson applicability", FormatNDJSON, "record", oneSel("//r"), app, "applicability is not supported for ndjson input"},
		{"xml record scope", "", "record", oneSel("//r"), nil, "match_scope: record is not supported for xml input in this release; it is supported for json and ndjson input"},
		{"record scope two selectors", FormatJSON, "record", &ExtractRecordMatch{MatchSelectors: []MatchSelector{{XPath: "//a"}, {XPath: "//b"}}}, nil, "exactly one extract match selector; found 2"},
		{"record scope complex selector", FormatJSON, "record", oneSel("/a/b"), nil, "not yet supported for streaming/index mode"},
		{"unknown scope", FormatJSON, "file", oneSel("//r"), nil, `match_scope "file" is not supported`},
		{"json record ok", FormatJSON, "record", oneSel("//r"), nil, ""},
		{"json document ok", FormatJSON, "document", oneSel("/a/b"), nil, ""},
		{"ndjson record ok", FormatNDJSON, "record", oneSel("r"), nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig := &FileSignature{FormatType: tc.format, MatchScope: tc.scope, MatchPatterns: []MatchPattern{{PatternID: "p", Selector: "/r", Weight: 1}}}
			_, err := ResolveInputFormat("", false, sig, tc.ext, tc.app)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

// TestRunBothRoutesTakesTwoRoutes proves the route harness above: a fault
// after the records reaches the streaming route only after it has emitted
// them, while the whole-document route emits nothing.
func TestRunBothRoutesTakesTwoRoutes(t *testing.T) {
	lowerStreamingThreshold(t)
	sig, ext := recordScopeConfigs("//r", MatchPattern{PatternID: "r", Selector: "/r", Weight: 1})
	path := writeTempFile(t, "in.json", `{"r":[{"id":"1"}]} trailing`)
	domSink, streamSink := &boundarySink{}, &boundarySink{}
	domRes := ProcessFileWithApplicabilityToSink(context.Background(), path, cloneSig(sig), CloneRecordMatch(ext), nil, nil, true, provenance.RuntimeOptions{}, domSink)
	streamRes := ProcessFileWithApplicabilityToSink(context.Background(), path, cloneSig(sig), CloneRecordMatch(ext), nil, nil, false, provenance.RuntimeOptions{}, streamSink)
	if domRes.Error == nil || streamRes.Error == nil {
		t.Fatalf("errors: dom %v, stream %v", domRes.Error, streamRes.Error)
	}
	if len(domSink.records) != 0 || len(streamSink.records) != 1 {
		t.Fatalf("records: dom %d, stream %d", len(domSink.records), len(streamSink.records))
	}
	if streamSink.boundaries[0].Disposition != DispositionFailed {
		t.Fatalf("stream boundary %+v", streamSink.boundaries[0])
	}
}

// TestMatchScopeContentHash pins that a signature without match_scope keeps
// the recipe content hash it had before the field existed, and that declaring
// a scope is a recipe change.
func TestMatchScopeContentHash(t *testing.T) {
	const legacy = "sha256:15e550f49856466afff7c92924d1e119f5f3949b389467813e202cc5b57e99e4" // examples/cases/14 at the release before match_scope
	read := func(p string) []byte {
		b, err := os.ReadFile(p) // #nosec G304 -- test reads repo fixtures.
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	sig := read("../../examples/cases/14-json-basic-extraction/recipe/signature/widget-signature.yaml")
	ext := read("../../examples/cases/14-json-basic-extraction/recipe/extract/widget-extract.yaml")
	hash := func(s []byte) string {
		h, err := provenance.RecipeContentHash(s, ext)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	if got := hash(sig); got != legacy {
		t.Fatalf("legacy recipe hash changed: %s", got)
	}
	document := hash(append(append([]byte{}, sig...), "match_scope: document\n"...))
	record := hash(append(append([]byte{}, sig...), "match_scope: record\n"...))
	if document == legacy || record == legacy || document == record {
		t.Fatalf("scoped hashes: document %s, record %s, legacy %s", document, record, legacy)
	}
}

func TestGeneratedJSONRecipeHoldsOnBothRoutes(t *testing.T) {
	src := `{"data":{"meta":{"n":3},"items":[{"id":"a","v":1},{"id":"b","v":2},{"id":"c","v":3}]}}`
	res, err := configgen.GenerateJSON(strings.NewReader(src), configgen.Options{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	extPath := filepath.Join(dir, "gen.yaml")
	if err := os.WriteFile(extPath, res.YAML, 0o600); err != nil {
		t.Fatal(err)
	}
	sigPath := filepath.Join(dir, "gen-signature.yaml")
	if err := os.WriteFile(sigPath, signatureBlock(t, string(res.YAML)), 0o600); err != nil {
		t.Fatal(err)
	}
	sig, err := LoadSignatureConfig(sigPath)
	if err != nil {
		t.Fatalf("generated signature does not load: %v", err)
	}
	ext, err := LoadExtractConfig(extPath)
	if err != nil {
		t.Fatalf("generated config does not load: %v", err)
	}
	if _, err := ResolveInputFormat("", false, sig, ext, nil); err != nil {
		t.Fatalf("generated recipe rejected: %v", err)
	}
	if !isRecordScope(sig) {
		t.Fatalf("generated signature is not record-scoped")
	}
	dom, stream, domRes, _ := runBothRoutes(t, src, sig, ext)
	if domRes.Error != nil {
		t.Fatalf("generated recipe did not admit its own sample: %v", domRes.Error)
	}
	if dom != stream || !strings.Contains(dom, "3:") {
		t.Fatalf("routes disagree:\nDOM    %s\nstream %s", dom, stream)
	}
}

// signatureBlock returns the commented signature block of a generated config,
// uncommented.
func signatureBlock(t *testing.T, config string) []byte {
	t.Helper()
	start := strings.Index(config, "# --- signature ---\n")
	end := strings.Index(config, "# --- end signature ---\n")
	if start < 0 || end < start {
		t.Fatalf("no signature block:\n%s", config)
	}
	var b strings.Builder
	for _, line := range strings.Split(config[start+len("# --- signature ---\n"):end], "\n") {
		b.WriteString(strings.TrimPrefix(line, "# "))
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// TestStreamingEntryRefusesUnscopedJSON calls the exported streaming entry
// points directly: a json or ndjson input whose signature is not
// record-scoped is refused before the input is read, so a signature that the
// whole-document route would reject can never admit records here.
func TestStreamingEntryRefusesUnscopedJSON(t *testing.T) {
	ndSig, ndExt := ndjsonConfigs()
	ndSig.MatchScope = ""
	docSig := &FileSignature{
		SignatureID:         "doc",
		FormatType:          FormatJSON,
		ConfidenceThreshold: 1,
		MatchPatterns:       []MatchPattern{{PatternID: "never", Selector: "/NoSuchRoot", Weight: 1}},
	}
	docExt := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//r"}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "id", Type: "string"}},
	}
	twoSig, twoExt := recordScopeConfigs("//r", MatchPattern{PatternID: "r", Selector: "/r", Weight: 1})
	twoExt.MatchSelectors = append(twoExt.MatchSelectors, MatchSelector{XPath: "//s"})
	for _, tc := range []struct {
		name   string
		file   string
		src    string
		sig    *FileSignature
		ext    *ExtractRecordMatch
		want   string
		reason DispositionReason
	}{
		{"json document scope", "in.json", `{"r":[{"id":"1"},{"id":"2"}]}`, docSig, docExt, "match_scope: document", DispositionReasonRouteUnsupported},
		{"ndjson without scope", "in.ndjson", "{\"id\":\"1\"}\n", ndSig, ndExt, `must declare "match_scope: record"`, DispositionReasonValidationError},
		{"record scope with two selectors", "in.json", `{"r":[{"id":"1"}]}`, twoSig, twoExt, "exactly one extract match selector", DispositionReasonValidationError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempFile(t, tc.file, tc.src)
			sink := &boundarySink{}
			res := ProcessFileStreamingToSink(context.Background(), path, tc.sig, tc.ext, nil, provenance.RuntimeOptions{}, sink)
			if res.Error == nil || !strings.Contains(res.Error.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", res.Error, tc.want)
			}
			if tc.reason == DispositionReasonRouteUnsupported && !errors.Is(res.Error, docnode.ErrRouteUnsupported) {
				t.Fatalf("error %v is not route_unsupported", res.Error)
			}
			if len(sink.records) != 0 || res.SignatureMatchStatus == SignatureMatchMatched {
				t.Fatalf("emitted %d records, status %s", len(sink.records), res.SignatureMatchStatus)
			}
			if len(sink.boundaries) != 1 || sink.boundaries[0].Disposition != DispositionFailed || sink.boundaries[0].DispositionReason != tc.reason {
				t.Fatalf("boundaries %+v", sink.boundaries)
			}
			buffered := ProcessFileStreaming(path, tc.sig, tc.ext, nil)
			if buffered.Error == nil || len(buffered.Records) != 0 {
				t.Fatalf("ProcessFileStreaming: %v, %d records", buffered.Error, len(buffered.Records))
			}
		})
	}
}
