package extract

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
)

const (
	nsCoreURI = "urn:example:sumpter-records"
	nsExtURI  = "urn:example:sumpter-records-ext"
)

func fixtureDoc(t *testing.T, name string) *xmlquery.Node {
	t.Helper()
	path := filepath.Join("..", "..", "tests", "fixtures", "namespace-conformance", name)
	data, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	doc, err := xmlquery.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	return doc
}

func coreExtractConfig() *ExtractRecordMatch {
	return &ExtractRecordMatch{
		RecordType:     "ledger_record",
		Namespaces:     map[string]string{"n": nsCoreURI},
		MatchSelectors: []MatchSelector{{XPath: "//n:Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "record_id", XPath: "@id", Type: "string"},
			{OutputField: "label", XPath: "n:Label", Type: "string"},
			{OutputField: "amount", XPath: "n:Amount", Type: "number"},
			{OutputField: "posted_date", XPath: "n:PostedDate", Type: "string"},
		},
	}
}

// TestNamespaceBindingTrioCoreEquivalence is the whole-document slice of the
// namespace-conformance oracle: one URI-bound core recipe extracts identical
// core records from the fully-prefixed, default-namespace, and dual
// serializations.
func TestNamespaceBindingTrioCoreEquivalence(t *testing.T) {
	shapes := []string{"prefixed.xml", "default-ns.xml", "dual.xml"}
	var baseline []map[string]interface{}
	for _, shape := range shapes {
		cfg := coreExtractConfig()
		if err := prepareExtractConfig(cfg); err != nil {
			t.Fatalf("%s: prepare: %v", shape, err)
		}
		records, err := extractRecords(fixtureDoc(t, shape), cfg, nil)
		if err != nil {
			t.Fatalf("%s: extract: %v", shape, err)
		}
		if len(records) != 2 {
			t.Fatalf("%s: expected 2 core records, got %d: %v", shape, len(records), records)
		}
		if baseline == nil {
			baseline = records
			continue
		}
		if !reflect.DeepEqual(records, baseline) {
			t.Errorf("%s: core records differ from baseline\n got: %v\nwant: %v", shape, records, baseline)
		}
	}
	// Sanity on the actual values so the equivalence is not "identically wrong".
	if got := baseline[0]["record_id"]; got != "R-0001" {
		t.Errorf("record_id = %v, want R-0001", got)
	}
	if got := baseline[0]["amount"]; got != 42.5 {
		t.Errorf("amount = %v, want 42.5", got)
	}
}

// TestNamespaceBindingDualDisambiguation proves same-local-name-in-two-URIs
// disambiguation and extension attribute binding on the dual fixture.
func TestNamespaceBindingDualDisambiguation(t *testing.T) {
	// //n:Record (core) selects the two core records, not the ext:Record ones.
	coreCfg := coreExtractConfig()
	if err := prepareExtractConfig(coreCfg); err != nil {
		t.Fatalf("core prepare: %v", err)
	}
	coreRecords, err := extractRecords(fixtureDoc(t, "dual.xml"), coreCfg, nil)
	if err != nil {
		t.Fatalf("core extract: %v", err)
	}
	if len(coreRecords) != 2 || coreRecords[0]["record_id"] != "R-0001" {
		t.Fatalf("core //n:Record selected wrong nodes: %v", coreRecords)
	}

	// //ext:Record (extension) selects the extension records — different nodes,
	// same local name.
	extCfg := &ExtractRecordMatch{
		RecordType:     "ext_record",
		Namespaces:     map[string]string{"ext": nsExtURI},
		MatchSelectors: []MatchSelector{{XPath: "//ext:Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "ext_id", XPath: "@id", Type: "string"},
		},
	}
	if err := prepareExtractConfig(extCfg); err != nil {
		t.Fatalf("ext prepare: %v", err)
	}
	extRecords, err := extractRecords(fixtureDoc(t, "dual.xml"), extCfg, nil)
	if err != nil {
		t.Fatalf("ext extract: %v", err)
	}
	if len(extRecords) != 2 || extRecords[0]["ext_id"] != "X-0001" {
		t.Fatalf("ext //ext:Record selected wrong nodes: %v", extRecords)
	}

	// Extension attribute reads by URI (@ext:origin).
	attrCfg := &ExtractRecordMatch{
		RecordType:     "ledger_record",
		Namespaces:     map[string]string{"n": nsCoreURI, "ext": nsExtURI},
		MatchSelectors: []MatchSelector{{XPath: "//n:Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "origin", XPath: "@ext:origin", Type: "string"},
		},
	}
	if err := prepareExtractConfig(attrCfg); err != nil {
		t.Fatalf("attr prepare: %v", err)
	}
	attrRecords, err := extractRecords(fixtureDoc(t, "dual.xml"), attrCfg, nil)
	if err != nil {
		t.Fatalf("attr extract: %v", err)
	}
	if len(attrRecords) != 2 || attrRecords[0]["origin"] != "import" {
		t.Fatalf("@ext:origin did not resolve by URI: %v", attrRecords)
	}
}

// TestNamespacePrefixShadowingScopingFidelity proves in-scope namespace capture:
// a core-URI-bound test matches the outer p:Label (core) but not the inner
// p:Label, which the fixture rebinds to the extension URI under the same literal
// prefix p.
func TestNamespacePrefixShadowingScopingFidelity(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "shadow",
		Namespaces:     map[string]string{"n": nsCoreURI},
		MatchSelectors: []MatchSelector{{XPath: "//n:Label"}},
		FieldMappings: []FieldMapping{
			{OutputField: "text", XPath: ".", Type: "string"},
		},
	}
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	path := filepath.Join("..", "..", "tests", "fixtures", "namespace-conformance", "adversarial", "prefix-shadowing.xml")
	data, err := os.ReadFile(path) // #nosec G304 - test fixture path
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	doc, err := xmlquery.Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 || records[0]["text"] != "outer-core" {
		t.Fatalf("core-bound //n:Label should match only the outer (core) label, got: %v", records)
	}
}

// TestNamespaceUnboundPrefixFailsClosed asserts fail-closed at load with a
// diagnostic naming the offending XPath and prefix.
func TestNamespaceUnboundPrefixFailsClosed(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "ledger_record",
		Namespaces:     map[string]string{"n": nsCoreURI},
		MatchSelectors: []MatchSelector{{XPath: "//bad:Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "record_id", XPath: "@id", Type: "string"},
		},
	}
	err := prepareExtractConfig(cfg)
	if err == nil {
		t.Fatal("expected fail-closed error for unbound prefix, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "//bad:Record") || !strings.Contains(msg, "bad") {
		t.Errorf("diagnostic should name the offending XPath and prefix, got: %s", msg)
	}
}

// TestNamespaceMapAbsentByteCompat confirms the no-map path is untouched: lenient
// matching still selects records without a namespaces map.
func TestNamespaceMapAbsentByteCompat(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "ledger_record",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "record_id", XPath: "@id", Type: "string"},
		},
	}
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	records, err := extractRecords(fixtureDoc(t, "default-ns.xml"), cfg, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("map-absent lenient matching should still select 2 records, got %d", len(records))
	}
}

