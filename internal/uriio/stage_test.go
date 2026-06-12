package uriio

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSafeStagePathRejectsTraversal is the S2 centerpiece: hostile object keys
// must never produce a staging path outside the run directory.
func TestSafeStagePathRejectsTraversal(t *testing.T) {
	stageDir := filepath.Join(t.TempDir(), "run")

	// True traversal/invalid keys must be rejected.
	rejected := []string{
		"../etc/passwd",
		"a/../../etc/passwd",
		"../../../../tmp/evil",
		"foo/../../bar",
		"with\x00nul",
		"ctrl\x01char",
		"back\\slash",
		"..",
		"",
		"/",
		"./",
		"a/..",
	}
	for _, key := range rejected {
		if got, err := safeStagePath(stageDir, key); err == nil {
			t.Errorf("safeStagePath(%q) = %q, want rejection", key, got)
		}
	}

	// Leading-separator keys are not escapes: they normalize to a path *inside*
	// the run dir (secrev S2 allows "stage safely inside the run dir"). The
	// invariant is containment, which we assert here.
	for _, key := range []string{"/abs/path", "/etc/passwd"} {
		got, err := safeStagePath(stageDir, key)
		if err != nil {
			t.Errorf("safeStagePath(%q) error: %v (want safe containment)", key, err)
			continue
		}
		rel, relErr := filepath.Rel(stageDir, got)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("safeStagePath(%q) = %q escaped stageDir", key, got)
		}
	}
}

// TestSafeStagePathAcceptsBenignKeys confirms normal keys map under the run dir.
func TestSafeStagePathAcceptsBenignKeys(t *testing.T) {
	stageDir := filepath.Join(t.TempDir(), "run")
	cases := map[string]string{
		"object.xml":               "object.xml",
		"prefix/object.xml":        filepath.Join("prefix", "object.xml"),
		"a/b/c/deep.xml":           filepath.Join("a", "b", "c", "deep.xml"),
		"trailing/dot./object.xml": filepath.Join("trailing", "dot.", "object.xml"),
		"double//slash/object.xml": filepath.Join("double", "slash", "object.xml"),
	}
	for key, wantRel := range cases {
		got, err := safeStagePath(stageDir, key)
		if err != nil {
			t.Errorf("safeStagePath(%q) error: %v", key, err)
			continue
		}
		want := filepath.Join(stageDir, wantRel)
		if got != want {
			t.Errorf("safeStagePath(%q) = %q, want %q", key, got, want)
		}
		// Hard invariant: result is always under stageDir.
		rel, err := filepath.Rel(stageDir, got)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			t.Errorf("safeStagePath(%q) escaped stageDir: %q", key, got)
		}
	}
}
