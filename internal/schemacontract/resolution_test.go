package schemacontract

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/goneat/pkg/schema"

	"github.com/fulmenhq/sumpter/internal/assets"
)

const (
	envinfoRootID   = "contract://sumpter.envinfo/v0.1.0/complete.schema.json"
	envinfoRootPath = "envinfo/v0.1.0/complete.schema.json"
)

// materializeEmbedded copies the embedded schema bundle into a private temp
// directory. goneat's ref-dir loader reads OS directories only, and this copy
// is the only resolution root the tests use.
func materializeEmbedded(t *testing.T) string {
	t.Helper()
	root, err := assets.GetSchemasFS()
	if err != nil {
		t.Fatalf("GetSchemasFS: %v", err)
	}
	bundle, err := fs.Sub(root, "schemas")
	if err != nil {
		t.Fatalf("fs.Sub schemas: %v", err)
	}
	dir, err := os.MkdirTemp("", "schemacontract-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	err = fs.WalkDir(bundle, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		target := filepath.Join(dir, filepath.FromSlash(p))
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := fs.ReadFile(bundle, p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
	if err != nil {
		t.Fatalf("materialize: %v", err)
	}
	return dir
}

func loadFixture(t *testing.T, mutate func(map[string]interface{})) interface{} {
	t.Helper()
	data, err := os.ReadFile("../../tests/fixtures/envinfo/complete.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if mutate != nil {
		mutate(doc)
	}
	return doc
}

// envinfoCompile validates doc against the envinfo root through goneat with
// dir as the only ref-dir.
func envinfoCompile(t *testing.T, dir string, doc interface{}) (*schema.Result, error) {
	t.Helper()
	root, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(envinfoRootPath)))
	if err != nil {
		t.Fatalf("read root schema: %v", err)
	}
	return schema.ValidateFromBytesWithRefDirs(root, doc, []string{dir})
}

