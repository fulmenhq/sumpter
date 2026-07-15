package extract

import (
	"context"
	"strings"
	"testing"

	"github.com/antchfx/xmlquery"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

func TestInternalXPathFieldMappingDrivesExpression(t *testing.T) {
	doc := mustParseXML(t, `<root><item kind="refund"><amount>10</amount></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "sign_factor", XPath: `1 - 2*count(self::item[@kind='refund'])`, Type: "number", Internal: true},
		{OutputField: "raw_amount", XPath: "amount", Type: "number"},
		{OutputField: "amount", Expression: "sign_factor * raw_amount", Type: "number"},
	})

	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec["amount"] != float64(-10) && rec["amount"] != -10 {
		// coerceNumber may yield float64 or int depending on path
		switch v := rec["amount"].(type) {
		case float64:
			if v != -10 {
				t.Errorf("amount = %v, want -10", v)
			}
		case int:
			if v != -10 {
				t.Errorf("amount = %v, want -10", v)
			}
		default:
			t.Errorf("amount = %#v (%T), want -10", rec["amount"], rec["amount"])
		}
	}
	if _, ok := rec["sign_factor"]; ok {
		t.Errorf("internal xpath mapping leaked: sign_factor=%#v", rec["sign_factor"])
	}
	assertNoInternalWrappers(t, rec)
}

func TestInternalExpressionFieldMappingDrivesLaterExpression(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>3</n></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "base", XPath: "n", Type: "number"},
		{OutputField: "doubled", Expression: "base * 2", Type: "number", Internal: true},
		{OutputField: "quad", Expression: "doubled * 2", Type: "number"},
	})

	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	rec := records[0]
	assertNumeric(t, rec["quad"], 12)
	if _, ok := rec["doubled"]; ok {
		t.Errorf("internal expression mapping leaked: doubled=%#v", rec["doubled"])
	}
	assertNoInternalWrappers(t, rec)
}

func TestInternalExpressionBeforeLaterXPathStillWorks(t *testing.T) {
	// Two-phase: expression listed before a later XPath mapping must still
	// resolve the XPath value (no global list-sequential regression).
	doc := mustParseXML(t, `<root><item><a>2</a><b>5</b></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "total", Expression: "a_count + b_count", Type: "number"},
		{OutputField: "a_count", XPath: "a", Type: "number"},
		{OutputField: "b_count", XPath: "b", Type: "number"},
	})

	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("want 1 record, got %d", len(records))
	}
	assertNumeric(t, records[0]["total"], 7)
}

