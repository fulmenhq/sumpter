package index

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFinalSummaryIsReadAfterRecordsAndUntrusted(t *testing.T) {
	sha := strings.Repeat("a", 64)
	head := `{"version":"record-index/v0.1.3","source":{"path":"in","size_bytes":1,"sha256":"` + sha + `","compressed":false,"offset_kind":"source_bytes","format":"json","created_at":"2026-01-01T00:00:00Z"},"selector":{"xpath":"//r","element_name":"r"},"namespace_contexts":[{"id":0,"declarations":[]}],"records":[{"record_num":1,"start_offset":0,"end_offset":1,"size_bytes":1,"sha256":"` + sha + `","element_name":"r","depth":1,"namespace_context_ref":0}]`
	summary := `"summary":{"total_records":1,"total_bytes":1,"avg_record_size_bytes":1,"min_record_size_bytes":1,"max_record_size_bytes":1}`
	for _, tc := range []struct{ name, body, want string }{
		{"trailing summary", head + `,` + summary + `,"metadata":{"generator":"t"}}`, ""},
		{"no summary", head + `,"metadata":{"generator":"t"}}`, "no summary field"},
		{"duplicate summary", head + `,` + summary + `,` + summary + `}`, "duplicate summary field"},
		{"negative count", head + `,"summary":{"total_records":-1,"total_bytes":0,"avg_record_size_bytes":0,"min_record_size_bytes":0,"max_record_size_bytes":0}}`, "negative total_records"},
		{"data after object", head + `,` + summary + `} {"x":1}`, "data after the index object"},
		{"malformed summary", head + `,"summary":{"total_records":"one"}}`, "cannot unmarshal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "i.recordindex.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := OpenRecordIndexStream(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = s.Close() }()
			h, err := s.Header()
			if err != nil {
				t.Fatal(err)
			}
			if h.Summary.TotalRecords != 0 {
				t.Fatalf("header snapshot already carries the trailing summary: %d", h.Summary.TotalRecords)
			}
			if _, err := s.FinalSummary(); err == nil || !strings.Contains(err.Error(), "not available until every record has been read") {
				t.Fatalf("FinalSummary before EOF: %v", err)
			}
			var iterErr error
			for {
				if _, iterErr = s.NextRecord(); iterErr != nil {
					break
				}
			}
			var got SummaryStats
			if iterErr == io.EOF {
				got, err = s.FinalSummary()
			} else {
				err = iterErr
			}
			if tc.want == "" {
				if err != nil || got.TotalRecords != 1 {
					t.Fatalf("summary %+v, %v", got, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want %q", err, tc.want)
			}
		})
	}
}
