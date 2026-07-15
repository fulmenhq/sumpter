package parquetwriter

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/fulmenhq/sumpter/internal/extract"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/parquet-go/parquet-go"
)

// Options controls Parquet output for one source file.
type Options struct {
	Compression     string
	Metadata        map[string]string
	WithholdColumns []string
	// SuppressPageMetadata enables data-artifact/v0 "Metadata Is Content"
	// protection: SkipPageBounds + SkipPageStatistics on every leaf column.
	// Off by default so ordinary Parquet output stays byte-compatible with the
	// pre-B2 writer; the extract B2 path turns this on with --artifact-descriptor.
	SuppressPageMetadata bool
}

type fieldSpec struct {
	name        string
	fieldType   string
	required    bool
	xpath       string
	description string
	children    []fieldSpec
	array       bool
}

type schemaProperty struct {
	fieldType   string
	description string
}

var nonIdentifierChars = regexp.MustCompile(`[^A-Za-z0-9_]`)

// WriteFile writes extract.data records to a Parquet file.
//
// The file is written to a same-directory temporary path and renamed into place
// only after the Parquet writer has closed successfully (footer present). That
// way a crash, cancel, or mid-write failure never leaves a truncated file at
// the canonical destination — readers see either the previous complete file or
// the new complete file.
func WriteFile(path string, cfg *extract.ExtractRecordMatch, records []map[string]interface{}, opts Options) error {
	if cfg == nil {
		return fmt.Errorf("extract config is required for parquet output")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create parquet output directory: %w", err)
	}

	withholdColumns := normalizeWithholdColumns(opts.WithholdColumns)
	specs, err := buildFieldSpecs(cfg, records, withholdColumns)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("parquet output requires at least one extract.data field")
	}
	rowType := structTypeForSpecs(specs)
	schema := parquet.SchemaOf(reflect.New(rowType).Interface())

	// Same-directory temp so os.Rename is atomic on the target filesystem
	// (no cross-device EXDEV window). #nosec G304 - output path is caller-controlled CLI output.
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create parquet temp output: %w", err)
	}
	tmpPath := tmp.Name()
	// Remove leftover temp on every path. After a successful rename this is a no-op.
	defer func() { _ = os.Remove(tmpPath) }()

	if err := writeParquetToFile(tmp, schema, records, specs, withholdColumns, opts); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("failed to close parquet temp output: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("failed to replace parquet output %s: %w", path, err)
	}
	return nil
}

