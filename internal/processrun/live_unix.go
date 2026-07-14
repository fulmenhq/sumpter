//go:build unix

package processrun

import "syscall"

// pidAlive reports whether a process with the given pid exists (signal 0).
// Used for (pid, started_at) liveness: a live pid is treated as a live run
// (fail-closed on run_id reclaim). PID-reuse of a different process is also
// refused — conservative and safe for same-host hygiene.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil
}