// TestSignatureNamespaceBinding proves signature selectors bind by URI at load
// and fail closed on an unbound prefix under a map.
func TestSignatureNamespaceBinding(t *testing.T) {
	sig := &FileSignature{
		SignatureID:         "sumpter-records-ledger",
		Name:                "Sumpter Records Ledger",
		Namespaces:          map[string]string{"n": nsCoreURI},
		MatchPatterns:       []MatchPattern{{PatternID: "root", Name: "Ledger root", Selector: "/n:Ledger", Weight: 1}},
		ConfidenceThreshold: 1,
	}
	if err := prepareSignatureConfig(sig); err != nil {
		t.Fatalf("prepare signature: %v", err)
	}
	// /n:Ledger (core URI) matches the default-namespace document.
	matched, _, err := matchesSignature(fixtureDoc(t, "default-ns.xml"), sig)
	if err != nil {
		t.Fatalf("matchesSignature: %v", err)
	}
	if !matched {
		t.Error("URI-bound /n:Ledger should match the default-namespace document")
	}

	// Unbound prefix under a map fails closed at load.
	bad := &FileSignature{
		SignatureID:   "bad",
		Name:          "bad",
		Namespaces:    map[string]string{"n": nsCoreURI},
		MatchPatterns: []MatchPattern{{PatternID: "root", Name: "root", Selector: "/nope:Ledger", Weight: 1}},
	}
	if err := prepareSignatureConfig(bad); err == nil {
		t.Error("expected fail-closed error for unbound signature prefix")
	}
}

