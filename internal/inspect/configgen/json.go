package configgen

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/antchfx/xpath"

	"github.com/fulmenhq/sumpter/internal/docnode"
	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
)

// Step returns an XPath location step that selects the JSON key name exactly:
// the name itself when it is an NCName, otherwise *[local-name()=LIT].
func Step(name string) string {
	if isNCName(name) {
		if _, err := xpath.Compile("/" + name); err == nil {
			return name
		}
	}
	return "*[local-name()=" + Literal(name) + "]"
}

// Literal returns s as an XPath 1.0 string literal. XPath 1.0 has no escapes:
// s is quoted with ' unless it contains one, then with " unless it contains
// both, and otherwise built with concat().
func Literal(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	if !strings.Contains(s, `"`) {
		return `"` + s + `"`
	}
	parts := strings.Split(s, "'")
	args := make([]string, 0, 2*len(parts)-1)
	for i, p := range parts {
		if i > 0 {
			args = append(args, `"'"`)
		}
		if p != "" {
			args = append(args, "'"+p+"'")
		}
	}
	return "concat(" + strings.Join(args, ",") + ")"
}

// relativeXPath joins the steps for segments with "/".
func relativeXPath(segments []string) string {
	steps := make([]string, len(segments))
	for i, s := range segments {
		steps[i] = Step(s)
	}
	return strings.Join(steps, "/")
}

// isNCName reports whether s is an XML NCName: an XML Name without ':'.
func isNCName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 {
			if !isNameStartChar(r) {
				return false
			}
		} else if !isNameChar(r) {
			return false
		}
	}
	return true
}

func isNameStartChar(r rune) bool {
	switch {
	case r == '_', 'A' <= r && r <= 'Z', 'a' <= r && r <= 'z':
		return true
	case 0xC0 <= r && r <= 0xD6, 0xD8 <= r && r <= 0xF6, 0xF8 <= r && r <= 0x2FF,
		0x370 <= r && r <= 0x37D, 0x37F <= r && r <= 0x1FFF, 0x200C <= r && r <= 0x200D,
		0x2070 <= r && r <= 0x218F, 0x2C00 <= r && r <= 0x2FEF, 0x3001 <= r && r <= 0xD7FF,
		0xF900 <= r && r <= 0xFDCF, 0xFDF0 <= r && r <= 0xFFFD, 0x10000 <= r && r <= 0xEFFFF:
		return true
	}
	return false
}

func isNameChar(r rune) bool {
	switch {
	case isNameStartChar(r), r == '-', r == '.', '0' <= r && r <= '9', r == 0xB7,
		0x300 <= r && r <= 0x36F, 0x203F <= r && r <= 0x2040:
		return true
	}
	return false
}

// jsonField gathers one key path's occurrences inside records.
type jsonField struct {
	segments     []string
	total        int
	recordsSeen  int
	maxPerRecord int
	strings      int
	numbers      int
	bools        int
	nulls        int
	containers   int
	nonInteger   bool
	samples      []string
}

