package processrun

import (
	"os"
	"path/filepath"
	"strings"
)

// ValidateEventsPath rejects empty paths and paths under the invocation's effective
// blocked roots (typically resolved SUMPTER_HOME and workdir). Callers must pass the
// already-resolved roots for this invocation — do not re-resolve/create directories
// inside this predicate.
//
// When blockedRoots is empty, falls back to SUMPTER_HOME / SUMPTER_WORKDIR env only
// (no default layout resolution, no directory creation).
func ValidateEventsPath(path string, blockedRoots []string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrStreamConfig
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ErrStreamSetup
	}
	// Resolve real path when the parent exists so symlink containment under a blocked root is caught.
	if real, rerr := filepath.EvalSymlinks(filepath.Dir(abs)); rerr == nil {
		abs = filepath.Join(real, filepath.Base(abs))
	}

	roots := normalizeBlockedRoots(blockedRoots)
	if len(roots) == 0 {
		roots = envBlockedRoots()
	}
	for _, root := range roots {
		if underPath(abs, root) {
			return ErrStreamPlacement
		}
	}
	return nil
}

func normalizeBlockedRoots(roots []string) []string {
	out := make([]string, 0, len(roots))
	seen := make(map[string]struct{}, len(roots))
	for _, r := range roots {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		abs, err := filepath.Abs(r)
		if err != nil {
			continue
		}
		if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
			abs = real
		}
		abs = filepath.Clean(abs)
		if _, ok := seen[abs]; ok {
			continue
		}
		seen[abs] = struct{}{}
		out = append(out, abs)
	}
	return out
}

func envBlockedRoots() []string {
	var roots []string
	if home := strings.TrimSpace(os.Getenv("SUMPTER_HOME")); home != "" {
		roots = append(roots, home)
	}
	if work := strings.TrimSpace(os.Getenv("SUMPTER_WORKDIR")); work != "" {
		roots = append(roots, work)
	}
	return normalizeBlockedRoots(roots)
}

func underPath(candidate, root string) bool {
	cand := filepath.Clean(candidate)
	rt := filepath.Clean(root)
	if cand == rt {
		return true
	}
	rel, err := filepath.Rel(rt, cand)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
