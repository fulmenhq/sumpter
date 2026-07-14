//go:build windows

package processrun

import (
	"os"

	"golang.org/x/sys/windows"
)

func tryLockFileExclusive(f *os.File) error {
	// Lock one byte of the file; exclusive + fail immediately.
	var ol windows.Overlapped
	err := windows.LockFileEx(
		windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&ol,
	)
	if err == nil {
		return nil
	}
	if err == windows.ERROR_LOCK_VIOLATION || err == windows.ERROR_IO_PENDING {
		return errReclaimLockBusy
	}
	return err
}

func unlockFileExclusive(f *os.File) error {
	var ol windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
