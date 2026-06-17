package recipes

import (
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestParamValueUnmarshalYAML covers SUM-040 strict parsing of recipe
// defaults.parameters: a scalar string stays scalar; a sequence becomes a strict
// []string; non-string / empty / nested members are rejected loudly.
func TestParamValueUnmarshalYAML(t *testing.T) {
	cases := []struct {
		name      string
		yaml      string
		wantList  bool
		wantValue interface{} // string for scalar, []string for list
		wantErr   string      // substring; empty = no error
	}{
		{name: "scalar string", yaml: `region_id: west`, wantValue: "west"},
		{name: "quoted scalar", yaml: `code: "1234"`, wantValue: "1234"},
		{name: "list of strings", yaml: `prefixes: ["NM_","NR_","NC_"]`, wantList: true, wantValue: []string{"NM_", "NR_", "NC_"}},
		{name: "list preserves order + duplicates", yaml: `p: ["b","a","b"]`, wantList: true, wantValue: []string{"b", "a", "b"}},
		{name: "empty list", yaml: `p: []`, wantList: true, wantValue: []string{}},
		{name: "block sequence", yaml: "p:\n  - NM_\n  - NR_\n", wantList: true, wantValue: []string{"NM_", "NR_"}},
		{name: "number member rejected", yaml: `p: ["NM_", 5]`, wantErr: "must be a string"},
		{name: "bool member rejected", yaml: `p: ["NM_", true]`, wantErr: "must be a string"},
		{name: "empty member rejected", yaml: `p: ["NM_", ""]`, wantErr: "empty string"},
		{name: "nested array rejected", yaml: `p: ["NM_", ["x"]]`, wantErr: "must be a string"},
		{name: "mapping member rejected", yaml: `p: ["NM_", {a: b}]`, wantErr: "must be a string"},
		{name: "mapping value rejected", yaml: "p:\n  a: b\n", wantErr: "must be a string or a list of strings"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m map[string]ParamValue
			err := yaml.Unmarshal([]byte(tc.yaml), &m)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want substring %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var got ParamValue
			for _, v := range m {
				got = v
			}
			if got.IsList() != tc.wantList {
				t.Fatalf("IsList() = %v, want %v", got.IsList(), tc.wantList)
			}
			if tc.wantList {
				want := tc.wantValue.([]string)
				gotList := got.List()
				if len(gotList) != len(want) || (len(want) > 0 && !reflect.DeepEqual(gotList, want)) {
					t.Fatalf("List() = %#v, want %#v", gotList, want)
				}
			} else if got.Scalar() != tc.wantValue.(string) {
				t.Fatalf("Scalar() = %q, want %q", got.Scalar(), tc.wantValue)
			}
		})
	}
}

// TestParamValueIsEmpty pins parameters_required emptiness: an empty scalar is
// "not provided"; any list — even an empty one — counts as provided.
func TestParamValueIsEmpty(t *testing.T) {
	cases := []struct {
		name string
		pv   ParamValue
		want bool
	}{
		{"non-empty scalar", ScalarParam("west"), false},
		{"empty scalar", ScalarParam(""), true},
		{"whitespace scalar", ScalarParam("   "), true},
		{"non-empty list", ListParam([]string{"a"}), false},
		{"empty list is provided", ListParam([]string{}), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pv.IsEmpty(); got != tc.want {
				t.Fatalf("IsEmpty() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParamValueAsScopeIsCopy confirms AsScope returns the right shape and that a
// list scope value is an independent copy (callers must not mutate the parameter).
func TestParamValueAsScope(t *testing.T) {
	if got := ScalarParam("west").AsScope(); got != "west" {
		t.Fatalf("scalar AsScope() = %v, want west", got)
	}
	pv := ListParam([]string{"NM_", "NR_"})
	scope, ok := pv.AsScope().([]string)
	if !ok {
		t.Fatalf("list AsScope() type = %T, want []string", pv.AsScope())
	}
	scope[0] = "MUTATED"
	if pv.List()[0] != "NM_" {
		t.Fatalf("mutating the scope slice mutated the parameter: %v", pv.List())
	}
}
