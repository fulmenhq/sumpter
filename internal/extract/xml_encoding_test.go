package extract

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

func TestProcessFileStreamingASCII(t *testing.T) {
	for _, label := range []string{"ASCII", "US-ASCII", "UTF-8"} {
		t.Run(label, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "records.xml")
			source := fmt.Sprintf(`<?xml version="1.0" encoding="%s"?><Root><Record id="one"><Text>A&amp;B &#xE9;</Text></Record><Record id="two"><Text>second</Text></Record></Root>`, label)
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			sig := &FileSignature{SignatureID: "synthetic", ConfidenceThreshold: 1, MatchPatterns: []MatchPattern{{Selector: "/Root", Weight: 1}}}
			cfg := &ExtractRecordMatch{RecordType: "record", MatchSelectors: []MatchSelector{{XPath: "//Record"}}, FieldMappings: []FieldMapping{{OutputField: "id", XPath: "@id", Type: "string"}, {OutputField: "text", XPath: "Text", Type: "string"}}}
			dom := ProcessFile(path, sig, cfg, nil, false)
			collected := ProcessFileStreaming(path, sig, cfg, nil)
			sink := &trackingRecordSink{}
			streamed := ProcessFileStreamingToSink(context.Background(), path, sig, cfg, nil, provenance.RuntimeOptions{}, sink)
			if dom.Error != nil || collected.Error != nil || streamed.Error != nil {
				t.Fatalf("DOM=%v collected=%v sink=%v", dom.Error, collected.Error, streamed.Error)
			}
			for route, records := range map[string][]map[string]interface{}{"DOM": dom.Records, "collected": collected.Records, "sink": sink.records} {
				if len(records) != 2 {
					t.Fatalf("%s emitted %d records", route, len(records))
				}
				for i, record := range records {
					data := extractDataBlock(t, record)
					wantID, wantText := "one", "A&B é"
					if i == 1 {
						wantID, wantText = "two", "second"
					}
					if data["id"] != wantID || data["text"] != wantText || extractRuntimeBlock(t, record)["record_num"] != i+1 {
						t.Fatalf("%s record %d = %#v", route, i, record)
					}
				}
			}
			if len(streamed.Records) != 0 {
				t.Fatal("sink route retained records")
			}
		})
	}
}

func TestProcessFileStreamingASCIIInvalidBytes(t *testing.T) {
	for _, text := range []string{"\x80", "café"} {
		t.Run(fmt.Sprintf("%x", text), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid.xml")
			source := `<?xml version="1.0" encoding="ASCII"?><Root><Record><Text>` + text + `</Text></Record></Root>`
			if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg := &ExtractRecordMatch{RecordType: "record", MatchSelectors: []MatchSelector{{XPath: "//Record"}}, FieldMappings: []FieldMapping{{OutputField: "text", XPath: "Text", Type: "string"}}}
			collected := ProcessFileStreaming(path, nil, cfg, nil)
			sink := &trackingRecordSink{}
			streamed := ProcessFileStreamingToSink(context.Background(), path, nil, cfg, nil, provenance.RuntimeOptions{}, sink)
			for route, result := range map[string]ExtractResult{"collected": collected, "sink": streamed} {
				if result.Error == nil || !strings.Contains(result.Error.Error(), "non-ASCII byte") || len(result.Records) != 0 {
					t.Fatalf("%s result = %+v", route, result)
				}
			}
			if len(sink.records) != 0 {
				t.Fatal("invalid record emitted to sink")
			}
		})
	}
}
