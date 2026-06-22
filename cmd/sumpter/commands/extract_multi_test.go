package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeriveRecipeOutputSlug_Valid(t *testing.T) {
	cases := []string{
		"summary",
		"line-items",
		"financial_rollup",
		"v2.summary",
		"a..b", // dots inside a single component are an ordinary dir name, not a parent ref
		"Recipe1",
		"0abc",
	}
	for _, id := range cases {
		slug, err := deriveRecipeOutputSlug(id)
		if err != nil {
			t.Errorf("deriveRecipeOutputSlug(%q) returned error: %v", id, err)
			continue
		}
		if slug != id {
			t.Errorf("deriveRecipeOutputSlug(%q) = %q, want verbatim id (no normalization)", id, slug)
		}
	}
}

func TestDeriveRecipeOutputSlug_RejectsTraversalAndUnsafe(t *testing.T) {
	cases := map[string]string{
		"empty":              "",
		"whitespace":         "   ",
		"leading-space":      " summary",
		"trailing-space":     "summary ",
		"inner-space":        "line items",
		"dot":                ".",
		"dotdot":             "..",
		"parent-prefix":      "../escape",
		"nested-separator":   "a/b",
		"absolute":           "/etc/passwd",
		"backslash":          "a\\b",
		"windows-parent":     "..\\escape",
		"volume-prefix":      "C:evil",
		"leading-dot":        ".hidden",
		"leading-hyphen":     "-flag",
		"control-char":       "a\x00b",
		"newline":            "a\nb",
		"trailing-separator": "a/",
	}
	for name, id := range cases {
		t.Run(name, func(t *testing.T) {
			if slug, err := deriveRecipeOutputSlug(id); err == nil {
				t.Errorf("deriveRecipeOutputSlug(%q) = %q, want error (unsafe slug must be rejected)", id, slug)
			}
		})
	}
}

func TestResolveRecipeOutputDirs_Contained(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	dirs, err := resolveRecipeOutputDirs(root, []string{"summary", "line-items", "financial"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs returned error: %v", err)
	}
	if len(dirs) != 3 {
		t.Fatalf("got %d output dirs, want 3", len(dirs))
	}
	for _, d := range dirs {
		// Every recipe directory must sit directly under the cleaned root.
		rel, err := filepath.Rel(filepath.Clean(root), d.Dir)
		if err != nil {
			t.Fatalf("Rel(%q, %q): %v", root, d.Dir, err)
		}
		if rel != d.Slug || strings.Contains(rel, "..") {
			t.Errorf("recipe %q dir %q not contained as a single component under root (rel=%q)", d.RecipeID, d.Dir, rel)
		}
	}
}

func TestResolveRecipeOutputDirs_RejectsDuplicateRecipe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	_, err := resolveRecipeOutputDirs(root, []string{"summary", "line-items", "summary"})
	if err == nil {
		t.Fatal("expected error for duplicate recipe id, got nil")
	}
	if !strings.Contains(err.Error(), "distinct output subdirectory") {
		t.Errorf("unexpected error for duplicate recipe: %v", err)
	}
}

func TestResolveRecipeOutputDirs_RejectsTraversalRecipe(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	for _, id := range []string{"../escape", "a/b", "/abs", ".."} {
		if _, err := resolveRecipeOutputDirs(root, []string{"summary", id}); err == nil {
			t.Errorf("expected error for traversal recipe id %q, got nil", id)
		}
	}
}

func TestResolveRecipeOutputDirs_RejectsExistingSymlinkDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// Pre-create <root>/summary as a symlink pointing OUTSIDE the output root.
	// Lexical containment passes (summary is a single component under root), but
	// writing through the symlink would escape; SF1 must reject it.
	if err := os.Symlink(outside, filepath.Join(root, "summary")); err != nil {
		t.Fatalf("symlink setup: %v", err)
	}
	if _, err := resolveRecipeOutputDirs(root, []string{"summary"}); err == nil {
		t.Fatal("expected rejection of a pre-existing symlinked output dir, got nil")
	} else if !strings.Contains(err.Error(), "symlink") {
		t.Errorf("unexpected error for symlinked output dir: %v", err)
	}
}

func TestResolveRecipeOutputDirs_AllowsExistingRealDir(t *testing.T) {
	root := t.TempDir()
	// A pre-existing *real* recipe directory (e.g. a re-run) is fine.
	if err := os.MkdirAll(filepath.Join(root, "summary"), 0o755); err != nil {
		t.Fatalf("mkdir setup: %v", err)
	}
	if _, err := resolveRecipeOutputDirs(root, []string{"summary"}); err != nil {
		t.Errorf("pre-existing real output dir should be allowed, got: %v", err)
	}
}

func TestResolveRecipeOutputDirs_RequiresRootAndRecipes(t *testing.T) {
	if _, err := resolveRecipeOutputDirs("", []string{"summary"}); err == nil {
		t.Error("expected error for empty output root, got nil")
	}
	if _, err := resolveRecipeOutputDirs(filepath.Join(t.TempDir(), "out"), nil); err == nil {
		t.Error("expected error for empty recipe set, got nil")
	}
}
