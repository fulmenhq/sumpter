//go:build darwin

package processrun

import (
	"time"

	"golang.org/x/sys/unix"
)

// processStartTime returns the OS start time for pid via kern.proc.pid.
func processStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	kinfo, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || kinfo == nil {
		return time.Time{}, false
	}
	sec := kinfo.Proc.P_starttime.Sec
	usec := kinfo.Proc.P_starttime.Usec
	if sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(sec, int64(usec)*1000).UTC(), true
}
