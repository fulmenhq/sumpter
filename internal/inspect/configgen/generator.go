package configgen

import (
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Options struct {
	SourcePath        string
	RecordSelector    string
	MinOccurrence     int
	OptionalThreshold float64
	GeneratedAt       time.Time
}

type Result struct {
	YAML           []byte
	RecordSelector string
	RecordCount    int
	SkippedSparse  int
	Warnings       []string
}

type ReaderFactory func() (io.ReadCloser, error)

type pathStats struct {
	Path       string
	Count      int
	Depth      int
	Samples    []string
	Attributes map[string]int
	Children   map[string]bool
}

type documentStats struct {
	Paths map[string]*pathStats
	Root  string
}

type recordPath struct {
	Dot        string
	Local      string
	Parts      []string
	AttrEquals map[string]string
}

type recordScan struct {
	RecordCount int
	Fields      map[string]*fieldStats
	Attrs       map[string]*fieldStats
}

type fieldStats struct {
	Path         string
	Samples      []string
	Total        int
	RecordsSeen  int
	MaxPerRecord int
}

type mapping struct {
	Name     string
	XPath    string
	Type     string
	Comment  string
	Children []mapping
}

var (
	integerPattern = regexp.MustCompile(`^[+-]?[0-9]+$`)
	numberPattern  = regexp.MustCompile(`^[+-]?((\d+\.\d*)|(\d*\.\d+))$`)
	datePattern    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}([T ][0-9:.+-Z]*)?$`)
	wordBoundary   = regexp.MustCompile(`([a-z0-9])([A-Z])`)
	nonNameChar    = regexp.MustCompile(`[^a-zA-Z0-9]+`)
	selectorTerm   = regexp.MustCompile(`^(?:\.?//)?([A-Za-z_][A-Za-z0-9_.:-]*)(?:\[\s*@([A-Za-z_][A-Za-z0-9_.:-]*)\s*=\s*(?:"([^"]*)"|'([^']*)')\s*\])?$`)
)

func Generate(readerFactory ReaderFactory, opts Options) (*Result, error) {
	if readerFactory == nil {
		return nil, fmt.Errorf("reader factory is required")
	}
	if opts.MinOccurrence <= 0 {
		opts.MinOccurrence = 2
	}
	if opts.OptionalThreshold <= 0 || opts.OptionalThreshold > 1 {
		opts.OptionalThreshold = 0.5
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}

	reader, err := readerFactory()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	doc, err := scanDocument(reader)
	if err != nil {
		return nil, err
	}

	selector, records, warnings, err := inferRecordSelector(doc, opts.RecordSelector, opts.MinOccurrence)
	if err != nil {
		return nil, err
	}

	reader, err = readerFactory()
	if err != nil {
		return nil, err
	}
	defer func() { _ = reader.Close() }()
	recScan, err := scanRecords(reader, records)
	if err != nil {
		return nil, err
	}
	if recScan.RecordCount == 0 && len(records) > 0 && records[0].Dot == doc.Root {
		recScan.RecordCount = 1
	}

	mappings, skipped := buildMappings(recScan, opts)
	var out strings.Builder
	writeHeader(&out, opts, recScan.RecordCount, len(mappings), skipped, warnings)
	writeConfig(&out, selector, records, mappings)
	if skipped > 0 {
		fmt.Fprintf(&out, "\n# %d sparse detected field(s) were skipped; set --min-occurrence 1 to include them.\n", skipped)
	}

	return &Result{
		YAML:           []byte(out.String()),
		RecordSelector: selector,
		RecordCount:    recScan.RecordCount,
		SkippedSparse:  skipped,
		Warnings:       warnings,
	}, nil
}

func scanDocument(reader io.Reader) (*documentStats, error) {
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	stats := &documentStats{Paths: make(map[string]*pathStats)}
	var stack []string
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parsing error: %w", err)
		}
		switch el := token.(type) {
		case xml.StartElement:
			stack = append(stack, el.Name.Local)
			current := strings.Join(stack, ".")
			if stats.Root == "" {
				stats.Root = current
			}
			ps := ensurePath(stats.Paths, current, len(stack))
			ps.Count++
			if len(stack) > 1 {
				parent := strings.Join(stack[:len(stack)-1], ".")
				ensurePath(stats.Paths, parent, len(stack)-1).Children[current] = true
			}
			for _, attr := range el.Attr {
				ps.Attributes[attr.Name.Local]++
			}
		case xml.CharData:
			text := strings.TrimSpace(string(el))
			if text != "" && len(stack) > 0 {
				current := strings.Join(stack, ".")
				ps := ensurePath(stats.Paths, current, len(stack))
				if len(ps.Samples) < 5 {
					ps.Samples = append(ps.Samples, truncateSample(text))
				}
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return stats, nil
}

func ensurePath(paths map[string]*pathStats, p string, depth int) *pathStats {
	ps := paths[p]
	if ps == nil {
		ps = &pathStats{
			Path:       p,
			Depth:      depth,
			Attributes: make(map[string]int),
			Children:   make(map[string]bool),
		}
		paths[p] = ps
	}
	return ps
}

func inferRecordSelector(doc *documentStats, override string, minOccurrence int) (string, []recordPath, []string, error) {
	if strings.TrimSpace(override) != "" {
		records, err := recordPathsForSelector(doc, override)
		if err != nil {
			return "", nil, nil, err
		}
		if len(records) == 0 {
			return "", nil, nil, fmt.Errorf("record selector %q did not match any element names in sample", override)
		}
		return override, records, nil, nil
	}

	type candidate struct {
		Path  string
		Count int
		Depth int
	}
	byDepth := make(map[int][]candidate)
	for p, ps := range doc.Paths {
		if p == doc.Root || ps.Count < minOccurrence {
			continue
		}
		byDepth[ps.Depth] = append(byDepth[ps.Depth], candidate{Path: p, Count: ps.Count, Depth: ps.Depth})
	}
	if len(byDepth) == 0 {
		rootLocal := lastPart(doc.Root)
		return "//" + rootLocal, []recordPath{{Dot: doc.Root, Local: rootLocal, Parts: strings.Split(doc.Root, ".")}},
			[]string{"could not confidently detect record element; using document root"}, nil
	}

	var depths []int
	for depth := range byDepth {
		depths = append(depths, depth)
	}
	sort.Ints(depths)

	var chosen []candidate
	for _, depth := range depths {
		group := byDepth[depth]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Count == group[j].Count {
				return group[i].Path < group[j].Path
			}
			return group[i].Count > group[j].Count
		})
		chosen = group
		break
	}

	top := chosen[0].Count
	var comparable []candidate
	for _, c := range chosen {
		if float64(c.Count) >= float64(top)*0.75 {
			comparable = append(comparable, c)
		}
	}
	sort.Slice(comparable, func(i, j int) bool { return comparable[i].Path < comparable[j].Path })

	var records []recordPath
	var selectors []string
	for _, c := range comparable {
		local := lastPart(c.Path)
		records = append(records, recordPath{Dot: c.Path, Local: local, Parts: strings.Split(c.Path, ".")})
		selectors = append(selectors, "//"+local)
	}
	selector := strings.Join(selectors, " | ")
	if len(records) > 1 {
		return selector, records, []string{"multiple comparable record elements detected; using a union selector"}, nil
	}
	return selector, records, nil, nil
}