func TestInternalForwardExpressionFailsAtRuntime(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>1</n></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "early", Expression: "later * 2", Type: "number"},
		{OutputField: "later", Expression: "n", Type: "number", Internal: true},
		{OutputField: "n", XPath: "n", Type: "number"},
	})

	_, err := extractRecords(doc, cfg, nil)
	if err == nil {
		t.Fatal("expected forward expression reference to fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "undefined") &&
		!strings.Contains(strings.ToLower(err.Error()), "unknown") &&
		!strings.Contains(err.Error(), "later") {
		t.Fatalf("want undefined-variable style error mentioning later, got: %v", err)
	}
}

func TestAbsentInternalXPathLeavesUnbound(t *testing.T) {
	doc := mustParseXML(t, `<root><item><amount>10</amount></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "sign_factor", XPath: "missing", Type: "number", Internal: true},
		{OutputField: "amount", Expression: "sign_factor * 1", Type: "number"},
	})

	_, err := extractRecords(doc, cfg, nil)
	if err == nil {
		t.Fatal("expected expression over unbound internal xpath helper to fail")
	}
}

func TestTwoInternalHelpersInOneRecord(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>4</n></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "half", Expression: "n / 2", Type: "number", Internal: true},
		{OutputField: "n", XPath: "n", Type: "number"},
		{OutputField: "double_half", Expression: "half * 2", Type: "number"},
		{OutputField: "half_plus_one", Expression: "half + 1", Type: "number"},
	})

	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	rec := records[0]
	assertNumeric(t, rec["double_half"], 4)
	assertNumeric(t, rec["half_plus_one"], 3)
	if _, ok := rec["half"]; ok {
		t.Errorf("internal half leaked: %#v", rec["half"])
	}
}

func TestInternalCollisionWithExternalBeforeProjection(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>1</n></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "helper", XPath: "n", Type: "number", Internal: true},
		{OutputField: "out", Expression: "helper + 1", Type: "number"},
	})

	external := map[string]interface{}{
		"helper": "external",
	}
	_, err := extractRecords(doc, cfg, external)
	if err == nil {
		t.Fatal("expected collision between external and internal mapped field")
	}
	if !strings.Contains(err.Error(), "helper") {
		t.Fatalf("want collision naming helper, got: %v", err)
	}
}

func TestInternalMappingBufferedAndWorkerParity(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>5</n></item></root>`)
	base := []FieldMapping{
		{OutputField: "helper", Expression: "n * 2", Type: "number", Internal: true},
		{OutputField: "n", XPath: "n", Type: "number"},
		{OutputField: "out", Expression: "helper + 1", Type: "number"},
	}
	cfg := preparedInternalMappingConfig(t, base)

	buffered, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("buffered: %v", err)
	}
	if len(buffered) != 1 {
		t.Fatalf("buffered records=%d", len(buffered))
	}

	// Worker/indexed path inherits the same helper via ExtractFieldsWithExternal.
	workerCfg := preparedFieldConfig(t, base, ".", nil)
	item := xmlquery.FindOne(doc, "//item")
	if item == nil {
		t.Fatal("missing item node")
	}
	worker, err := ExtractFieldsWithExternal(item, workerCfg, nil)
	if err != nil {
		t.Fatalf("worker ExtractFieldsWithExternal: %v", err)
	}

	// Sink path shares buildProjectedRecord with buffered, then enriches.
	sinkCfg := preparedInternalMappingConfig(t, base)
	sink := &recordCollectingSink{}
	_, _, err = extractRecordsWithCountsAndRecordNumsToSink(context.Background(), doc, sinkCfg, nil, "test.xml", nil, provenance.RuntimeOptions{}, sink)
	if err != nil {
		t.Fatalf("sink path: %v", err)
	}
	envelopes := sink.Records()
	if len(envelopes) != 1 {
		t.Fatalf("sink envelopes=%d", len(envelopes))
	}
	extractObj, ok := envelopes[0]["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("sink envelope missing extract: %#v", envelopes[0])
	}
	sinkData, ok := extractObj["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("sink envelope missing extract.data: %#v", envelopes[0])
	}

	assertNumeric(t, buffered[0]["out"], 11)
	assertNumeric(t, worker["out"], 11)
	assertNumeric(t, sinkData["out"], 11)
	for name, rec := range map[string]map[string]interface{}{
		"buffered": buffered[0],
		"worker":   worker,
		"sink":     sinkData,
	} {
		if _, ok := rec["helper"]; ok {
			t.Errorf("%s leaked helper", name)
		}
		assertNoInternalWrappers(t, rec)
	}
}

func TestPrepareRejectsNestedInternal(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		FieldMappings: []FieldMapping{
			{
				OutputField: "lines",
				XPath:       "line",
				Type:        "array",
				ItemMapping: []FieldMapping{
					{OutputField: "hidden", XPath: "x", Type: "string", Internal: true},
				},
			},
		},
	}
	err := prepareExtractConfig(cfg)
	if err == nil {
		t.Fatal("expected nested internal rejection")
	}
	if !strings.Contains(err.Error(), "internal") {
		t.Fatalf("want internal in error, got: %v", err)
	}
}

func TestPrepareRejectsInternalInOutputSchema(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		OutputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"helper": map[string]interface{}{"type": "number"},
				"out":    map[string]interface{}{"type": "number"},
			},
			"required": []interface{}{"out"},
		},
		FieldMappings: []FieldMapping{
			{OutputField: "helper", XPath: "n", Type: "number", Internal: true},
			{OutputField: "out", Expression: "helper", Type: "number"},
		},
	}
	err := prepareExtractConfig(cfg)
	if err == nil {
		t.Fatal("expected output_schema.properties rejection")
	}
	if !strings.Contains(err.Error(), "output_schema") {
		t.Fatalf("want output_schema error, got: %v", err)
	}
}

