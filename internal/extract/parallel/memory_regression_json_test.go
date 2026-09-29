package parallel

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

// TestRecordSinkMemoryRegression_JSONInputStreaming is the JSON twin of the
// sequential fixture: the same records as a keyed array and as ndjson, read
// by the streaming route into the record sink.
func TestRecordSinkMemoryRegression_JSONInputStreaming(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory-regression fixture in short mode")
	}
	for _, format := range []string{extract.FormatJSON, extract.FormatNDJSON} {
		t.Run(format, func(t *testing.T) {
			restoreGC := tuneGCForMemoryRegression()
			defer restoreGC()

			recordCount := memoryRegressionRecordCount(t)
			path := writeMemoryRegressionJSON(t, t.TempDir(), format, recordCount)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}

			forceGC()
			baseline := currentHeapAlloc()
			sink := newHeapTrackingJSONSink(io.Discard, baseline)
			result := extract.ProcessFileStreamingToSink(
				context.Background(),
				path,
				memoryRegressionJSONSignature(format),
				memoryRegressionJSONExtractConfig(),
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
			assertMemoryRegressionSink(t, format, sink, recordCount)
			assertRetainedHeapBound(t, format, baseline)
			t.Logf("%s input %s", format, byteCount(uint64(info.Size())))
		})
	}
}

func writeMemoryRegressionJSON(t *testing.T, dir, format string, records int) string {
	t.Helper()
	path := filepath.Join(dir, "memory-regression."+format)
	file, err := os.Create(path) // #nosec G304 - test fixture path under t.TempDir
	if err != nil {
		t.Fatalf("Create JSON fixture: %v", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Fatalf("Close JSON fixture: %v", err)
		}
	}()
	w := bufio.NewWriterSize(file, 64*1024)
	payload := strings.Repeat("x", memoryRegressionPayloadBytes)
	if format == extract.FormatJSON {
		_, _ = w.WriteString(`{"root":{"record":[`)
	}
	for i := 1; i <= records; i++ {
		sep := "\n"
		if format == extract.FormatJSON {
			sep = ""
			if i > 1 {
				sep = ","
			}
			_, _ = w.WriteString(sep)
			sep = ""
		}
		if _, err := fmt.Fprintf(w, `{"id":"%06d","name":"item-%06d","value":%d,"payload":"%s"}%s`, i, i, i, payload, sep); err != nil {
			t.Fatalf("Write JSON record %d: %v", i, err)
		}
	}
	if format == extract.FormatJSON {
		_, _ = w.WriteString(`]}}`)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("Flush JSON fixture: %v", err)
	}
	return path
}

func memoryRegressionJSONSignature(format string) *extract.FileSignature {
	return &extract.FileSignature{
		SignatureID:         "memory-regression-json",
		FormatType:          format,
		MatchScope:          extract.MatchScopeRecord,
		ConfidenceThreshold: 1.0,
		MatchPatterns:       []extract.MatchPattern{{PatternID: "record", Selector: "/record", Weight: 1.0}},
	}
}

func memoryRegressionJSONExtractConfig() *extract.ExtractRecordMatch {
	return &extract.ExtractRecordMatch{
		RecordType:     "memory_record",
		MatchSelectors: []extract.MatchSelector{{XPath: "//record"}},
		FieldMappings: []extract.FieldMapping{
			{OutputField: "id", XPath: "id", Type: "string"},
			{OutputField: "name", XPath: "name", Type: "string"},
			{OutputField: "payload", XPath: "payload", Type: "string"},
		},
	}
}
