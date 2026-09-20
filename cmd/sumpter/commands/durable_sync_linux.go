//go:build linux

package commands

import (
	"fmt"
	"os"
)

func validateLocalDurableCommitSupport(path string) error {
	return probeExistingDirectorySync(path)
}

func platformSyncData(f *os.File) error {
	if f == nil {
		return fmt.Errorf("sync file: nil file")
	}
	return f.Sync()
}

func platformSyncDirectory(f *os.File) error {
	if f == nil {
		return fmt.Errorf("sync directory: nil file")
	}
	return f.Sync()
}
