package extract

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

const xpathStringRegexConformanceXML = `<?xml version="1.0" encoding="UTF-8"?>
<Catalog>
  <Record id="REC-001" flag="active">
    <SKU>  WGT-2026-ALPHA  </SKU>
    <RawCode>ab-12-cd</RawCode>
    <Price>42.50</Price>
    <Qty>3</Qty>
    <Tag>primary</Tag>
    <EmptyNode></EmptyNode>
    <Items>
      <Item code="ITM-01">  First   Line  </Item>
      <Item code="ITM-02">Second-Line</Item>
    </Items>
  </Record>
</Catalog>
`

// TestXPathStringAndRegexConformanceThreeRoutes verifies the promised XPath
// string and regular-expression function contract across DOM, streaming, and
// worker/indexed evaluation routes, including nested item_mapping.
func TestXPathStringAndRegexConformanceThreeRoutes(t *testing.T) {
	tmp := t.TempDir()
	xmlPath := writeXPathSumMultiplyFixture(t, tmp, "string-regex.xml", xpathStringRegexConformanceXML)

	mappings := []FieldMapping{
		{OutputField: "id_str", XPath: "string(@id)", Type: "string"},
		{OutputField: "concat_id_tag", XPath: "concat(@id, ':', Tag)", Type: "string"},
		{OutputField: "starts_rec", XPath: "starts-with(@id, 'REC-')", Type: "boolean"},
		{OutputField: "ends_alpha", XPath: "ends-with(normalize-space(SKU), '-ALPHA')", Type: "boolean"},
		{OutputField: "contains_year", XPath: "contains(SKU, '2026')", Type: "boolean"},
		{OutputField: "before_dash", XPath: "substring-before(normalize-space(SKU), '-')", Type: "string"},
		{OutputField: "after_dash", XPath: "substring-after(normalize-space(SKU), '-')", Type: "string"},
		{OutputField: "sub_2arg", XPath: "substring(normalize-space(SKU), 5)", Type: "string"},
		{OutputField: "sub_3arg", XPath: "substring(normalize-space(SKU), 5, 4)", Type: "string"},
		{OutputField: "len_norm_sku", XPath: "string-length(normalize-space(SKU))", Type: "integer"},
		{OutputField: "norm_sku", XPath: "normalize-space(SKU)", Type: "string"},
		{OutputField: "translate_code", XPath: "translate(RawCode, 'abcd-', 'ABCD_')", Type: "string"},
		{OutputField: "lower_sku", XPath: "lower-case(normalize-space(SKU))", Type: "string"},
		{OutputField: "num_price", XPath: "number(Price)", Type: "number"},
		{OutputField: "bool_present", XPath: "boolean(Tag)", Type: "boolean"},
		{OutputField: "bool_absent", XPath: "boolean(MissingNode)", Type: "boolean"},
		{OutputField: "bool_empty_elem", XPath: "boolean(EmptyNode)", Type: "boolean"},
		{OutputField: "bool_empty_str", XPath: "boolean(string(EmptyNode))", Type: "boolean"},
		{OutputField: "matches_sku", XPath: "matches(normalize-space(SKU), '^WGT-[0-9]{4}-[A-Z]+$')", Type: "boolean"},
		{OutputField: "matches_icase", XPath: "matches(normalize-space(SKU), '(?i)^wgt-[0-9]{4}-alpha$')", Type: "boolean"},
		{OutputField: "replace_punct", XPath: "replace(RawCode, '-', '_')", Type: "string"},
		{OutputField: "replace_group", XPath: "replace(normalize-space(SKU), '^WGT-([0-9]{4})-([A-Z]+)$', '$2/$1')", Type: "string"},
		{OutputField: "replace_braced", XPath: "replace(normalize-space(SKU), '^WGT-(?P<yr>[0-9]{4})-(?P<tag>[A-Z]+)$', '${tag}:${yr}')", Type: "string"},
		{OutputField: "replace_dollar", XPath: "replace(Price, '^([0-9]+\\.[0-9]{2})$', '$$USD-$1')", Type: "string"},
		{
			OutputField: "items",
			XPath:       "Items/Item",
			Type:        "array",
			ItemMapping: []FieldMapping{
				{OutputField: "code_num", XPath: "replace(@code, '^ITM-', '')", Type: "string"},
				{OutputField: "norm_self", XPath: "normalize-space()", Type: "string"},
				{OutputField: "lower_norm", XPath: "lower-case(normalize-space(.))", Type: "string"},
				{OutputField: "is_itm", XPath: "matches(@code, '^ITM-[0-9]{2}$')", Type: "boolean"},
			},
		},
	}

	sig := &FileSignature{
		SignatureID:         "xpath-string-regex-sig",
		ConfidenceThreshold: 0.5,
		MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/Catalog", Weight: 1.0}},
	}

	want := map[string]interface{}{
		"id_str":          "REC-001",
		"concat_id_tag":   "REC-001:primary",
		"starts_rec":      true,
		"ends_alpha":      true,
		"contains_year":   true,
		"before_dash":     "WGT",
		"after_dash":      "2026-ALPHA",
		"sub_2arg":        "2026-ALPHA",
		"sub_3arg":        "2026",
		"len_norm_sku":    int64(14),
		"norm_sku":        "WGT-2026-ALPHA",
		"translate_code":  "AB_12_CD",
		"lower_sku":       "wgt-2026-alpha",
		"num_price":       float64(42.5),
		"bool_present":    true,
		"bool_absent":     false,
		"bool_empty_elem": true,
		"bool_empty_str":  false,
		"matches_sku":     true,
		"matches_icase":   true,
		"replace_punct":   "ab_12_cd",
		"replace_group":   "ALPHA/2026",
		"replace_braced":  "ALPHA:2026",
		"replace_dollar":  "$USD-42.50",
		"items": []map[string]interface{}{
			{
				"code_num":   "01",
				"norm_self":  "First Line",
				"lower_norm": "first line",
				"is_itm":     true,
			},
			{
				"code_num":   "02",
				"norm_self":  "Second-Line",
				"lower_norm": "second-line",
				"is_itm":     true,
			},
		},
	}

	// 1. DOM route
	domCfg := preparedFieldConfig(t, mappings, "//Record", nil)
	domRes := ProcessFile(xmlPath, sig, domCfg, nil, false)
	if domRes.Error != nil {
		t.Fatalf("DOM ProcessFile: %v", domRes.Error)
	}
	assertUnionRouteRecords(t, "DOM", domRes.Records, want)

	// 2. Streaming route
	streamCfg := preparedFieldConfig(t, mappings, "//Record", nil)
	streamRes := ProcessFileStreaming(xmlPath, sig, streamCfg, nil)
	if streamRes.Error != nil {
		t.Fatalf("Streaming ProcessFileStreaming: %v", streamRes.Error)
	}
	assertUnionRouteRecords(t, "Streaming", streamRes.Records, want)

	sinkCfg := preparedFieldConfig(t, mappings, "//Record", nil)
	sink := &recordCollectingSink{}
	sinkRes := ProcessFileStreamingToSink(context.Background(), xmlPath, sig, sinkCfg, nil, provenance.RuntimeOptions{}, sink)
	if sinkRes.Error != nil {
		t.Fatalf("StreamingToSink: %v", sinkRes.Error)
	}
	assertUnionRouteRecords(t, "StreamingToSink", sink.Records(), want)

	// 3. Worker / indexed field evaluation route
	doc, err := parseXMLDoc(strings.NewReader(xpathStringRegexConformanceXML))
	if err != nil {
		t.Fatalf("parseXMLDoc: %v", err)
	}
	workerCfg := preparedFieldConfig(t, mappings, ".", nil)
	n := findNode(doc, "//Record[1]")
	if n == nil {
		t.Fatal("findNode //Record[1] returned nil")
	}
	fields, err := ExtractFieldsWithExternal(subDoc(doc, n), workerCfg, nil)
	if err != nil {
		t.Fatalf("worker ExtractFieldsWithExternal: %v", err)
	}
	if !reflect.DeepEqual(fields, want) {
		t.Fatalf("worker fields mismatch:\ngot:  %#v\nwant: %#v", fields, want)
	}
}

