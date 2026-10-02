package examples

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	inlineURI    = "http://www.xbrl.org/2013/inlineXBRL"
	inline08URI  = "http://www.xbrl.org/2008/inlineXBRL"
	instanceURI  = "http://www.xbrl.org/2003/instance"
	dimensionURI = "http://xbrl.org/2006/xbrldi"
	linkbaseURI  = "http://www.xbrl.org/2003/linkbase"
	xlinkURI     = "http://www.w3.org/1999/xlink"
	xsiURI       = "http://www.w3.org/2001/XMLSchema-instance"
	xmlURI       = "http://www.w3.org/XML/1998/namespace"
)

// This oracle uses only encoding/xml tokens, never Sumpter's parser, XPath,
// config mappings or golden output. Text follows the documented projection:
// decoded descendant character data in order, with outer whitespace trimmed.
type inventoryNode struct {
	name     xml.Name
	attrs    []xml.Attr
	text     strings.Builder
	parent   *inventoryNode
	children []*inventoryNode
}

func inventorySource(t *testing.T, path string) []*inventoryNode {
	t.Helper()
	input, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	decoder := xml.NewDecoder(input)
	var stack, preorder []*inventoryNode
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch token := token.(type) {
		case xml.StartElement:
			node := &inventoryNode{name: token.Name, attrs: token.Attr}
			if len(stack) > 0 {
				node.parent = stack[len(stack)-1]
				node.parent.children = append(node.parent.children, node)
			}
			preorder = append(preorder, node)
			stack = append(stack, node)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.CharData:
			for _, node := range stack {
				node.text.Write(token)
			}
		}
	}
	return preorder
}

func (n *inventoryNode) isInline() bool {
	return n.name.Space == inlineURI || n.name.Space == inline08URI
}

func (n *inventoryNode) addAttrs(data map[string]interface{}, fields map[string]xml.Name) {
	for field, name := range fields {
		for _, attr := range n.attrs {
			if attr.Name == name {
				data[field] = strings.TrimSpace(attr.Value)
			}
		}
	}
}

func lexicalAttrs(names map[string]string) map[string]xml.Name {
	fields := make(map[string]xml.Name, len(names))
	for field, name := range names {
		fields[field] = xml.Name{Local: name}
	}
	return fields
}

func inventoryProjection(n *inventoryNode, profile string) map[string]interface{} {
	data := map[string]interface{}{
		"kind": n.name.Local, "namespace_uri": n.name.Space, "text": strings.TrimSpace(n.text.String()),
	}
	n.addAttrs(data, lexicalAttrs(map[string]string{"id": "id", "target": "target"}))
	switch profile {
	case "facts":
		n.addAttrs(data, lexicalAttrs(map[string]string{
			"name": "name", "context_ref": "contextRef", "unit_ref": "unitRef", "decimals": "decimals",
			"precision": "precision", "scale": "scale", "sign": "sign", "format": "format",
			"continued_at": "continuedAt", "escape": "escape", "footnote_refs": "footnoteRefs",
		}))
		n.addAttrs(data, map[string]xml.Name{"nil": {Space: xsiURI, Local: "nil"}, "language": {Space: xmlURI, Local: "lang"}})
		for _, child := range n.children {
			if child.isInline() && child.name.Local == "numerator" {
				data["numerator_text"] = strings.TrimSpace(child.text.String())
			}
			if child.isInline() && child.name.Local == "denominator" {
				data["denominator_text"] = strings.TrimSpace(child.text.String())
			}
		}
	case "resources":
		n.addAttrs(data, lexicalAttrs(map[string]string{"continued_at": "continuedAt", "footnote_role": "footnoteRole"}))
		n.addAttrs(data, map[string]xml.Name{"language": {Space: xmlURI, Local: "lang"}})
		var visit func(*inventoryNode)
		visit = func(child *inventoryNode) {
			item := map[string]interface{}{"text": strings.TrimSpace(child.text.String())}
			field := ""
			switch {
			case child.name == (xml.Name{Space: instanceURI, Local: "identifier"}) && child.parent.name == (xml.Name{Space: instanceURI, Local: "entity"}) && child.parent.parent == n:
				field = "identifiers"
				// Nested item mappings retain absent values as null.
				item["scheme"] = nil
				child.addAttrs(item, lexicalAttrs(map[string]string{"scheme": "scheme"}))
			case child.parent != nil && child.parent.name == (xml.Name{Space: instanceURI, Local: "period"}) && child.parent.parent == n:
				field = "periods"
				item["kind"], item["namespace_uri"] = child.name.Local, child.name.Space
			case child.name.Space == dimensionURI && (child.name.Local == "explicitMember" || child.name.Local == "typedMember"):
				field = "dimensions"
				item["kind"], item["dimension"] = child.name.Local, nil
				child.addAttrs(item, lexicalAttrs(map[string]string{"dimension": "dimension"}))
			case child.name == (xml.Name{Space: instanceURI, Local: "measure"}):
				field = "measures"
				item["container"] = child.parent.name.Local
			}
			if field != "" {
				items, _ := data[field].([]interface{})
				data[field] = append(items, item)
			}
			for _, next := range child.children {
				visit(next)
			}
		}
		for _, child := range n.children {
			visit(child)
		}
	case "references":
		n.addAttrs(data, lexicalAttrs(map[string]string{
			"from_refs": "fromRefs", "to_refs": "toRefs", "arcrole": "arcrole", "link_role": "linkRole",
			"order": "order", "role_uri": "roleURI", "arcrole_uri": "arcroleURI",
		}))
		n.addAttrs(data, map[string]xml.Name{"href": {Space: xlinkURI, Local: "href"}, "link_type": {Space: xlinkURI, Local: "type"}})
	}
	return data
}

