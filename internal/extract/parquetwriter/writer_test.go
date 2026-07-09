package parquetwriter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/parquet-go/parquet-go"
)

func TestWriteFileWritesExtractDataAndMetadata(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string", Description: "Order identifier"},
			{OutputField: "quantity", XPath: "Quantity", Type: "integer"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string", "description": "Order identifier from schema"},
				"quantity": map[string]interface{}{"type": "integer"},
			},
			"required": []interface{}{"order_id"},
		},
	}
	records := []map[string]interface{}{
		{
			"_runtime": map[string]interface{}{"run_id": "ignored"},
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"order_id": "A-1",
					"quantity": 3,
				},
				"summary": map[string]interface{}{"ignored": true},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	err := WriteFile(path, cfg, records, Options{
		Compression: "none",
		Metadata: map[string]string{
			"sumpter.recipe_id": "recipe-1",
			"sumpter.run_id":    "run-1",
		},
	})
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type Row struct {
		OrderID  string `parquet:"order_id"`
		Quantity int64  `parquet:"quantity,optional"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderID != "A-1" || rows[0].Quantity != 3 {
		t.Fatalf("rows = %#v, want one extract.data row", rows)
	}

	file := openParquetFile(t, path)
	if got, ok := file.Lookup("sumpter.recipe_id"); !ok || got != "recipe-1" {
		t.Fatalf("sumpter.recipe_id metadata = %q/%t, want recipe-1/true", got, ok)
	}
	if got, ok := file.Lookup("sumpter.column.order_id.xpath"); !ok || got != "@id" {
		t.Fatalf("column xpath metadata = %q/%t, want @id/true", got, ok)
	}
	if got, ok := file.Lookup("sumpter.column.order_id.description"); !ok || got != "Order identifier" {
		t.Fatalf("column description metadata = %q/%t, want mapping description/true", got, ok)
	}
	if _, ok := file.Lookup("sumpter.column._runtime.recipe_field"); ok {
		t.Fatalf("runtime metadata should not be emitted as a parquet column")
	}
}

func TestWriteFileIncludesUndeclaredInjectedFields(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"order_id"},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"order_id":  "A-1",
					"client_id": "acme",
					"site_id":   "site-1",
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	if err := WriteFile(path, cfg, records, Options{Compression: "none"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type Row struct {
		OrderID  string `parquet:"order_id"`
		ClientID string `parquet:"client_id,optional"`
		SiteID   string `parquet:"site_id,optional"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rows) != 1 || rows[0].ClientID != "acme" || rows[0].SiteID != "site-1" {
		t.Fatalf("rows = %#v, want injected client/site fields", rows)
	}

	file := openParquetFile(t, path)
	if got, ok := file.Lookup("sumpter.column.client_id.recipe_field"); !ok || got != "client_id" {
		t.Fatalf("client_id metadata = %q/%t, want client_id/true", got, ok)
	}
	if got, ok := file.Lookup("sumpter.column.site_id.recipe_field"); !ok || got != "site_id" {
		t.Fatalf("site_id metadata = %q/%t, want site_id/true", got, ok)
	}
}