// writeParquetToFile streams rows into an already-open file and closes the
// parquet writer (footer). The caller owns closing the underlying *os.File.
//
// When opts.SuppressPageMetadata is true (B2 opt-in via --artifact-descriptor),
// page bounds and page statistics are suppressed for every leaf column
// (data-artifact/v0 "Metadata Is Content", default-deny). Bloom filters are
// never configured — they remain opt-in in parquet-go and would form a
// membership oracle on restricted-class values if wired.
func writeParquetToFile(
	file *os.File,
	schema *parquet.Schema,
	records []map[string]interface{},
	specs []fieldSpec,
	withholdColumns []string,
	opts Options,
) error {
	writerOpts := make([]parquet.WriterOption, 0, 2)
	if opts.SuppressPageMetadata {
		writerOpts = append(writerOpts, protectionWriterOptions(schema)...)
	}
	writerOpts = append(writerOpts, schema, compressionOption(opts.Compression))
	writer := parquet.NewGenericWriter[map[string]any](file, writerOpts...)
	for key, value := range opts.Metadata {
		if strings.TrimSpace(key) != "" && value != "" {
			writer.SetKeyValueMetadata(key, value)
		}
	}
	if len(withholdColumns) > 0 {
		writer.SetKeyValueMetadata("sumpter.parquet.withhold_columns", strings.Join(withholdColumns, ","))
	}
	for key, value := range columnMetadata(specs) {
		writer.SetKeyValueMetadata(key, value)
	}

	rows := make([]map[string]any, 0, len(records))
	for _, record := range records {
		data, err := extractData(record)
		if err != nil {
			_ = writer.Close()
			return err
		}
		rows = append(rows, normalizeRecord(data, specs))
	}

	if len(rows) > 0 {
		if _, err := writer.Write(rows); err != nil {
			_ = writer.Close()
			return fmt.Errorf("failed to write parquet rows: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close parquet writer: %w", err)
	}
	return nil
}

func buildFieldSpecs(cfg *extract.ExtractRecordMatch, records []map[string]interface{}, withholdColumns []string) ([]fieldSpec, error) {
	schemaProperties := outputSchemaProperties(cfg.OutputSchema)
	required := outputSchemaRequired(cfg.OutputSchema)
	withhold := withholdColumnSet(withholdColumns)
	byName := make(map[string]fieldSpec)
	var ordered []fieldSpec

	for _, mapping := range cfg.FieldMappings {
		// Derive-only internals never become Parquet columns (config-derived
		// skip; do not rely on emit-time absence alone).
		if mapping.Internal {
			continue
		}
		if _, skip := withhold[mapping.OutputField]; skip {
			continue
		}
		property := schemaProperties[mapping.OutputField]
		spec := specFromMapping(mapping, property, parquetFieldRequired(cfg, required[mapping.OutputField]))
		if spec.name == "" {
			continue
		}
		byName[spec.name] = spec
		ordered = append(ordered, spec)
	}

	var extra []string
	for name := range schemaProperties {
		if _, skip := withhold[name]; skip {
			continue
		}
		if _, ok := byName[name]; !ok {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		property := schemaProperties[name]
		ordered = append(ordered, fieldSpec{
			name:        name,
			fieldType:   property.fieldType,
			required:    parquetFieldRequired(cfg, required[name]),
			description: property.description,
		})
		byName[name] = ordered[len(ordered)-1]
	}

	injected, err := observedDataFields(records)
	if err != nil {
		return nil, err
	}
	var injectedNames []string
	for name := range injected {
		if _, skip := withhold[name]; skip {
			continue
		}
		if _, ok := byName[name]; !ok {
			injectedNames = append(injectedNames, name)
		}
	}
	sort.Strings(injectedNames)
	for _, name := range injectedNames {
		ordered = append(ordered, fieldSpec{
			name:      name,
			fieldType: inferObservedFieldType(injected[name]),
			required:  false,
		})
	}

	return ordered, nil
}

func parquetFieldRequired(cfg *extract.ExtractRecordMatch, required bool) bool {
	if cfg != nil && cfg.UniformSchema {
		return false
	}
	return required
}

func normalizeWithholdColumns(columns []string) []string {
	if len(columns) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(columns))
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if _, ok := seen[column]; ok {
			continue
		}
		seen[column] = struct{}{}
		normalized = append(normalized, column)
	}
	return normalized
}

func withholdColumnSet(columns []string) map[string]struct{} {
	set := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		set[column] = struct{}{}
	}
	return set
}

func specFromMapping(mapping extract.FieldMapping, property schemaProperty, required bool) fieldSpec {
	fieldType := firstNonEmpty(property.fieldType, mapping.Type, "string")
	spec := fieldSpec{
		name:        mapping.OutputField,
		fieldType:   fieldType,
		required:    required,
		xpath:       mapping.XPath,
		description: firstNonEmpty(mapping.Description, property.description),
		array:       fieldType == "array" || len(mapping.ItemMapping) > 0 || len(mapping.Polymorphic) > 0,
	}

	switch {
	case len(mapping.ItemMapping) > 0:
		for _, child := range mapping.ItemMapping {
			spec.children = append(spec.children, specFromMapping(child, schemaProperty{}, false))
		}
	case len(mapping.Polymorphic) > 0:
		childNames := map[string]int{"item_type": 1}
		spec.children = append(spec.children, fieldSpec{name: "item_type", fieldType: "string", required: true})
		for _, variant := range mapping.Polymorphic {
			for _, child := range variant.FieldMappings {
				count := childNames[child.OutputField]
				if count > 0 {
					continue
				}
				childNames[child.OutputField] = count + 1
				spec.children = append(spec.children, specFromMapping(child, schemaProperty{}, false))
			}
		}
	}

	return spec
}

func outputSchemaProperties(schema map[string]interface{}) map[string]schemaProperty {
	propertiesByName := make(map[string]schemaProperty)
	properties, ok := schema["properties"].(map[string]interface{})
	if !ok {
		return propertiesByName
	}
	for name, raw := range properties {
		property, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		schemaProp := schemaProperty{}
		if typ, ok := property["type"].(string); ok {
			schemaProp.fieldType = typ
		} else if typeList, ok := property["type"].([]interface{}); ok {
			for _, candidate := range typeList {
				if typ, ok := candidate.(string); ok && typ != "null" {
					schemaProp.fieldType = typ
					break
				}
			}
		}
		if description, ok := property["description"].(string); ok {
			schemaProp.description = description
		}
		if schemaProp.fieldType != "" || schemaProp.description != "" {
			propertiesByName[name] = schemaProp
		}
	}
	return propertiesByName
}