func inventorySelected(n *inventoryNode, profile string) bool {
	switch profile {
	case "facts":
		return n.isInline() && (n.name.Local == "nonFraction" || n.name.Local == "nonNumeric" || n.name.Local == "fraction")
	case "resources":
		return (n.name.Space == instanceURI && (n.name.Local == "context" || n.name.Local == "unit")) ||
			(n.isInline() && (n.name.Local == "continuation" || n.name.Local == "footnote"))
	case "references":
		return (n.isInline() && n.name.Local == "references") || (n.name.Space == inlineURI && n.name.Local == "relationship") ||
			(n.name.Space == linkbaseURI && (n.name.Local == "schemaRef" || n.name.Local == "roleRef" || n.name.Local == "arcroleRef"))
	}
	return false
}

type inventoryRecord struct {
	Runtime struct {
		RecordNum int `json:"record_num"`
	} `json:"_runtime"`
	Extract struct {
		Data map[string]interface{} `json:"data"`
	} `json:"extract"`
}

func readInventoryRecords(t *testing.T, path string) []inventoryRecord {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	var records []inventoryRecord
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record inventoryRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}

// ID buckets hold multisets, not one record per ID. Missing and present-empty
// IDs have separate keys, so neither collisions nor absence can hide data loss.
func inventoryMultiset(t *testing.T, rows []map[string]interface{}) map[string]map[string]int {
	t.Helper()
	result := make(map[string]map[string]int)
	for _, row := range rows {
		id, present := row["id"]
		keyBytes, err := json.Marshal([]interface{}{present, id})
		if err != nil {
			t.Fatal(err)
		}
		bytes, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		key := string(keyBytes)
		if result[key] == nil {
			result[key] = make(map[string]int)
		}
		result[key][string(bytes)]++
	}
	return result
}

