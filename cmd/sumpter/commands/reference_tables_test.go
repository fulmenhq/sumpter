package commands

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

func membershipDecl(source string) recipesmanifest.ReferenceTableDecl {
	return recipesmanifest.ReferenceTableDecl{
		Name: "curated", Source: source, Format: "csv", Header: true, Column: "accession", MaxRows: 100,
	}
}

func writeRefFile(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestBuildReferenceRegistryLoadsContainedSource(t *testing.T) {
	root := t.TempDir()
	writeRefFile(t, root, "refdata/curated.csv", "accession\nNM_000546\nNR_001234\n")

	opts := &ExtractOptions{
		ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")},
		ReferenceTableRoot:  root,
	}
	reg, prov, err := buildReferenceRegistry(context.Background(), opts, "test-run", true)
	if err != nil {
		t.Fatalf("buildReferenceRegistry: %v", err)
	}
	if reg == nil || !reg.Declared("curated") {
		t.Fatal("registry missing curated table")
	}
	if ok, _ := reg.Contains("curated", "NM_000546"); !ok {
		t.Error("curated did not contain NM_000546")
	}
	if len(prov) != 1 {
		t.Fatalf("prov len = %d, want 1", len(prov))
	}
	p := prov[0]
	if p.Name != "curated" || p.Source != "refdata/curated.csv" || p.Format != "csv" || p.Mode != "membership" {
		t.Errorf("prov identity wrong: %#v", p)
	}
	if p.RowCount != 2 || !strings.HasPrefix(p.ContentSHA256, "sha256:") || p.MaxRows != 100 {
		t.Errorf("prov metrics wrong: %#v", p)
	}
}

func TestBuildReferenceRegistryC1Containment(t *testing.T) {
	root := t.TempDir()
	writeRefFile(t, root, "refdata/curated.csv", "accession\nNM_000546\n")
	// A real file just outside the workspace, the exfil target a containment escape
	// would reach.
	outside := filepath.Join(filepath.Dir(root), "outside.csv")
	if err := os.WriteFile(outside, []byte("accession\nSECRET\n"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	// Symlink inside the workspace pointing at the outside file.
	linkRel := "refdata/link.csv"
	if err := os.Symlink(outside, filepath.Join(root, linkRel)); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}

	for _, tc := range []struct {
		name, source, wantErr string
	}{
		{"absolute", outside, "absolute path"},
		{"dotdot", "../outside.csv", "escapes the workspace"},
		{"symlink", linkRel, "symlink"},
		{"missing", "refdata/nope.csv", "not found or unreadable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			opts := &ExtractOptions{
				ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{membershipDecl(tc.source)},
				ReferenceTableRoot:  root,
			}
			_, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantErr)
			}
			// The escaped file's contents must never leak into the error.
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Errorf("error leaked source contents: %v", err)
			}
		})
	}
}

func TestBuildReferenceRegistryDryRunDoesNotLoad(t *testing.T) {
	root := t.TempDir()
	// Malformed/empty source: a real load fails loud, but a dry run must not read it.
	writeRefFile(t, root, "refdata/curated.csv", "")

	opts := &ExtractOptions{
		ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")},
		ReferenceTableRoot:  root,
	}
	// Dry run (load=false): validates containment/resolvability, does NOT parse.
	reg, prov, err := buildReferenceRegistry(context.Background(), opts, "test-run", false)
	if err != nil {
		t.Fatalf("dry-run build failed (should not load): %v", err)
	}
	if reg != nil || prov != nil {
		t.Errorf("dry-run returned a loaded registry: reg=%v prov=%v", reg, prov)
	}
	// Real run (load=true): the empty source now fails loud.
	if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("real-run err = %v, want empty-source abort", err)
	}
}

func TestBuildReferenceRegistryDryRunStillEnforcesContainment(t *testing.T) {
	root := t.TempDir()
	opts := &ExtractOptions{
		ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{membershipDecl("../escape.csv")},
		ReferenceTableRoot:  root,
	}
	if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", false); err == nil || !strings.Contains(err.Error(), "escapes the workspace") {
		t.Fatalf("dry-run err = %v, want containment rejection even without loading", err)
	}
}

func TestBuildReferenceRegistryOverride(t *testing.T) {
	root := t.TempDir()
	writeRefFile(t, root, "refdata/curated.csv", "accession\nNM_000546\n")
	writeRefFile(t, root, "refdata/alt.csv", "accession\nXR_999999\n")

	opts := &ExtractOptions{
		ReferenceTableDecls:     []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")},
		ReferenceTableRoot:      root,
		ReferenceTableOverrides: []string{"curated=refdata/alt.csv"},
	}
	reg, prov, err := buildReferenceRegistry(context.Background(), opts, "test-run", true)
	if err != nil {
		t.Fatalf("buildReferenceRegistry: %v", err)
	}
	if ok, _ := reg.Contains("curated", "XR_999999"); !ok {
		t.Error("override source not used (XR_999999 absent)")
	}
	if ok, _ := reg.Contains("curated", "NM_000546"); ok {
		t.Error("original source still present after override")
	}
	// Provenance records the effective (overridden) source.
	if prov[0].Source != "refdata/alt.csv" {
		t.Errorf("prov source = %q, want overridden refdata/alt.csv", prov[0].Source)
	}
}

