package dsl

import (
	"fmt"
	"strings"
)

// ReferenceTableNames walks a parsed expression and returns the literal table names
// referenced by in_reference / lookup_reference calls anywhere in it (including
// nested in binary/ternary/unary operands and other function arguments).
//
// It enforces the C3 control at config-validation / pre-flight time: a non-literal
// (variable or expression) table-name argument is a loud error — record data must
// never select a run resource. The returned names let the caller verify each table
// is declared in the registry before any file is extracted, so an unknown literal
// table name also fails pre-flight rather than per-record.
func ReferenceTableNames(expr *Expression) ([]string, error) {
	var names []string
	if err := walkReferenceCalls(expr, &names); err != nil {
		return nil, err
	}
	return names, nil
}

func walkReferenceCalls(expr *Expression, names *[]string) error {
	if expr == nil {
		return nil
	}
	switch expr.Type {
	case ExprBinary:
		b, ok := expr.Value.(*BinaryExpression)
		if !ok {
			return nil
		}
		if err := walkReferenceCalls(b.Left, names); err != nil {
			return err
		}
		return walkReferenceCalls(b.Right, names)
	case ExprUnary:
		u, ok := expr.Value.(*UnaryExpression)
		if !ok {
			return nil
		}
		return walkReferenceCalls(u.Operand, names)
	case ExprTernary:
		t, ok := expr.Value.(*TernaryExpression)
		if !ok {
			return nil
		}
		if err := walkReferenceCalls(t.Cond, names); err != nil {
			return err
		}
		if err := walkReferenceCalls(t.Then, names); err != nil {
			return err
		}
		return walkReferenceCalls(t.Else, names)
	case ExprFunction:
		fc, ok := expr.Value.(*FunctionCall)
		if !ok {
			return nil
		}
		switch strings.ToLower(fc.Name) {
		case "in_reference", "lookup_reference":
			if len(fc.Args) < 2 {
				return fmt.Errorf("%s() requires at least a table name and a field argument", strings.ToLower(fc.Name))
			}
			arg0 := fc.Args[0]
			if arg0.Type != ExprConstant {
				return fmt.Errorf("%s() table name must be a string literal, not a variable or expression (record data must not select a reference table)", strings.ToLower(fc.Name))
			}
			name, isStr := arg0.Value.(string)
			if !isStr {
				return fmt.Errorf("%s() table name must be a string literal, got a non-string constant", strings.ToLower(fc.Name))
			}
			*names = append(*names, name)
		}
		for _, a := range fc.Args {
			if err := walkReferenceCalls(a, names); err != nil {
				return err
			}
		}
	}
	return nil
}
