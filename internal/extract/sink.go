package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sync"
)

// EmittedRecord is the immutable envelope handed to RecordSink implementations.
//
// The envelope has the same shape as a JSONL output record: _runtime, optional
// _validation, and extract. Callers receive defensive copies so sinks cannot
// mutate records visible to later sinks or accounting.
type EmittedRecord struct {
	envelope map[string]interface{}
}

// NewEmittedRecord wraps an already-enriched output envelope.
func NewEmittedRecord(envelope map[string]interface{}) EmittedRecord {
	return EmittedRecord{envelope: cloneInterfaceMap(envelope)}
}

// Envelope returns a defensive copy of the emitted record envelope.
func (r EmittedRecord) Envelope() map[string]interface{} {
	return cloneInterfaceMap(r.envelope)
}

// MarshalJSON serializes the immutable envelope without exposing mutable state.
func (r EmittedRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(r.envelope)
}

// FileEmissionSummary reports per-file output state without retaining records.
type FileEmissionSummary struct {
	SourceFile        string
	RecordType        string
	RecordCount       int
	Disposition       Disposition
	DispositionReason DispositionReason
	DispositionDetail string
}

// RecordSink consumes already-enriched emitted records in final output order.
type RecordSink interface {
	OnRecord(ctx context.Context, record EmittedRecord) error
	OnFileBoundary(ctx context.Context, summary FileEmissionSummary) error
	Close(ctx context.Context) error
}

type recordCollectingSink struct {
	records []map[string]interface{}
}

func (s *recordCollectingSink) OnRecord(_ context.Context, record EmittedRecord) error {
	s.records = append(s.records, record.Envelope())
	return nil
}

func (s *recordCollectingSink) OnFileBoundary(context.Context, FileEmissionSummary) error {
	return nil
}

func (s *recordCollectingSink) Close(context.Context) error {
	return nil
}

func (s *recordCollectingSink) Records() []map[string]interface{} {
	if s == nil || len(s.records) == 0 {
		return nil
	}
	records := make([]map[string]interface{}, len(s.records))
	for i, record := range s.records {
		records[i] = cloneInterfaceMap(record)
	}
	return records
}

// JSONLRecordSink writes emitted envelopes as newline-delimited JSON.
type JSONLRecordSink struct {
	writer io.Writer
	mu     sync.Mutex
	closed bool
	count  int
}

// NewJSONLRecordSink creates a sink that writes one JSON object per line.
func NewJSONLRecordSink(writer io.Writer) *JSONLRecordSink {
	return &JSONLRecordSink{writer: writer}
}

// OnRecord writes one emitted record. Slow writers provide synchronous
// backpressure to the caller.
func (s *JSONLRecordSink) OnRecord(ctx context.Context, record EmittedRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil || s.writer == nil {
		return fmt.Errorf("jsonl record sink writer is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("jsonl record sink is closed")
	}
	if err := json.NewEncoder(s.writer).Encode(record); err != nil {
		return fmt.Errorf("write jsonl record: %w", err)
	}
	s.count++
	return nil
}

// WriteMarshaled writes one already-marshaled record verbatim. data must be exactly what
// OnRecord would have written — a single JSON object plus its trailing newline — i.e.
// json.Marshal(record) followed by '\n' (which is byte-identical to json.Encoder.Encode,
// the encoding OnRecord uses). It exists so callers that marshal upstream (extract-multi
// marshals each record on its worker) can replay the bytes here without re-marshaling,
// producing output identical to streaming the records through OnRecord.
func (s *JSONLRecordSink) WriteMarshaled(data []byte) error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("jsonl record sink writer is nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return fmt.Errorf("jsonl record sink is closed")
	}
	if _, err := s.writer.Write(data); err != nil {
		return fmt.Errorf("write jsonl record: %w", err)
	}
	s.count++
	return nil
}

// OnFileBoundary records the boundary contract for JSONL. The current JSONL
// sink has no per-file flush state, so this validates cancellation only.
func (s *JSONLRecordSink) OnFileBoundary(ctx context.Context, _ FileEmissionSummary) error {
	return ctx.Err()
}

// Close is idempotent. The writer lifecycle remains owned by the caller.
func (s *JSONLRecordSink) Close(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.closed = true
	return nil
}

// Count returns the number of records successfully written by the sink.
func (s *JSONLRecordSink) Count() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.count
}

func cloneInterfaceMap(src map[string]interface{}) map[string]interface{} {
	if src == nil {
		return nil
	}
	dst := make(map[string]interface{}, len(src))
	for key, value := range src {
		dst[key] = cloneInterfaceValue(value)
	}
	return dst
}

func cloneInterfaceValue(value interface{}) interface{} {
	if value == nil {
		return nil
	}

	switch typed := value.(type) {
	case map[string]interface{}:
		return cloneInterfaceMap(typed)
	case []interface{}:
		copied := make([]interface{}, len(typed))
		for i, item := range typed {
			copied[i] = cloneInterfaceValue(item)
		}
		return copied
	case []map[string]interface{}:
		copied := make([]map[string]interface{}, len(typed))
		for i, item := range typed {
			copied[i] = cloneInterfaceMap(item)
		}
		return copied
	default:
		return cloneReflectedJSONValue(typed)
	}
}

func cloneReflectedJSONValue(value interface{}) interface{} {
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return value
		}
		copied := make(map[string]interface{}, rv.Len())
		for _, key := range rv.MapKeys() {
			copied[key.String()] = cloneInterfaceValue(rv.MapIndex(key).Interface())
		}
		return copied
	case reflect.Slice, reflect.Array:
		copied := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			copied[i] = cloneInterfaceValue(rv.Index(i).Interface())
		}
		return copied
	default:
		return value
	}
}
