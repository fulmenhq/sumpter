package extract

import (
	"errors"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

func jsonTestSignature(formatType string) *FileSignature {
	return &FileSignature{
		SignatureID:   "t",
		FormatType:    formatType,
		MatchPatterns: []MatchPattern{{PatternID: "root", Selector: "/WidgetCoData", Weight: 1}},
	}
}

func jsonTestExtract(fieldXPath string) *ExtractRecordMatch {
	return &ExtractRecordMatch{
		RecordType:     "order",
		MatchSelectors: []MatchSelector{{XPath: "//Order"}},
		FieldMappings:  []FieldMapping{{OutputField: "id", XPath: fieldXPath, Type: "string"}},
	}
}

func TestNormalizeInputFormat(t *testing.T) {
	for _, tc := range []struct {
		in, want, errPart string
	}{
		{in: "", want: FormatXML},
		{in: "xml", want: FormatXML},
		{in: " JSON ", want: FormatJSON},
		{in: "ndjson", errPart: "arrives in a later release"},
		{in: "protobuf", errPart: "reserved, not implemented"},
		{in: "yaml", errPart: "unknown input format"},
	} {
		got, err := NormalizeInputFormat(tc.in)
		if tc.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Errorf("NormalizeInputFormat(%q) error = %v, want containing %q", tc.in, err, tc.errPart)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("NormalizeInputFormat(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestResolveInputFormat(t *testing.T) {
	for _, tc := range []struct {
		name         string
		recipeFormat string
		recipeMode   bool
		sigFormat    string
		want         string
		errPart      string
	}{
		{name: "non-recipe default", want: FormatXML},
		{name: "non-recipe json signature", sigFormat: "json", want: FormatJSON},
		{name: "recipe xml, signature omitted", recipeMode: true, want: FormatXML},
		{name: "recipe json, signature json", recipeFormat: "json", recipeMode: true, sigFormat: "json", want: FormatJSON},
		{name: "recipe json, signature omitted", recipeFormat: "json", recipeMode: true,
			errPart: `signature format_type defaults to xml: add "format_type: json" to the signature`},
		{name: "recipe xml, signature json", recipeFormat: "xml", recipeMode: true, sigFormat: "json",
			errPart: `does not match signature format_type "json"`},
		{name: "signature protobuf", sigFormat: "protobuf", errPart: "reserved, not implemented"},
		{name: "recipe ndjson", recipeFormat: "ndjson", recipeMode: true, errPart: "arrives in a later release"},
		{name: "signature unknown", sigFormat: "csv", errPart: "unknown input format"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig := jsonTestSignature(tc.sigFormat)
			got, err := ResolveInputFormat(tc.recipeFormat, tc.recipeMode, sig, jsonTestExtract("id"), nil)
			if tc.errPart != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errPart) {
					t.Fatalf("error = %v, want containing %q", err, tc.errPart)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if sig.FormatType != tc.want {
				t.Fatalf("sig.FormatType = %q, want %q", sig.FormatType, tc.want)
			}
		})
	}
}

func TestResolveInputFormatJSONTotality(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(*FileSignature, *ExtractRecordMatch, *ApplicabilityConfig)
		errPart string
	}{
		{name: "field attribute axis", mutate: func(_ *FileSignature, e *ExtractRecordMatch, _ *ApplicabilityConfig) {
			e.FieldMappings[0].XPath = "@id"
		}, errPart: `xpath "@id"`},
		{name: "attribute:: axis", mutate: func(_ *FileSignature, e *ExtractRecordMatch, _ *ApplicabilityConfig) {
			e.FieldMappings[0].XPath = "attribute::id"
		}, errPart: `"attribute::id"`},
		{name: "match selector predicate", mutate: func(_ *FileSignature, e *ExtractRecordMatch, _ *ApplicabilityConfig) {
			e.MatchSelectors[0].XPath = "//Order[@type='return']"
		}, errPart: "match selector 1"},
		{name: "signature selector", mutate: func(s *FileSignature, _ *ExtractRecordMatch, _ *ApplicabilityConfig) {
			s.MatchPatterns[0].Selector = "/WidgetCoData[@date]"
		}, errPart: "signature selector"},
		{name: "applicability", mutate: func(_ *FileSignature, _ *ExtractRecordMatch, a *ApplicabilityConfig) {
			a.Applicability = ApplicabilityPredicate{Type: "xpath", Expression: "count(//Order[@x]) > 0"}
		}, errPart: "applicability expression"},
		{name: "item mapping", mutate: func(_ *FileSignature, e *ExtractRecordMatch, _ *ApplicabilityConfig) {
			e.FieldMappings[0].Type = "array"
			e.FieldMappings[0].ItemMapping = []FieldMapping{{OutputField: "sku", XPath: "@sku"}}
		}, errPart: `item mapping "sku"`},
		{name: "polymorphic mapping", mutate: func(_ *FileSignature, e *ExtractRecordMatch, _ *ApplicabilityConfig) {
			e.FieldMappings[0].Type = "array"
			e.FieldMappings[0].Polymorphic = []PolymorphicMapping{{ElementType: "OrderLine", FieldMappings: []FieldMapping{{OutputField: "sku", XPath: "@sku"}}}}
		}, errPart: `polymorphic mapping "sku"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sig, ext, app := jsonTestSignature("json"), jsonTestExtract("id"), &ApplicabilityConfig{}
			tc.mutate(sig, ext, app)
			_, err := ResolveInputFormat("json", true, sig, ext, app)
			if err == nil {
				t.Fatal("expected a load error")
			}
			if !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("error %q does not name %q", err, tc.errPart)
			}
			if strings.Contains(tc.name, "attribute") || strings.Contains(tc.name, "field") ||
				strings.Contains(tc.name, "selector") || strings.Contains(tc.name, "mapping") || tc.name == "applicability" {
				if !strings.Contains(err.Error(), "JSON has no attributes: use k instead of @k") {
					t.Fatalf("error %q lacks the fix sentence", err)
				}
			}
		})
	}
}

func TestResolveInputFormatJSONNamespaces(t *testing.T) {
	sig := jsonTestSignature("json")
	sig.Namespaces = map[string]string{"w": "urn:w"}
	if _, err := ResolveInputFormat("json", true, sig, jsonTestExtract("id"), nil); err == nil || !strings.Contains(err.Error(), "signature namespaces") {
		t.Fatalf("signature namespaces error = %v", err)
	}
	ext := jsonTestExtract("id")
	ext.Namespaces = map[string]string{"w": "urn:w"}
	if _, err := ResolveInputFormat("json", true, jsonTestSignature("json"), ext, nil); err == nil || !strings.Contains(err.Error(), "extract namespaces") {
		t.Fatalf("extract namespaces error = %v", err)
	}
	for _, expr := range []string{"w:Order", "namespace::w", "//w:*"} {
		_, err := ResolveInputFormat("json", true, jsonTestSignature("json"), jsonTestExtract(expr), nil)
		if err == nil || !strings.Contains(err.Error(), "json input has no namespaces") {
			t.Errorf("%q: error = %v", expr, err)
		}
	}
}

func TestResolveInputFormatXMLAllowsAttributes(t *testing.T) {
	ext := jsonTestExtract("@id")
	ext.MatchSelectors[0].XPath = "//Order[@type='return']"
	if _, err := ResolveInputFormat("", true, jsonTestSignature(""), ext, nil); err != nil {
		t.Fatalf("xml recipe rejected: %v", err)
	}
}

func TestXPathAxisViolations(t *testing.T) {
	for _, tc := range []struct {
		expr  string
		kinds []axisKind
		texts []string
	}{
		{expr: "Customer/text()"},
		{expr: "//Order[type='return']/id"},
		{expr: "local-name() = 'x' and starts-with(name, 'a')"},
		{expr: "ancestor-or-self::Order/child::id"},
		{expr: `concat("@id", '@x', "a:b")`},
		{expr: "count(//Record) div 2"},
		{expr: "@id", kinds: []axisKind{axisAttribute}, texts: []string{"@id"}},
		{expr: "Lines/OrderLine/@sku", kinds: []axisKind{axisAttribute}, texts: []string{"@sku"}},
		{expr: "@*", kinds: []axisKind{axisAttribute}, texts: []string{"@*"}},
		{expr: "attribute::id", kinds: []axisKind{axisAttribute}, texts: []string{"attribute::"}},
		{expr: "namespace::*", kinds: []axisKind{axisNamespace}, texts: []string{"namespace::"}},
		{expr: "w:Order/w:id", kinds: []axisKind{axisPrefixedName, axisPrefixedName}, texts: []string{"w:Order", "w:id"}},
		{expr: "//x[@a='1' and @b]", kinds: []axisKind{axisAttribute, axisAttribute}, texts: []string{"@a", "@b"}},
	} {
		got := xpathAxisViolations(tc.expr)
		if len(got) != len(tc.kinds) {
			t.Errorf("%q: got %d violations %+v, want %d", tc.expr, len(got), got, len(tc.kinds))
			continue
		}
		for i, v := range got {
			if v.Kind != tc.kinds[i] || v.Text != tc.texts[i] {
				t.Errorf("%q[%d] = %+v, want kind %d text %q", tc.expr, i, v, tc.kinds[i], tc.texts[i])
			}
			if tc.expr[v.Start:v.End] != v.Text {
				t.Errorf("%q[%d]: span %d:%d = %q, want %q", tc.expr, i, v.Start, v.End, tc.expr[v.Start:v.End], v.Text)
			}
		}
	}
}

func TestInputFormatLookup(t *testing.T) {
	for _, tc := range []struct {
		sig  *FileSignature
		want string
	}{
		{sig: nil, want: "xml"},
		{sig: &FileSignature{}, want: "xml"},
		{sig: &FileSignature{FormatType: "json"}, want: "json"},
	} {
		f, err := inputFormat(tc.sig)
		if err != nil || f.Token() != tc.want {
			t.Errorf("inputFormat(%+v) = %v, %v; want %s", tc.sig, f, err, tc.want)
		}
	}
	if _, err := inputFormat(&FileSignature{FormatType: "protobuf"}); err == nil {
		t.Error("protobuf resolved to a format")
	}
}

func TestJSONStreamingUnsupportedIsRouteError(t *testing.T) {
	if !errors.Is(errJSONStreamingUnsupported, docnode.ErrRouteUnsupported) {
		t.Fatal("errJSONStreamingUnsupported does not wrap docnode.ErrRouteUnsupported")
	}
	if !strings.Contains(errJSONStreamingUnsupported.Error(), "use --allow-large-files for whole-document parsing") {
		t.Fatalf("message = %q", errJSONStreamingUnsupported)
	}
}
