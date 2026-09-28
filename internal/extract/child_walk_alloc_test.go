package extract

import (
	"strings"
	"testing"
)

// findChildrenByNameAll matches element children by name, case-insensitively,
// in document order. Its allocation parity with the pre-seam pointer walk is
// pinned in internal/docnode/xml.
func TestFindChildrenByNameAll(t *testing.T) {
	doc, err := parseXMLDoc(strings.NewReader(`<r><a>1</a><B/><!--c--><A>2</A><c/></r>`))
	if err != nil {
		t.Fatal(err)
	}
	r := findNode(doc, "/r")
	got := findChildrenByNameAll(r, "a")
	if len(got) != 2 || got[0].Text() != "1" || got[1].Text() != "2" {
		t.Fatalf("matches %d, want the two a elements in order", len(got))
	}
	if len(findChildrenByNameAll(r, "zzz")) != 0 {
		t.Fatal("unexpected match")
	}
}
