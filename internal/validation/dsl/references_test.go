package dsl

import (
	"strings"
	"testing"
)

// fakeRefs is a stub ReferenceLookup for evaluator tests.
type fakeRefs struct {
	members map[string]map[string]bool   // table -> set
	values  map[string]map[string]string // table -> key -> value
}

func (f fakeRefs) Contains(table, key string) (bool, error) {
	return f.members[table][key], nil
}

func (f fakeRefs) Lookup(table, key string) (string, bool, error) {
	v, ok := f.values[table][key]
	return v, ok, nil
}

func evalWithRefs(t *testing.T, expr string, vars map[string]interface{}, refs ReferenceLookup) (interface{}, error) {
	t.Helper()
	parsed, err := ParseExpression(expr)
	if err != nil {
		t.Fatalf("ParseExpression(%q): %v", expr, err)
	}
	return NewEvaluator(vars, WithReferenceLookup(refs)).EvaluateExpression(parsed)
}

func TestInReference(t *testing.T) {
	refs := fakeRefs{members: map[string]map[string]bool{"curated": {"NM_000001": true}}}

	cases := []struct {
		name    string
		expr    string
		vars    map[string]interface{}
		want    interface{}
		wantErr string
	}{
		{"hit", `in_reference('curated', accession)`, map[string]interface{}{"accession": "NM_000001"}, true, ""},
		{"miss", `in_reference('curated', accession)`, map[string]interface{}{"accession": "XR_999"}, false, ""},
		{"nil field is miss", `in_reference('curated', accession)`, map[string]interface{}{"accession": nil}, false, ""},
		{"empty field is miss", `in_reference('curated', accession)`, map[string]interface{}{"accession": ""}, false, ""},
		{"non-string field", `in_reference('curated', n)`, map[string]interface{}{"n": float64(5)}, nil, "must be a string"},
		{"dynamic table name", `in_reference(tbl, accession)`, map[string]interface{}{"tbl": "curated", "accession": "NM_000001"}, nil, "must be a string literal"},
		{"wrong arity", `in_reference('curated')`, nil, nil, "requires exactly 2 arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalWithRefs(t, tc.expr, tc.vars, refs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLookupReference(t *testing.T) {
	refs := fakeRefs{values: map[string]map[string]string{"mol": {"NM_000001": "mRNA"}}}

	cases := []struct {
		name    string
		expr    string
		vars    map[string]interface{}
		want    interface{}
		wantErr string
	}{
		{"hit", `lookup_reference('mol', accession, 'unknown')`, map[string]interface{}{"accession": "NM_000001"}, "mRNA", ""},
		{"miss returns default", `lookup_reference('mol', accession, 'unknown')`, map[string]interface{}{"accession": "ABSENT"}, "unknown", ""},
		{"nil key returns default", `lookup_reference('mol', accession, 'unknown')`, map[string]interface{}{"accession": nil}, "unknown", ""},
		{"null default", `lookup_reference('mol', accession, null)`, map[string]interface{}{"accession": "ABSENT"}, nil, ""},
		{"non-string default", `lookup_reference('mol', accession, 5)`, map[string]interface{}{"accession": "NM_000001"}, nil, "default must be a string or null"},
		{"dynamic table", `lookup_reference(t, accession, 'x')`, map[string]interface{}{"t": "mol", "accession": "NM_000001"}, nil, "must be a string literal"},
		{"wrong arity", `lookup_reference('mol', accession)`, map[string]interface{}{"accession": "NM_000001"}, nil, "requires exactly 3 arguments"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := evalWithRefs(t, tc.expr, tc.vars, refs)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestReferenceFunctionsWithoutRegistry covers the no-context guard: an evaluator
// with no reference registry errors loudly rather than silently returning false.
func TestReferenceFunctionsWithoutRegistry(t *testing.T) {
	parsed, _ := ParseExpression(`in_reference('curated', accession)`)
	_, err := NewEvaluator(map[string]interface{}{"accession": "NM_1"}).EvaluateExpression(parsed)
	if err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v, want not-available", err)
	}
}

// TestReferenceTableNames covers the pre-flight walker: literal names are extracted
// (including nested), and a dynamic table name is a loud error.
func TestReferenceTableNames(t *testing.T) {
	mustParse := func(s string) *Expression {
		e, err := ParseExpression(s)
		if err != nil {
			t.Fatalf("ParseExpression(%q): %v", s, err)
		}
		return e
	}

	names, err := ReferenceTableNames(mustParse(`(string_length(accession) >= 5) && in_reference('curated', accession)`))
	if err != nil {
		t.Fatalf("ReferenceTableNames: %v", err)
	}
	if len(names) != 1 || names[0] != "curated" {
		t.Errorf("names = %v, want [curated]", names)
	}

	names, err = ReferenceTableNames(mustParse(`in_reference('a', x) ? lookup_reference('b', x, 'd') : 'no'`))
	if err != nil {
		t.Fatalf("ReferenceTableNames nested: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("names = %v, want 2", names)
	}

	if _, err := ReferenceTableNames(mustParse(`in_reference(tbl, accession)`)); err == nil || !strings.Contains(err.Error(), "string literal") {
		t.Errorf("dynamic table name: err = %v, want literal rejection", err)
	}
}
