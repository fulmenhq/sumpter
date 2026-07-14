package xpath

import (
	"math"
	"testing"
)

// Synthetic ledger-style document for numeric operand context isolation.
// Context node for evaluation is the Credit element.
func createCreditDoc() *TNode {
	// <Root><Credit><Entry><Amount>20</Amount></Entry></Credit></Root>
	doc := createNode("", RootNode)
	root := doc.createChildNode("Root", ElementNode)
	ev := root.createChildNode("Credit", ElementNode)
	line := ev.createChildNode("Entry", ElementNode)
	amt := line.createChildNode("Amount", ElementNode)
	amt.createChildNode("20", TextNode)
	return doc
}

func selectCredit(doc *TNode) *TNode {
	return selectNode(doc, "//Credit")
}

func evalNumberAt(t *testing.T, context *TNode, expr string) float64 {
	t.Helper()
	exp, err := Compile(expr)
	if err != nil {
		t.Fatalf("Compile(%q): %v", expr, err)
	}
	nav := createNavigator(context)
	v := exp.Evaluate(nav)
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("Evaluate(%q) type %T want float64 (value=%v)", expr, v, v)
	}
	return f
}

func assertNear(t *testing.T, got, want float64, expr string) {
	t.Helper()
	if math.IsNaN(got) || math.IsNaN(want) {
		if !(math.IsNaN(got) && math.IsNaN(want)) {
			t.Fatalf("%s: got %v want %v", expr, got, want)
		}
		return
	}
	if got != want {
		t.Fatalf("%s: got %v want %v", expr, got, want)
	}
}

// TestNumericOperatorContextIsolation covers the predicated-sum × context-sensitive
// factor class: predicates move the shared navigator; the right operand must still
// observe the operator's original context.
func TestNumericOperatorContextIsolation(t *testing.T) {
	doc := createCreditDoc()
	ctx := selectCredit(doc)
	if ctx == nil {
		t.Fatal("Credit context not found")
	}

	const (
		predSumLit  = `sum(.//Entry[not(@excluded='true')]/Amount) * -1`
		plainSumCS  = `sum(.//Amount) * (1 - 2*count(self::Credit))`
		predSumCS   = `sum(.//Entry[not(@excluded='true')]/Amount) * (1 - 2*count(self::Credit))`
		factorFirst = `(1 - 2*count(self::Credit)) * sum(.//Entry[not(@excluded='true')]/Amount)`
		// Magnitude > 1 non-identity factor
		predSumMag = `sum(.//Entry[not(@excluded='true')]/Amount) * (3 * count(self::Credit))`
		// Div and minus neighbors sharing numericQuery
		predSumDiv = `sum(.//Entry[not(@excluded='true')]/Amount) div (count(self::Credit) + 1)`
		predSumSub = `sum(.//Entry[not(@excluded='true')]/Amount) - count(self::Credit)`
	)

	// Controls and the defect row (must all be correct after the fix).
	cases := []struct {
		expr string
		want float64
	}{
		{predSumLit, -20},
		{plainSumCS, -20},
		{predSumCS, -20},
		{factorFirst, -20},
		{predSumMag, 60},
		{predSumDiv, 10},
		{predSumSub, 19},
	}
	for _, tc := range cases {
		got := evalNumberAt(t, ctx, tc.expr)
		assertNear(t, got, tc.want, tc.expr)
	}

	// Original context navigator must be unchanged after full evaluation.
	nav := createNavigator(ctx)
	beforeName := nav.LocalName()
	exp := MustCompile(predSumCS)
	_ = exp.Evaluate(nav)
	if nav.LocalName() != beforeName {
		t.Fatalf("context navigator moved after Evaluate: got %q want %q", nav.LocalName(), beforeName)
	}

	// Same compiled expression on different contexts must not contaminate.
	// Second event with amount 5 under a sibling structure:
	// evaluate twice on the same Credit with a fresh navigator each time.
	exp2 := MustCompile(predSumCS)
	g1 := exp2.Evaluate(createNavigator(ctx)).(float64)
	g2 := exp2.Evaluate(createNavigator(ctx)).(float64)
	if g1 != -20 || g2 != -20 {
		t.Fatalf("repeat Evaluate contamination: g1=%v g2=%v want both -20", g1, g2)
	}

	// Empty node-set: sum empty = 0; context-sensitive factor still non-identity.
	emptyExpr := `sum(.//Entry[@missing='yes']/Amount) * (1 - 2*count(self::Credit))`
	gotEmpty := evalNumberAt(t, ctx, emptyExpr)
	assertNear(t, gotEmpty, 0, emptyExpr) // 0 * -1 = 0
}

// TestNumericOperatorNodeSetCoercion ensures a bare node-set left operand still
// converts correctly when the right operand is context-sensitive (eager asNumber
// path after the isolation fix).
func TestNumericOperatorNodeSetCoercion(t *testing.T) {
	doc := createCreditDoc()
	ctx := selectCredit(doc)
	// First Amount node value is 20; times context-sensitive -1.
	expr := `.//Amount * (1 - 2*count(self::Credit))`
	got := evalNumberAt(t, ctx, expr)
	assertNear(t, got, -20, expr)
}