// gate runs the audit gate over dir and reports whether compile was reached.
func gate(t *testing.T, dir string, companions Companions, compile CompileFunc) (reached bool, err error) {
	t.Helper()
	b, err := LoadBundle(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	err = Validate(b, companions, func() error {
		reached = true
		if compile == nil {
			return nil
		}
		return compile()
	})
	return reached, err
}

func writeSchema(t *testing.T, dir, rel string, doc map[string]interface{}) {
	t.Helper()
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	target := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(target, data, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func readSchema(t *testing.T, dir, rel string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	return doc
}

// setRootRef points the envinfo root's "network" property at ref.
func setRootRef(t *testing.T, dir, ref string) {
	t.Helper()
	root := readSchema(t, dir, envinfoRootPath)
	props := root["properties"].(map[string]interface{})
	props["network"].(map[string]interface{})["$ref"] = ref
	writeSchema(t, dir, envinfoRootPath, root)
}

func expectAuditFailure(t *testing.T, reached bool, err error, want string) {
	t.Helper()
	if !errors.Is(err, ErrAuditFailed) {
		t.Fatalf("expected ErrAuditFailed, got %v", err)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("expected %q in:\n%v", want, err)
	}
	if reached {
		t.Fatal("compile was reached despite a failed audit")
	}
}

func TestEmbeddedBundlePassesAudit(t *testing.T) {
	dir := materializeEmbedded(t)
	b, err := LoadBundle(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	if len(b.Resources) == 0 {
		t.Fatal("embedded bundle is empty")
	}
	if errs := append(CheckIDs(b), Audit(b, NoCompanions)...); len(errs) > 0 {
		t.Fatalf("embedded bundle fails: %v", errors.Join(errs...))
	}
}

func TestEnvinfoResolvesOfflineThroughEmbeddedBundle(t *testing.T) {
	dir := materializeEmbedded(t)

	var result *schema.Result
	reached, err := gate(t, dir, NoCompanions, func() error {
		var cerr error
		result, cerr = envinfoCompile(t, dir, loadFixture(t, nil))
		return cerr
	})
	if err != nil || !reached {
		t.Fatalf("valid document: reached=%v err=%v", reached, err)
	}
	if !result.Valid {
		t.Fatalf("fixture should validate, errors: %v", result.Errors)
	}

	// A rule that lives in a sibling schema proves the sibling was resolved.
	_, err = gate(t, dir, NoCompanions, func() error {
		var cerr error
		result, cerr = envinfoCompile(t, dir, loadFixture(t, func(d map[string]interface{}) {
			d["xml"].(map[string]interface{})["maxMemoryTarget"] = "unbounded"
		}))
		return cerr
	})
	if err != nil {
		t.Fatalf("invalid document: %v", err)
	}
	if result.Valid {
		t.Fatal("document violating a sibling-schema rule validated")
	}
}

func TestAuditAgreesWithLibraryResolution(t *testing.T) {
	dir := materializeEmbedded(t)
	b, err := LoadBundle(os.DirFS(dir))
	if err != nil {
		t.Fatalf("LoadBundle: %v", err)
	}
	var root Resource
	for _, r := range b.Resources {
		if r.Path == envinfoRootPath {
			root = r
		}
	}
	if len(root.Refs) != 5 {
		t.Fatalf("expected 5 sibling refs in the envinfo root, got %v", root.Refs)
	}
	for _, ref := range root.Refs {
		resolved, err := resolveRef(root.ID, ref)
		if err != nil {
			t.Fatalf("resolve %q: %v", ref, err)
		}
		want := "contract://sumpter.envinfo/v0.1.0/" + ref
		if resolved != want {
			t.Fatalf("audit resolved %q to %q, want %q", ref, resolved, want)
		}
		// Bypass the audit and remove the sibling: the library must fail on
		// exactly the id the audit computed.
		sibDir := materializeEmbedded(t)
		if err := os.Remove(filepath.Join(sibDir, "envinfo", "v0.1.0", ref)); err != nil {
			t.Fatalf("remove sibling: %v", err)
		}
		_, libErr := envinfoCompile(t, sibDir, loadFixture(t, nil))
		if libErr == nil || !strings.Contains(libErr.Error(), resolved) {
			t.Fatalf("library error for missing %s does not name %q: %v", ref, resolved, libErr)
		}
	}
}

func TestAuditRejectsBeforeCompile(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"sibling removed", func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "envinfo", "v0.1.0", "network.schema.json")); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}, `resolved id "contract://sumpter.envinfo/v0.1.0/network.schema.json" not in catalog`},
		{"https ref", func(t *testing.T, dir string) {
			setRootRef(t, dir, "https://example.com/schemas/network.schema.json")
		}, "not a contract:// bundle id"},
		{"file ref", func(t *testing.T, dir string) {
			setRootRef(t, dir, "file:///etc/network.schema.json")
		}, "not a contract:// bundle id"},
		{"dangling relative ref", func(t *testing.T, dir string) {
			setRootRef(t, dir, "nosuch.schema.json")
		}, `resolved id "contract://sumpter.envinfo/v0.1.0/nosuch.schema.json" not in catalog`},
		{"cross-version ref", func(t *testing.T, dir string) {
			sib := readSchema(t, dir, "envinfo/v0.1.0/network.schema.json")
			sib["$id"] = "contract://sumpter.envinfo/v0.2.0/network.schema.json"
			writeSchema(t, dir, "envinfo/v0.2.0/network.schema.json", sib)
			setRootRef(t, dir, "../v0.2.0/network.schema.json")
		}, "outside envinfo/v0.1.0 and is not a declared companion"},
		{"cross-family ref", func(t *testing.T, dir string) {
			setRootRef(t, dir, "contract://sumpter.index/v0.1.2/record-index.schema.json")
		}, "outside envinfo/v0.1.0 and is not a declared companion"},
		{"opaque id", func(t *testing.T, dir string) {
			sib := readSchema(t, dir, "envinfo/v0.1.0/network.schema.json")
			sib["$id"] = "contract:sumpter.envinfo/v0.1.0/network.schema.json"
			writeSchema(t, dir, "envinfo/v0.1.0/network.schema.json", sib)
		}, "opaque form is not allowed"},
		{"duplicate id", func(t *testing.T, dir string) {
			sib := readSchema(t, dir, "envinfo/v0.1.0/network.schema.json")
			writeSchema(t, dir, "envinfo/v0.1.0/network-copy.schema.json", sib)
		}, "duplicate $id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := materializeEmbedded(t)
			tc.setup(t, dir)
			reached, err := gate(t, dir, NoCompanions, nil)
			expectAuditFailure(t, reached, err, tc.want)
		})
	}
}

func TestAuditAllowsDeclaredCompanion(t *testing.T) {
	dir := materializeEmbedded(t)
	target := "contract://sumpter.index/v0.1.2/record-index.schema.json"
	setRootRef(t, dir, target)
	companions := func(r Resource) map[string]bool {
		if path.Clean(r.Path) == envinfoRootPath {
			return map[string]bool{target: true}
		}
		return nil
	}
	reached, err := gate(t, dir, companions, nil)
	if err != nil || !reached {
		t.Fatalf("declared companion rejected: reached=%v err=%v", reached, err)
	}
}
