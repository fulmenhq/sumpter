package parquetwriter

import (
	"os"
	"path/filepath"
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