func TestBuildReferenceRegistryErrors(t *testing.T) {
	root := t.TempDir()
	writeRefFile(t, root, "refdata/curated.csv", "accession\nNM_000546\n")

	t.Run("override unknown name", func(t *testing.T) {
		opts := &ExtractOptions{
			ReferenceTableDecls:     []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")},
			ReferenceTableRoot:      root,
			ReferenceTableOverrides: []string{"ghost=refdata/curated.csv"},
		}
		if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true); err == nil || !strings.Contains(err.Error(), "no reference table named") {
			t.Fatalf("err = %v, want unknown-override", err)
		}
	})

	t.Run("override without declarations", func(t *testing.T) {
		opts := &ExtractOptions{ReferenceTableOverrides: []string{"curated=x.csv"}}
		if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true); err == nil || !strings.Contains(err.Error(), "declares no reference_tables") {
			t.Fatalf("err = %v, want no-declarations", err)
		}
	})

	t.Run("duplicate override fails loud", func(t *testing.T) {
		opts := &ExtractOptions{
			ReferenceTableDecls:     []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")},
			ReferenceTableRoot:      root,
			ReferenceTableOverrides: []string{"curated=refdata/curated.csv", "curated=refdata/curated.csv"},
		}
		if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true); err == nil || !strings.Contains(err.Error(), "overridden more than once") {
			t.Fatalf("err = %v, want duplicate-override (no silent last-wins)", err)
		}
	})

	t.Run("duplicate declaration", func(t *testing.T) {
		opts := &ExtractOptions{
			ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv"), membershipDecl("refdata/curated.csv")},
			ReferenceTableRoot:  root,
		}
		if _, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", true); err == nil || !strings.Contains(err.Error(), "declared more than once") {
			t.Fatalf("err = %v, want duplicate-declaration", err)
		}
	})

	t.Run("no declarations no overrides", func(t *testing.T) {
		reg, prov, err := buildReferenceRegistry(context.Background(), &ExtractOptions{}, "test-run", true)
		if err != nil || reg != nil || prov != nil {
			t.Fatalf("want (nil,nil,nil), got reg=%v prov=%v err=%v", reg, prov, err)
		}
	})
}

// TestBuildReferenceRegistryCloudRejectsPrefix asserts a cloud reference-table source
// that is a prefix or glob (not a single object) fails statically — before any
// network — on both a dry run (load=false) and a real run (load=true), rather than
// only surfacing later in acquire.
func TestBuildReferenceRegistryCloudRejectsPrefix(t *testing.T) {
	for _, src := range []string{
		"s3://bucket/refdata/",      // prefix (trailing slash)
		"s3://bucket/refdata/*.csv", // glob pattern
	} {
		for _, load := range []bool{false, true} {
			opts := &ExtractOptions{
				ReferenceTableDecls: []recipesmanifest.ReferenceTableDecl{{
					Name: "curated", Source: src, Format: "csv", Header: true, Column: "accession", MaxRows: 100,
					CredentialsHandle: "reader",
				}},
			}
			_, _, err := buildReferenceRegistry(context.Background(), opts, "test-run", load)
			if err == nil || !strings.Contains(err.Error(), "single object") {
				t.Fatalf("src=%q load=%v: err = %v, want single-object rejection", src, load, err)
			}
		}
	}
}

func TestValidateReferenceTableDeclarationsPreflight(t *testing.T) {
	decls := []recipesmanifest.ReferenceTableDecl{membershipDecl("refdata/curated.csv")}

	t.Run("declared reference ok", func(t *testing.T) {
		opts := &ExtractOptions{ReferenceTableDecls: decls}
		mappings := []extract.FieldMapping{{OutputField: "f", Expression: "in_reference('curated', accession)", Type: "boolean"}}
		if err := validateReferenceTableDeclarations(opts, mappings); err != nil {
			t.Fatalf("declared reference rejected: %v", err)
		}
	})

	t.Run("undeclared reference fails preflight", func(t *testing.T) {
		opts := &ExtractOptions{ReferenceTableDecls: decls}
		mappings := []extract.FieldMapping{{OutputField: "f", Expression: "in_reference('nope', accession)", Type: "boolean"}}
		err := validateReferenceTableDeclarations(opts, mappings)
		if err == nil || !strings.Contains(err.Error(), "not declared") {
			t.Fatalf("err = %v, want undeclared-table preflight failure", err)
		}
	})

	t.Run("dynamic table name fails preflight", func(t *testing.T) {
		opts := &ExtractOptions{ReferenceTableDecls: decls}
		mappings := []extract.FieldMapping{{OutputField: "f", Expression: "in_reference(accession, accession)", Type: "boolean"}}
		err := validateReferenceTableDeclarations(opts, mappings)
		if err == nil || !strings.Contains(err.Error(), "string literal") {
			t.Fatalf("err = %v, want dynamic-name rejection", err)
		}
	})

	t.Run("no reference calls ok", func(t *testing.T) {
		opts := &ExtractOptions{}
		mappings := []extract.FieldMapping{{OutputField: "f", Expression: "lower(accession)", Type: "string"}}
		if err := validateReferenceTableDeclarations(opts, mappings); err != nil {
			t.Fatalf("non-reference expression rejected: %v", err)
		}
	})
}