func recordPathsForSelector(doc *documentStats, selector string) ([]recordPath, error) {
	terms, err := parseSelectorTerms(selector)
	if err != nil {
		return nil, err
	}
	var records []recordPath
	for p := range doc.Paths {
		local := lastPart(p)
		for _, term := range terms {
			if term.Local == local {
				records = append(records, recordPath{
					Dot:        p,
					Local:      local,
					Parts:      strings.Split(p, "."),
					AttrEquals: term.AttrEquals,
				})
			}
		}
	}
	sort.Slice(records, func(i, j int) bool {
		if len(records[i].Parts) == len(records[j].Parts) {
			return records[i].Dot < records[j].Dot
		}
		return len(records[i].Parts) < len(records[j].Parts)
	})
	return records, nil
}

func parseSelectorTerms(selector string) ([]recordPath, error) {
	parts := strings.Split(selector, "|")
	var terms []recordPath
	seen := map[string]bool{}
	for _, part := range parts {
		s := strings.TrimSpace(part)
		if s == "" {
			return nil, fmt.Errorf("empty selector term in %q", selector)
		}
		matches := selectorTerm.FindStringSubmatch(s)
		if matches == nil {
			return nil, fmt.Errorf("unsupported record selector %q; supported forms are //Element, //Element[@attr=\"value\"], and unions of those", selector)
		}
		local := localName(matches[1])
		term := recordPath{Local: local}
		if matches[2] != "" {
			value := matches[3]
			if value == "" {
				value = matches[4]
			}
			term.AttrEquals = map[string]string{localName(matches[2]): value}
		}
		key := term.Local + fmt.Sprintf("%v", term.AttrEquals)
		if !seen[key] {
			terms = append(terms, term)
			seen[key] = true
		}
	}
	if len(terms) == 0 {
		return nil, fmt.Errorf("unsupported record selector %q; supported forms are //Element, //Element[@attr=\"value\"], and unions of those", selector)
	}
	return terms, nil
}

