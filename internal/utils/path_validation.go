package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// resolvePathWithSymlinks resolves a path by evaluating symlinks in the closest existing parent
func resolvePathWithSymlinks(path string) (string, error) {
	// Try to evaluate the full path first
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return resolved, err
		}
		return abs, nil
	}

	// If path doesn't exist, find the closest existing parent and resolve that
	current := path
	nonExistentParts := []string{}

	for {
		parent := filepath.Dir(current)
		if parent == current {
			// Reached the root without finding an existing directory
			// Use absolute path as fallback
			abs, err := filepath.Abs(path)
			if err != nil {
				return path, err
			}
			return abs, nil
		}

		// Check if parent exists
		if _, err := os.Stat(parent); err == nil {
			// Parent exists, resolve its symlinks
			resolvedParent, err := filepath.EvalSymlinks(parent)
			if err != nil {
				// Can't resolve parent symlinks, use absolute
				abs, err := filepath.Abs(path)
				if err != nil {
					return path, err
				}
				return abs, nil
			}
			if !filepath.IsAbs(resolvedParent) {
				abs, err := filepath.Abs(resolvedParent)
				if err != nil {
					return resolvedParent, err
				}
				resolvedParent = abs
			}

			// Rebuild path with resolved parent, including the first
			// non-existent child whose parent exists.
			result := resolvedParent
			result = filepath.Join(result, filepath.Base(current))
			for i := len(nonExistentParts) - 1; i >= 0; i-- {
				result = filepath.Join(result, nonExistentParts[i])
			}
			return result, nil
		}

		// Parent doesn't exist, go up one more level
		nonExistentParts = append(nonExistentParts, filepath.Base(current))
		current = parent
	}
}

// AllowedRoot defines the base directories where Sumpter can operate
type AllowedRoot int

const (
	// RootHome allows operations within SUMPTER_HOME directory
	RootHome AllowedRoot = iota
	// RootWork allows operations within SUMPTER_WORKDIR directory
	RootWork
	// RootCwd allows operations within current working directory (explicit opt-in)
	RootCwd
)

// String returns a human-readable name for the AllowedRoot
func (r AllowedRoot) String() string {
	switch r {
	case RootHome:
		return "SUMPTER_HOME"
	case RootWork:
		return "SUMPTER_WORKDIR"
	case RootCwd:
		return "current working directory"
	default:
		return "unknown root"
	}
}

// ValidateUserPathWithRoot validates a user-provided path against an allowed root directory
// This function prevents directory traversal attacks by:
// 1. Resolving the user path to absolute (follows symlinks)
// 2. Resolving the allowed root to absolute
// 3. Using filepath.Rel() to check if path is within root
// 4. Blocking system directories (/dev, /proc, /sys)
func ValidateUserPathWithRoot(userPath string, root AllowedRoot, rootDir string) error {
	if userPath == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Resolve path with symlink evaluation (handles non-existent files/dirs)
	evalPath, err := resolvePathWithSymlinks(userPath)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	// Resolve allowed root with symlink evaluation
	evalRoot, err := resolvePathWithSymlinks(rootDir)
	if err != nil {
		return fmt.Errorf("failed to resolve root directory: %w", err)
	}

	// Check if path is within allowed root using filepath.Rel
	// If the relative path starts with "..", it's outside the root
	rel, err := filepath.Rel(evalRoot, evalPath)
	if err != nil {
		return fmt.Errorf("path validation failed: %w", err)
	}

	// Check for directory traversal (path outside root)
	if strings.HasPrefix(rel, "..") || strings.HasPrefix(rel, string(filepath.Separator)) {
		return fmt.Errorf("path outside allowed %s: %s (resolved to %s, root is %s)",
			root.String(), userPath, evalPath, evalRoot)
	}

	// Block system directories for additional safety
	if strings.HasPrefix(evalPath, "/dev/") ||
		strings.HasPrefix(evalPath, "/proc/") ||
		strings.HasPrefix(evalPath, "/sys/") {
		return fmt.Errorf("access to system directories not allowed: %s", evalPath)
	}

	return nil
}

// ValidateUserPathForRead validates a user path for file reading operations
func ValidateUserPathForRead(userPath string, root AllowedRoot, rootDir string) error {
	if err := ValidateUserPathWithRoot(userPath, root, rootDir); err != nil {
		return err
	}

	// Check if file exists and is readable
	info, err := os.Stat(userPath)
	if err != nil {
		return fmt.Errorf("file access error: %w", err)
	}

	if info.IsDir() {
		return fmt.Errorf("path points to directory, not file: %s", userPath)
	}

	return nil
}

// ValidateUserPathForWrite validates a user path for file writing operations
func ValidateUserPathForWrite(userPath string, root AllowedRoot, rootDir string) error {
	if err := ValidateUserPathWithRoot(userPath, root, rootDir); err != nil {
		return err
	}

	// Check if parent directory exists and is writable
	parentDir := filepath.Dir(userPath)

	// Validate parent directory is also within root
	if err := ValidateUserPathWithRoot(parentDir, root, rootDir); err != nil {
		return fmt.Errorf("parent directory validation failed: %w", err)
	}

	// Create parent directory if it doesn't exist
	if err := os.MkdirAll(parentDir, 0750); err != nil {
		return fmt.Errorf("cannot create parent directory: %w", err)
	}

	// Check if parent directory is writable by attempting to create a temp file
	tmpFile, err := os.CreateTemp(parentDir, ".write_test_*")
	if err != nil {
		return fmt.Errorf("parent directory is not writable: %w", err)
	}
	_ = tmpFile.Close()
	_ = os.Remove(tmpFile.Name()) // Clean up test file

	return nil
}

// ValidateUserPathForCreate validates a user path for file creation operations
func ValidateUserPathForCreate(userPath string, root AllowedRoot, rootDir string) error {
	if err := ValidateUserPathWithRoot(userPath, root, rootDir); err != nil {
		return err
	}

	// Check if parent directory exists and is writable
	parentDir := filepath.Dir(userPath)

	// Validate parent directory is also within root
	if err := ValidateUserPathWithRoot(parentDir, root, rootDir); err != nil {
		return fmt.Errorf("parent directory validation failed: %w", err)
	}

	// Create parent directory if it doesn't exist
	if err := os.MkdirAll(parentDir, 0750); err != nil {
		return fmt.Errorf("cannot create parent directory: %w", err)
	}

	return nil
}