// GenerateJSON emits a starter extract.yaml for one JSON document. The document
// is parsed as extraction parses it, and every selector is built from key names
// with Step, so it selects exactly the keys in the sample.
func GenerateJSON(r io.Reader, opts Options) (*Result, error) {
	applyDefaults(&opts)
	doc, err := docjson.Format{}.Parse(r)
	if err != nil {
		return nil, err
	}
	root := doc.Root()

	selector, records, warnings, err := jsonRecords(root, opts)
	if err != nil {
		return nil, err
	}
	scope := signatureScope{}
	if strings.TrimSpace(opts.RecordSelector) == "" {
		var warning string
		scope, warning = recordScope(root, records)
		if scope.record {
			selector = scope.selector
		} else if warning != "" {
			warnings = append(warnings, warning)
		}
	} else if streaming.ValidateRecordSelector(selector) == nil {
		if name := commonLocalName(records); name != "" {
			scope = signatureScope{record: true, selector: selector, name: name}
		}
	}
	fields := scanJSONRecords(records)
	mappings, skipped := jsonMappings(fields, len(records), opts)

	recordType := "generated_record"
	if name := commonLocalName(records); name != "" {
		recordType = snakeCase(name)
	}

	var out strings.Builder
	writeJSONHeader(&out, opts, rootSelector(root), scope, len(records), len(mappings), skipped, warnings)
	writeConfigBody(&out, recordType, selector, mappings)
	if skipped > 0 {
		fmt.Fprintf(&out, "\n# %d sparse detected field(s) were skipped; set --min-occurrence 1 to include them.\n", skipped)
	}
	return &Result{
		YAML:           []byte(out.String()),
		RecordSelector: selector,
		RecordCount:    len(records),
		SkippedSparse:  skipped,
		Warnings:       warnings,
	}, nil
}

// jsonRecords picks the record selector, or uses the override, and returns the
// records it selects in the sample.
func jsonRecords(root docnode.Node, opts Options) (string, []docnode.Node, []string, error) {
	var warnings []string
	selector := strings.TrimSpace(opts.RecordSelector)
	if selector == "" {
		selector, warnings = inferJSONSelector(root, opts.MinOccurrence)
	}
	expr, err := xpath.Compile(selector)
	if err != nil {
		return "", nil, nil, fmt.Errorf("invalid record selector %q: %w", selector, err)
	}
	var records []docnode.Node
	it := expr.Select(root.Navigator())
	for it.MoveNext() {
		if it.Current().NodeType() != xpath.ElementNode {
			continue
		}
		if n, ok := (docjson.Format{}).NodeOf(it.Current()); ok {
			records = append(records, n)
		}
	}
	if len(records) == 0 {
		return "", nil, nil, fmt.Errorf("record selector %q did not match any element in the sample", selector)
	}
	return selector, records, warnings, nil
}

// inferJSONSelector chooses the most-repeated object path at the shallowest
// depth that repeats at least minOccurrence times.
func inferJSONSelector(root docnode.Node, minOccurrence int) (string, []string) {
	type pathStat struct {
		segments   []string
		count      int
		containers int
	}
	stats := map[string]*pathStat{}
	var visit func(n docnode.Node, segments []string)
	visit = func(n docnode.Node, segments []string) {
		for c, ok := n.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
			segs := append(append([]string(nil), segments...), c.LocalName())
			key := strings.Join(segs, "\x00")
			ps := stats[key]
			if ps == nil {
				ps = &pathStat{segments: segs}
				stats[key] = ps
			}
			ps.count++
			if isContainer(c) {
				ps.containers++
				visit(c, segs)
			}
		}
	}
	visit(root, nil)

	var best *pathStat
	for _, ps := range stats {
		if ps.count < minOccurrence || ps.containers != ps.count {
			continue
		}
		switch {
		case best == nil,
			len(ps.segments) < len(best.segments),
			len(ps.segments) == len(best.segments) && ps.count > best.count,
			len(ps.segments) == len(best.segments) && ps.count == best.count &&
				strings.Join(ps.segments, "\x00") < strings.Join(best.segments, "\x00"):
			best = ps
		}
	}
	if best != nil {
		return "/" + relativeXPath(best.segments), nil
	}
	if only, ok := singleTopLevel(root); ok && isContainer(only) {
		return "/" + Step(only.LocalName()), []string{"could not confidently detect record element; using the top-level element"}
	}
	return "/*", []string{"could not confidently detect record element; using every top-level element"}
}

func isContainer(n docnode.Node) bool {
	_, scalar := n.(docnode.Scalar)
	return !scalar
}

func singleTopLevel(root docnode.Node) (docnode.Node, bool) {
	first, ok := root.FirstElementChild()
	if !ok {
		return nil, false
	}
	if _, more := first.NextElementSibling(); more {
		return nil, false
	}
	return first, true
}