func scanRecords(reader io.Reader, records []recordPath) (*recordScan, error) {
	decoder := xml.NewDecoder(reader)
	decoder.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	scan := &recordScan{
		Fields: make(map[string]*fieldStats),
		Attrs:  make(map[string]*fieldStats),
	}
	var stack []string
	inRecord := false
	recordDepth := 0
	var recordParts []string
	currentCounts := map[string]int{}
	currentAttrCounts := map[string]int{}

	finishRecord := func() {
		scan.RecordCount++
		for p, count := range currentCounts {
			fs := ensureField(scan.Fields, p)
			fs.Total += count
			fs.RecordsSeen++
			if count > fs.MaxPerRecord {
				fs.MaxPerRecord = count
			}
		}
		for p, count := range currentAttrCounts {
			fs := ensureField(scan.Attrs, p)
			fs.Total += count
			fs.RecordsSeen++
			if count > fs.MaxPerRecord {
				fs.MaxPerRecord = count
			}
		}
		currentCounts = map[string]int{}
		currentAttrCounts = map[string]int{}
	}

	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parsing error: %w", err)
		}
		switch el := token.(type) {
		case xml.StartElement:
			stack = append(stack, el.Name.Local)
			if !inRecord && matchesRecordPath(stack, el, records) {
				inRecord = true
				recordDepth = len(stack)
				recordParts = append([]string(nil), stack...)
			}
			if inRecord {
				rel := relativePath(recordParts, stack)
				if rel != "" {
					currentCounts[rel]++
				}
				for _, attr := range el.Attr {
					attrPath := "@" + attr.Name.Local
					if rel != "" {
						attrPath = rel + "/@" + attr.Name.Local
					}
					currentAttrCounts[attrPath]++
					fs := ensureField(scan.Attrs, attrPath)
					if len(fs.Samples) < 5 {
						fs.Samples = append(fs.Samples, truncateSample(strings.TrimSpace(attr.Value)))
					}
				}
			}
		case xml.CharData:
			text := strings.TrimSpace(string(el))
			if inRecord && text != "" {
				rel := relativePath(recordParts, stack)
				if rel != "" {
					fs := ensureField(scan.Fields, rel)
					if len(fs.Samples) < 5 {
						fs.Samples = append(fs.Samples, truncateSample(text))
					}
				}
			}
		case xml.EndElement:
			if inRecord && len(stack) == recordDepth {
				finishRecord()
				inRecord = false
				recordDepth = 0
				recordParts = nil
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return scan, nil
}

func ensureField(fields map[string]*fieldStats, p string) *fieldStats {
	fs := fields[p]
	if fs == nil {
		fs = &fieldStats{Path: p}
		fields[p] = fs
	}
	return fs
}

func matchesRecordPath(stack []string, el xml.StartElement, records []recordPath) bool {
	for _, record := range records {
		if record.Dot == "" {
			if record.Local == el.Name.Local && attrsMatch(el.Attr, record.AttrEquals) {
				return true
			}
			continue
		}
		if len(stack) != len(record.Parts) {
			continue
		}
		match := true
		for i := range stack {
			if stack[i] != record.Parts[i] {
				match = false
				break
			}
		}
		if match && attrsMatch(el.Attr, record.AttrEquals) {
			return true
		}
	}
	return false
}

func attrsMatch(attrs []xml.Attr, expected map[string]string) bool {
	if len(expected) == 0 {
		return true
	}
	actual := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		actual[attr.Name.Local] = attr.Value
	}
	for name, value := range expected {
		if actual[name] != value {
			return false
		}
	}
	return true
}