// TestXPathStringAndRegexLoadTimeAdmission verifies that invalid regexes,
// non-literal pattern/replacement arguments, XPath 2.0 flag arities, and
// invalid replacement group templates are rejected at config preparation time.
func TestXPathStringAndRegexLoadTimeAdmission(t *testing.T) {
	cases := []struct {
		name        string
		xpath       string
		wantSubstr  string
		inItemArray bool
	}{
		{
			name:       "matches invalid regex",
			xpath:      "matches(SKU, '[unclosed')",
			wantSubstr: `xpath matches() invalid regular expression "[unclosed"`,
		},
		{
			name:       "matches 3 args XPath 2.0 flags rejected",
			xpath:      "matches(SKU, '^WGT-', 'i')",
			wantSubstr: "xpath matches() requires 2 arguments (input, pattern), got 3",
		},
		{
			name:       "matches 1 arg rejected",
			xpath:      "matches(SKU)",
			wantSubstr: "xpath matches() requires 2 arguments (input, pattern), got 1",
		},
		{
			name:       "matches non-literal pattern path rejected",
			xpath:      "matches(SKU, PatternNode)",
			wantSubstr: "xpath matches() pattern (argument 2) must be a string literal",
		},
		{
			name:       "matches non-literal pattern concat rejected",
			xpath:      "matches(SKU, concat('^', Prefix))",
			wantSubstr: "xpath matches() pattern (argument 2) must be a string literal",
		},
		{
			name:       "matches numeric literal pattern rejected without panic",
			xpath:      "matches(SKU, 123)",
			wantSubstr: "xpath matches() pattern (argument 2) must be a string literal",
		},
		{
			name:       "replace invalid regex",
			xpath:      "replace(SKU, '(unclosed', 'x')",
			wantSubstr: `xpath replace() invalid regular expression "(unclosed"`,
		},
		{
			name:       "replace 4 args XPath 2.0 flags rejected",
			xpath:      "replace(SKU, '-', '_', 'i')",
			wantSubstr: "xpath replace() requires 3 arguments (input, pattern, replacement), got 4",
		},
		{
			name:       "replace non-literal pattern rejected",
			xpath:      "replace(SKU, PatternNode, '_')",
			wantSubstr: "xpath replace() pattern (argument 2) must be a string literal",
		},
		{
			name:       "replace non-literal replacement rejected",
			xpath:      "replace(SKU, '-', ReplNode)",
			wantSubstr: "xpath replace() replacement (argument 3) must be a string literal",
		},
		{
			name:       "replace out-of-range group $1 on 0-group pattern",
			xpath:      "replace(SKU, '^WGT-[0-9]+$', '$1')",
			wantSubstr: "references capturing group $1, but pattern \"^WGT-[0-9]+$\" has 0 capturing group(s)",
		},
		{
			name:       "replace out-of-range group $2 on 1-group pattern",
			xpath:      "replace(SKU, '^WGT-([0-9]+)$', '$2')",
			wantSubstr: "references capturing group $2, but pattern \"^WGT-([0-9]+)$\" has 1 capturing group(s)",
		},
		{
			name:       "replace out-of-range group $10 on 1-group pattern",
			xpath:      "replace(SKU, '^WGT-([0-9]+)$', '$10')",
			wantSubstr: "references capturing group $10, but pattern \"^WGT-([0-9]+)$\" has 1 capturing group(s)",
		},
		{
			name:       "replace out-of-range braced group ${2}",
			xpath:      "replace(SKU, '^WGT-([0-9]+)$', '${2}')",
			wantSubstr: "references capturing group ${2}, but pattern \"^WGT-([0-9]+)$\" has 1 capturing group(s)",
		},
		{
			name:       "replace unknown named group ${other}",
			xpath:      "replace(SKU, '^WGT-(?P<id>[0-9]+)$', '${other}')",
			wantSubstr: "references unknown named capturing group ${other}",
		},
		{
			name:       "replace unbraced named group $id",
			xpath:      "replace(SKU, '^WGT-(?P<id>[0-9]+)$', '$id')",
			wantSubstr: "unbraced group reference starting at \"$id\"",
		},
		{
			name:       "replace XPath 2.0 backslash-dollar escape rejected",
			xpath:      `replace(SKU, '^WGT-', '\$1')`,
			wantSubstr: `uses XPath 2.0 '\$' escape`,
		},
		{
			name:       "replace $$1 with capturing group rejected",
			xpath:      "replace(SKU, '^WGT-([0-9]+)$', '$$1')",
			wantSubstr: "cannot combine '$$' with a leading digit when pattern",
		},
		{
			name:       "replace trailing bare dollar rejected",
			xpath:      "replace(SKU, '-', 'USD$')",
			wantSubstr: "trailing unescaped '$'",
		},
		{
			name:       "replace unclosed braced group rejected",
			xpath:      "replace(SKU, '^WGT-([0-9]+)$', '${1')",
			wantSubstr: "unclosed '${'",
		},
		{
			name:       "nested replace inside first argument validated",
			xpath:      "replace(replace(SKU, '[bad', 'x'), '-', '_')",
			wantSubstr: `xpath replace() invalid regular expression "[bad"`,
		},
		{
			name:        "nested item_mapping invalid regex rejected at prepare",
			xpath:       "matches(@code, '[unclosed')",
			wantSubstr:  `xpath matches() invalid regular expression "[unclosed"`,
			inItemArray: true,
		},
		{
			name:       "lower-case 0 args rejected without compile panic",
			xpath:      "lower-case()",
			wantSubstr: "xpath lower-case() requires 1 argument (input), got 0",
		},
		{
			name:       "starts-with 1 arg rejected without compile panic",
			xpath:      "starts-with(SKU)",
			wantSubstr: "xpath starts-with() requires 2 arguments, got 1",
		},
		{
			name:       "ends-with 1 arg rejected without compile panic",
			xpath:      "ends-with(SKU)",
			wantSubstr: "xpath ends-with() requires 2 arguments, got 1",
		},
		{
			name:       "contains 1 arg rejected without compile panic",
			xpath:      "contains(SKU)",
			wantSubstr: "xpath contains() requires 2 arguments, got 1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mappings []FieldMapping
			if tc.inItemArray {
				mappings = []FieldMapping{
					{
						OutputField: "items",
						XPath:       "Items/Item",
						Type:        "array",
						ItemMapping: []FieldMapping{
							{OutputField: "bad_item_field", XPath: tc.xpath, Type: "boolean"},
						},
					},
				}
			} else {
				mappings = []FieldMapping{
					{OutputField: "bad_field", XPath: tc.xpath, Type: "string"},
				}
			}
			cfg := &ExtractRecordMatch{
				RecordType:     "test_record",
				MatchSelectors: []MatchSelector{{XPath: "//Record"}},
				FieldMappings:  mappings,
			}
			err := PrepareRecordMatch(cfg)
			if err == nil {
				t.Fatalf("expected PrepareRecordMatch error containing %q, got nil", tc.wantSubstr)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("PrepareRecordMatch error = %q, want substring %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}

// TestXPathRuntimePanicContainment verifies that runtime engine panics in
// antchfx/xpath v1.3.8 (such as substring('abc', 2, 100) slice-bounds panics)
// are caught by recover() in evaluateXPathValue and returned as a
// field-attributed error rather than crashing the process.
func TestXPathRuntimePanicContainment(t *testing.T) {
	tmp := t.TempDir()
	xmlPath := writeXPathSumMultiplyFixture(t, tmp, "panic.xml", xpathStringRegexConformanceXML)
	sig := &FileSignature{
		SignatureID:         "xpath-panic-sig",
		ConfidenceThreshold: 0.5,
		MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/Catalog", Weight: 1.0}},
	}

	t.Run("top-level-field-panic-attributed", func(t *testing.T) {
		cfg := preparedFieldConfig(t, []FieldMapping{
			{OutputField: "overflow_sub", XPath: "substring('abc', 2, 100)", Type: "string"},
		}, "//Record", nil)

		res := ProcessFile(xmlPath, sig, cfg, nil, false)
		if res.Error == nil {
			t.Fatal("expected error from substring('abc', 2, 100) panic, got nil")
		}
		if !strings.Contains(res.Error.Error(), "overflow_sub") || !strings.Contains(res.Error.Error(), "panicked") {
			t.Fatalf("unexpected error: %v", res.Error)
		}
	})

	t.Run("nested-item-mapping-panic-attributed", func(t *testing.T) {
		cfg := preparedFieldConfig(t, []FieldMapping{
			{
				OutputField: "items",
				XPath:       "Items/Item",
				Type:        "array",
				ItemMapping: []FieldMapping{
					{OutputField: "bad_item_sub", XPath: "substring('abc', 2, 100)", Type: "string"},
				},
			},
		}, "//Record", nil)

		res := ProcessFile(xmlPath, sig, cfg, nil, false)
		if res.Error == nil {
			t.Fatal("expected error from nested substring('abc', 2, 100) panic, got nil")
		}
		if !strings.Contains(res.Error.Error(), "bad_item_sub") || !strings.Contains(res.Error.Error(), "panicked") {
			t.Fatalf("unexpected error: %v", res.Error)
		}
	})
}
