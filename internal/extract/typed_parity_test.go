package extract_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"
)

// typedRecordsEqual compares two ordered record sequences without the float64
// round-trip used by the golden comparator. Numbers are decoded with UseNumber
// and compared by exact value; records and array items are ordered; object key
// order is ignored; number vs string and null vs absent are distinct.
func typedRecordsEqual(want, got [][]byte) error {
	if len(want) != len(got) {
		return fmt.Errorf("record count %d, want %d", len(got), len(want))
	}
	for i := range want {
		w, err := decodeTyped(want[i])
		if err != nil {
			return fmt.Errorf("record %d: decode want: %w", i, err)
		}
		g, err := decodeTyped(got[i])
		if err != nil {
			return fmt.Errorf("record %d: decode got: %w", i, err)
		}
		if err := typedValueEqual("$", w, g); err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
	}
	return nil
}

func decodeTyped(b []byte) (interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v interface{}
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

func typedValueEqual(path string, want, got interface{}) error {
	switch w := want.(type) {
	case nil:
		if got != nil {
			return fmt.Errorf("%s: got %T, want null", path, got)
		}
	case bool:
		g, ok := got.(bool)
		if !ok || g != w {
			return fmt.Errorf("%s: got %#v, want %v", path, got, w)
		}
	case string:
		g, ok := got.(string)
		if !ok || g != w {
			return fmt.Errorf("%s: got %#v, want string %q", path, got, w)
		}
	case json.Number:
		g, ok := got.(json.Number)
		if !ok {
			return fmt.Errorf("%s: got %#v, want number %s", path, got, w)
		}
		wr, ok1 := new(big.Rat).SetString(w.String())
		gr, ok2 := new(big.Rat).SetString(g.String())
		if !ok1 || !ok2 {
			return fmt.Errorf("%s: unparseable number want=%s got=%s", path, w, g)
		}
		if wr.Cmp(gr) != 0 {
			return fmt.Errorf("%s: got number %s, want %s", path, g, w)
		}
	case []interface{}:
		g, ok := got.([]interface{})
		if !ok {
			return fmt.Errorf("%s: got %T, want array", path, got)
		}
		if len(g) != len(w) {
			return fmt.Errorf("%s: array length %d, want %d", path, len(g), len(w))
		}
		for i := range w {
			if err := typedValueEqual(fmt.Sprintf("%s[%d]", path, i), w[i], g[i]); err != nil {
				return err
			}
		}
	case map[string]interface{}:
		g, ok := got.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: got %T, want object", path, got)
		}
		for k, wv := range w {
			gv, present := g[k]
			if !present {
				return fmt.Errorf("%s.%s: absent, want present", path, k)
			}
			if err := typedValueEqual(path+"."+k, wv, gv); err != nil {
				return err
			}
		}
		for k := range g {
			if _, present := w[k]; !present {
				return fmt.Errorf("%s.%s: present, want absent", path, k)
			}
		}
	default:
		return fmt.Errorf("%s: unexpected decoded type %T", path, want)
	}
	return nil
}

func TestTypedRecordsEqualAdversarial(t *testing.T) {
	recs := func(lines ...string) [][]byte {
		out := make([][]byte, len(lines))
		for i, l := range lines {
			out[i] = []byte(l)
		}
		return out
	}
	for _, tc := range []struct {
		name      string
		want, got [][]byte
		equal     bool
	}{
		{"identical", recs(`{"a":1,"b":[1,2]}`), recs(`{"a":1,"b":[1,2]}`), true},
		{"reordered keys", recs(`{"a":1,"b":{"x":1,"y":2}}`), recs(`{"b":{"y":2,"x":1},"a":1}`), true},
		{"equal value different spelling", recs(`{"p":4.50}`), recs(`{"p":4.5}`), true},
		{"adjacent ints above 2^53", recs(`{"n":9007199254740993}`), recs(`{"n":9007199254740992}`), false},
		{"number vs string", recs(`{"n":1}`), recs(`{"n":"1"}`), false},
		{"null vs absent", recs(`{"a":1,"b":null}`), recs(`{"a":1}`), false},
		{"absent vs null", recs(`{"a":1}`), recs(`{"a":1,"b":null}`), false},
		{"swapped records", recs(`{"id":1}`, `{"id":2}`), recs(`{"id":2}`, `{"id":1}`), false},
		{"swapped array items", recs(`{"l":[1,2]}`), recs(`{"l":[2,1]}`), false},
		{"removed duplicate record", recs(`{"id":1}`, `{"id":1}`), recs(`{"id":1}`), false},
		{"removed duplicate item", recs(`{"l":[1,1]}`), recs(`{"l":[1]}`), false},
		{"bool vs string", recs(`{"b":true}`), recs(`{"b":"true"}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := typedRecordsEqual(tc.want, tc.got)
			if tc.equal && err != nil {
				t.Fatalf("expected equal, got %v", err)
			}
			if !tc.equal && err == nil {
				t.Fatal("expected difference, got equal")
			}
		})
	}

	// Guard the reason this comparator exists: the float64 golden
	// canonicalization cannot see the 2^53 distinction.
	a := canonicalJSON(t, []byte(`{"n":9007199254740993}`))
	b := canonicalJSON(t, []byte(`{"n":9007199254740992}`))
	if !bytes.Equal(a, b) {
		t.Fatalf("float64 canonicalization unexpectedly distinguished 2^53 neighbours: %s vs %s", a, b)
	}
}
