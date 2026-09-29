package index

import (
	"encoding/json"
	"testing"
)

// TestNamespaceContextWireShape pins the serialized form of namespace
// contexts, which existing XML indexes carry and readers depend on.
func TestNamespaceContextWireShape(t *testing.T) {
	ctx := []NamespaceContext{
		{ID: 0, Declarations: nil},
		{ID: 1, Declarations: NormalizeNamespaceDeclarations([]NamespaceDeclaration{
			{Prefix: "ext", URI: "urn:b"},
			{Prefix: "", URI: "urn:a"},
			{Prefix: "xml", URI: "urn:ignored"},
			{Prefix: "ext", URI: "urn:b2"},
		})},
	}
	got, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"id":0,"declarations":null},{"id":1,"declarations":[{"prefix":"","uri":"urn:a"},{"prefix":"ext","uri":"urn:b2"}]}]`
	if string(got) != want {
		t.Fatalf("namespace wire shape\n got %s\nwant %s", got, want)
	}
}