func relativePath(recordParts, stack []string) string {
	if len(stack) <= len(recordParts) {
		return ""
	}
	return strings.Join(stack[len(recordParts):], "/")
}

func buildMappings(scan *recordScan, opts Options) ([]mapping, int) {
	scalarPaths := make(map[string]*fieldStats)
	for p, fs := range scan.Fields {
		if len(fs.Samples) > 0 {
			scalarPaths[p] = fs
		}
	}

	arrayPaths := findArrayPaths(scan, scalarPaths, opts.MinOccurrence)
	var mappings []mapping
	usedNames := map[string]int{}
	skipped := 0

	for _, arrPath := range arrayPaths {
		fs := scan.Fields[arrPath]
		if fs == nil {
			fs = &fieldStats{Path: arrPath}
		}
		if fs.Total > 0 && fs.Total < opts.MinOccurrence {
			skipped++
			continue
		}
		children := childMappingsForArray(arrPath, scalarPaths, scan.Attrs, opts, usedNames, scan.RecordCount)
		m := mapping{
			Name:     uniqueName(snakeCase(lastSlashPart(arrPath)), usedNames),
			XPath:    arrPath,
			Type:     "array",
			Children: children,
			Comment:  optionalComment(fs, scan.RecordCount, opts.OptionalThreshold),
		}
		mappings = append(mappings, m)
	}

	isUnderArray := func(p string) bool {
		for _, arr := range arrayPaths {
			if p == arr || strings.HasPrefix(p, arr+"/") {
				return true
			}
		}
		return false
	}

	var fieldPaths []string
	for p := range scalarPaths {
		if !isUnderArray(p) {
			fieldPaths = append(fieldPaths, p)
		}
	}
	for p := range scan.Attrs {
		if !isUnderArray(strings.TrimSuffix(strings.Split(p, "/@")[0], "/")) {
			fieldPaths = append(fieldPaths, p)
		}
	}
	sort.Strings(fieldPaths)

	for _, p := range fieldPaths {
		fs := scalarPaths[p]
		if fs == nil {
			fs = scan.Attrs[p]
		}
		if fs == nil {
			continue
		}
		if fs.Total < opts.MinOccurrence {
			skipped++
			continue
		}
		mappings = append(mappings, fieldMapping(p, fs, opts, usedNames, scan.RecordCount))
	}
	return mappings, skipped
}

func findArrayPaths(scan *recordScan, scalars map[string]*fieldStats, minOccurrence int) []string {
	candidateSet := map[string]bool{}
	for p, fs := range scan.Fields {
		if fs.MaxPerRecord <= 1 || fs.Total < minOccurrence {
			continue
		}
		for scalar := range scalars {
			if strings.HasPrefix(scalar, p+"/") {
				candidateSet[p] = true
				break
			}
		}
		if _, ok := scalars[p]; ok {
			candidateSet[p] = true
		}
	}
	var candidates []string
	for p := range candidateSet {
		parentRepeated := false
		for other := range candidateSet {
			if other != p && strings.HasPrefix(p, other+"/") {
				parentRepeated = true
				break
			}
		}
		if !parentRepeated {
			candidates = append(candidates, p)
		}
	}
	sort.Strings(candidates)
	return candidates
}

