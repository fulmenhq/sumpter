package reftable

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveLocalSource validates a workspace-relative reference-table source path and
// returns the absolute path to open.
//
// This is the C1 containment control, the load-bearing security boundary for local
// reference tables: a table source is read, and Pattern B (lookup_reference) can
// emit its values into output records, so an unconstrained local path would be a
// local-file read+exfil primitive (and Pattern A a file-content confirmation
// oracle). The path is rejected unless it is:
//
//   - relative — absolute paths and volume names are refused;
//   - free of ".." escapes (lexical);
//   - free of symlinks in every workspace-relative component (a symlinked final
//     file OR a symlinked parent directory is refused, even one that stays inside
//     the workspace — a hard ban, not just a containment check); and
//   - contained within root after resolving symlinks on the real path (defence in
//     depth against a TOCTOU/symlink escape the component walk might miss).
//
// root is the recipe/workspace directory. root itself may legitimately be a symlink
// (e.g. /tmp -> /private/tmp on macOS); only the workspace-relative portion is held
// to the no-symlink rule.
func ResolveLocalSource(root, source string) (string, error) {
	if strings.TrimSpace(source) == "" {
		return "", errors.New("reference table source path is empty")
	}
	if filepath.IsAbs(source) || filepath.VolumeName(source) != "" {
		return "", fmt.Errorf("reference table source %q must be a workspace-relative path, not an absolute path", source)
	}
	clean := filepath.Clean(source)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference table source %q escapes the workspace with %q", source, "..")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("reference table source %q: cannot resolve workspace root: %w", source, err)
	}
	joined := filepath.Join(absRoot, clean)

	// Hard symlink ban on the workspace-relative components (final + parents).
	if err := rejectSymlinkComponents(absRoot, clean, source); err != nil {
		return "", err
	}

	// Defence in depth: containment recheck on the real (symlink-resolved) path.
	realRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("reference table source %q: cannot resolve workspace root: %w", source, err)
	}
	realPath, err := filepath.EvalSymlinks(joined)
	if err != nil {
		return "", fmt.Errorf("reference table source %q: source file not found or unreadable", source)
	}
	rel, err := filepath.Rel(realRoot, realPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("reference table source %q resolves outside the workspace", source)
	}
	return joined, nil
}

// rejectSymlinkComponents walks each workspace-relative path component under root and
// refuses any that is a symlink. root itself is not checked (it may be a legitimate
// symlink); only the portion the recipe author controls is held to the ban.
func rejectSymlinkComponents(absRoot, relClean, source string) error {
	cur := absRoot
	for _, part := range strings.Split(relClean, string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if err != nil {
			return fmt.Errorf("reference table source %q: source file not found or unreadable", source)
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("reference table source %q: path component %q is a symlink; symlinked reference-table sources are not allowed", source, part)
		}
	}
	return nil
}
