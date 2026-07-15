package extract

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
)

// Synthetic ledger-style document for xpath-sum-multiply regressions.
// One Credit record with a single non-excluded Entry Amount=20.
const xpathSumMultiplyXML = `<?xml version="1.0" encoding="UTF-8"?>
<Root>
  <Credit>
    <Entry>
      <Amount>20</Amount>
    </Entry>
  </Credit>
</Root>
`

// Namespaced variant of the same shape (CompileWithNS path).
const xpathSumMultiplyNSXML = `<?xml version="1.0" encoding="UTF-8"?>
<Root xmlns="urn:example:sumpter-xpath-arith">
  <Credit>
    <Entry>
      <Amount>20</Amount>
    </Entry>
  </Credit>
</Root>
`

const xpathSumMultiplyNS = "urn:example:sumpter-xpath-arith"

// floatNear reports whether got is within an absolute epsilon of want.
func floatNear(got, want, eps float64) bool {
	if math.IsNaN(got) || math.IsNaN(want) {
		return math.IsNaN(got) && math.IsNaN(want)
	}
	return math.Abs(got-want) <= eps
}

func writeXPathSumMultiplyFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func preparedFieldConfig(t *testing.T, mappings []FieldMapping, matchXPath string, ns map[string]string) *ExtractRecordMatch {
	t.Helper()
	if matchXPath == "" {
		matchXPath = "//Credit"
	}
	cfg := &ExtractRecordMatch{
		RecordType:     "credit_record",
		Namespaces:     ns,
		MatchSelectors: []MatchSelector{{XPath: matchXPath}},
		FieldMappings:  mappings,
	}
	if err := PrepareRecordMatch(cfg); err != nil {
		t.Fatalf("PrepareRecordMatch: %v", err)
	}
	for i := range cfg.FieldMappings {
		m := &cfg.FieldMappings[i]
		if strings.TrimSpace(m.XPath) == "" {
			continue
		}
		if m.CompiledXPath == nil {
			t.Fatalf("field %q: CompiledXPath is nil after PrepareRecordMatch", m.OutputField)
		}
	}
	if len(cfg.MatchSelectors) > 0 && cfg.MatchSelectors[0].CompiledXPath == nil {
		t.Fatalf("match selector CompiledXPath is nil after PrepareRecordMatch")
	}
	return cfg
}