func TestValidateNamespaceMap(t *testing.T) {
	cases := []struct {
		name    string
		nsMap   map[string]string
		wantErr bool
	}{
		{"nil ok", nil, false},
		{"valid", map[string]string{"n": nsCoreURI, "ext": nsExtURI}, false},
		{"empty alias", map[string]string{"": nsCoreURI}, true},
		{"whitespace alias", map[string]string{" n": nsCoreURI}, true},
		{"colon alias", map[string]string{"n:x": nsCoreURI}, true},
		{"reserved xml", map[string]string{"xml": nsCoreURI}, true},
		{"reserved xmlns", map[string]string{"xmlns": nsCoreURI}, true},
		{"empty uri", map[string]string{"n": "  "}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateNamespaceMap("extract", tc.nsMap)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateNamespaceMap(%v) err=%v, wantErr=%v", tc.nsMap, err, tc.wantErr)
			}
		})
	}
}

func TestBareNameTests(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{"//Record", []string{"Record"}},
		{"//n:Record", nil},
		{"/Ledger/Record", []string{"Ledger", "Record"}},
		{"@id", nil},
		{"n:Label", nil},
		{"Label", []string{"Label"}},
		{"count(//Record)", []string{"Record"}},
		{"local-name()", nil},
		{"//*", nil},
		{"text()", nil},
		{"@ext:origin", nil},
		{".", nil},
	}
	for _, tc := range cases {
		t.Run(tc.expr, func(t *testing.T) {
			got := bareNameTests(tc.expr)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("bareNameTests(%q) = %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

// TestNamespaceSchemaParity confirms the extract schema agrees with the runtime
// contract: an empty map is accepted (equivalent to omission) and the reserved
// aliases xml/xmlns are rejected at the schema layer, not only by the loader.
func TestNamespaceSchemaParity(t *testing.T) {
	validator, err := getExtractSchemaValidator()
	if err != nil {
		t.Fatalf("validator: %v", err)
	}
	base := func(ns string) []byte {
		return []byte("record_type: r\n" + ns +
			"match_selectors:\n  - xpath: \"//Record\"\n" +
			"field_mappings:\n  - output_field: id\n    xpath: \"@id\"\n    type: string\n" +
			"output_schema:\n  type: object\n  properties:\n    id:\n      type: string\n  required: [id]\n")
	}
	cases := []struct {
		name  string
		ns    string
		valid bool
	}{
		{"no map", "", true},
		{"empty map", "namespaces: {}\n", true},
		{"valid alias", "namespaces:\n  n: \"urn:example:sumpter-records\"\n", true},
		{"reserved xml", "namespaces:\n  xml: \"urn:x\"\n", false},
		{"reserved xmlns", "namespaces:\n  xmlns: \"urn:x\"\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := validator.ValidateExtractConfig(base(tc.ns), "test.yaml")
			if err != nil {
				t.Fatalf("validate: %v", err)
			}
			if res.IsValid() != tc.valid {
				t.Errorf("IsValid()=%v want %v (errors: %s)", res.IsValid(), tc.valid, res.ErrorSummary())
			}
		})
	}
}

// TestNamespaceURINotDereferenced is the secrev structural guard: namespace URIs
// are inert compile-time match keys. (1) Behaviorally, an SSRF-shaped URI used as
// a namespace binding matches by string equality with no resolution. (2)
// Structurally, the namespace code imports no network package, foreclosing a
// future "resolve the schema at the namespace URI" regression.
func TestNamespaceURINotDereferenced(t *testing.T) {
	const ssrfURI = "http://169.254.169.254/latest/meta-data"
	xml := `<Ledger xmlns:s="` + ssrfURI + `"><s:Record id="R-1"/></Ledger>`
	doc, err := xmlquery.Parse(strings.NewReader(xml))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg := &ExtractRecordMatch{
		RecordType:     "r",
		Namespaces:     map[string]string{"s": ssrfURI},
		MatchSelectors: []MatchSelector{{XPath: "//s:Record"}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: "@id", Type: "string"}},
	}
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	if len(records) != 1 || records[0]["id"] != "R-1" {
		t.Fatalf("URI should be an opaque match key (equality match), got: %v", records)
	}

	// Structural: the namespace source imports no networking package.
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "namespaces.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse namespaces.go: %v", err)
	}
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "net/http" || path == "net/url" || path == "net" {
			t.Errorf("namespaces.go must not import %q — namespace URIs are never dereferenced", path)
		}
	}
}