func TestWriteFileWithholdColumnsOmitsAllFieldSourcesAndWritesMetadata(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string"},
			{OutputField: "program", XPath: "Program", Type: "string"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string"},
				"program":  map[string]interface{}{"type": "string"},
				"site":     map[string]interface{}{"type": "string"},
				"year":     map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"order_id"},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"order_id": "A-1",
					"program":  "retail",
					"site":     "store_17",
					"year":     "2026",
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	if err := WriteFile(path, cfg, records, Options{
		Compression:     "none",
		WithholdColumns: []string{"program", "site", "year"},
	}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type Row struct {
		OrderID string `parquet:"order_id"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderID != "A-1" {
		t.Fatalf("rows = %#v, want one row with order_id only", rows)
	}

	file := openParquetFile(t, path)
	if got, ok := file.Lookup("sumpter.parquet.withhold_columns"); !ok || got != "program,site,year" {
		t.Fatalf("withhold metadata = %q/%t, want program,site,year/true", got, ok)
	}
	fields := parquetFieldNames(file)
	for _, omitted := range []string{"program", "site", "year"} {
		if fields[omitted] {
			t.Fatalf("field %q was written to parquet schema: %#v", omitted, fields)
		}
		if _, ok := file.Lookup("sumpter.column." + omitted + ".recipe_field"); ok {
			t.Fatalf("metadata for withheld field %q was emitted", omitted)
		}
	}
	if !fields["order_id"] {
		t.Fatalf("order_id missing from parquet schema: %#v", fields)
	}
}

func TestWriteFileUniformSchemaKeepsDeclaredNullColumnsAndWithhold(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType:    "sample_record",
		UniformSchema: true,
		FieldMappings: []extract.FieldMapping{
			{OutputField: "record_id", XPath: "@id", Type: "string"},
			{OutputField: "label", XPath: "Label", Type: "string"},
			{OutputField: "quantity", XPath: "Quantity", Type: "integer"},
			{OutputField: "segment", XPath: "Segment", Type: "string"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"record_id": map[string]interface{}{"type": "string"},
				"label":     map[string]interface{}{"type": "string"},
				"quantity":  map[string]interface{}{"type": "integer"},
				"segment":   map[string]interface{}{"type": "string"},
			},
			"required": []interface{}{"record_id", "label", "quantity", "segment"},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"record_id": "A-1",
					"label":     nil,
					"quantity":  nil,
					"segment":   nil,
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "records.parquet")
	if err := WriteFile(path, cfg, records, Options{
		Compression:     "none",
		WithholdColumns: []string{"segment"},
	}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type Row struct {
		RecordID *string `parquet:"record_id,optional"`
		Label    *string `parquet:"label,optional"`
		Quantity *int64  `parquet:"quantity,optional"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rows) != 1 || rows[0].RecordID == nil || *rows[0].RecordID != "A-1" {
		t.Fatalf("rows = %#v, want one row with record_id", rows)
	}
	if rows[0].Label != nil || rows[0].Quantity != nil {
		t.Fatalf("uniform null cells = label:%#v quantity:%#v, want nil/nil", rows[0].Label, rows[0].Quantity)
	}

	file := openParquetFile(t, path)
	fields := parquetFieldNames(file)
	for _, field := range []string{"record_id", "label", "quantity"} {
		if !fields[field] {
			t.Fatalf("field %q missing from parquet schema: %#v", field, fields)
		}
	}
	if fields["segment"] {
		t.Fatalf("withheld field segment was written to parquet schema: %#v", fields)
	}
	if _, ok := file.Lookup("sumpter.column.segment.recipe_field"); ok {
		t.Fatal("metadata for withheld field segment was emitted")
	}
}

func TestWriteFileWithholdColumnsSkipsObservedOnlyFields(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string"},
			},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"order_id": "A-1",
					"site":     "store_17",
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	if err := WriteFile(path, cfg, records, Options{
		Compression:     "none",
		WithholdColumns: []string{"site"},
	}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fields := parquetFieldNames(openParquetFile(t, path))
	if fields["site"] {
		t.Fatalf("observed-only field site was written to parquet schema: %#v", fields)
	}
	if !fields["order_id"] {
		t.Fatalf("order_id missing from parquet schema: %#v", fields)
	}
}

func TestWriteFileUsesOutputSchemaDescriptionMetadata(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "program_id", XPath: "ProgramID", Type: "string"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"program_id": map[string]interface{}{"type": "string", "description": "Analytics program key"},
			},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"program_id": "kickback",
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	if err := WriteFile(path, cfg, records, Options{Compression: "none"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	file := openParquetFile(t, path)
	if got, ok := file.Lookup("sumpter.column.program_id.description"); !ok || got != "Analytics program key" {
		t.Fatalf("program_id description metadata = %q/%t, want schema description/true", got, ok)
	}
}

func TestWriteFileWritesListOfStructs(t *testing.T) {
	cfg := &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string"},
			{
				OutputField: "lines",
				XPath:       "Lines/Line",
				Type:        "array",
				ItemMapping: []extract.FieldMapping{
					{OutputField: "sku", XPath: "@sku", Type: "string"},
					{OutputField: "amount", XPath: "Amount", Type: "number"},
				},
			},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string"},
				"lines":    map[string]interface{}{"type": "array"},
			},
			"required": []interface{}{"order_id", "lines"},
		},
	}
	records := []map[string]interface{}{
		{
			"extract": map[string]interface{}{
				"data": map[string]interface{}{
					"order_id": "A-1",
					"lines": []interface{}{
						map[string]interface{}{"sku": "SKU-1", "amount": 12.50},
					},
				},
			},
		},
	}

	path := filepath.Join(t.TempDir(), "orders.parquet")
	if err := WriteFile(path, cfg, records, Options{Compression: "none"}); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	type Line struct {
		SKU    string  `parquet:"sku,optional"`
		Amount float64 `parquet:"amount,optional"`
	}
	type Row struct {
		OrderID string `parquet:"order_id"`
		Lines   []Line `parquet:"lines,list"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderID != "A-1" || len(rows[0].Lines) != 1 {
		t.Fatalf("rows = %#v, want one row with one line", rows)
	}
	if rows[0].Lines[0].SKU != "SKU-1" || rows[0].Lines[0].Amount != 12.50 {
		t.Fatalf("line = %#v, want SKU-1/12.50", rows[0].Lines[0])
	}
}

func TestWriteFileReplacesExistingFileAtomically(t *testing.T) {
	cfg := minimalParquetConfig()
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.parquet")

	// Seed a complete prior artifact so a failed rename must leave it intact
	// and a successful write must replace it with a readable new file.
	oldRecords := []map[string]interface{}{
		{"extract": map[string]interface{}{"data": map[string]interface{}{"order_id": "OLD", "quantity": 1}}},
	}
	if err := WriteFile(path, cfg, oldRecords, Options{Compression: "none"}); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}

	newRecords := []map[string]interface{}{
		{"extract": map[string]interface{}{"data": map[string]interface{}{"order_id": "NEW", "quantity": 9}}},
	}
	if err := WriteFile(path, cfg, newRecords, Options{Compression: "none"}); err != nil {
		t.Fatalf("replace WriteFile: %v", err)
	}

	type Row struct {
		OrderID  string `parquet:"order_id"`
		Quantity int64  `parquet:"quantity,optional"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile after replace: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderID != "NEW" || rows[0].Quantity != 9 {
		t.Fatalf("rows after replace = %#v, want NEW/9", rows)
	}
	assertNoParquetTempFiles(t, dir, "orders.parquet")
}

