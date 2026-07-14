package processrun

import (
	"errors"
	"os"
	"path/filepath"
)

// reclaimLockFile is a stable per-slot advisory lock path. The file inode must
// never be unlinked during normal residue cleanup — only the open handle is
// the concurrency authority (kernel-held exclusive lock).
const reclaimLockFile = "reclaim.lock"

// errReclaimLockBusy means another process holds the exclusive reclaim lock.
var errReclaimLockBusy = errors.New("process-run: reclaim lock busy")

// reclaimLock is an exclusive, non-blocking OS advisory lock on reclaim.lock.
// Ownership is the open kernel handle; process death releases it automatically.
type reclaimLock struct {
	f    *os.File
	path string
}

func reclaimLockPath(runDir string) string {
	return filepath.Join(runDir, reclaimLockFile)
}

// tryAcquireReclaimLock opens/creates <runDir>/reclaim.lock (0600) and takes an
// exclusive non-blocking OS lock. On success the caller must Release() (or let
// process exit). The lock file is never removed by Release.
func tryAcquireReclaimLock(runDir string) (*reclaimLock, error) {
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, err
	}
	// Owner-only runtime slot directory (process-run discovery root). G302
	// expects ≤0600 which applies to files; directories need execute for traversal.
	_ = os.Chmod(runDir, 0o700) // #nosec G302 - intentional owner-only 0700 directory mode for process-run runtime slot
	path := reclaimLockPath(runDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304
	if err != nil {
		return nil, err
	}
	_ = f.Chmod(0o600)
	if err := tryLockFileExclusive(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &reclaimLock{f: f, path: path}, nil
}

// Release drops the OS lock and closes the handle. It never unlinks reclaim.lock.
func (l *reclaimLock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unlockFileExclusive(l.f)
	_ = l.f.Close()
	l.f = nil
}

// Path returns the stable reclaim.lock pathname (for tests).
func (l *reclaimLock) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// ensureReclaimLockFile creates reclaim.lock if missing without locking it.
// The stable inode must persist for the life of the run directory.
func ensureReclaimLockFile(runDir string) error {
	path := reclaimLockPath(runDir)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	_ = f.Chmod(0o600)
	return f.Close()
}