// signatureScope is the scope the generated signature declares. A
// record-scoped signature holds on the whole-document and streaming routes
// alike; it needs a record selector of the form Name or //Name.
type signatureScope struct {
	record   bool
	selector string // the record selector, "//" + name
	name     string // the record element's local name
}

// recordScope returns a record-scoped signature for the inferred records when
// "//" + their name selects exactly those records in the sample; otherwise it
// explains why the signature keeps document scope.
func recordScope(root docnode.Node, records []docnode.Node) (signatureScope, string) {
	const fallback = "; the signature keeps match_scope: document, so an input above the large-file threshold needs --allow-large-files"
	name := commonLocalName(records)
	if name == "" {
		return signatureScope{}, "the records have different names" + fallback
	}
	candidate := "//" + name
	if streaming.ValidateRecordSelector(candidate) != nil {
		return signatureScope{}, fmt.Sprintf("the record name %q cannot be a streaming record selector", name) + fallback
	}
	expr, err := xpath.Compile(candidate)
	if err != nil {
		return signatureScope{}, fmt.Sprintf("the record name %q cannot be a streaming record selector", name) + fallback
	}
	var same []docnode.Node
	it := expr.Select(root.Navigator())
	for it.MoveNext() {
		if n, ok := (docjson.Format{}).NodeOf(it.Current()); ok {
			same = append(same, n)
		}
	}
	if len(same) != len(records) {
		return signatureScope{}, fmt.Sprintf("%s also selects elements outside the detected records", candidate) + fallback
	}
	for i := range same {
		if same[i] != records[i] {
			return signatureScope{}, fmt.Sprintf("%s also selects elements outside the detected records", candidate) + fallback
		}
	}
	return signatureScope{record: true, selector: candidate, name: name}, ""
}

// rootSelector is the signature's match selector: the top-level key when the
// document has exactly one, otherwise any top-level element.
func rootSelector(root docnode.Node) string {
	if only, ok := singleTopLevel(root); ok {
		return "/" + Step(only.LocalName())
	}
	return "/*"
}

func commonLocalName(records []docnode.Node) string {
	name := records[0].LocalName()
	for _, r := range records[1:] {
		if r.LocalName() != name {
			return ""
		}
	}
	return name
}

// scanJSONRecords gathers every key path below each record, relative to it.
func scanJSONRecords(records []docnode.Node) map[string]*jsonField {
	fields := map[string]*jsonField{}
	for _, rec := range records {
		counts := map[string]int{}
		var visit func(n docnode.Node, segments []string)
		visit = func(n docnode.Node, segments []string) {
			for c, ok := n.FirstElementChild(); ok; c, ok = c.NextElementSibling() {
				segs := append(append([]string(nil), segments...), c.LocalName())
				key := strings.Join(segs, "\x00")
				f := fields[key]
				if f == nil {
					f = &jsonField{segments: segs}
					fields[key] = f
				}
				counts[key]++
				f.total++
				sc, scalar := c.(docnode.Scalar)
				if !scalar {
					f.containers++
					visit(c, segs)
					continue
				}
				switch sc.Kind() {
				case docnode.ScalarNull:
					f.nulls++
					continue
				case docnode.ScalarNumber:
					f.numbers++
					if !integerPattern.MatchString(c.Text()) {
						f.nonInteger = true
					}
				case docnode.ScalarBool:
					f.bools++
				default:
					f.strings++
				}
				if len(f.samples) < 5 {
					f.samples = append(f.samples, truncateSample(c.Text()))
				}
			}
		}
		visit(rec, nil)
		for key, n := range counts {
			f := fields[key]
			f.recordsSeen++
			if n > f.maxPerRecord {
				f.maxPerRecord = n
			}
		}
	}
	return fields
}