func TestPrepareRejectsInternalFilterKey(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		Filters:        map[string]interface{}{"helper": "> 0"},
		FieldMappings: []FieldMapping{
			{OutputField: "helper", XPath: "n", Type: "number", Internal: true},
			{OutputField: "out", Expression: "helper", Type: "number"},
		},
	}
	err := prepareExtractConfig(cfg)
	if err == nil {
		t.Fatal("expected filter key rejection")
	}
	if !strings.Contains(err.Error(), "filters") {
		t.Fatalf("want filters error, got: %v", err)
	}
}

func TestPrepareRejectsDuplicateWhenInternalPresent(t *testing.T) {
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		FieldMappings: []FieldMapping{
			{OutputField: "helper", XPath: "n", Type: "number", Internal: true},
			{OutputField: "out", XPath: "n", Type: "number"},
			{OutputField: "out", Expression: "helper", Type: "number"},
		},
	}
	err := prepareExtractConfig(cfg)
	if err == nil {
		t.Fatal("expected duplicate rejection when internal present")
	}
	if !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("want duplicated error, got: %v", err)
	}
}

func TestPrepareAllowsDuplicateWhenNoInternal(t *testing.T) {
	// Legacy: all-emitted duplicates are not rejected (later overwrites earlier).
	cfg := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//item"}},
		FieldMappings: []FieldMapping{
			{OutputField: "out", XPath: "a", Type: "string"},
			{OutputField: "out", XPath: "b", Type: "string"},
		},
	}
	if err := prepareExtractConfig(cfg); err != nil {
		t.Fatalf("no-internal duplicate must remain allowed: %v", err)
	}
}

func TestUniformSchemaDoesNotResurrectInternal(t *testing.T) {
	doc := mustParseXML(t, `<root><item><n>2</n></item></root>`)
	cfg := preparedInternalMappingConfig(t, []FieldMapping{
		{OutputField: "helper", XPath: "n", Type: "number", Internal: true},
		{OutputField: "out", Expression: "helper * 3", Type: "number"},
	})
	// Internal names must be absent from output_schema; only emit fields listed.
	cfg.OutputSchema = map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"out": map[string]interface{}{"type": "number"},
			"pad": map[string]interface{}{"type": "string"},
		},
	}
	cfg.UniformSchema = true

	records, err := extractRecords(doc, cfg, nil)
	if err != nil {
		t.Fatalf("extractRecords: %v", err)
	}
	rec := records[0]
	assertNumeric(t, rec["out"], 6)
	if _, ok := rec["helper"]; ok {
		t.Errorf("uniform schema resurrected helper: %#v", rec["helper"])
	}
	if v, ok := rec["pad"]; !ok || v != nil {
		t.Errorf("pad = %#v, want explicit null from uniform schema", rec["pad"])
	}
}

func preparedInternalMappingConfig(t *testing.T, mappings []FieldMapping) *ExtractRecordMatch {
	t.Helper()
	return preparedFieldConfig(t, mappings, "//item", nil)
}

func mustParseXML(t *testing.T, raw string) *xmlquery.Node {
	t.Helper()
	doc, err := xmlquery.Parse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}
	return doc
}

func assertNoInternalWrappers(t *testing.T, rec map[string]interface{}) {
	t.Helper()
	for k, v := range rec {
		if _, ok := v.(InternalField); ok {
			t.Errorf("field %q still wrapped in InternalField", k)
		}
	}
}

func assertNumeric(t *testing.T, got interface{}, want float64) {
	t.Helper()
	switch v := got.(type) {
	case float64:
		if v != want {
			t.Errorf("got %v, want %v", v, want)
		}
	case float32:
		if float64(v) != want {
			t.Errorf("got %v, want %v", v, want)
		}
	case int:
		if float64(v) != want {
			t.Errorf("got %v, want %v", v, want)
		}
	case int64:
		if float64(v) != want {
			t.Errorf("got %v, want %v", v, want)
		}
	default:
		t.Errorf("got %#v (%T), want numeric %v", got, got, want)
	}
}
