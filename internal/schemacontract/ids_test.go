package schemacontract

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseID(t *testing.T) {
	good := []struct {
		id      string
		family  string
		version string
		major   int
	}{
		{"contract://sumpter.index/v0.1.2/record-index.schema.json", "index", "v0.1.2", 0},
		{"contract://sumpter.provenance/v1/provenance.schema.json", "provenance", "v1", 1},
		{"contract://sumpter.envinfo/v0.1.0/complete.schema.json", "envinfo", "v0.1.0", 0},
	}
	for _, tc := range good {
		got, err := ParseID(tc.id)
		if err != nil {
			t.Fatalf("ParseID(%q): %v", tc.id, err)
		}
		if got.Family != tc.family || got.Version != tc.version || got.Major() != tc.major {
			t.Fatalf("ParseID(%q) = %+v major %d", tc.id, got, got.Major())
		}
	}

	bad := []struct {
		id   string
		want string
	}{
		{"contract:sumpter.index/v0.1.2/record-index.schema.json", "opaque form"},
		{"https://sumpter.dev/schemas/index/v0.1.2/record-index.schema.json", "scheme must be"},
		{"contract://Sumpter.index/v0.1.2/x.json", "lower-case"},
		{"contract://sumpter.Index/v0.1.2/x.json", "lower-case"},
		{"contract://other.index/v0.1.2/x.json", "authority must be"},
		{"contract://sumpter./v0.1.2/x.json", "authority must be"},
		{"contract://user@sumpter.index/v0.1.2/x.json", "userinfo"},
		{"contract://sumpter.index:8080/v0.1.2/x.json", "userinfo"},
		{"contract://sumpter.index/v0.1.2/x.json?q=1", "userinfo"},
		{"contract://sumpter.index/v0.1.2/x.json#frag", "userinfo"},
		{"contract://sumpter.index/x.json", "path must be"},
		{"contract://sumpter.index/v0.1.2/sub/x.json", "path must be"},
		{"contract://sumpter.index/0.1.2/x.json", "version segment"},
		{"contract://sumpter.index/v0.1/x.json", "version segment"},
	}
	for _, tc := range bad {
		_, err := ParseID(tc.id)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("ParseID(%q) error = %v, want %q", tc.id, err, tc.want)
		}
	}
}

func TestCheckIDs(t *testing.T) {
	schema := func(id string) *fstest.MapFile {
		if id == "" {
			return &fstest.MapFile{Data: []byte(`{"type":"object"}`)}
		}
		return &fstest.MapFile{Data: []byte(`{"$id":"` + id + `","type":"object"}`)}
	}
	cases := []struct {
		name  string
		files fstest.MapFS
		want  string
	}{
		{"missing id", fstest.MapFS{"index/v0.1.0/a.json": schema("")}, "missing $id"},
		{"version does not match directory", fstest.MapFS{
			"index/v0.1.0/a.json": schema("contract://sumpter.index/v0.1.1/a.json"),
		}, "does not match its location"},
		{"family does not match directory", fstest.MapFS{
			"index/v0.1.0/a.json": schema("contract://sumpter.envinfo/v0.1.0/a.json"),
		}, "does not match its location"},
		{"file does not match name", fstest.MapFS{
			"index/v0.1.0/a.json": schema("contract://sumpter.index/v0.1.0/b.json"),
		}, "does not match its location"},
		{"not under family/version", fstest.MapFS{
			"index/a.json": schema("contract://sumpter.index/v0.1.0/a.json"),
		}, "must live at <family>/<version>/<file>"},
		{"provenance exception enforced", fstest.MapFS{
			"provenance/v1.json": schema("contract://sumpter.provenance/v1/v1.json"),
		}, "does not match its location"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := LoadBundle(tc.files)
			if err != nil {
				t.Fatalf("LoadBundle: %v", err)
			}
			errs := CheckIDs(b)
			if len(errs) == 0 {
				t.Fatalf("expected error containing %q", tc.want)
			}
			found := false
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("errors %v do not contain %q", errs, tc.want)
			}
		})
	}

	ok := fstest.MapFS{
		"provenance/v1.json":  schema("contract://sumpter.provenance/v1/provenance.schema.json"),
		"index/v0.1.0/a.json": schema("contract://sumpter.index/v0.1.0/a.json"),
		"index/README.md":     &fstest.MapFile{Data: []byte("# not a schema")},
		"index/contract.json": &fstest.MapFile{Data: []byte(`{"capability":"contract: sumpter.index/v0"}`)},
	}
	b, err := LoadBundle(ok)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	if errs := CheckIDs(b); len(errs) != 0 {
		t.Fatalf("valid bundle rejected: %v", errs)
	}
}
