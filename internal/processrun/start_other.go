//go:build !darwin && !linux

package processrun

import "time"

// processStartTime is unavailable on this platform.
func processStartTime(pid int) (time.Time, bool) {
	_ = pid
	return time.Time{}, false
}