func outputSchemaRequired(schema map[string]interface{}) map[string]bool {
	required := make(map[string]bool)
	raw, ok := schema["required"].([]interface{})
	if !ok {
		return required
	}
	for _, item := range raw {
		if name, ok := item.(string); ok {
			required[name] = true
		}
	}
	return required
}

func observedDataFields(records []map[string]interface{}) (map[string][]any, error) {
	fields := make(map[string][]any)
	for _, record := range records {
		data, err := extractData(record)
		if err != nil {
			return nil, err
		}
		for key, value := range data {
			if value == nil {
				continue
			}
			fields[key] = append(fields[key], value)
		}
	}
	return fields, nil
}

func inferObservedFieldType(values []any) string {
	if len(values) == 0 {
		return "string"
	}
	var sawNumber, sawInteger, sawBool, sawArray bool
	for _, value := range values {
		switch {
		case isArrayValue(value):
			sawArray = true
		case isConcreteBoolValue(value):
			sawBool = true
		case isConcreteIntegerValue(value):
			sawInteger = true
		case isConcreteNumberValue(value):
			sawNumber = true
		default:
			return "string"
		}
	}
	switch {
	case sawArray:
		if sawNumber || sawInteger || sawBool {
			return "string"
		}
		return "array"
	case sawBool && !sawNumber && !sawInteger:
		return "boolean"
	case sawNumber:
		return "number"
	case sawInteger:
		return "integer"
	default:
		return "string"
	}
}

func isArrayValue(value any) bool {
	rv := reflect.ValueOf(value)
	return rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array)
}

func isConcreteBoolValue(value any) bool {
	_, ok := value.(bool)
	return ok
}

func isConcreteIntegerValue(value any) bool {
	switch value.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	default:
		return false
	}
}

func isConcreteNumberValue(value any) bool {
	switch value.(type) {
	case float32, float64:
		return true
	default:
		return false
	}
}

func structTypeForSpecs(specs []fieldSpec) reflect.Type {
	fields := make([]reflect.StructField, 0, len(specs))
	usedNames := make(map[string]int, len(specs))
	for _, spec := range specs {
		fieldName := exportedFieldName(spec.name, usedNames)
		fields = append(fields, reflect.StructField{
			Name: fieldName,
			Type: goTypeForSpec(spec),
			Tag:  reflect.StructTag(`parquet:"` + parquetTag(spec) + `"`),
		})
	}
	return reflect.StructOf(fields)
}

func goTypeForSpec(spec fieldSpec) reflect.Type {
	if spec.array {
		if len(spec.children) > 0 {
			return reflect.SliceOf(structTypeForSpecs(spec.children))
		}
		return reflect.SliceOf(goScalarType(spec.fieldType))
	}
	return goScalarType(spec.fieldType)
}

func goScalarType(fieldType string) reflect.Type {
	switch fieldType {
	case "integer", "int", "int64":
		return reflect.TypeOf(int64(0))
	case "number", "float", "float64", "decimal":
		return reflect.TypeOf(float64(0))
	case "boolean", "bool":
		return reflect.TypeOf(false)
	default:
		return reflect.TypeOf("")
	}
}

func parquetTag(spec fieldSpec) string {
	parts := []string{spec.name}
	if spec.array {
		parts = append(parts, "list")
	}
	if !spec.required {
		parts = append(parts, "optional")
	}
	return strings.Join(parts, ",")
}

func exportedFieldName(name string, used map[string]int) string {
	name = nonIdentifierChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		name = "Field"
	}
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(strings.ToLower(part))
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	out := strings.Join(parts, "")
	if out == "" || !unicode.IsLetter([]rune(out)[0]) {
		out = "Field" + out
	}
	used[out]++
	if used[out] > 1 {
		out = out + strconv.Itoa(used[out])
	}
	return out
}

func extractData(record map[string]interface{}) (map[string]any, error) {
	extractBlock, ok := record["extract"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("record is missing extract object required for parquet output")
	}
	data, ok := extractBlock["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("record is missing extract.data object required for parquet output")
	}
	return data, nil
}

func normalizeRecord(data map[string]any, specs []fieldSpec) map[string]any {
	row := make(map[string]any, len(specs))
	for _, spec := range specs {
		value, ok := data[spec.name]
		if !ok || value == nil {
			continue
		}
		row[spec.name] = normalizeValue(value, spec)
	}
	return row
}

