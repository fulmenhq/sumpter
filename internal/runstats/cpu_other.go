//go:build !darwin && !linux && !windows

package runstats

import "time"

// processCPU has no portable implementation on this platform, so it reports CPU
// as unavailable; the run is never failed for this.
func processCPU() (time.Duration, bool) {
	return 0, false
}
