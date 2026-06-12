package uriio

import (
	"fmt"
	"path/filepath"
	"strings"
)

// safeStagePath maps an object key to a local staging path guaranteed to stay
// under stageDir.
//
// Object keys are attacker-influenced: they can carry ".." segments, leading
// separators, NUL/control characters, backslashes, or Windows drive/UNC
// prefixes. A naive filepath.Join(stageDir, key) would let such a key escape the
// run directory and overwrite arbitrary files. This rejects every traversal
// vector and verifies containment after cleaning.
func safeStagePath(stageDir, key string) (string, error) {
	if key == "" {
		return "", fmt.Errorf("uriio: empty object key")
	}
	if strings.IndexByte(key, 0) >= 0 {
		return "", fmt.Errorf("uriio: object key contains a NUL byte")
	}
	for _, r := range key {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("uriio: object key contains a control character")
		}
	}
	// S3 keys are '/'-delimited. Reject backslashes so the key cannot acquire
	// Windows path semantics on any platform.
	if strings.IndexByte(key, '\\') >= 0 {
		return "", fmt.Errorf("uriio: object key contains a backslash")
	}

	segments := strings.Split(key, "/")
	clean := make([]string, 0, len(segments))
	for _, seg := range segments {
		switch seg {
		case "", ".":
			// Empty (leading/duplicate/trailing separators) and current-dir
			// segments carry no path component.
			continue
		case "..":
			return "", fmt.Errorf("uriio: object key contains a parent-directory segment (..)")
		default:
			clean = append(clean, seg)
		}
	}
	if len(clean) == 0 {
		return "", fmt.Errorf("uriio: object key %q has no usable path component", key)
	}
	// Reject a volume/drive prefix in the leading segment (e.g. "C:").
	if filepath.VolumeName(clean[0]) != "" {
		return "", fmt.Errorf("uriio: object key has a volume/drive prefix")
	}

	staged := filepath.Join(append([]string{stageDir}, clean...)...)

	// Defense in depth: confirm the cleaned result is still under stageDir.
	rel, err := filepath.Rel(stageDir, staged)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("uriio: object key %q escapes the staging directory", key)
	}
	return staged, nil
}