// jsonMappings builds field mappings: a path that repeats within a record
// becomes an array (with an item mapping when its items are objects), and
// every other scalar path becomes a field. Object paths that do not repeat
// contribute only through their scalar descendants.
func jsonMappings(fields map[string]*jsonField, recordCount int, opts Options) ([]mapping, int) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var arrays []*jsonField
	under := func(f, arr *jsonField) bool {
		return len(f.segments) > len(arr.segments) && strings.HasPrefix(strings.Join(f.segments, "\x00"), strings.Join(arr.segments, "\x00")+"\x00")
	}
	underAny := func(f *jsonField) bool {
		for _, arr := range arrays {
			if f == arr || under(f, arr) {
				return true
			}
		}
		return false
	}
	for _, k := range keys {
		f := fields[k]
		if f.maxPerRecord > 1 && f.total >= opts.MinOccurrence && !underAny(f) {
			arrays = append(arrays, f)
		}
	}

	names := map[string]int{}
	skipped := 0
	var mappings []mapping
	for _, arr := range arrays {
		m := mapping{
			Name:    uniqueName(snakeCase(arr.segments[len(arr.segments)-1]), names),
			XPath:   relativeXPath(arr.segments),
			Type:    "array",
			Comment: optionalComment(&fieldStats{RecordsSeen: arr.recordsSeen}, recordCount, opts.OptionalThreshold),
		}
		if arr.containers > 0 {
			childNames := map[string]int{}
			for _, k := range keys {
				f := fields[k]
				if !under(f, arr) || f.containers > 0 || f.total < opts.MinOccurrence {
					continue
				}
				rel := f.segments[len(arr.segments):]
				child := jsonFieldMapping(f, rel, 1, recordCount, opts, childNames)
				m.Children = append(m.Children, child)
			}
		}
		mappings = append(mappings, m)
	}
	for _, k := range keys {
		f := fields[k]
		if f.containers > 0 || underAny(f) {
			continue
		}
		if f.total < opts.MinOccurrence {
			skipped++
			continue
		}
		mappings = append(mappings, jsonFieldMapping(f, f.segments, f.maxPerRecord, recordCount, opts, names))
	}
	return mappings, skipped
}

func jsonFieldMapping(f *jsonField, rel []string, maxPerRecord, recordCount int, opts Options, names map[string]int) mapping {
	typeName, note := jsonType(f)
	if maxPerRecord > 1 {
		typeName = "array"
	}
	comment := optionalComment(&fieldStats{RecordsSeen: f.recordsSeen}, recordCount, opts.OptionalThreshold)
	if typeName == "string" && hasDateShape(f.samples) {
		comment = joinComment(comment, "date-shaped; consider transform if downstream consumers need dates")
	}
	comment = joinComment(comment, note)
	return mapping{
		Name:    uniqueName(snakeCase(rel[len(rel)-1]), names),
		XPath:   relativeXPath(rel),
		Type:    typeName,
		Comment: comment,
	}
}

// jsonType infers a field type from the JSON value kinds seen.
func jsonType(f *jsonField) (string, string) {
	kinds := 0
	for _, n := range []int{f.strings, f.numbers, f.bools} {
		if n > 0 {
			kinds++
		}
	}
	switch {
	case kinds == 0:
		return "string", "only null values in the sample; review inferred string type"
	case kinds > 1:
		return "string", "mixed value kinds; review inferred string type"
	case f.numbers > 0 && f.nonInteger:
		return "number", ""
	case f.numbers > 0:
		return "integer", ""
	case f.bools > 0:
		return "boolean", ""
	}
	return "string", ""
}

