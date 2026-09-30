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
		ctx := ""
		if contexts != "" {
			ctx = `"namespace_contexts":` + contexts + `,`
		}
		return `{"version":"` + version + `","source":{"path":"in","size_bytes":1,"sha256":"` + sha + `","compressed":false,"offset_kind":"source_bytes",` + f + `"created_at":"2026-01-01T00:00:00Z"},"selector":{"xpath":"//r","element_name":"r"},` + ctx + `"records":[],"summary":{"total_records":0,"total_bytes":0,"avg_record_size_bytes":0,"min_record_size_bytes":0,"max_record_size_bytes":0},"metadata":{"generator":"t"}}`
	}
	for _, tc := range []struct{ name, body, want string }{
		{"v0.1.3 without format", head("record-index/v0.1.3", "", `[{"id":0,"declarations":[]}]`), "requires source.format"},
		{"v0.1.3 unknown format", head("record-index/v0.1.3", "yaml", `[{"id":0,"declarations":[]}]`), `source.format "yaml"`},
		{"v0.1.3 null context", head("record-index/v0.1.3", "json", `[{"id":0,"declarations":null}]`), "has no declarations array"},
		{"v0.1.3 json missing table", head("record-index/v0.1.3", "json", ""), "requires a namespace_contexts table"},
		{"v0.1.3 json empty table", head("record-index/v0.1.3", "json", `[]`), "requires a namespace_contexts table"},
		{"v0.1.3 xml missing table", head("record-index/v0.1.3", "xml", ""), "requires a namespace_contexts table"},
		{"v0.1.3 xml empty table", head("record-index/v0.1.3", "xml", `[]`), "requires a namespace_contexts table"},
		{"v0.1.3 xml duplicate id", head("record-index/v0.1.3", "xml", `[{"id":0,"declarations":[]},{"id":0,"declarations":[]}]`), "more than once"},
		{"v0.1.3 xml negative id", head("record-index/v0.1.3", "xml", `[{"id":-1,"declarations":[]}]`), "is negative"},
		{"v0.1.3 json wrong id", head("record-index/v0.1.3", "json", `[{"id":7,"declarations":[]}]`), "only namespace context 0; found context 7"},
		{"v0.1.3 json extra context", head("record-index/v0.1.3", "json", `[{"id":0,"declarations":[]},{"id":1,"declarations":[]}]`), "exactly one namespace context"},
		{"v0.1.3 json duplicate context", head("record-index/v0.1.3", "json", `[{"id":0,"declarations":[]},{"id":0,"declarations":[]}]`), "more than once"},
		{"v0.1.3 json declarations", head("record-index/v0.1.3", "json", `[{"id":0,"declarations":[{"prefix":"p","uri":"urn:x"}]}]`), "carries namespace declarations"},
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
	for name, body := range map[string]string{
		"null context":  head("record-index/v0.1.2", "", `[{"id":0,"declarations":null}]`),
		"missing table": head("record-index/v0.1.2", "", ""),
		"empty table":   head("record-index/v0.1.2", "", `[]`),
	} {
		legacy := filepath.Join(t.TempDir(), "legacy.recordindex.json")
		if err := os.WriteFile(legacy, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := Open(legacy)
		if err != nil {
			t.Fatalf("legacy v0.1.2 with %s refused: %v", name, err)
		}
		h, err := s.Header()
		_ = s.Close()
		if err != nil || len(h.NamespaceContexts) != 1 || h.NamespaceContexts[0].ID != 0 {
			t.Fatalf("legacy v0.1.2 with %s: contexts %+v, %v", name, h.NamespaceContexts, err)
		}
	}
}
