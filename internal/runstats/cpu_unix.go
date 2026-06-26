//go:build darwin || linux

package runstats

import (
	"time"

	"golang.org/x/sys/unix"
)

// processCPU returns cumulative process (self) user+system CPU time via
// getrusage(RUSAGE_SELF). ok is false if the syscall fails, in which case the
// caller reports CPU as unavailable rather than a wrong value.
func processCPU() (time.Duration, bool) {
	var ru unix.Rusage
	if err := unix.Getrusage(unix.RUSAGE_SELF, &ru); err != nil {
		return 0, false
	}
	return timevalToDuration(ru.Utime) + timevalToDuration(ru.Stime), true
}

func timevalToDuration(tv unix.Timeval) time.Duration {
	return time.Duration(tv.Sec)*time.Second + time.Duration(tv.Usec)*time.Microsecond
}
