package schemacontract

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func schemaFile(id string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(`{"$id":"` + id + `","type":"object"}`)}
}

func manifestFile(t *testing.T, m map[string]interface{}) *fstest.MapFile {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	return &fstest.MapFile{Data: data}
}

// baseBundle is a small valid bundle: a recipes version that owns an entry and
// one object schema and declares an extract schema as a companion.
func baseBundle(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"recipes/v0.1.0/recipe.json": schemaFile("contract://sumpter.recipes/v0.1.0/recipe.json"),
		"recipes/v0.1.0/extra.json":  schemaFile("contract://sumpter.recipes/v0.1.0/extra.json"),
		"recipes/v0.1.0/contract.json": manifestFile(t, map[string]interface{}{
			"capability":     "contract: sumpter.recipes/v0",
			"entry_schema":   "recipe.json",
			"object_schemas": map[string]string{"extra": "extra.json"},
			"kinds":          map[string]string{"entry": "input", "extra": "input"},
			"companions":     []string{"../../extract/v0.1.0/signature.json"},
		}),
		"extract/v0.1.0/envelope.json":  schemaFile("contract://sumpter.extract/v0.1.0/envelope.json"),
		"extract/v0.1.0/signature.json": schemaFile("contract://sumpter.extract/v0.1.0/signature.json"),
		"extract/v0.1.0/contract.json": manifestFile(t, map[string]interface{}{
			"capability":     "contract: sumpter.extract/v0",
			"entry_schema":   "envelope.json",
			"object_schemas": map[string]string{"signature": "signature.json"},
			"kinds":          map[string]string{"entry": "output", "signature": "input"},
		}),
	}
}

func checkBundle(t *testing.T, files fstest.MapFS) []error {
	t.Helper()
	b, err := LoadBundle(files)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	manifests, err := LoadManifests(files)
	if err != nil {
		return []error{err}
	}
	errs := append(CheckIDs(b), CheckManifests(b, manifests)...)
	return append(errs, Audit(b, CompanionsFrom(b, manifests))...)
}

func setManifest(t *testing.T, files fstest.MapFS, dir string, edit func(m map[string]interface{})) {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(files[dir+"/contract.json"].Data, &m); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	edit(m)
	files[dir+"/contract.json"] = manifestFile(t, m)
}

func TestManifestsValidBundle(t *testing.T) {
	if errs := checkBundle(t, baseBundle(t)); len(errs) != 0 {
		t.Fatalf("valid bundle rejected: %v", errs)
	}
}

