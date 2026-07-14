//go:build !unix && !windows

package processrun

import (
	"errors"
	"os"
)

func tryLockFileExclusive(f *os.File) error {
	_ = f
	return errors.New("process-run: reclaim lock unsupported on this platform")
}

func unlockFileExclusive(f *os.File) error {
	_ = f
	return nil
}
