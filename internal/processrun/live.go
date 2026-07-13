package processrun

import (
	"time"
)

// startTimeTolerance is the allowed skew when comparing a card's started_at to
// the OS process start time (clock truncation / encoding differences).
const startTimeTolerance = 2 * time.Second

// identityLive reports whether (pid, startedAt) still identifies a live process.
//
// Contract identity is the pair: PIDs are reusable, so a live pid with a
// mismatched start time is treated as stale (reclaimable). When the OS start
// time cannot be determined for a live pid, the result is fail-closed (live).
func identityLive(pid int, startedAt time.Time) bool {
	if pid <= 0 {
		return false
	}
	if !pidAlive(pid) {
		return false
	}
	osStart, ok := processStartTime(pid)
	if !ok {
		// Alive but start time unavailable — refuse reclaim (fail-closed).
		return true
	}
	if startedAt.IsZero() {
		return true
	}
	delta := osStart.Sub(startedAt)
	if delta < 0 {
		delta = -delta
	}
	return delta <= startTimeTolerance
}

// resolveIdentity returns the (pid, started_at) pair recorded on the card.
// Prefer the OS process start time when available so reclaim comparisons match.
func resolveIdentity(pid int, fallback time.Time) (int, time.Time) {
	if pid < 1 {
		pid = 0
	}
	if st, ok := processStartTime(pid); ok {
		return pid, st.UTC()
	}
	if fallback.IsZero() {
		fallback = time.Now().UTC()
	}
	return pid, fallback.UTC()
}
