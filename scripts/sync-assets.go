package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <source> <target>\n", os.Args[0])
		os.Exit(1)
	}

	source := os.Args[1]
	target := os.Args[2]

	if err := syncDir(source, target); err != nil {
		fmt.Fprintf(os.Stderr, "Error syncing %s to %s: %v\n", source, target, err)
		os.Exit(1)
	}

	fmt.Printf("Synced %s to %s\n", source, target)
}

func syncDir(source, target string) error {
	sourceRoot, targetRoot, err := prepareSyncRoots(source, target)
	if err != nil {
		return err
	}

	return filepath.WalkDir(sourceRoot, func(path string, d os.DirEntry, err error) error { // #nosec G703 - sourceRoot is absolute, cleaned, and validated before traversal
		if err != nil {
			return err
		}

		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to sync symlink %s", path)
		}

		relPath, err := containedRel(sourceRoot, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		targetPath, err := containedJoin(targetRoot, relPath)
		if err != nil {
			return err
		}

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0o750) // #nosec G122 G703 - symlinks are rejected and targetPath is contained under validated targetRoot
		}

		return copyFile(sourceRoot, targetRoot, path, targetPath)
	})
}

func prepareSyncRoots(source, target string) (string, string, error) {
	sourceRoot, err := filepath.Abs(filepath.Clean(source))
	if err != nil {
		return "", "", fmt.Errorf("resolve source root: %w", err)
	}
	targetRoot, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", "", fmt.Errorf("resolve target root: %w", err)
	}
	if sourceRoot == targetRoot {
		return "", "", fmt.Errorf("source and target must be different: %s", sourceRoot)
	}
	if !isWithinRoot(filepath.Dir(sourceRoot), sourceRoot) || !isWithinRoot(filepath.Dir(targetRoot), targetRoot) {
		return "", "", fmt.Errorf("source and target roots must be clean absolute paths")
	}
	return sourceRoot, targetRoot, nil
}

func containedRel(root, path string) (string, error) {
	relPath, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if relPath == "." {
		return relPath, nil
	}
	if !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("path %s escapes root %s", path, root)
	}
	return relPath, nil
}

func containedJoin(root, relPath string) (string, error) {
	if !filepath.IsLocal(relPath) {
		return "", fmt.Errorf("relative path %s is not local", relPath)
	}
	targetPath := filepath.Join(root, relPath)
	if !isWithinRoot(root, targetPath) {
		return "", fmt.Errorf("target path %s escapes root %s", targetPath, root)
	}
	return targetPath, nil
}

func isWithinRoot(root, path string) bool {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && filepath.IsLocal(rel) && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyFile(sourceRoot, targetRoot, src, dst string) error {
	if _, err := containedRel(sourceRoot, src); err != nil {
		return err
	}
	if !isWithinRoot(targetRoot, dst) {
		return fmt.Errorf("destination path %s escapes root %s", dst, targetRoot)
	}

	// Ensure target directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil { // #nosec G703 - dst is contained under targetRoot above
		return err
	}

	srcFile, err := os.Open(src) // #nosec G304 - src is contained under sourceRoot above
	if err != nil {
		return err
	}
	defer func() { _ = srcFile.Close() }()

	dstFile, err := os.Create(dst) // #nosec G304 G703 - dst is contained under targetRoot above
	if err != nil {
		return err
	}
	defer func() { _ = dstFile.Close() }()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
