//go:build windows

package runstats

import (
	"time"

	"golang.org/x/sys/windows"
)

// processCPU returns cumulative process kernel+user CPU time via GetProcessTimes.
// ok is false if the call fails, in which case the caller reports CPU as
// unavailable rather than a wrong value.
func processCPU() (time.Duration, bool) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		return 0, false
	}
	return filetimeToDuration(kernel) + filetimeToDuration(user), true
}

// filetimeToDuration converts a FILETIME (100-nanosecond ticks) to a Duration.
func filetimeToDuration(ft windows.Filetime) time.Duration {
	ticks := int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
	return time.Duration(ticks) * 100 * time.Nanosecond
}