func TestInlineXBRLInventory(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("run the inventory harness from source examples")
	}
	root := repoRoot(t)
	caseDir := filepath.Join(root, "examples", "cases", "16-inline-xbrl-inventory")
	input := filepath.Join(caseDir, "input.xml")
	nodes := inventorySource(t, input)
	for _, profile := range []string{"facts", "resources", "references"} {
		t.Run(profile, func(t *testing.T) {
			manifest := profile + ".yaml"
			if profile == "facts" {
				manifest = "recipe.yaml"
			}
			output := t.TempDir()
			cmd := exec.Command(exampleBinary(t, root), "recipes", "run", "extract", filepath.Join(caseDir, "recipe"), "--manifest", manifest, "--files", input,
				"--output-path", output, "--output-pattern", "records.jsonl", "--no-manifest")
			cmd.Dir = root
			if logs, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("inventory run: %v\n%s", err, logs)
			}
			actual := readInventoryRecords(t, filepath.Join(output, "records.jsonl"))
			var want, got []map[string]interface{}
			for _, node := range nodes {
				if inventorySelected(node, profile) {
					want = append(want, inventoryProjection(node, profile))
				}
			}
			for _, record := range actual {
				got = append(got, record.Extract.Data)
			}
			t.Run("duplicate-aware-fidelity", func(t *testing.T) {
				if !reflect.DeepEqual(inventoryMultiset(t, got), inventoryMultiset(t, want)) {
					t.Fatalf("independent projection differs\ngot: %#v\nwant: %#v", got, want)
				}
			})
			t.Run("preorder", func(t *testing.T) {
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("source preorder differs\ngot: %#v\nwant: %#v", got, want)
				}
			})
			t.Run("record-num", func(t *testing.T) {
				for i, record := range actual {
					if record.Runtime.RecordNum != i+1 {
						t.Fatalf("row %d record_num=%d", i+1, record.Runtime.RecordNum)
					}
				}
			})
		})
	}
}

func TestInlineXBRLUnknownEntityRefusal(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("run the inventory harness from source examples")
	}
	root := repoRoot(t)
	caseDir := filepath.Join(root, "examples", "cases", "16-inline-xbrl-inventory")
	input, err := os.ReadFile(filepath.Join(caseDir, "input.xml"))
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, `<!ENTITY nbsp "external">`)
	}))
	defer server.Close()
	// A declaration and reference URL must not trigger external resolution.
	source := strings.Replace(string(input), "&#233;", "&nbsp;", 1)
	source = strings.Replace(source, "<html ", "<!DOCTYPE html SYSTEM \""+server.URL+"/external.dtd\">\n<html ", 1)
	source = strings.ReplaceAll(source, "urn:example:taxonomy", server.URL+"/taxonomy")
	path := filepath.Join(t.TempDir(), "unsupported.xml")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	cmd := exec.Command(exampleBinary(t, root), "recipes", "run", "extract", filepath.Join(caseDir, "recipe"), "--files", path,
		"--output-path", output, "--output-pattern", "records.jsonl")
	cmd.Dir = root
	logs, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(logs), "nbsp") {
		t.Fatalf("expected precise entity refusal, got %v\n%s", err, logs)
	}
	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("pre-commit refusal published output: %v", entries)
	}
	if requests.Load() != 0 {
		t.Fatalf("external DTD/reference fetched: %d requests", requests.Load())
	}
}

