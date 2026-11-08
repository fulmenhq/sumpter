package extract

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

const xpathUnionOrderXML = `<?xml version="1.0" encoding="UTF-8"?>
<Ledger>
  <Record code="R-100">
    <Alpha>A1</Alpha>
    <Beta>B1</Beta>
    <Alpha>A2</Alpha>
  </Record>
  <Record code="R-200">
    <Beta>B2</Beta>
    <Gamma>G1</Gamma>
    <Alpha>A3</Alpha>
  </Record>
</Ledger>
`

const xpathUnionOrderNSXML = `<?xml version="1.0" encoding="UTF-8"?>
<Ledger xmlns:n1="urn:example:sumpter-union-n1" xmlns:n2="urn:example:sumpter-union-n2">
  <Record code="NS-1">
    <n1:Entry>N1-first</n1:Entry>
    <n2:Entry>N2-middle</n2:Entry>
    <n1:Entry>N1-last</n1:Entry>
  </Record>
  <Record code="NS-2">
    <n2:Entry>N2-lead</n2:Entry>
    <n1:Entry>N1-trail</n1:Entry>
  </Record>
</Ledger>
`

// TestXPathUnionDocumentOrderThreeRoutes verifies that XPath union (|) returns
// nodes in document order across DOM, streaming, and worker/indexed field
// evaluation paths, both unnamespaced and namespace-bound.
func TestXPathUnionDocumentOrderThreeRoutes(t *testing.T) {
	t.Run("unnamespaced", func(t *testing.T) {
		tmp := t.TempDir()
		xmlPath := writeXPathSumMultiplyFixture(t, tmp, "union.xml", xpathUnionOrderXML)

		mappings := []FieldMapping{
			{OutputField: "code", XPath: "@code", Type: "string"},
			// Reverse operand order: Beta written first, but Alpha appears first in R-100.
			{OutputField: "first_ba", XPath: "(Beta | Alpha)[1]", Type: "string"},
			{OutputField: "first_ab", XPath: "(Alpha | Beta)[1]", Type: "string"},
			{OutputField: "third_abg", XPath: "(Alpha | Beta | Gamma)[3]", Type: "string"},
			{OutputField: "third_gba", XPath: "(Gamma | Beta | Alpha)[3]", Type: "string"},
			{OutputField: "string_ba", XPath: "string(Beta | Alpha)", Type: "string"},
			{OutputField: "second_ba", XPath: "(Beta | Alpha)[2]", Type: "string"},
			{OutputField: "union_array", XPath: "Gamma | Beta | Alpha | Beta", Type: "array"},
			// Attribute vs element child: attribute sorts after its element and before children.
			{OutputField: "attr_vs_child_first", XPath: "string(Beta | @code)", Type: "string"},
			{OutputField: "child_vs_attr_first", XPath: "string(@code | Beta)", Type: "string"},
			// Guarded priority fallback: prefer Beta even when Alpha precedes it in document order.
			{OutputField: "priority_beta_else_alpha", XPath: "(Beta | Alpha[not(../Beta)])[1]", Type: "string"},
			{
				OutputField: "union_items",
				XPath:       "Beta | Alpha",
				Type:        "array",
				ItemMapping: []FieldMapping{
					{OutputField: "val", XPath: ".", Type: "string"},
				},
			},
		}

		sig := &FileSignature{
			SignatureID:         "xpath-union-order-sig",
			ConfidenceThreshold: 0.5,
			MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/Ledger", Weight: 1.0}},
		}

		wantR1 := map[string]interface{}{
			"code":                     "R-100",
			"first_ba":                 "A1",
			"first_ab":                 "A1",
			"third_abg":                "A2",
			"third_gba":                "A2",
			"string_ba":                "A1",
			"second_ba":                "B1",
			"union_array":              []interface{}{"A1", "B1", "A2"},
			"attr_vs_child_first":      "R-100",
			"child_vs_attr_first":      "R-100",
			"priority_beta_else_alpha": "B1",
			"union_items": []map[string]interface{}{
				{"val": "A1"},
				{"val": "B1"},
				{"val": "A2"},
			},
		}
		wantR2 := map[string]interface{}{
			"code":                     "R-200",
			"first_ba":                 "B2",
			"first_ab":                 "B2",
			"third_abg":                "A3",
			"third_gba":                "A3",
			"string_ba":                "B2",
			"second_ba":                "A3",
			"union_array":              []interface{}{"B2", "G1", "A3"},
			"attr_vs_child_first":      "R-200",
			"child_vs_attr_first":      "R-200",
			"priority_beta_else_alpha": "B2",
			"union_items": []map[string]interface{}{
				{"val": "B2"},
				{"val": "A3"},
			},
		}

		// 1. DOM route
		domCfg := preparedFieldConfig(t, mappings, "//Record", nil)
		domRes := ProcessFile(xmlPath, sig, domCfg, nil, false)
		if domRes.Error != nil {
			t.Fatalf("DOM ProcessFile: %v", domRes.Error)
		}
		assertUnionRouteRecords(t, "DOM", domRes.Records, wantR1, wantR2)

		// 2. Streaming route (ProcessFileStreaming + ProcessFileStreamingToSink)
		streamCfg := preparedFieldConfig(t, mappings, "//Record", nil)
		streamRes := ProcessFileStreaming(xmlPath, sig, streamCfg, nil)
		if streamRes.Error != nil {
			t.Fatalf("Streaming ProcessFileStreaming: %v", streamRes.Error)
		}
		assertUnionRouteRecords(t, "Streaming", streamRes.Records, wantR1, wantR2)

		sinkCfg := preparedFieldConfig(t, mappings, "//Record", nil)
		sink := &recordCollectingSink{}
		sinkRes := ProcessFileStreamingToSink(context.Background(), xmlPath, sig, sinkCfg, nil, provenance.RuntimeOptions{}, sink)
		if sinkRes.Error != nil {
			t.Fatalf("StreamingToSink: %v", sinkRes.Error)
		}
		assertUnionRouteRecords(t, "StreamingToSink", sink.Records(), wantR1, wantR2)

		// 3. Worker / indexed field evaluation route (ExtractFieldsWithExternal)
		doc, err := parseXMLDoc(strings.NewReader(xpathUnionOrderXML))
		if err != nil {
			t.Fatalf("parseXMLDoc: %v", err)
		}
		workerCfg := preparedFieldConfig(t, mappings, ".", nil)
		var workerRecords []map[string]interface{}
		for _, sel := range []string{"//Record[1]", "//Record[2]"} {
			n := findNode(doc, sel)
			if n == nil {
				t.Fatalf("findNode %s returned nil", sel)
			}
			fields, err := ExtractFieldsWithExternal(subDoc(doc, n), workerCfg, nil)
			if err != nil {
				t.Fatalf("worker ExtractFieldsWithExternal(%s): %v", sel, err)
			}
			workerRecords = append(workerRecords, fields)
		}
		assertUnionRouteRecords(t, "Worker", workerRecords, wantR1, wantR2)
	})

	t.Run("namespace-bound", func(t *testing.T) {
		tmp := t.TempDir()
		xmlPath := writeXPathSumMultiplyFixture(t, tmp, "union-ns.xml", xpathUnionOrderNSXML)

		ns := map[string]string{
			"n1": "urn:example:sumpter-union-n1",
			"n2": "urn:example:sumpter-union-n2",
		}
		mappings := []FieldMapping{
			{OutputField: "code", XPath: "@code", Type: "string"},
			{OutputField: "first_n2_n1", XPath: "(n2:Entry | n1:Entry)[1]", Type: "string"},
			{OutputField: "second_n1_n2", XPath: "(n1:Entry | n2:Entry)[2]", Type: "string"},
			{OutputField: "second_n2_n1", XPath: "(n2:Entry | n1:Entry)[2]", Type: "string"},
			{OutputField: "entries", XPath: "n2:Entry | n1:Entry", Type: "array"},
		}
		sig := &FileSignature{
			SignatureID:         "xpath-union-order-ns-sig",
			ConfidenceThreshold: 0.5,
			MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/Ledger", Weight: 1.0}},
		}

		wantNS1 := map[string]interface{}{
			"code":         "NS-1",
			"first_n2_n1":  "N1-first",
			"second_n1_n2": "N2-middle",
			"second_n2_n1": "N2-middle",
			"entries":      []interface{}{"N1-first", "N2-middle", "N1-last"},
		}
		wantNS2 := map[string]interface{}{
			"code":         "NS-2",
			"first_n2_n1":  "N2-lead",
			"second_n1_n2": "N1-trail",
			"second_n2_n1": "N1-trail",
			"entries":      []interface{}{"N2-lead", "N1-trail"},
		}

		domCfg := preparedFieldConfig(t, mappings, "//Record", ns)
		domRes := ProcessFile(xmlPath, sig, domCfg, nil, false)
		if domRes.Error != nil {
			t.Fatalf("DOM ProcessFile: %v", domRes.Error)
		}
		assertUnionRouteRecords(t, "DOM-NS", domRes.Records, wantNS1, wantNS2)

		streamCfg := preparedFieldConfig(t, mappings, "//Record", ns)
		streamRes := ProcessFileStreaming(xmlPath, sig, streamCfg, nil)
		if streamRes.Error != nil {
			t.Fatalf("Streaming ProcessFileStreaming: %v", streamRes.Error)
		}
		assertUnionRouteRecords(t, "Streaming-NS", streamRes.Records, wantNS1, wantNS2)
	})

	t.Run("rooted-union-last-single-record", func(t *testing.T) {
		const singleRecordXML = `<?xml version="1.0" encoding="UTF-8"?>
<Ledger>
  <Record code="R-SINGLE">
    <Alpha>A1</Alpha>
    <Beta>B1</Beta>
    <Alpha>A2</Alpha>
  </Record>
</Ledger>
`
		tmp := t.TempDir()
		xmlPath := writeXPathSumMultiplyFixture(t, tmp, "union-single.xml", singleRecordXML)

		mappings := []FieldMapping{
			{OutputField: "code", XPath: "@code", Type: "string"},
			{OutputField: "last_ab", XPath: "(//Alpha | //Beta)[last()]", Type: "string"},
			{OutputField: "last_ba", XPath: "(//Beta | //Alpha)[last()]", Type: "string"},
		}
		sig := &FileSignature{
			SignatureID:         "xpath-union-order-single-sig",
			ConfidenceThreshold: 0.5,
			MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/Ledger", Weight: 1.0}},
		}
		want := map[string]interface{}{
			"code":    "R-SINGLE",
			"last_ab": "A2",
			"last_ba": "A2",
		}

		domCfg := preparedFieldConfig(t, mappings, "//Record", nil)
		domRes := ProcessFile(xmlPath, sig, domCfg, nil, false)
		if domRes.Error != nil {
			t.Fatalf("DOM ProcessFile: %v", domRes.Error)
		}
		assertUnionRouteRecords(t, "DOM-RootedLast", domRes.Records, want)

		streamCfg := preparedFieldConfig(t, mappings, "//Record", nil)
		streamRes := ProcessFileStreaming(xmlPath, sig, streamCfg, nil)
		if streamRes.Error != nil {
			t.Fatalf("Streaming ProcessFileStreaming: %v", streamRes.Error)
		}
		assertUnionRouteRecords(t, "Streaming-RootedLast", streamRes.Records, want)

		doc, err := parseXMLDoc(strings.NewReader(singleRecordXML))
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
		assertUnionRouteRecords(t, "Worker-RootedLast", []map[string]interface{}{fields}, want)
	})
}

