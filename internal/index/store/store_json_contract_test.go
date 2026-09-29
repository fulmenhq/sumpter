package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJSONStoreRefusesInvalidCurrentHeaders(t *testing.T) {
	sha := strings.Repeat("a", 64)
	head := func(version, format, contexts string) string {
		f := ""
		if format != "" {
			f = `"format":"` + format + `",`
		}
		return `{"version":"` + version + `","source":{"path":"in","size_bytes":1,"sha256":"` + sha + `","compressed":false,"offset_kind":"source_bytes",` + f + `"created_at":"2026-01-01T00:00:00Z"},"selector":{"xpath":"//r","element_name":"r"},"namespace_contexts":` + contexts + `,"records":[],"summary":{"total_records":0,"total_bytes":0,"avg_record_size_bytes":0,"min_record_size_bytes":0,"max_record_size_bytes":0},"metadata":{"generator":"t"}}`
	}
	for _, tc := range []struct{ name, body, want string }{
		{"v0.1.3 without format", head("record-index/v0.1.3", "", `[{"id":0,"declarations":[]}]`), "requires source.format"},
		{"v0.1.3 unknown format", head("record-index/v0.1.3", "yaml", `[{"id":0,"declarations":[]}]`), `source.format "yaml"`},
		{"v0.1.3 null context", head("record-index/v0.1.3", "json", `[{"id":0,"declarations":null}]`), "has no declarations array"},
		{"v0.1.2 forging json", head("record-index/v0.1.2", "json", `[{"id":0,"declarations":null}]`), "XML only"},
		{"unknown version", head("record-index/v9", "json", `[{"id":0,"declarations":[]}]`), "unsupported index version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "i.recordindex.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			s, err := Open(path)
			if err == nil {
				_ = s.Close()
				t.Fatal("header accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q, want %q", err, tc.want)
			}
		})
	}
	legacy := filepath.Join(t.TempDir(), "legacy.recordindex.json")
	if err := os.WriteFile(legacy, []byte(head("record-index/v0.1.2", "", `[{"id":0,"declarations":null}]`)), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(legacy)
	if err != nil {
		t.Fatalf("legacy v0.1.2 with null context refused: %v", err)
	}
	_ = s.Close()
}
