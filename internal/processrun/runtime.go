package processrun

import (
	"os"
	"path/filepath"
	"strings"
)

// Runtime directory resolution order (entarch/secrev):
//
//	flag > SUMPTER_PROCESS_RUN_RUNTIME_DIR > $XDG_RUNTIME_DIR/sumpter > $TMPDIR/sumpter-process-run
//
// Never under SUMPTER_HOME / workdir (ValidateRuntimeDir + blocked roots).

const (
	// EnvRuntimeDir is the process-run runtime directory override.
	EnvRuntimeDir = "SUMPTER_PROCESS_RUN_RUNTIME_DIR"

	// runtimeSubdirXDG is the namespace under $XDG_RUNTIME_DIR.
	runtimeSubdirXDG = "sumpter"
	// runtimeSubdirTmp is the namespace under TMPDIR / os.TempDir().
	runtimeSubdirTmp = "sumpter-process-run"
)

// ResolveRuntimeDir returns the process-run runtime root.
// flagValue, when non-empty, wins. Otherwise env, then platform defaults.
// Does not create directories. Caller must ValidateRuntimeDir before use.
func ResolveRuntimeDir(flagValue string) (string, error) {
	if v := strings.TrimSpace(flagValue); v != "" {
		return absClean(v)
	}
	if v := strings.TrimSpace(os.Getenv(EnvRuntimeDir)); v != "" {
		return absClean(v)
	}
	if xdg := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR")); xdg != "" {
		return absClean(filepath.Join(xdg, runtimeSubdirXDG))
	}
	tmp := strings.TrimSpace(os.Getenv("TMPDIR"))
	if tmp == "" {
		tmp = os.TempDir()
	}
	return absClean(filepath.Join(tmp, runtimeSubdirTmp))
}

// ValidateRuntimeDir rejects empty paths and paths under blocked roots
// (typically SUMPTER_HOME and workdir for this invocation).
func ValidateRuntimeDir(path string, blockedRoots []string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return ErrCardConfig
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ErrCardSetup
	}
	if real, rerr := filepath.EvalSymlinks(abs); rerr == nil {
		abs = real
	} else if parent, perr := filepath.EvalSymlinks(filepath.Dir(abs)); perr == nil {
		// Parent exists but leaf does not yet — still check containment of the intended path.
		abs = filepath.Join(parent, filepath.Base(abs))
	}
	abs = filepath.Clean(abs)

	roots := normalizeBlockedRoots(blockedRoots)
	if len(roots) == 0 {
		roots = envBlockedRoots()
	}
	for _, root := range roots {
		if underPath(abs, root) {
			return ErrCardPlacement
		}
	}
	return nil
}

// RunDir returns <runtime>/proc/<run_id> for a process-run slot.
func RunDir(runtimeDir, runID string) string {
	return filepath.Join(runtimeDir, "proc", runID)
}

// CardFileName is the discovery-root filename under a run directory.
const CardFileName = "card.json"

// EventsFileName is the default auto-placed event stream under a run directory.
const EventsFileName = "events.ndjson"

// CardPath returns the card path for a run slot.
func CardPath(runtimeDir, runID string) string {
	return filepath.Join(RunDir(runtimeDir, runID), CardFileName)
}

// DefaultEventsPath returns the auto-placed stream path for a run slot.
func DefaultEventsPath(runtimeDir, runID string) string {
	return filepath.Join(RunDir(runtimeDir, runID), EventsFileName)
}

func absClean(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", ErrCardSetup
	}
	return filepath.Clean(abs), nil
}
