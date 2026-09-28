package json

import (
	stdjson "encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/antchfx/xpath"
)

func fixtureDir() string {
	return filepath.Join("..", "..", "..", "tests", "fixtures", "docnode", "json")
}

// conformanceRow is one (fixture, xpath, expected) case. Rows with XPath
// select a node set and compare string-values (Expected) and local names
// (Names) of the selected nodes; rows with Eval compare the formatted result
// of evaluating the expression.
type conformanceRow struct {
	Name     string    `json:"name"`
	Fixture  string    `json:"fixture"`
	XPath    string    `json:"xpath"`
	Expected *[]string `json:"expected"`
	Names    []string  `json:"names"`
	Eval     string    `json:"eval"`
	Value    *string   `json:"value"`
}

func TestConformance(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(fixtureDir(), "conformance.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rows []conformanceRow
	if err := stdjson.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("conformance.json: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no conformance rows")
	}
	for _, row := range rows {
		t.Run(row.Name, func(t *testing.T) {
			f, err := os.Open(filepath.Join(fixtureDir(), row.Fixture))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			doc, err := Format{}.Parse(f)
			if err != nil {
				t.Fatalf("Parse %s: %v", row.Fixture, err)
			}
			switch {
			case row.Eval != "":
				if row.Value == nil {
					t.Fatal("eval row without value")
				}
				got := formatResult(xpath.MustCompile(row.Eval).Evaluate(doc.Root().Navigator()))
				if got != *row.Value {
					t.Fatalf("%s = %q, want %q", row.Eval, got, *row.Value)
				}
			case row.XPath != "":
				if row.Expected == nil && row.Names == nil {
					t.Fatal("xpath row without expected or names")
				}
				nodes := selectNodes(t, doc, row.XPath)
				texts, names := []string{}, []string{}
				for _, n := range nodes {
					texts = append(texts, n.Text())
					names = append(names, n.LocalName())
				}
				if row.Expected != nil && !reflect.DeepEqual(texts, *row.Expected) {
					t.Fatalf("%s values %q, want %q", row.XPath, texts, *row.Expected)
				}
				if row.Names != nil && !reflect.DeepEqual(names, row.Names) {
					t.Fatalf("%s names %q, want %q", row.XPath, names, row.Names)
				}
			default:
				t.Fatal("row has neither xpath nor eval")
			}
		})
	}
}

func formatResult(v any) string {
	switch r := v.(type) {
	case float64:
		return strconv.FormatFloat(r, 'f', -1, 64)
	case string:
		return r
	case bool:
		return strconv.FormatBool(r)
	default:
		return fmt.Sprintf("%v", r)
	}
}
