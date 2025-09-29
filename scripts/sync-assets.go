package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	return filepath.WalkDir(source, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Get relative path from source
		relPath, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}

		// Skip the root directory itself
		if relPath == "." {
			return nil
		}

		targetPath := filepath.Join(target, relPath)

		if d.IsDir() {
			return os.MkdirAll(targetPath, 0755)
		}

		// Copy file
		return copyFile(path, targetPath)
	})
}

func copyFile(src, dst string) error {
	// Ensure target directory exists
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
