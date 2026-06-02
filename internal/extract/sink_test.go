package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestEmittedRecordEnvelopeDefensiveCopy(t *testing.T) {
	original := map[string]interface{}{
		"_runtime": map[string]interface{}{
			"record_num": 1,
		},
		"extract": map[string]interface{}{
			"summary": map[string]interface{}{
				"totals": map[string]interface{}{
					"components": []map[string]interface{}{
						{"name": "quantity", "value": 10},
					},
				},
			},
			"data": map[string]interface{}{
				"id": "before",
				"lines": []map[string]interface{}{
					{"sku": "sku-1", "quantity": 2},
				},
				"tags": []interface{}{
					"alpha",
					map[string]interface{}{"label": "nested"},
				},
			},
		},
	}

	record := NewEmittedRecord(original)

	original["new_field"] = "mutated"
	original["_runtime"].(map[string]interface{})["record_num"] = 99
	original["extract"].(map[string]interface{})["data"].(map[string]interface{})["id"] = "after"
	original["extract"].(map[string]interface{})["data"].(map[string]interface{})["lines"].([]map[string]interface{})[0]["sku"] = "sku-mutated"
	original["extract"].(map[string]interface{})["data"].(map[string]interface{})["tags"].([]interface{})[1].(map[string]interface{})["label"] = "changed"
	original["extract"].(map[string]interface{})["summary"].(map[string]interface{})["totals"].(map[string]interface{})["components"].([]map[string]interface{})[0]["value"] = 99

	envelope := record.Envelope()
	if _, ok := envelope["new_field"]; ok {
		t.Fatalf("emitted record included mutation from original map")
	}
	if got := envelope["_runtime"].(map[string]interface{})["record_num"]; got != 1 {
		t.Fatalf("record_num changed through original map mutation: %v", got)
	}
	data := envelope["extract"].(map[string]interface{})["data"].(map[string]interface{})
	if got := data["id"]; got != "before" {
		t.Fatalf("extract.data changed through original map mutation: %v", got)
	}
	tags := data["tags"].([]interface{})
	if got := tags[1].(map[string]interface{})["label"]; got != "nested" {
		t.Fatalf("nested slice/map changed through original map mutation: %v", got)
	}
	lines := data["lines"].([]map[string]interface{})
	if got := lines[0]["sku"]; got != "sku-1" {
		t.Fatalf("typed payload slice changed through original map mutation: %v", got)
	}
	components := envelope["extract"].(map[string]interface{})["summary"].(map[string]interface{})["totals"].(map[string]interface{})["components"].([]map[string]interface{})
	if got := components[0]["value"]; got != 10 {
		t.Fatalf("typed summary components changed through original map mutation: %v", got)
	}

	envelope["_runtime"].(map[string]interface{})["record_num"] = 42
	envelope["extract"].(map[string]interface{})["data"].(map[string]interface{})["lines"].([]map[string]interface{})[0]["sku"] = "sku-returned-mutation"
	envelope["extract"].(map[string]interface{})["summary"].(map[string]interface{})["totals"].(map[string]interface{})["components"].([]map[string]interface{})[0]["value"] = 123
	again := record.Envelope()
	if got := again["_runtime"].(map[string]interface{})["record_num"]; got != 1 {
		t.Fatalf("record envelope changed through returned map mutation: %v", got)
	}
	againData := again["extract"].(map[string]interface{})["data"].(map[string]interface{})
	if got := againData["lines"].([]map[string]interface{})[0]["sku"]; got != "sku-1" {
		t.Fatalf("typed payload slice changed through returned map mutation: %v", got)
	}
	againComponents := again["extract"].(map[string]interface{})["summary"].(map[string]interface{})["totals"].(map[string]interface{})["components"].([]map[string]interface{})
	if got := againComponents[0]["value"]; got != 10 {
		t.Fatalf("typed summary components changed through returned map mutation: %v", got)
	}
}

func TestJSONLRecordSinkWritesRecordsInOrder(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONLRecordSink(&buf)
	ctx := context.Background()

	records := []map[string]interface{}{
		{"_runtime": map[string]interface{}{"record_num": 1}, "extract": map[string]interface{}{"data": map[string]interface{}{"id": "A"}}},
		{"_runtime": map[string]interface{}{"record_num": 3}, "extract": map[string]interface{}{"data": map[string]interface{}{"id": "C"}}},
	}

	for _, record := range records {
		if err := sink.OnRecord(ctx, NewEmittedRecord(record)); err != nil {
			t.Fatalf("OnRecord failed: %v", err)
		}
	}
	if err := sink.OnFileBoundary(ctx, FileEmissionSummary{SourceFile: "input.xml", RecordType: "item", RecordCount: 2}); err != nil {
		t.Fatalf("OnFileBoundary failed: %v", err)
	}
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("idempotent Close failed: %v", err)
	}

	if got := sink.Count(); got != 2 {
		t.Fatalf("Count() = %d, want 2", got)
	}

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d JSONL lines, want 2: %q", len(lines), buf.String())
	}
	for i, line := range lines {
		var decoded map[string]interface{}
		if err := json.Unmarshal([]byte(line), &decoded); err != nil {
			t.Fatalf("line %d is not JSON: %v", i, err)
		}
		runtimeBlock := decoded["_runtime"].(map[string]interface{})
		if got := int(runtimeBlock["record_num"].(float64)); got != []int{1, 3}[i] {
			t.Fatalf("line %d record_num = %d", i, got)
		}
	}
}

func TestJSONLRecordSinkFailureSemantics(t *testing.T) {
	ctx := context.Background()

	if err := NewJSONLRecordSink(errWriter{}).OnRecord(ctx, NewEmittedRecord(map[string]interface{}{"extract": map[string]interface{}{"data": map[string]interface{}{}}})); err == nil {
		t.Fatalf("OnRecord succeeded with failing writer")
	}

	sink := NewJSONLRecordSink(&bytes.Buffer{})
	if err := sink.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
	if err := sink.OnRecord(ctx, NewEmittedRecord(map[string]interface{}{"extract": map[string]interface{}{"data": map[string]interface{}{}}})); err == nil {
		t.Fatalf("OnRecord succeeded after Close")
	}

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewJSONLRecordSink(&bytes.Buffer{}).OnRecord(canceled, NewEmittedRecord(map[string]interface{}{})); !errors.Is(err, context.Canceled) {
		t.Fatalf("OnRecord canceled error = %v, want context.Canceled", err)
	}
	if err := NewJSONLRecordSink(&bytes.Buffer{}).Close(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close canceled error = %v, want context.Canceled", err)
	}
}

type errWriter struct{}

func (errWriter) Write(_ []byte) (int, error) {
	return 0, errors.New("write failed")
}