func TestInlineXBRLFixtureCoverage(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("run the inventory harness from source examples")
	}
	nodes := inventorySource(t, filepath.Join(repoRoot(t), "examples", "cases", "16-inline-xbrl-inventory", "input.xml"))
	counts := map[string]int{}
	byID := map[string][]map[string]interface{}{}
	for _, node := range nodes {
		for _, profile := range []string{"facts", "resources", "references"} {
			if !inventorySelected(node, profile) {
				continue
			}
			counts[profile]++
			if profile == "facts" {
				row := inventoryProjection(node, profile)
				id, present := row["id"].(string)
				if !present {
					id = "<absent>"
				}
				byID[id] = append(byID[id], row)
			}
		}
	}
	if !reflect.DeepEqual(counts, map[string]int{"facts": 13, "resources": 8, "references": 5}) {
		t.Fatalf("fixture lost inventory coverage: %v", counts)
	}
	if len(byID["dup"]) != 2 || len(byID["<absent>"]) != 1 || len(byID[""]) != 1 {
		t.Fatalf("missing/empty/duplicate ID coverage: %#v", byID)
	}
	checks := []struct{ id, field, value string }{
		{"hidden", "text", "9007199254740993"}, {"hidden", "scale", "-2"}, {"hidden", "sign", "-"},
		{"hidden", "format", "sample:unsupported"}, {"text", "text", "AlphaBetaGammaEXCLUDED"},
		{"text", "continued_at", "cont-a"}, {"text", "language", "en"},
		{"fraction", "kind", "fraction"}, {"fraction", "numerator_text", "9007199254740993"},
		{"fraction", "denominator_text", "7"}, {"outer", "text", "ABC"}, {"inner", "text", "B"},
		{"nil", "nil", "true"}, {"nil", "text", ""}, {"", "format", ""},
		{"legacy", "namespace_uri", inline08URI}, {"legacy", "footnote_refs", "foot-a missing"},
		{"rebound", "name", "sample:Amount"}, {"numeric-reference", "text", "é & text"},
	}
	for _, check := range checks {
		t.Run(check.id+"/"+check.field, func(t *testing.T) {
			rows := byID[check.id]
			if len(rows) != 1 || rows[0][check.field] != check.value {
				t.Fatalf("fixture projection lost %s/%s=%q: %#v", check.id, check.field, check.value, rows)
			}
		})
	}
	if _, present := byID["<absent>"][0]["id"]; present {
		t.Fatal("missing ID became defined")
	}
	if len(byID["collision"]) != 0 || len(byID["foreign"]) != 0 {
		t.Fatal("foreign namespaces selected")
	}
	if byID["dup"][0]["scale"] != "2" || byID["dup"][0]["text"] != "123.4500" || byID["dup"][1]["escape"] != "true" || byID["dup"][1]["text"] != "<b>literal</b>" {
		t.Fatal("scale/lexical number/escaped text coverage changed")
	}
}

func TestInlineXBRLDispatchHelp(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("run the CLI help checks from source examples")
	}
	root := repoRoot(t)
	for _, args := range [][]string{{"--help"}, {"extract", "files", "--help"}} {
		cmd := exec.Command(exampleBinary(t, root), args...)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("help: %v\n%s", err, output)
		}
		text := string(output)
		if !strings.Contains(text, "XML above 100 MiB uses streaming (not force-DOM)") || strings.Contains(text, "XML >1GB") {
			t.Fatalf("misleading XML opt-in help:\n%s", text)
		}
		if len(args) > 1 && (!strings.Contains(text, "strict >100 MiB") || !strings.Contains(text, "Buffered output or applicability without the flag can retain")) {
			t.Fatalf("missing dispatch qualification:\n%s", text)
		}
	}
}

func TestSecEdgarRoleURIExample(t *testing.T) {
	if runningFromEmbeddedMirror() {
		t.Skip("run the recipe regression from source examples")
	}
	root := repoRoot(t)
	dir := t.TempDir()
	input := filepath.Join(dir, "roles.xml")
	fixture := `<schema xmlns:link="http://www.xbrl.org/2003/linkbase"><link:roleType id="statement" roleURI="urn:example:statement"><link:definition>Statement</link:definition></link:roleType></schema>`
	if err := os.WriteFile(input, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	signature := filepath.Join(dir, "signature.yaml")
	if err := os.WriteFile(signature, []byte("signature_id: synthetic_roles\nname: Synthetic roles\nmatch_patterns:\n  - pattern_id: schema\n    name: Schema\n    selector: /schema\n    weight: 1\nconfidence_threshold: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "roles.jsonl")
	cmd := exec.Command(exampleBinary(t, root), "extract", "files", "--files", input, "--signature-config-path", signature,
		"--extract-config-path", filepath.Join(root, "examples", "config", "extract", "sec-edgar-role-extract.yaml"), "--output-path", dir, "--output-pattern", "roles.jsonl", "--no-manifest")
	cmd.Dir = root
	if logs, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("role example: %v\n%s", err, logs)
	}
	records := readInventoryRecords(t, output)
	if len(records) != 1 || records[0].Extract.Data["role_uri"] != "urn:example:statement" {
		t.Fatalf("roleURI is an attribute of roleType: %#v", records)
	}
}