func TestManifestNegatives(t *testing.T) {
	cases := []struct {
		name string
		edit func(t *testing.T, files fstest.MapFS)
		want string
	}{
		{"unowned resource", func(t *testing.T, files fstest.MapFS) {
			files["recipes/v0.1.0/stray.json"] = schemaFile("contract://sumpter.recipes/v0.1.0/stray.json")
		}, "not owned by a contract.json"},
		{"resource listed twice", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["object_schemas"] = map[string]string{"extra": "extra.json", "again": "extra.json"}
				m["kinds"] = map[string]string{"entry": "input", "extra": "input", "again": "input"}
			})
		}, "listed twice"},
		{"missing entry_schema", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) { delete(m, "entry_schema") })
		}, "entry_schema is required"},
		{"missing kind", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["kinds"] = map[string]string{"entry": "input"}
			})
		}, `kinds has no entry for "extra"`},
		{"bad kind", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["kinds"] = map[string]string{"entry": "input", "extra": "both"}
			})
		}, `must be "input" or "output"`},
		{"kind for unowned name", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["kinds"] = map[string]string{"entry": "input", "extra": "input", "ghost": "input"}
			})
		}, `kinds["ghost"] does not name an owned resource`},
		{"reserved object name", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["object_schemas"] = map[string]string{"entry": "extra.json"}
			})
		}, "reserved name"},
		{"capability family mismatch", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["capability"] = "contract: sumpter.index/v0"
			})
		}, "does not match directory family"},
		{"capability major mismatch", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["capability"] = "contract: sumpter.recipes/v1"
			})
		}, "family and major must be equal"},
		{"malformed capability", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["capability"] = "contract:recipes/v0"
			})
		}, "must be \"contract: sumpter.<family>/v<major>\""},
		{"same major, different token", func(t *testing.T, files fstest.MapFS) {
			files["recipes/v0.2.0/recipe.json"] = schemaFile("contract://sumpter.recipes/v0.2.0/recipe.json")
			files["recipes/v0.2.0/contract.json"] = manifestFile(t, map[string]interface{}{
				"capability":   "contract: sumpter.recipes/v00",
				"entry_schema": "recipe.json",
				"kinds":        map[string]string{"entry": "input"},
			})
		}, "differs from"},
		{"unknown manifest field", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) { m["surprise"] = true })
		}, "unknown field"},
		{"manifest outside a version dir", func(t *testing.T, files fstest.MapFS) {
			files["recipes/contract.json"] = manifestFile(t, map[string]interface{}{
				"capability": "contract: sumpter.recipes/v0", "entry_schema": "x.json", "kinds": map[string]string{"entry": "input"},
			})
		}, "must live in a <family>/<version> directory"},
		{"companion escapes bundle", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["companions"] = []string{"../../../outside.json"}
			})
		}, "escapes the schema bundle"},
		{"absolute companion", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["companions"] = []string{"/etc/passwd"}
			})
		}, "must be a relative slash path"},
		{"dangling companion", func(t *testing.T, files fstest.MapFS) {
			setManifest(t, files, "recipes/v0.1.0", func(m map[string]interface{}) {
				m["companions"] = []string{"../../extract/v0.1.0/nosuch.json"}
			})
		}, "does not resolve to a schema file"},
		{"cross-family ref without companion", func(t *testing.T, files fstest.MapFS) {
			files["recipes/v0.1.0/extra.json"] = &fstest.MapFile{Data: []byte(`{"$id":"contract://sumpter.recipes/v0.1.0/extra.json","$ref":"contract://sumpter.extract/v0.1.0/envelope.json"}`)}
		}, "is not a declared companion"},
		{"relative ref cannot leave its family", func(t *testing.T, files fstest.MapFS) {
			// The family is the id authority, so ../ stops at it: this resolves
			// to contract://sumpter.recipes/extract/..., which is not a bundle id.
			files["recipes/v0.1.0/extra.json"] = &fstest.MapFile{Data: []byte(`{"$id":"contract://sumpter.recipes/v0.1.0/extra.json","$ref":"../../extract/v0.1.0/signature.json"}`)}
		}, `resolved id "contract://sumpter.recipes/extract/v0.1.0/signature.json" not in catalog`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := baseBundle(t)
			tc.edit(t, files)
			errs := checkBundle(t, files)
			for _, e := range errs {
				if strings.Contains(e.Error(), tc.want) {
					return
				}
			}
			t.Fatalf("errors %v do not contain %q", errs, tc.want)
		})
	}
}

func TestCompanionRefAllowed(t *testing.T) {
	files := baseBundle(t)
	files["recipes/v0.1.0/extra.json"] = &fstest.MapFile{Data: []byte(`{"$id":"contract://sumpter.recipes/v0.1.0/extra.json","$ref":"contract://sumpter.extract/v0.1.0/signature.json#/definitions/x"}`)}
	if errs := checkBundle(t, files); len(errs) != 0 {
		t.Fatalf("declared companion ref rejected: %v", errs)
	}
}

func TestRepositoryBundleAndCatalog(t *testing.T) {
	dir := materializeEmbedded(t)
	fsys := os.DirFS(dir)
	b, err := LoadBundle(fsys)
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	manifests, err := LoadManifests(fsys)
	if err != nil {
		t.Fatalf("LoadManifests: %v", err)
	}
	errs := append(CheckIDs(b), CheckManifests(b, manifests)...)
	errs = append(errs, Audit(b, CompanionsFrom(b, manifests))...)
	if len(errs) != 0 {
		t.Fatalf("embedded bundle fails: %v", errs)
	}
	catalog, err := BuildCatalog(fsys, b, manifests)
	if err != nil {
		t.Fatalf("BuildCatalog: %v", err)
	}
	want, err := catalog.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	have, err := os.ReadFile(filepath.Join(dir, CatalogFile))
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if string(have) != string(want) {
		t.Fatal("embedded schemas/index.json is out of date with the bundle")
	}
	if len(catalog.Resources) != len(b.Resources) {
		t.Fatalf("catalog lists %d resources, bundle has %d", len(catalog.Resources), len(b.Resources))
	}
	for _, e := range catalog.Resources {
		if e.Kind != "input" && e.Kind != "output" {
			t.Fatalf("%s has kind %q", e.ID, e.Kind)
		}
	}

	// A hand-edited digest must no longer match a regenerated catalog.
	tampered := strings.Replace(string(have), catalog.Resources[0].SHA256, "sha256:"+strings.Repeat("0", 64), 1)
	if tampered == string(want) {
		t.Fatal("tampering did not change the catalog")
	}
}

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v0.1.2", "v0.1.10", -1},
		{"v0.2.0", "v0.1.9", 1},
		{"v1", "v0.9.9", 1},
		{"v0.1.0", "v0.1.0", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.a, tc.b); got != tc.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}