// TestXPathUnionRecordMatchSelectorDocumentOrder verifies that DOM record
// match selectors using XPath unions emit records in document preorder rather
// than grouping by union operand, including the inline-XBRL S0 divergence shape
// (where nonFraction and nonNumeric facts interleave across ~45 facts).
func TestXPathUnionRecordMatchSelectorDocumentOrder(t *testing.T) {
	t.Run("synthetic-s0-interleaved-ixbrl-union", func(t *testing.T) {
		// Construct a 45-fact inline-XBRL-shaped document where nonFraction and
		// nonNumeric alternate and interleave at index 39+. On unpatched v1.3.8,
		// //ix:nonFraction | //ix:nonNumeric emitted all nonFraction first and all
		// nonNumeric second; with #178 it emits exact document preorder.
		var b strings.Builder
		b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
		b.WriteString(`<html xmlns="http://www.w3.org/1999/xhtml" xmlns:ix="http://www.xbrl.org/2013/inlineXBRL" xmlns:ix08="http://www.xbrl.org/2008/inlineXBRL"><body>` + "\n")
		wantIDs := make([]string, 0, 45)
		for i := 1; i <= 45; i++ {
			id := "F-" + leftPadInt(i)
			wantIDs = append(wantIDs, id)
			switch {
			case i == 39 || i == 41 || i%5 == 0:
				b.WriteString(`<ix:nonNumeric id="` + id + `">text-` + id + `</ix:nonNumeric>` + "\n")
			case i == 40:
				b.WriteString(`<ix08:nonFraction id="` + id + `">40</ix08:nonFraction>` + "\n")
			default:
				b.WriteString(`<ix:nonFraction id="` + id + `">` + leftPadInt(i) + `</ix:nonFraction>` + "\n")
			}
		}
		b.WriteString(`</body></html>`)

		tmp := t.TempDir()
		xmlPath := writeXPathSumMultiplyFixture(t, tmp, "ixbrl-s0.xml", b.String())

		ns := map[string]string{
			"h":    "http://www.w3.org/1999/xhtml",
			"ix":   "http://www.xbrl.org/2013/inlineXBRL",
			"ix08": "http://www.xbrl.org/2008/inlineXBRL",
		}
		sig := &FileSignature{
			SignatureID:         "ixbrl-union-sig",
			ConfidenceThreshold: 0.5,
			Namespaces:          ns,
			MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/h:html", Weight: 1.0}},
		}
		unionSelector := `//ix:nonFraction | //ix:nonNumeric | //ix08:nonFraction | //ix08:nonNumeric`
		cfg := preparedFieldConfig(t, []FieldMapping{
			{OutputField: "id", XPath: "@id", Type: "string"},
			{OutputField: "kind", XPath: "local-name(.)", Type: "string"},
		}, unionSelector, ns)

		res := ProcessFile(xmlPath, sig, cfg, nil, false)
		if res.Error != nil {
			t.Fatalf("ProcessFile: %v", res.Error)
		}
		if len(res.Records) != len(wantIDs) {
			t.Fatalf("got %d records, want %d", len(res.Records), len(wantIDs))
		}
		for i, rec := range res.Records {
			fields := recordFields(t, rec)
			if got := fields["id"]; got != wantIDs[i] {
				t.Fatalf("record index %d: got id=%v, want %s (document order broken)", i, got, wantIDs[i])
			}
		}
	})

	t.Run("case-16-ixbrl-fixture-union-vs-predicate-parity", func(t *testing.T) {
		fixturePath := filepath.Join("..", "..", "examples", "cases", "16-inline-xbrl-inventory", "input.xml")
		if _, err := os.Stat(fixturePath); err != nil {
			t.Fatalf("stat case 16 fixture: %v", err)
		}
		ns := map[string]string{
			"h":    "http://www.w3.org/1999/xhtml",
			"ix":   "http://www.xbrl.org/2013/inlineXBRL",
			"ix08": "http://www.xbrl.org/2008/inlineXBRL",
		}
		sig := &FileSignature{
			SignatureID:         "ixbrl-case16-sig",
			ConfidenceThreshold: 0.5,
			Namespaces:          ns,
			MatchPatterns:       []MatchPattern{{PatternID: "root", Selector: "/h:html", Weight: 1.0}},
		}
		mappings := []FieldMapping{
			{OutputField: "id", XPath: "@id", Type: "string"},
			{OutputField: "kind", XPath: "local-name(.)", Type: "string"},
			{OutputField: "ns", XPath: "namespace-uri(.)", Type: "string"},
			{OutputField: "text", XPath: "string(.)", Type: "string"},
		}

		predSelector := `descendant::*[self::ix:nonFraction or self::ix:nonNumeric or self::ix:fraction or self::ix08:nonFraction or self::ix08:nonNumeric or self::ix08:fraction]`
		unionSelector := `//ix:nonFraction | //ix:nonNumeric | //ix:fraction | //ix08:nonFraction | //ix08:nonNumeric | //ix08:fraction`

		predCfg := preparedFieldConfig(t, mappings, predSelector, ns)
		unionCfg := preparedFieldConfig(t, mappings, unionSelector, ns)

		predRes := ProcessFile(fixturePath, sig, predCfg, nil, false)
		if predRes.Error != nil {
			t.Fatalf("predicate ProcessFile: %v", predRes.Error)
		}
		unionRes := ProcessFile(fixturePath, sig, unionCfg, nil, false)
		if unionRes.Error != nil {
			t.Fatalf("union ProcessFile: %v", unionRes.Error)
		}
		if len(predRes.Records) != 13 || len(unionRes.Records) != 13 {
			t.Fatalf("record counts: pred=%d union=%d, want 13", len(predRes.Records), len(unionRes.Records))
		}
		for i := range predRes.Records {
			pf := recordFields(t, predRes.Records[i])
			uf := recordFields(t, unionRes.Records[i])
			if !reflect.DeepEqual(pf, uf) {
				t.Fatalf("row %d mismatch:\nunion: %#v\npred:  %#v", i, uf, pf)
			}
		}
	})
}

