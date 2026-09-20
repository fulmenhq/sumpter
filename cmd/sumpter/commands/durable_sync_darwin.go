//go:build darwin

package commands

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func validateLocalDurableCommitSupport(path string) error {
	ancestor, err := existingDirectoryAncestor(path)
	if err != nil {
		return err
	}
	var stat unix.Statfs_t
	if err := unix.Statfs(ancestor, &stat); err != nil {
		return fmt.Errorf("inspect aggregate output filesystem: %w", err)
	}
	fsType := strings.TrimRight(string(stat.Fstypename[:]), "\x00")
	if fsType != "apfs" {
		return fmt.Errorf("local aggregate output filesystem %q is unsupported on Darwin: durable commit is verified for APFS only", fsType)
	}
	return probeExistingDirectorySync(path)
}

// File.Sync on Darwin attempts F_FULLFSYNC and falls back to fsync when the
// filesystem reports ENOTSUP (Go's internal/poll implementation). APFS is the
// verified durable-commit target; other filesystems must pass the actual sync
// operations or the run fails closed.
func platformSyncData(f *os.File) error {
	if f == nil {
		return fmt.Errorf("sync file: nil file")
	}
	return f.Sync()
}

// Directory entries use fsync through File.Sync; F_FULLFSYNC is a regular-file
// data barrier and is not supported for directory descriptors on APFS.
func platformSyncDirectory(f *os.File) error {
	if f == nil {
		return fmt.Errorf("sync directory: nil file")
	}
	return f.Sync()
}
