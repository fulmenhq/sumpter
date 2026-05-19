package configgen

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

func TestGenerateDominantRecordConfig(t *testing.T) {
	xml := `<Orders>
  <OrderEvent id="001">
    <StoreNumber>70857</StoreNumber>
    <Sequence>12</Sequence>
    <Total>14.50</Total>
    <Voided>false</Voided>
    <BusinessDate>2026-05-18</BusinessDate>
  </OrderEvent>
  <OrderEvent id="002">
    <StoreNumber>70858</StoreNumber>
    <Sequence>13</Sequence>
    <Total>15.75</Total>
    <Voided>true</Voided>
    <BusinessDate>2026-05-18</BusinessDate>
  </OrderEvent>
</Orders>`

	result := generateFromString(t, xml, Options{
		SourcePath:        "orders.xml",
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	if result.RecordSelector != "//OrderEvent" {
		t.Fatalf("record selector = %q, want //OrderEvent", result.RecordSelector)
	}

	cfg := parseExtractConfig(t, result.YAML)
	if cfg.RecordType != "order_event" {
		t.Fatalf("record type = %q, want order_event", cfg.RecordType)
	}
	if cfg.MatchSelectors[0].XPath != "//OrderEvent" {
		t.Fatalf("match selector = %q", cfg.MatchSelectors[0].XPath)
	}
	if strings.Contains(string(result.YAML), "min_occurrences: 1") {
		t.Fatalf("generated config should not emit default min_occurrences: 1:\n%s", result.YAML)
	}

	fieldTypes := map[string]string{}
	for _, field := range cfg.FieldMappings {
		fieldTypes[field.OutputField] = field.Type
	}
	if fieldTypes["store_number"] != "integer" {
		t.Fatalf("store_number type = %q, want integer", fieldTypes["store_number"])
	}
	if fieldTypes["sequence"] != "integer" {
		t.Fatalf("sequence type = %q, want integer", fieldTypes["sequence"])
	}
	if fieldTypes["total"] != "number" {
		t.Fatalf("total type = %q, want number", fieldTypes["total"])
	}
	if fieldTypes["voided"] != "boolean" {
		t.Fatalf("voided type = %q, want boolean", fieldTypes["voided"])
	}
	if fieldTypes["id"] != "string" {
		t.Fatalf("id type = %q, want string for leading-zero identifier", fieldTypes["id"])
	}

	assertSchemaValid(t, result.YAML)
}

func TestGenerateAmbiguousSelectorUnion(t *testing.T) {
	xml := `<Events>
  <Order><ID>1</ID></Order>
  <Return><ID>2</ID></Return>
  <Order><ID>3</ID></Order>
  <Return><ID>4</ID></Return>
</Events>`

	result := generateFromString(t, xml, Options{
		MinOccurrence:     2,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	if result.RecordSelector != "//Order | //Return" {
		t.Fatalf("record selector = %q, want union", result.RecordSelector)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected ambiguous selector warning")
	}
	if !strings.Contains(string(result.YAML), "WARNING: multiple comparable record elements") {
		t.Fatalf("generated YAML missing warning:\n%s", result.YAML)
	}
	assertSchemaValid(t, result.YAML)
}

func TestGenerateFallsBackToRoot(t *testing.T) {
	xml := `<Invoice><ID>1</ID><Amount>10.00</Amount></Invoice>`

	result := generateFromString(t, xml, Options{
		MinOccurrence:     2,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	if result.RecordSelector != "//Invoice" {
		t.Fatalf("record selector = %q, want //Invoice", result.RecordSelector)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected root fallback warning")
	}
	assertSchemaValid(t, result.YAML)
}

func TestGenerateArrayItemMapping(t *testing.T) {
	xml := `<Orders>
  <OrderEvent>
    <OrderLine><SKU>A1</SKU><Quantity>2</Quantity></OrderLine>
    <OrderLine><SKU>B2</SKU><Quantity>3</Quantity></OrderLine>
  </OrderEvent>
  <OrderEvent>
    <OrderLine><SKU>C3</SKU><Quantity>1</Quantity></OrderLine>
  </OrderEvent>
</Orders>`

	result := generateFromString(t, xml, Options{
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})
	cfg := parseExtractConfig(t, result.YAML)

	var lines *extract.FieldMapping
	for i := range cfg.FieldMappings {
		if cfg.FieldMappings[i].OutputField == "order_line" {
			lines = &cfg.FieldMappings[i]
			break
		}
	}
	if lines == nil {
		t.Fatalf("expected order_line array mapping, got %+v", cfg.FieldMappings)
	}
	if lines.Type != "array" {
		t.Fatalf("order_line type = %q, want array", lines.Type)
	}
	if len(lines.ItemMapping) != 2 {
		t.Fatalf("item mapping len = %d, want 2", len(lines.ItemMapping))
	}
	for _, child := range lines.ItemMapping {
		if child.Type == "array" {
			t.Fatalf("item_mapping child %s inferred as array; want scalar", child.OutputField)
		}
	}
	assertSchemaValid(t, result.YAML)
}

func TestGenerateOverrideRecordSelector(t *testing.T) {
	xml := `<Envelope><Header>ignored</Header><Item><ID>1</ID></Item><Item><ID>2</ID></Item></Envelope>`

	result := generateFromString(t, xml, Options{
		RecordSelector:    "//Item",
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	if result.RecordSelector != "//Item" {
		t.Fatalf("record selector = %q, want override", result.RecordSelector)
	}
	cfg := parseExtractConfig(t, result.YAML)
	if cfg.MatchSelectors[0].XPath != "//Item" {
		t.Fatalf("match selector = %q, want //Item", cfg.MatchSelectors[0].XPath)
	}
}

func TestGenerateOverrideRecordSelectorPredicateFiltersSampledFields(t *testing.T) {
	xml := `<Events>
  <OrderEvent kind="sale"><ID>1</ID><SaleOnly>yes</SaleOnly></OrderEvent>
  <OrderEvent kind="return"><ID>2</ID><ReturnOnly>yes</ReturnOnly></OrderEvent>
  <OrderEvent kind="sale"><ID>3</ID><SaleOnly>no</SaleOnly></OrderEvent>
</Events>`

	result := generateFromString(t, xml, Options{
		RecordSelector:    `//OrderEvent[@kind="sale"]`,
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	cfg := parseExtractConfig(t, result.YAML)
	if cfg.MatchSelectors[0].XPath != `//OrderEvent[@kind="sale"]` {
		t.Fatalf("match selector = %q", cfg.MatchSelectors[0].XPath)
	}
	fields := map[string]bool{}
	for _, field := range cfg.FieldMappings {
		fields[field.OutputField] = true
	}
	if !fields["sale_only"] {
		t.Fatalf("expected sale_only field, got %+v", cfg.FieldMappings)
	}
	if fields["return_only"] {
		t.Fatalf("predicate override sampled non-sale field return_only:\n%s", result.YAML)
	}
}

func TestGenerateRejectsUnsupportedOverrideSelector(t *testing.T) {
	xml := `<Events><OrderEvent><Total>1</Total></OrderEvent></Events>`
	_, err := Generate(func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(xml)), nil
	}, Options{
		RecordSelector:    `//OrderEvent[Total > 0]`,
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})
	if err == nil {
		t.Fatal("expected unsupported selector error")
	}
	if !strings.Contains(err.Error(), "unsupported record selector") {
		t.Fatalf("error = %v", err)
	}
}

func TestGeneratedConfigExtractsRecords(t *testing.T) {
	xml := `<Orders>
  <OrderEvent><ID>1</ID><Total>14.50</Total></OrderEvent>
  <OrderEvent><ID>2</ID><Total>15.75</Total></OrderEvent>
</Orders>`
	result := generateFromString(t, xml, Options{
		MinOccurrence:     1,
		OptionalThreshold: 0.5,
		GeneratedAt:       fixedTime(),
	})

	tmpDir := t.TempDir()
	xmlPath := filepath.Join(tmpDir, "orders.xml")
	configPath := filepath.Join(tmpDir, "extract.yaml")
	if err := os.WriteFile(xmlPath, []byte(xml), 0o600); err != nil {
		t.Fatalf("write xml: %v", err)
	}
	if err := os.WriteFile(configPath, result.YAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := extract.LoadExtractConfig(configPath)
	if err != nil {
		t.Fatalf("LoadExtractConfig: %v\n%s", err, result.YAML)
	}
	sig := &extract.FileSignature{
		SignatureID:         "test-orders",
		Name:                "Test orders",
		ConfidenceThreshold: 1.0,
		MatchPatterns: []extract.MatchPattern{
			{PatternID: "root", Name: "Root", Selector: "//Orders", Weight: 1.0},
		},
	}
	extractResult := extract.ProcessFile(xmlPath, sig, cfg, nil, false)
	if extractResult.Error != nil {
		t.Fatalf("ProcessFile error: %v", extractResult.Error)
	}
	if len(extractResult.Records) != 2 {
		t.Fatalf("records len = %d, want 2", len(extractResult.Records))
	}
}

func generateFromString(t *testing.T, input string, opts Options) *Result {
	t.Helper()
	result, err := Generate(func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(input)), nil
	}, opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	return result
}

func parseExtractConfig(t *testing.T, data []byte) *extract.ExtractRecordMatch {
	t.Helper()
	var cfg extract.ExtractRecordMatch
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("yaml.Unmarshal: %v\n%s", err, data)
	}
	return &cfg
}

func assertSchemaValid(t *testing.T, data []byte) {
	t.Helper()
	schemaFS, err := os.DirFS("../../..").Open("schemas")
	if err != nil {
		t.Fatalf("open schemas: %v", err)
	}
	_ = schemaFS.Close()
	validator := validation.NewSchemaValidator("../../..")
	result, err := validator.ValidateExtractConfig(data, "generated-extract.yaml")
	if err != nil {
		t.Fatalf("ValidateExtractConfig: %v", err)
	}
	if !result.IsValid() {
		t.Fatalf("generated config is invalid: %s\n%s", result.ErrorSummary(), data)
	}
}

func fixedTime() time.Time {
	return time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
}
