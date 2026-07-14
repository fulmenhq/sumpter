//go:build !unix

package processrun

import "os"

// pidAlive reports whether a process with the given pid is believed to exist.
// On non-Unix platforms Signal(0) is unavailable; os.FindProcess always
// succeeds on Windows for any pid, so we conservatively treat a positive pid
// as live when FindProcess succeeds. Live-collision reclaim is therefore
// fail-closed (refuse) on these platforms when a prior card exists.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	if _, err := os.FindProcess(pid); err != nil {
		return false
	}
	// Windows FindProcess does not prove the process exists. Without a reliable
	// OpenProcess check here, treat non-zero pids as live when a card is present
	// (fail-closed reclaim). Tests that need dead-pid sweep run on Unix.
	return true
}
