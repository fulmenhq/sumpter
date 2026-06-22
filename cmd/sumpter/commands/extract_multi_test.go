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

func TestResolveRecipeOutputDirs_RejectsCaseInsensitiveCollision(t *testing.T) {
	root := filepath.Join(t.TempDir(), "out")
	// "Summary" and "summary" are distinct strings but name the same directory
	// on a case-insensitive filesystem (macOS APFS, Windows NTFS) — they must be
	// rejected as a collision rather than silently sharing one output dir.
	_, err := resolveRecipeOutputDirs(root, []string{"Summary", "line-items", "summary"})
	if err == nil {
		t.Fatal("expected error for case-insensitive output-slug collision, got nil")
	}
	if !strings.Contains(err.Error(), "distinct output subdirectory") {
		t.Errorf("unexpected error for case-insensitive collision: %v", err)
	}
}

func TestResolveRecipeOutputDirs_RejectsExistingRegularFile(t *testing.T) {
	root := t.TempDir()
	// A pre-existing regular file at <root>/<slug> cannot serve as the
	// recipe-owned output directory; reject it at preflight rather than letting
	// a downstream writer fail later.
	if err := os.WriteFile(filepath.Join(root, "summary"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write setup: %v", err)
	}
	if _, err := resolveRecipeOutputDirs(root, []string{"summary"}); err == nil {
		t.Fatal("expected rejection of a pre-existing regular file at the output dir, got nil")
	} else if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("unexpected error for regular-file collision: %v", err)
	}
}

func TestResolveRecipeOutputDirs_PreservesCloudRoot(t *testing.T) {
	// A cloud (s3://) output root must keep its URI form: filepath.Clean would
	// collapse "s3://bucket/prefix" to "s3:/bucket/prefix" and the dispatcher
	// would then treat it as a local path, bypassing the cloud output session.
	dirs, err := resolveRecipeOutputDirs("s3://bucket/prefix", []string{"summary", "line-items"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs(cloud): %v", err)
	}
	want := map[string]string{
		"summary":    "s3://bucket/prefix/summary",
		"line-items": "s3://bucket/prefix/line-items",
	}
	for _, d := range dirs {
		if d.Dir != want[d.RecipeID] {
			t.Errorf("recipe %q cloud dir = %q, want %q", d.RecipeID, d.Dir, want[d.RecipeID])
		}
		if !strings.HasPrefix(d.Dir, "s3://") {
			t.Errorf("recipe %q cloud dir %q lost its s3:// scheme", d.RecipeID, d.Dir)
		}
	}
}

func TestResolveRecipeOutputDirs_RejectsExistingSymlinkDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	// Pre-create <root>/summary as a symlink pointing OUTSIDE the output root.
	// Lexical containment passes (summary is a single component under root), but
	// writing through the symlink would escape; the preflight must reject it.
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
