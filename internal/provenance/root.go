package provenance

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrInvalidRoot indicates that a provenance root is not an existing directory
// whose symlink target can be evaluated.
var ErrInvalidRoot = errors.New("invalid provenance root")

// ErrOutsideRoot indicates that a local input is not strictly contained by a
// provenance root, lexically or after symlink evaluation.
var ErrOutsideRoot = errors.New("input is outside the provenance root")

// Root holds the two forms needed for provenance-root containment checks. The
// lexical form preserves the path spelling used for the recorded relative path;
// the evaluated form closes the symlink escape case.
type Root struct {
	lexical   string
	evaluated string
}

// ResolveRoot resolves a provenance root once. Errors intentionally do not
// include the supplied path: the root is sensitive host-local material and must
// never reach command errors, logs, or manifests.
func ResolveRoot(path string) (*Root, error) {
	if strings.TrimSpace(path) == "" || strings.Contains(path, "://") {
		return nil, ErrInvalidRoot
	}
	lexical, err := filepath.Abs(path)
	if err != nil {
		return nil, ErrInvalidRoot
	}
	lexical = filepath.Clean(lexical)
	info, err := os.Stat(lexical)
	if err != nil || !info.IsDir() {
		return nil, ErrInvalidRoot
	}

	evaluated, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return nil, ErrInvalidRoot
	}
	evaluated, err = filepath.Abs(evaluated)
	if err != nil {
		return nil, ErrInvalidRoot
	}
	evaluated = filepath.Clean(evaluated)
	evaluatedInfo, err := os.Stat(evaluated)
	if err != nil || !evaluatedInfo.IsDir() {
		return nil, ErrInvalidRoot
	}

	return &Root{lexical: lexical, evaluated: evaluated}, nil
}

// Relative returns the slash-separated lexical path relative to the root after
// requiring strict containment in both lexical and evaluated path space.
// Missing inputs and symlink-evaluation failures fail closed.
func (r *Root) Relative(path string) (string, error) {
	if r == nil || strings.TrimSpace(path) == "" || strings.Contains(path, "://") {
		return "", ErrOutsideRoot
	}
	lexical, err := filepath.Abs(path)
	if err != nil {
		return "", ErrOutsideRoot
	}
	lexical = filepath.Clean(lexical)
	lexicalRelative, ok := strictRelative(r.lexical, lexical)
	if !ok {
		return "", ErrOutsideRoot
	}

	evaluated, err := filepath.EvalSymlinks(lexical)
	if err != nil {
		return "", ErrOutsideRoot
	}
	evaluated, err = filepath.Abs(evaluated)
	if err != nil {
		return "", ErrOutsideRoot
	}
	if _, ok := strictRelative(r.evaluated, filepath.Clean(evaluated)); !ok {
		return "", ErrOutsideRoot
	}

	return filepath.ToSlash(lexicalRelative), nil
}

// RedactText removes both retained root spellings from diagnostic text. The
// lexical and evaluated roots can differ when the selected root itself is a
// symlink, and either spelling can appear in an operating-system error.
func (r *Root) RedactText(text string) string {
	if r == nil {
		return text
	}
	text = strings.ReplaceAll(text, r.lexical, "<provenance-root>")
	text = strings.ReplaceAll(text, r.evaluated, "<provenance-root>")
	return text
}

func strictRelative(root, candidate string) (string, bool) {
	relative, err := filepath.Rel(root, candidate)
	if err != nil || relative == "." || relative == ".." {
		return "", false
	}
	if strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return relative, true
}