func writeJSONHeader(out *strings.Builder, opts Options, rootSel string, scope signatureScope, recordCount, emitted, skipped int, warnings []string) {
	fmt.Fprintf(out, "# Auto-generated by `sumpter inspect --input-format json --generate-config` on %s.\n", opts.GeneratedAt.UTC().Format(time.RFC3339))
	if opts.SourcePath != "" {
		fmt.Fprintf(out, "# Source sample: %s\n", opts.SourcePath)
	}
	out.WriteString("# This is a STARTING POINT. Review before production use:\n")
	out.WriteString("#   - rename output_field values to match downstream consumer expectations\n")
	out.WriteString("#   - add validation_metadata for invariants the data must satisfy\n")
	out.WriteString("#   - add output_schema.required for fields that must be non-empty\n")
	out.WriteString("#   - consider derived fields via expression: for convenience sums\n")
	out.WriteString("#   - inject operational identifiers via defaults.parameters when needed\n")
	out.WriteString("#   - consider filename or path captures for fields encoded outside the JSON\n")
	fmt.Fprintf(out, "# Generated against %d record(s); emitted %d top-level field mapping(s); skipped %d sparse path(s).\n", recordCount, emitted, skipped)
	for _, warning := range warnings {
		fmt.Fprintf(out, "# WARNING: %s.\n", warning)
	}

	signature, config := "<name>-signature.yaml", "<this file>"
	if opts.ConfigPath != "" {
		signature = strings.TrimSuffix(opts.ConfigPath, filepath.Ext(opts.ConfigPath)) + "-signature.yaml"
		config = opts.ConfigPath
	}
	input := "<input.json>"
	if opts.SourcePath != "" {
		input = opts.SourcePath
	}
	out.WriteString("#\n")
	out.WriteString("# JSON input is declared by the signature's format_type, not by this file.\n")
	out.WriteString("# First run:\n")
	fmt.Fprintf(out, "#   1. Copy the signature block below to %s, removing \"# \" from each line.\n", signature)
	fmt.Fprintf(out, "#   2. sumpter extract files --signature-config-path %s --extract-config-path %s --files %s --output-path extract-out\n",
		shellWord(signature, opts.ConfigPath == ""), shellWord(config, opts.ConfigPath == ""), shellWord(input, opts.SourcePath == ""))
	out.WriteString("#\n")
	out.WriteString("# --- signature ---\n")
	out.WriteString("# signature_id: \"generated-json\"\n")
	out.WriteString("# name: \"Generated JSON signature\"\n")
	out.WriteString("# format_type: json\n")
	if scope.record {
		// Evaluated against each record, so it holds on both routes.
		out.WriteString("# match_scope: record\n")
		out.WriteString("# match_patterns:\n")
		out.WriteString("#   - pattern_id: \"record\"\n")
		out.WriteString("#     name: \"Record element\"\n")
		fmt.Fprintf(out, "#     selector: %s\n", quote("//"+scope.name))
	} else {
		out.WriteString("# match_patterns:\n")
		out.WriteString("#   - pattern_id: \"root\"\n")
		out.WriteString("#     name: \"Document root\"\n")
		fmt.Fprintf(out, "#     selector: %s\n", quote(rootSel))
	}
	out.WriteString("#     weight: 1\n")
	out.WriteString("# confidence_threshold: 1\n")
	out.WriteString("# --- end signature ---\n")
	out.WriteString("#\n")
	out.WriteString("# --- recipe manifest fragment (recipe.yaml) ---\n")
	out.WriteString("# defaults:\n")
	out.WriteString("#   input:\n")
	out.WriteString("#     format: json\n")
	out.WriteString("# --- end recipe manifest fragment ---\n")
	out.WriteString("\n")
}

// shellWord quotes s for a POSIX shell unless it is a placeholder or made
// only of characters that need no quoting.
func shellWord(s string, placeholder bool) string {
	if placeholder {
		return s
	}
	for _, r := range s {
		if !shellSafe(r) {
			return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
		}
	}
	return s
}

func shellSafe(r rune) bool {
	switch {
	case r == '/', r == '.', r == '_', r == '-':
		return true
	case 'a' <= r && r <= 'z', 'A' <= r && r <= 'Z', '0' <= r && r <= '9':
		return true
	}
	return false
}