// TestXPathSumMultiplyPreparedFieldMatrix is the core prepared-field-path matrix
// for predicated sum × trailing context-sensitive factor (xpath-sum-multiply).
//
// Explicit wants (never leading==trailing equality alone). Non-vacuous rows use
// non-zero aggregate (20) and non-identity factors (negative and magnitude > 1).
func TestXPathSumMultiplyPreparedFieldMatrix(t *testing.T) {
	tmp := t.TempDir()
	inputPath := writeXPathSumMultiplyFixture(t, tmp, "credit.xml", xpathSumMultiplyXML)

	const (
		predSum     = `sum(.//Entry[not(@excluded='true')]/Amount)`
		plainSum    = `sum(.//Amount)`
		singleNode  = `.//Amount`
		csFactor    = `(1 - 2*count(self::Credit))` // -1 when context is Credit
		csFactorMag = `(3 * count(self::Credit))`   // 3 when context is Credit
	)

	type row struct {
		name    string
		xpath   string
		typ     string
		want    float64
		intWant int64
		// role documents whether this row is the defect detector or a control.
		role string
	}

	rows := []row{
		// --- Must-prove quartet ---
		{
			name:  "predicated_sum_literal_trailing_control",
			xpath: predSum + ` * -1`,
			typ:   "number",
			want:  -20,
			role:  "control",
		},
		{
			name:  "plain_sum_context_sensitive_trailing_control",
			xpath: plainSum + ` * ` + csFactor,
			typ:   "number",
			want:  -20,
			role:  "control",
		},
		{
			name:  "predicated_sum_context_sensitive_trailing",
			xpath: predSum + ` * ` + csFactor,
			typ:   "number",
			want:  -20,
			role:  "defect", // red on antchfx/xpath v1.3.6 (+20); green after fix
		},
		{
			name:  "factor_first_predicated_sum_control",
			xpath: csFactor + ` * ` + predSum,
			typ:   "number",
			want:  -20,
			role:  "control",
		},
		// --- Non-vacuous magnitude > 1 ---
		{
			name:  "predicated_sum_context_sensitive_trailing_magnitude",
			xpath: predSum + ` * ` + csFactorMag,
			typ:   "number",
			want:  60,
			role:  "defect",
		},
		// --- Integer exact after coercion ---
		{
			name:    "predicated_sum_context_sensitive_trailing_integer",
			xpath:   predSum + ` * ` + csFactor,
			typ:     "integer",
			want:    -20,
			intWant: -20,
			role:    "defect",
		},
		// --- Neighboring single-node multiplicative guard (not the core defect) ---
		{
			name:  "single_node_context_sensitive_trailing_guard",
			xpath: singleNode + ` * ` + csFactor,
			typ:   "number",
			want:  -20,
			role:  "guard",
		},
		// --- No-opt / already-correct parity ---
		{
			name:  "no_opt_plain_sum_literal",
			xpath: plainSum + ` * -1`,
			typ:   "number",
			want:  -20,
			role:  "parity",
		},
		// --- Absent aggregate robustness (0 * factor = 0; not defect proof) ---
		{
			name:  "absent_predicated_sum_context_sensitive",
			xpath: `sum(.//Entry[@missing='yes']/Amount) * ` + csFactor,
			typ:   "number",
			want:  0,
			role:  "robustness",
		},
	}

	// Build one prepared config with all present-value fields; absent is separate
	// only in naming — same document is fine (empty sum).
	mappings := make([]FieldMapping, 0, len(rows))
	for _, r := range rows {
		mappings = append(mappings, FieldMapping{
			OutputField: r.name,
			XPath:       r.xpath,
			Type:        r.typ,
		})
	}
	cfg := preparedFieldConfig(t, mappings, "//Credit", nil)

	signature := &FileSignature{
		SignatureID:         "xpath-sum-multiply-test",
		ConfidenceThreshold: 0.2,
		MatchPatterns: []MatchPattern{
			{PatternID: "root", Selector: "/Root", Weight: 1.0},
		},
	}

	result := ProcessFile(inputPath, signature, cfg, nil, false)
	if result.Error != nil {
		t.Fatalf("ProcessFile error: %v", result.Error)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result.Records))
	}
	// ProcessFile wraps extract under "extract" for some paths — use raw fields
	// when present at top level (depends on provenance). Mirror existing tests.
	record := result.Records[0]
	fields := recordFields(t, record)

	for _, r := range rows {
		got, ok := fields[r.name]
		if !ok {
			t.Errorf("[%s] field missing from record (role=%s); keys=%v", r.name, r.role, fieldKeys(fields))
			continue
		}
		switch r.typ {
		case "integer":
			// Contract: integer field mappings coerce to int64 exactly.
			v, ok := got.(int64)
			if !ok {
				t.Errorf("[%s] role=%s integer type %T value %v; want int64", r.name, r.role, got, got)
				continue
			}
			if v != r.intWant {
				t.Errorf("[%s] role=%s integer got %d want %d", r.name, r.role, v, r.intWant)
			}
		default:
			f, ok := got.(float64)
			if !ok {
				t.Errorf("[%s] role=%s number type %T value %v", r.name, r.role, got, got)
				continue
			}
			if !floatNear(f, r.want, 1e-9) {
				t.Errorf("[%s] role=%s number got %v want %v", r.name, r.role, f, r.want)
			}
		}
	}
}

func recordFields(t *testing.T, record map[string]interface{}) map[string]interface{} {
	t.Helper()
	// ProcessFile envelopes: extract.data.<fields> (common) or data.<fields>.
	if extractBlock, ok := record["extract"].(map[string]interface{}); ok {
		if data, ok := extractBlock["data"].(map[string]interface{}); ok {
			return data
		}
		return extractBlock
	}
	if data, ok := record["data"].(map[string]interface{}); ok {
		return data
	}
	return record
}

func fieldKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// TestXPathSumMultiplyNamespaceBoundPrepared exercises CompileWithNS on a
// predicated sum × context-sensitive factor through the prepared field path.
func TestXPathSumMultiplyNamespaceBoundPrepared(t *testing.T) {
	tmp := t.TempDir()
	inputPath := writeXPathSumMultiplyFixture(t, tmp, "credit-ns.xml", xpathSumMultiplyNSXML)

	xpathExpr := `sum(.//n:Entry[not(@excluded='true')]/n:Amount) * (1 - 2*count(self::n:Credit))`
	cfg := preparedFieldConfig(t, []FieldMapping{
		{OutputField: "signed_amount", XPath: xpathExpr, Type: "number"},
	}, "//n:Credit", map[string]string{"n": xpathSumMultiplyNS})

	signature := &FileSignature{
		SignatureID:         "xpath-sum-multiply-ns-test",
		ConfidenceThreshold: 0.2,
		MatchPatterns: []MatchPattern{
			{PatternID: "root", Selector: "/*", Weight: 1.0},
		},
		Namespaces: map[string]string{"n": xpathSumMultiplyNS},
	}

	result := ProcessFile(inputPath, signature, cfg, nil, false)
	if result.Error != nil {
		t.Fatalf("ProcessFile error: %v", result.Error)
	}
	if len(result.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(result.Records))
	}
	fields := recordFields(t, result.Records[0])
	got, ok := fields["signed_amount"].(float64)
	if !ok {
		t.Fatalf("signed_amount type %T value %v", fields["signed_amount"], fields["signed_amount"])
	}
	if !floatNear(got, -20, 1e-9) {
		t.Fatalf("signed_amount = %v, want -20", got)
	}
}

// TestXPathSumMultiplySignatureRouting covers the shared antchfx pin class on a
// real signature routing path. The selector predicates on a context-sensitive
// product evaluated *relative to each candidate Credit node* (self:: inside the
// filter). On the vulnerable pin the product is +20 so the node-set is empty
// (no match); after the fix the product is -20 and the signature matches with
// confidence 1.0.
//
// Selector (defect class through matchesSignature):
//
//	//Credit[sum(.//Entry[not(@excluded='true')]/Amount) * (1 - 2*count(self::Credit)) < 0]
func TestXPathSumMultiplySignatureRouting(t *testing.T) {
	doc, err := xmlquery.Parse(strings.NewReader(xpathSumMultiplyXML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	// Primary AC: prepareSignatureConfig + matchesSignature on a node-set
	// selector whose predicate embeds the defect-class expression.
	const defectSelector = `//Credit[sum(.//Entry[not(@excluded='true')]/Amount) * (1 - 2*count(self::Credit)) < 0]`
	sig := &FileSignature{
		SignatureID:         "xpath-sum-multiply-sig",
		ConfidenceThreshold: 0.99,
		MatchPatterns: []MatchPattern{
			{PatternID: "pred_sum_context_sensitive_gate", Selector: defectSelector, Weight: 1.0},
		},
	}
	if err := prepareSignatureConfig(sig); err != nil {
		t.Fatalf("prepareSignatureConfig: %v", err)
	}
	matched, conf, err := matchesSignature(doc, sig)
	if err != nil {
		t.Fatalf("matchesSignature: %v", err)
	}
	if !matched {
		t.Fatalf("signature defect-class gate should match after fix; conf=%v selector=%s (broken pin: product +20 → predicate false → no match)", conf, defectSelector)
	}
	if conf != 1.0 {
		t.Fatalf("signature confidence = %v, want 1.0 (single weight-1 pattern matched)", conf)
	}

	// Applicability uses the same evaluateXPathBoolean / boolean coercion path
	// on a document-rooted expression of the same defect class (node-set truthy
	// iff at least one Credit satisfies the predicate).
	appCfg := &ApplicabilityConfig{
		Applicability: ApplicabilityPredicate{
			Type:       "xpath",
			Expression: defectSelector,
		},
	}
	if err := validateApplicabilityConfig(appCfg); err != nil {
		t.Fatalf("validateApplicabilityConfig: %v", err)
	}
	appOK, err := evaluateXPathBoolean(doc, appCfg.Applicability.Expression)
	if err != nil {
		t.Fatalf("applicability evaluateXPathBoolean: %v", err)
	}
	if !appOK {
		t.Fatalf("applicability defect-class gate false; want true (node-set non-empty)")
	}

	// Supplemental helper-seam check on the Credit node (not a substitute for
	// the signature/applicability rows above).
	rec := xmlquery.FindOne(doc, "//Credit")
	if rec == nil {
		t.Fatal("Credit not found")
	}
	selfTrailing := `sum(.//Entry[not(@excluded='true')]/Amount) * (1 - 2*count(self::Credit)) < 0`
	ok, err := evaluateXPathBoolean(rec, selfTrailing)
	if err != nil {
		t.Fatalf("evaluateXPathBoolean(self:: trailing): %v", err)
	}
	if !ok {
		t.Fatalf("self:: trailing product gate false; want true (product -20 < 0)")
	}
}