// TestXPathUnionReverseAxisSingleOperandCaveat pins the documented engine
// caveat on the vendored antchfx/xpath v1.3.8 + #178 implementation: a union
// sorts into forward document order when both operands contribute at least one
// distinct node, whereas a union where only a reverse-axis operand contributes
// distinct nodes retains that reverse-axis evaluation order.
func TestXPathUnionReverseAxisSingleOperandCaveat(t *testing.T) {
	const xmlFixture = `<Ledger><Record><Alpha>A1</Alpha><Beta>B1</Beta><Gamma>G1</Gamma></Record></Ledger>`
	doc, err := parseXMLDoc(strings.NewReader(xmlFixture))
	if err != nil {
		t.Fatalf("parseXMLDoc: %v", err)
	}
	gamma := findNode(doc, "//Gamma")
	if gamma == nil {
		t.Fatal("findNode //Gamma returned nil")
	}

	cases := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "reverse-axis-with-empty-right-retains-reverse-order",
			expr: "preceding-sibling::* | Missing",
			want: []string{"B1", "A1"},
		},
		{
			name: "empty-left-with-reverse-axis-right-retains-reverse-order",
			expr: "Missing | preceding-sibling::*",
			want: []string{"B1", "A1"},
		},
		{
			name: "reverse-axis-with-duplicate-only-right-retains-reverse-order",
			expr: "preceding-sibling::* | ../Alpha",
			want: []string{"B1", "A1"},
		},
		{
			name: "reverse-axis-first-with-empty-right-selects-nearest-sibling",
			expr: "(preceding-sibling::* | Missing)[1]",
			want: []string{"B1"},
		},
		{
			name: "reverse-axis-with-distinct-right-sorts-into-document-order",
			expr: "preceding-sibling::* | .",
			want: []string{"A1", "B1", "G1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			xp, err := compileXPath(tc.expr, nil)
			if err != nil {
				t.Fatalf("compileXPath(%q): %v", tc.expr, err)
			}
			nodes, err := evaluateNodeSet(doc.Format(), gamma, xp, tc.expr)
			if err != nil {
				t.Fatalf("evaluateNodeSet(%q): %v", tc.expr, err)
			}
			got := make([]string, 0, len(nodes))
			for _, n := range nodes {
				got = append(got, n.Navigator().Value())
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s: got %v, want %v", tc.expr, got, tc.want)
			}
		})
	}
}

func assertUnionRouteRecords(t *testing.T, route string, gotRecords []map[string]interface{}, wantRecords ...map[string]interface{}) {
	t.Helper()
	if len(gotRecords) != len(wantRecords) {
		t.Fatalf("[%s] got %d records, want %d", route, len(gotRecords), len(wantRecords))
	}
	for i, want := range wantRecords {
		got := recordFields(t, gotRecords[i])
		for k, wv := range want {
			gv, ok := got[k]
			if !ok {
				t.Errorf("[%s] record %d missing field %q; got=%#v", route, i, k, got)
				continue
			}
			if !reflect.DeepEqual(gv, wv) {
				t.Errorf("[%s] record %d field %q = %#v (%T), want %#v (%T)", route, i, k, gv, gv, wv, wv)
			}
		}
	}
}

func leftPadInt(n int) string {
	if n < 10 {
		return "0" + string(rune('0'+n))
	}
	tens := n / 10
	ones := n % 10
	return string([]rune{rune('0' + tens), rune('0' + ones)})
}
