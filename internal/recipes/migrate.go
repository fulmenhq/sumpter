package recipes

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// StarterContentVersion is the SemVer value stamped onto recipe manifests
// that lack a `content_version` field when migrated with
// `sumpter recipes migrate`. Per ADR-0006 this is a conservative pre-1.0
// starter; authors are expected to manage the value going forward.
const StarterContentVersion = "0.0.1"

// ManifestFileName is the canonical filename for recipe manifests; the
// migrate walker only considers files with this exact basename when scanning
// directories.
const ManifestFileName = "recipe.yaml"

// MigrationAction describes what happened when MigrateBytes processed a
// manifest's raw bytes.
type MigrationAction string

const (
	// MigrationStamped indicates the manifest was missing content_version and
	// the starter value was inserted.
	MigrationStamped MigrationAction = "stamped"

	// MigrationAlreadyStamped indicates the manifest already had a
	// content_version key (the value is not re-validated here; LoadManifest
	// handles strict validation downstream).
	MigrationAlreadyStamped MigrationAction = "already-stamped"
)

// topLevelContentVersion matches `content_version:` at column zero. Lines
// that are full comments (`# content_version: ...`) are filtered separately
// by trimming.
var topLevelContentVersion = regexp.MustCompile(`^content_version\s*:`)

// topLevelKey matches any `key:` (or `key :`) anchored at column zero. Used
// to locate insertion points relative to siblings like created_at / id /
// version.
var topLevelKeyPattern = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_]*)\s*:`)

// MigrateBytes returns updated manifest bytes with content_version stamped
// when absent, plus the action taken. The function is idempotent — calling
// it twice on the same input produces the same output on the second call
// with MigrationAlreadyStamped. The input is treated as a recipe manifest;
// callers are responsible for confirming the file is actually a recipe
// before invoking.
//
// Insertion strategy is textual to preserve comments, blank lines, and
// quoting style. The new `content_version: "0.0.1"` line is placed
// immediately after the first of (created_at, id, version) that appears at
// column zero. If none are present (degenerate manifest) the line is
// prepended at the top of the document body, after any leading comments or
// document-start markers.
func MigrateBytes(data []byte) ([]byte, MigrationAction, error) {
	// Defensive parse: ensure the input is at least syntactically valid YAML
	// before we mutate it. We do NOT call validate() here because pre-migration
	// manifests are by definition missing content_version and would fail the
	// strict path; the migrate command's job is to fix that.
	var probe map[string]any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return nil, "", fmt.Errorf("manifest is not valid YAML: %w", err)
	}

	if _, exists := probe["content_version"]; exists {
		return data, MigrationAlreadyStamped, nil
	}

	// Preserve original line ending style (CRLF vs LF).
	lineEnding := "\n"
	if bytes.Contains(data, []byte("\r\n")) {
		lineEnding = "\r\n"
	}

	lines := splitKeepEmpty(string(data), lineEnding)

	// Walk lines to find insertion point. We also defensively re-check for an
	// uncommented top-level content_version line — yaml.Unmarshal already
	// confirms the key is absent, but the textual scan keeps this function
	// hermetic if a future caller bypasses the YAML probe.
	insertAfter := -1
	priorities := map[string]int{
		"version":    1,
		"id":         2,
		"created_at": 3,
	}
	bestPriority := 0

	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if topLevelContentVersion.MatchString(line) {
			// Already present per textual scan; treat as idempotent.
			return data, MigrationAlreadyStamped, nil
		}
		match := topLevelKeyPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		key := match[1]
		if pri, ok := priorities[key]; ok && pri > bestPriority {
			bestPriority = pri
			insertAfter = idx
		}
	}

	stamped := fmt.Sprintf(`content_version: "%s"`, StarterContentVersion)
	newLines := make([]string, 0, len(lines)+1)
	switch {
	case insertAfter < 0:
		// No version/id/created_at anchor — prepend after any leading comment
		// or document-start lines.
		anchor := leadingPreambleEnd(lines)
		newLines = append(newLines, lines[:anchor]...)
		newLines = append(newLines, stamped)
		newLines = append(newLines, lines[anchor:]...)
	default:
		newLines = append(newLines, lines[:insertAfter+1]...)
		newLines = append(newLines, stamped)
		newLines = append(newLines, lines[insertAfter+1:]...)
	}

	out := strings.Join(newLines, lineEnding)

	// Verify the rewritten bytes still parse and now declare content_version.
	var verify map[string]any
	if err := yaml.Unmarshal([]byte(out), &verify); err != nil {
		return nil, "", fmt.Errorf("internal error: rewritten manifest is not valid YAML: %w", err)
	}
	got, ok := verify["content_version"].(string)
	if !ok || got != StarterContentVersion {
		return nil, "", errors.New("internal error: content_version was not set as expected after rewrite")
	}

	return []byte(out), MigrationStamped, nil
}

// splitKeepEmpty splits s on the given line ending without dropping a
// trailing empty element when s ends with the line ending. strings.Split
// already preserves trailing empties; the wrapper keeps the call site
// expressive.
func splitKeepEmpty(s, sep string) []string {
	return strings.Split(s, sep)
}

// leadingPreambleEnd returns the index of the first line that is neither a
// comment, blank, nor a YAML document-start marker (`---`).
func leadingPreambleEnd(lines []string) int {
	for idx, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "---" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return idx
	}
	return len(lines)
}

// MigrateFile loads a manifest from disk, stamps content_version when
// missing, and writes the result back atomically (via a same-directory temp
// file rename). When dryRun is true, no on-disk changes are made and the
// returned action still reflects what would happen. A successful migrate
// also re-validates the file by invoking LoadManifest on the post-rewrite
// bytes; if that fails, the original file is left untouched and an error
// is returned.
func MigrateFile(path string, dryRun bool) (MigrationAction, error) {
	data, err := os.ReadFile(path) // #nosec G304 - User-specified manifest file (CLI input)
	if err != nil {
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}

	newData, action, err := MigrateBytes(data)
	if err != nil {
		return "", fmt.Errorf("migrate %s: %w", path, err)
	}
	if action == MigrationAlreadyStamped {
		return action, nil
	}

	// Validate by writing to a temp file in the same directory and calling
	// LoadManifest on it. This catches cases where the textual insert
	// inadvertently produced an invalid manifest (e.g., schema violations
	// that LoadManifest's schema validator would flag).
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".sumpter-migrate-*.yaml")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup if we error out before the rename.
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmp.Write(newData); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("failed to write temp file %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("failed to close temp file %s: %w", tmpPath, err)
	}

	if _, err := LoadManifest(tmpPath); err != nil {
		return "", fmt.Errorf("rewritten manifest %s failed validation: %w", path, err)
	}

	if dryRun {
		return action, nil
	}

	// Preserve original file mode on rename target.
	info, err := os.Stat(path)
	if err == nil {
		_ = os.Chmod(tmpPath, info.Mode().Perm())
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return "", fmt.Errorf("failed to replace %s: %w", path, err)
	}
	return action, nil
}