func normalizeValue(value any, spec fieldSpec) any {
	if spec.array {
		items, ok := toSlice(value)
		if !ok {
			return nil
		}
		out := make([]any, 0, len(items))
		for _, item := range items {
			if len(spec.children) == 0 {
				out = append(out, normalizeScalar(item, spec.fieldType))
				continue
			}
			if itemMap, ok := toStringMap(item); ok {
				out = append(out, normalizeRecord(itemMap, spec.children))
			}
		}
		return out
	}
	return normalizeScalar(value, spec.fieldType)
}

func normalizeScalar(value any, fieldType string) any {
	switch fieldType {
	case "integer", "int", "int64":
		if i, ok := toInt64(value); ok {
			return i
		}
	case "number", "float", "float64", "decimal":
		if f, ok := toFloat64(value); ok {
			return f
		}
	case "boolean", "bool":
		if b, ok := toBool(value); ok {
			return b
		}
	default:
		return stringify(value)
	}
	return nil
}

func toSlice(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	default:
		rv := reflect.ValueOf(value)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil, false
		}
		out := make([]any, 0, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			out = append(out, rv.Index(i).Interface())
		}
		return out, true
	}
}

func toStringMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	default:
		return nil, false
	}
}

func toInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		if uint64(typed) <= math.MaxInt64 {
			return int64(typed), true
		}
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		if typed <= math.MaxInt64 {
			return int64(typed), true
		}
	case float64:
		if math.Trunc(typed) == typed && typed >= math.MinInt64 && typed <= math.MaxInt64 {
			return int64(typed), true
		}
	case float32:
		f := float64(typed)
		if math.Trunc(f) == f && f >= math.MinInt64 && f <= math.MaxInt64 {
			return int64(f), true
		}
	case string:
		i, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return i, err == nil
	}
	return 0, false
}

func toFloat64(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int8:
		return float64(typed), true
	case int16:
		return float64(typed), true
	case int32:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint:
		return float64(typed), true
	case uint8:
		return float64(typed), true
	case uint16:
		return float64(typed), true
	case uint32:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return f, err == nil
	}
	return 0, false
}

func toBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(typed))
		return b, err == nil
	}
	return false, false
}

func stringify(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(data)
	}
}

func compressionOption(compression string) parquet.WriterOption {
	switch strings.ToLower(strings.TrimSpace(compression)) {
	case recipesmanifest.ParquetCompressionSnappy:
		return parquet.Compression(&parquet.Snappy)
	case recipesmanifest.ParquetCompressionGzip:
		return parquet.Compression(&parquet.Gzip)
	case recipesmanifest.ParquetCompressionNone:
		return parquet.Compression(&parquet.Uncompressed)
	default:
		return parquet.Compression(&parquet.Zstd)
	}
}

// protectionWriterOptions wires the SkipPageBounds + SkipPageStatistics pair
// for every leaf column. Both options are required: SkipPageStatistics closes
// the dominant DataPageHeader full-value leak; SkipPageBounds closes ColumnIndex
// and footer ColumnChunk min/max. Callers must never add BloomFilters here.
func protectionWriterOptions(schema *parquet.Schema) []parquet.WriterOption {
	if schema == nil {
		return nil
	}
	columns := schema.Columns()
	opts := make([]parquet.WriterOption, 0, 2*len(columns))
	for _, path := range columns {
		if len(path) == 0 {
			continue
		}
		// Copy: parquet-go stores the path slice on the writer config.
		leaf := append([]string(nil), path...)
		opts = append(opts,
			parquet.SkipPageBounds(leaf...),
			parquet.SkipPageStatistics(leaf...),
		)
	}
	return opts
}

func columnMetadata(specs []fieldSpec) map[string]string {
	metadata := make(map[string]string)
	var walk func(prefix string, fields []fieldSpec)
	walk = func(prefix string, fields []fieldSpec) {
		for _, spec := range fields {
			name := spec.name
			if prefix != "" {
				name = prefix + "." + spec.name
			}
			if spec.xpath != "" {
				metadata["sumpter.column."+name+".xpath"] = spec.xpath
			}
			if spec.description != "" {
				metadata["sumpter.column."+name+".description"] = spec.description
			}
			metadata["sumpter.column."+name+".recipe_field"] = spec.name
			if len(spec.children) > 0 {
				walk(name, spec.children)
			}
		}
	}
	walk("", specs)
	return metadata
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
