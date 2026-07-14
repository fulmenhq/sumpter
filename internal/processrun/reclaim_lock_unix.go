//go:build unix

package processrun

import (
	"os"

	"golang.org/x/sys/unix"
)

func tryLockFileExclusive(f *os.File) error {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return nil
	}
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		return errReclaimLockBusy
	}
	return err
}

func unlockFileExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