func TestWriteFileCleansTempOnRenameFailure(t *testing.T) {
	cfg := minimalParquetConfig()
	dir := t.TempDir()
	// Block the canonical path with a directory so rename into place fails.
	// The prior complete content (none) and the blocking dir must survive.
	path := filepath.Join(dir, "orders.parquet")
	if err := os.Mkdir(path, 0o750); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}

	records := []map[string]interface{}{
		{"extract": map[string]interface{}{"data": map[string]interface{}{"order_id": "A-1", "quantity": 3}}},
	}
	err := WriteFile(path, cfg, records, Options{Compression: "none"})
	if err == nil {
		t.Fatal("WriteFile succeeded; want replace failure")
	}
	if !strings.Contains(err.Error(), "failed to replace parquet output") {
		t.Fatalf("error = %v, want replace context", err)
	}
	assertNoParquetTempFiles(t, dir, "orders.parquet")

	info, statErr := os.Stat(path)
	if statErr != nil {
		t.Fatalf("stat blocking directory: %v", statErr)
	}
	if !info.IsDir() {
		t.Fatalf("blocking path was replaced with a file; want directory preserved")
	}
}

func TestWriteFileLeavesExistingIntactOnWriteFailure(t *testing.T) {
	cfg := minimalParquetConfig()
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.parquet")

	seed := []map[string]interface{}{
		{"extract": map[string]interface{}{"data": map[string]interface{}{"order_id": "KEEP", "quantity": 2}}},
	}
	if err := WriteFile(path, cfg, seed, Options{Compression: "none"}); err != nil {
		t.Fatalf("seed WriteFile: %v", err)
	}
	before, err := os.ReadFile(path) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}

	// Missing extract.data forces a failure after the temp is created but
	// before a valid footer lands — the canonical path must stay the seed.
	bad := []map[string]interface{}{
		{"extract": map[string]interface{}{"summary": map[string]interface{}{"x": true}}},
	}
	if err := WriteFile(path, cfg, bad, Options{Compression: "none"}); err == nil {
		t.Fatal("WriteFile with bad records succeeded; want failure")
	}
	assertNoParquetTempFiles(t, dir, "orders.parquet")

	after, err := os.ReadFile(path) // #nosec G304 - test-owned path
	if err != nil {
		t.Fatalf("read after failed write: %v", err)
	}
	if string(after) != string(before) {
		t.Fatalf("canonical file changed on write failure; want seed bytes preserved")
	}

	type Row struct {
		OrderID  string `parquet:"order_id"`
		Quantity int64  `parquet:"quantity,optional"`
	}
	rows, err := parquet.ReadFile[Row](path)
	if err != nil {
		t.Fatalf("ReadFile seed after failed write: %v", err)
	}
	if len(rows) != 1 || rows[0].OrderID != "KEEP" {
		t.Fatalf("seed rows = %#v, want KEEP preserved", rows)
	}
}

func minimalParquetConfig() *extract.ExtractRecordMatch {
	return &extract.ExtractRecordMatch{
		RecordType: "order",
		FieldMappings: []extract.FieldMapping{
			{OutputField: "order_id", XPath: "@id", Type: "string"},
			{OutputField: "quantity", XPath: "Quantity", Type: "integer"},
		},
		OutputSchema: map[string]interface{}{
			"properties": map[string]interface{}{
				"order_id": map[string]interface{}{"type": "string"},
				"quantity": map[string]interface{}{"type": "integer"},
			},
			"required": []interface{}{"order_id"},
		},
	}
}

func assertNoParquetTempFiles(t *testing.T, dir, finalName string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, "."+finalName+".tmp-*"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files = %#v, want none", matches)
	}
}

func openParquetFile(t *testing.T, path string) *parquet.File {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 - test-owned temp path.
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	pqFile, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	return pqFile
}

func parquetFieldNames(file *parquet.File) map[string]bool {
	fields := make(map[string]bool)
	for _, field := range file.Schema().Fields() {
		fields[field.Name()] = true
	}
	return fields
}