func childMappingsForArray(arrPath string, scalars map[string]*fieldStats, attrs map[string]*fieldStats, opts Options, names map[string]int, recordCount int) []mapping {
	childNames := map[string]int{}
	var paths []string
	for p := range scalars {
		if strings.HasPrefix(p, arrPath+"/") {
			paths = append(paths, p)
		}
	}
	for p := range attrs {
		if strings.HasPrefix(p, arrPath+"/") {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	var children []mapping
	for _, p := range paths {
		fs := scalars[p]
		if fs == nil {
			fs = attrs[p]
		}
		if fs == nil || fs.Total < opts.MinOccurrence {
			continue
		}
		rel := strings.TrimPrefix(p, arrPath+"/")
		itemScoped := *fs
		itemScoped.MaxPerRecord = 1
		child := fieldMapping(rel, &itemScoped, opts, childNames, recordCount)
		child.XPath = rel
		children = append(children, child)
	}
	_ = names
	return children
}

func fieldMapping(p string, fs *fieldStats, opts Options, names map[string]int, recordCount int) mapping {
	nameSource := lastSlashPart(p)
	if strings.Contains(p, "/@") {
		parts := strings.Split(p, "/@")
		if parts[0] == "" {
			nameSource = parts[1]
		} else {
			nameSource = lastSlashPart(parts[0]) + "_" + parts[1]
		}
	}
	typeName := inferType(fs.Samples)
	if fs.MaxPerRecord > 1 && typeName != "array" {
		typeName = "array"
	}
	comment := optionalComment(fs, recordCount, opts.OptionalThreshold)
	if typeName == "string" && hasDateShape(fs.Samples) {
		comment = joinComment(comment, "date-shaped; consider transform if downstream consumers need dates")
	}
	if hasMixedTypes(fs.Samples) {
		comment = joinComment(comment, "mixed sample value shapes; review inferred string type")
	}
	return mapping{
		Name:    uniqueName(snakeCase(nameSource), names),
		XPath:   p,
		Type:    typeName,
		Comment: comment,
	}
}

func inferType(samples []string) string {
	if len(samples) == 0 {
		return "string"
	}
	seen := map[string]bool{}
	for _, sample := range samples {
		s := strings.TrimSpace(sample)
		if s == "" {
			continue
		}
		seen[sampleType(s)] = true
	}
	if len(seen) != 1 {
		return "string"
	}
	for t := range seen {
		return t
	}
	return "string"
}

func sampleType(s string) string {
	lower := strings.ToLower(s)
	if lower == "true" || lower == "false" || lower == "yes" || lower == "no" {
		return "boolean"
	}
	if integerPattern.MatchString(s) {
		unsigned := strings.TrimLeft(s, "+-")
		if len(unsigned) > 1 && strings.HasPrefix(unsigned, "0") {
			return "string"
		}
		return "integer"
	}
	if numberPattern.MatchString(s) {
		return "number"
	}
	return "string"
}

func hasDateShape(samples []string) bool {
	for _, sample := range samples {
		if datePattern.MatchString(strings.TrimSpace(sample)) {
			return true
		}
	}
	return false
}

func hasMixedTypes(samples []string) bool {
	seen := map[string]bool{}
	for _, sample := range samples {
		seen[sampleType(strings.TrimSpace(sample))] = true
	}
	return len(seen) > 1
}

func optionalComment(fs *fieldStats, recordCount int, threshold float64) string {
	if fs == nil || recordCount == 0 {
		return ""
	}
	if float64(fs.RecordsSeen) < threshold*float64(recordCount) {
		return fmt.Sprintf("TODO: appears in %d/%d records; mark optional or investigate", fs.RecordsSeen, recordCount)
	}
	return ""
}

func joinComment(parts ...string) string {
	var kept []string
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "; ")
}

func writeHeader(out *strings.Builder, opts Options, recordCount, emitted, skipped int, warnings []string) {
	fmt.Fprintf(out, "# Auto-generated by `sumpter inspect --generate-config` on %s.\n", opts.GeneratedAt.UTC().Format(time.RFC3339))
	if opts.SourcePath != "" {
		fmt.Fprintf(out, "# Source sample: %s\n", opts.SourcePath)
	}
	out.WriteString("# This is a STARTING POINT. Review before production use:\n")
	out.WriteString("#   - rename output_field values to match downstream consumer expectations\n")
	out.WriteString("#   - add validation_metadata for invariants the data must satisfy\n")
	out.WriteString("#   - add output_schema.required for fields that must be non-empty\n")
	out.WriteString("#   - consider derived fields via expression: for convenience sums\n")
	out.WriteString("#   - inject operational identifiers via defaults.parameters when needed\n")
	out.WriteString("#   - consider filename or path captures for fields encoded outside the XML\n")
	fmt.Fprintf(out, "# Generated against %d record(s); emitted %d top-level field mapping(s); skipped %d sparse path(s).\n", recordCount, emitted, skipped)
	for _, warning := range warnings {
		fmt.Fprintf(out, "# WARNING: %s.\n", warning)
	}
	out.WriteString("\n")
}

func writeConfig(out *strings.Builder, selector string, records []recordPath, mappings []mapping) {
	recordType := "generated_record"
	if len(records) == 1 && records[0].Local != "" {
		recordType = snakeCase(records[0].Local)
	}
	fmt.Fprintf(out, "record_type: %s\n", quote(recordType))
	out.WriteString("match_selectors:\n")
	fmt.Fprintf(out, "  - xpath: %s\n", quote(selector))
	out.WriteString("    min_occurrences: 1\n")
	out.WriteString("output_schema:\n")
	out.WriteString("  type: object\n")
	if len(mappings) == 0 {
		out.WriteString("  properties: {}\n")
	} else {
		out.WriteString("  properties:\n")
		for _, m := range mappings {
			writeSchemaProperty(out, m, 4)
		}
	}
	out.WriteString("field_mappings:\n")
	if len(mappings) == 0 {
		out.WriteString("  []\n")
		return
	}
	for _, m := range mappings {
		writeMapping(out, m, 2)
	}
}

func writeSchemaProperty(out *strings.Builder, m mapping, indent int) {
	prefix := strings.Repeat(" ", indent)
	fmt.Fprintf(out, "%s%s:\n", prefix, m.Name)
	fmt.Fprintf(out, "%s  type: %s\n", prefix, quote(m.Type))
	if m.Type == "array" && len(m.Children) > 0 {
		out.WriteString(prefix + "  items:\n")
		out.WriteString(prefix + "    type: object\n")
		out.WriteString(prefix + "    properties:\n")
		for _, child := range m.Children {
			writeSchemaProperty(out, child, indent+6)
		}
	}
}

func writeMapping(out *strings.Builder, m mapping, indent int) {
	prefix := strings.Repeat(" ", indent)
	if m.Comment != "" {
		fmt.Fprintf(out, "%s# %s\n", prefix, m.Comment)
	}
	fmt.Fprintf(out, "%s- output_field: %s\n", prefix, quote(m.Name))
	fmt.Fprintf(out, "%s  xpath: %s\n", prefix, quote(m.XPath))
	fmt.Fprintf(out, "%s  type: %s\n", prefix, quote(m.Type))
	if len(m.Children) > 0 {
		fmt.Fprintf(out, "%s  item_mapping:\n", prefix)
		for _, child := range m.Children {
			writeMapping(out, child, indent+4)
		}
	}
}

func snakeCase(s string) string {
	s = strings.TrimSpace(s)
	s = wordBoundary.ReplaceAllString(s, `${1}_${2}`)
	s = nonNameChar.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_")
	s = strings.ToLower(s)
	if s == "" {
		return "field"
	}
	return s
}

func uniqueName(base string, used map[string]int) string {
	if base == "" {
		base = "field"
	}
	used[base]++
	if used[base] == 1 {
		return base
	}
	return fmt.Sprintf("%s_%d", base, used[base])
}

func lastPart(p string) string {
	parts := strings.Split(p, ".")
	return parts[len(parts)-1]
}

func lastSlashPart(p string) string {
	p = strings.TrimPrefix(path.Clean(p), "./")
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

func localName(name string) string {
	if idx := strings.LastIndex(name, ":"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

func truncateSample(s string) string {
	if len(s) <= 100 {
		return s
	}
	return s[:97] + "..."
}

func quote(s string) string {
	return strconv.Quote(s)
}
