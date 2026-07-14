//go:build unix

package processrun

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLockFileExclusive(f *os.File) error {
	// Fd() is a live OS file descriptor; flock(2) requires int. Conversion is
	// the standard Go pattern for unix.Flock and cannot overflow a valid fd.
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB) // #nosec G115 - Fd is a valid OS fd; unix.Flock requires int
	if err == nil {
		return nil
	}
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		return errReclaimLockBusy
	}
	return err
}

func unlockFileExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN) // #nosec G115 - Fd is a valid OS fd; unix.Flock requires int
}
