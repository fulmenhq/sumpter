package provenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveRootRelativeRequiresStrictLexicalAndEvaluatedContainment(t *testing.T) {
	rootPath := t.TempDir()
	insideDir := filepath.Join(rootPath, "inputs")
	if err := os.MkdirAll(insideDir, 0o750); err != nil {
		t.Fatalf("mkdir inside directory: %v", err)
	}
	inside := filepath.Join(insideDir, "source.xml")
	if err := os.WriteFile(inside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write inside input: %v", err)
	}

	root, err := ResolveRoot(rootPath)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, err := root.Relative(inside); err != nil || got != "inputs/source.xml" {
		t.Fatalf("Relative(inside) = %q, %v; want inputs/source.xml", got, err)
	}
	if _, err := root.Relative(rootPath); err == nil {
		t.Fatal("Relative(root) unexpectedly succeeded; containment must be strict")
	}

	outside := filepath.Join(t.TempDir(), "source.xml")
	if err := os.WriteFile(outside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write outside input: %v", err)
	}
	if _, err := root.Relative(outside); err == nil {
		t.Fatal("Relative(outside) unexpectedly succeeded")
	}

	// A sibling sharing the root's textual prefix is not contained.
	prefixSibling := rootPath + "-sibling/source.xml"
	if _, err := root.Relative(prefixSibling); err == nil {
		t.Fatal("Relative(prefix sibling) unexpectedly succeeded")
	}
}

func TestRootRelativeRejectsSymlinkEscapeAndPreservesLexicalForm(t *testing.T) {
	rootPath := t.TempDir()
	outsideDir := t.TempDir()
	insideDir := filepath.Join(rootPath, "real")
	if err := os.MkdirAll(insideDir, 0o750); err != nil {
		t.Fatalf("mkdir inside directory: %v", err)
	}
	inside := filepath.Join(insideDir, "source.xml")
	if err := os.WriteFile(inside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write inside input: %v", err)
	}
	outside := filepath.Join(outsideDir, "source.xml")
	if err := os.WriteFile(outside, []byte("<root/>"), 0o600); err != nil {
		t.Fatalf("write outside input: %v", err)
	}

	insideLink := filepath.Join(rootPath, "linked-inside")
	outsideLink := filepath.Join(rootPath, "linked-outside")
	if err := os.Symlink(insideDir, insideLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(outsideDir, outsideLink); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	root, err := ResolveRoot(rootPath)
	if err != nil {
		t.Fatalf("ResolveRoot: %v", err)
	}
	if got, err := root.Relative(filepath.Join(insideLink, "source.xml")); err != nil || got != "linked-inside/source.xml" {
		t.Fatalf("Relative(inside symlink) = %q, %v; want linked-inside/source.xml", got, err)
	}
	if _, err := root.Relative(filepath.Join(outsideLink, "source.xml")); err == nil {
		t.Fatal("Relative(outside symlink) unexpectedly succeeded")
	}
}

func TestResolveRootRejectsInvalidRootsWithoutEchoingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if _, err := ResolveRoot(missing); err == nil {
		t.Fatal("ResolveRoot(missing) unexpectedly succeeded")
	} else if strings.Contains(err.Error(), missing) {
		t.Fatalf("invalid-root error leaked the supplied path: %v", err)
	}

	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file root: %v", err)
	}
	if _, err := ResolveRoot(file); err == nil {
		t.Fatal("ResolveRoot(file) unexpectedly succeeded")
	} else if strings.Contains(err.Error(), file) {
		t.Fatalf("file-root error leaked the supplied path: %v", err)
	}
}
