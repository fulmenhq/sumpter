package parallel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

const (
	defaultMemoryRegressionRecords = 20000
	memoryRegressionPayloadBytes   = 512
	memoryRegressionSampleEvery    = 512
	maxRetainedHeapGrowthBytes     = 16 * 1024 * 1024
	maxPeakHeapGrowthBytes         = 128 * 1024 * 1024
)

func TestRecordSinkMemoryRegression_SequentialJSONStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory-regression fixture in short mode")
	}
	restoreGC := tuneGCForMemoryRegression()
	defer restoreGC()

	recordCount := memoryRegressionRecordCount(t)
	tmpDir := t.TempDir()
	xmlPath := writeMemoryRegressionXML(t, tmpDir, recordCount)

	forceGC()
	baseline := currentHeapAlloc()
	sink := newHeapTrackingJSONSink(io.Discard, baseline)

	result := extract.ProcessFileStreamingToSink(
		context.Background(),
		xmlPath,
		memoryRegressionSignature(),
		memoryRegressionExtractConfig(),
		nil,
		memoryRegressionRuntime(),
		sink,
	)
	if closeErr := sink.Close(context.Background()); closeErr != nil {
		t.Fatalf("Close sink: %v", closeErr)
	}
	if result.Error != nil {
		t.Fatalf("ProcessFileStreamingToSink: %v", result.Error)
	}
	if len(result.Records) != 0 {
		t.Fatalf("streaming sink path retained %d records in ExtractResult.Records", len(result.Records))
	}
	assertMemoryRegressionSink(t, "sequential", sink, recordCount)
	assertRetainedHeapBound(t, "sequential", baseline)
}

func TestRecordSinkMemoryRegression_ParallelJSONStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory-regression fixture in short mode")
	}
	restoreGC := tuneGCForMemoryRegression()
	defer restoreGC()

	recordCount := memoryRegressionRecordCount(t)
	tmpDir := t.TempDir()
	xmlPath := writeMemoryRegressionXML(t, tmpDir, recordCount)
	indexPath := filepath.Join(tmpDir, "memory.recordindex.json")
	builder := index.NewBuilder(index.BuildOptions{
		InputPath:  xmlPath,
		OutputPath: indexPath,
		Selector:   "//record",
	})
	if _, err := builder.BuildTo(index.NewJSONIndexWriter(indexPath)); err != nil {
		t.Fatalf("BuildTo index: %v", err)
	}

	forceGC()
	baseline := currentHeapAlloc()
	sink := newHeapTrackingJSONSink(io.Discard, baseline)
	opts := ExtractionOptions{
		IndexPath:         indexPath,
		SourcePath:        xmlPath,
		Workers:           4,
		VerifyIndex:       false,
		ExtractConfig:     memoryRegressionExtractConfig(),
		SignatureConfig:   memoryRegressionSignature(),
		RuntimeProvenance: memoryRegressionRuntime(),
		ReorderWindow:     64,
	}

	summary, err := NewParallelExtractor(opts).ExtractToSink(context.Background(), sink)
	if closeErr := sink.Close(context.Background()); closeErr != nil {
		t.Fatalf("Close sink: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("ExtractToSink: %v", err)
	}
	if summary.RecordCount != recordCount {
		t.Fatalf("summary RecordCount = %d, want %d", summary.RecordCount, recordCount)
	}
	assertMemoryRegressionSink(t, "parallel", sink, recordCount)
	assertRetainedHeapBound(t, "parallel", baseline)
}

func memoryRegressionRecordCount(t *testing.T) int {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv("SUMPTER_MEMORY_REGRESSION_RECORDS"))
	if raw == "" {
		return defaultMemoryRegressionRecords
	}
	count, err := strconv.Atoi(raw)
	if err != nil || count <= 0 {
		t.Fatalf("SUMPTER_MEMORY_REGRESSION_RECORDS=%q is not a positive integer", raw)
	}
	return count
}

func tuneGCForMemoryRegression() func() {
	previous := debug.SetGCPercent(20)
	return func() {
		debug.SetGCPercent(previous)
	}
}

func writeMemoryRegressionXML(t *testing.T, dir string, records int) string {
	t.Helper()

	path := filepath.Join(dir, "memory-regression.xml")
	file, err := os.Create(path) // #nosec G304 - test fixture path under t.TempDir
	if err != nil {
		t.Fatalf("Create XML fixture: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("Close XML fixture: %v", err)
		}
	}()

	writer := bufio.NewWriterSize(file, 64*1024)
	payload := strings.Repeat("x", memoryRegressionPayloadBytes)
	if _, err := writer.WriteString(`<?xml version="1.0" encoding="UTF-8"?><root>`); err != nil {
		t.Fatalf("Write XML header: %v", err)
	}
	for i := 1; i <= records; i++ {
		if _, err := fmt.Fprintf(
			writer,
			`<record id="%06d"><name>item-%06d</name><value>%d</value><payload>%s</payload></record>`,
			i,
			i,
			i,
			payload,
		); err != nil {
			t.Fatalf("Write XML record %d: %v", i, err)
		}
	}
	if _, err := writer.WriteString(`</root>`); err != nil {
		t.Fatalf("Write XML footer: %v", err)
	}
	if err := writer.Flush(); err != nil {
		t.Fatalf("Flush XML fixture: %v", err)
	}

	return path
}

