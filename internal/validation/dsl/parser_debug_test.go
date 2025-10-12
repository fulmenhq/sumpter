package dsl

import "testing"

func TestParseExpressionHandlesLogicalOrGroups(t *testing.T) {
	exprStr := "transaction_link_reason == null || transaction_link_reason != 'cancelled'"
	expr, err := ParseExpression(exprStr)
	if err != nil {
		t.Fatalf("ParseExpression error: %v", err)
	}

	if expr.Type != ExprBinary {
		t.Fatalf("expected binary expression, got type %v", expr.Type)
	}

	bin := expr.Value.(*BinaryExpression)
	if bin.Operator != "||" {
		t.Fatalf("expected operator ||, got %s", bin.Operator)
	}
}