func memoryRegressionSignature() *extract.FileSignature {
	return &extract.FileSignature{
		SignatureID:         "memory-regression",
		ConfidenceThreshold: 1.0,
		MatchPatterns: []extract.MatchPattern{
			{PatternID: "root", Selector: "/root", Weight: 1.0},
		},
	}
}

func memoryRegressionExtractConfig() *extract.ExtractRecordMatch {
	return &extract.ExtractRecordMatch{
		RecordType: "memory_record",
		MatchSelectors: []extract.MatchSelector{
			{XPath: "//record"},
		},
		FieldMappings: []extract.FieldMapping{
			{OutputField: "id", XPath: "//@id", Type: "string"},
			{OutputField: "name", XPath: "//name", Type: "string"},
			{OutputField: "payload", XPath: "//payload", Type: "string"},
		},
	}
}

func memoryRegressionRuntime() provenance.RuntimeOptions {
	return provenance.RuntimeOptions{
		RunID:          "01923f7d-0000-7000-8000-000000000035",
		SumpterVersion: "test",
	}
}

type heapTrackingJSONSink struct {
	sink         *extract.JSONLRecordSink
	baselineHeap uint64
	maxHeapAlloc uint64
	records      int
	boundaries   int
	lastBoundary extract.FileEmissionSummary
}

func newHeapTrackingJSONSink(writer io.Writer, baseline uint64) *heapTrackingJSONSink {
	return &heapTrackingJSONSink{
		sink:         extract.NewJSONLRecordSink(writer),
		baselineHeap: baseline,
		maxHeapAlloc: baseline,
	}
}

func (s *heapTrackingJSONSink) OnRecord(ctx context.Context, record extract.EmittedRecord) error {
	if err := s.sink.OnRecord(ctx, record); err != nil {
		return err
	}
	s.records++
	if s.records%memoryRegressionSampleEvery == 0 {
		if heap := currentHeapAlloc(); heap > s.maxHeapAlloc {
			s.maxHeapAlloc = heap
		}
	}
	return nil
}

func (s *heapTrackingJSONSink) OnFileBoundary(ctx context.Context, summary extract.FileEmissionSummary) error {
	s.boundaries++
	s.lastBoundary = summary
	return s.sink.OnFileBoundary(ctx, summary)
}

func (s *heapTrackingJSONSink) Close(ctx context.Context) error {
	return s.sink.Close(ctx)
}

func assertMemoryRegressionSink(t *testing.T, name string, sink *heapTrackingJSONSink, wantRecords int) {
	t.Helper()

	if sink.records != wantRecords {
		t.Fatalf("%s sink records = %d, want %d", name, sink.records, wantRecords)
	}
	if sink.sink.Count() != wantRecords {
		t.Fatalf("%s JSONL count = %d, want %d", name, sink.sink.Count(), wantRecords)
	}
	if sink.boundaries != 1 {
		t.Fatalf("%s file boundaries = %d, want 1", name, sink.boundaries)
	}
	if sink.lastBoundary.RecordCount != wantRecords {
		t.Fatalf("%s boundary RecordCount = %d, want %d", name, sink.lastBoundary.RecordCount, wantRecords)
	}
	if growth := positiveDelta(sink.maxHeapAlloc, sink.baselineHeap); growth > maxPeakHeapGrowthBytes {
		t.Fatalf("%s peak heap growth = %s, want <= %s", name, byteCount(growth), byteCount(maxPeakHeapGrowthBytes))
	}
	t.Logf("%s streamed %d records; peak heap growth %s", name, wantRecords, byteCount(positiveDelta(sink.maxHeapAlloc, sink.baselineHeap)))
}

func assertRetainedHeapBound(t *testing.T, name string, baseline uint64) {
	t.Helper()

	forceGC()
	retained := positiveDelta(currentHeapAlloc(), baseline)
	if retained > maxRetainedHeapGrowthBytes {
		t.Fatalf("%s retained heap growth = %s, want <= %s", name, byteCount(retained), byteCount(maxRetainedHeapGrowthBytes))
	}
	t.Logf("%s retained heap growth after GC %s", name, byteCount(retained))
}

func forceGC() {
	runtime.GC()
	runtime.GC()
}

func currentHeapAlloc() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.HeapAlloc
}

func positiveDelta(current, baseline uint64) uint64 {
	if current <= baseline {
		return 0
	}
	return current - baseline
}

func byteCount(bytes uint64) string {
	const mib = 1024 * 1024
	return fmt.Sprintf("%.1f MiB", float64(bytes)/mib)
}
