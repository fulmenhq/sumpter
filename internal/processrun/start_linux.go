//go:build linux

package processrun

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"time"
)

// processStartTime returns the OS start time for pid from /proc.
func processStartTime(pid int) (time.Time, bool) {
	if pid <= 0 {
		return time.Time{}, false
	}
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat") // #nosec G304
	if err != nil {
		return time.Time{}, false
	}
	// Format: pid (comm) state ... starttime is field 22 (1-based) after comm.
	// Comm may contain spaces/parens — find the last ')' then split the rest.
	idx := bytes.LastIndexByte(stat, ')')
	if idx < 0 || idx+2 >= len(stat) {
		return time.Time{}, false
	}
	fields := strings.Fields(string(stat[idx+2:]))
	// After ")": state=1, ..., starttime is the 20th remaining field (stat field 22).
	if len(fields) < 20 {
		return time.Time{}, false
	}
	startTicks, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	// Boot time from /proc/stat "btime".
	boot, ok := linuxBootTime()
	if !ok {
		return time.Time{}, false
	}
	hz := linuxClockTicks()
	sec := int64(startTicks) / hz
	nsec := (int64(startTicks) % hz) * (int64(time.Second) / hz)
	return boot.Add(time.Duration(sec)*time.Second + time.Duration(nsec)).UTC(), true
}

func linuxBootTime() (time.Time, bool) {
	data, err := os.ReadFile("/proc/stat") // #nosec G304
	if err != nil {
		return time.Time{}, false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "btime ") {
			parts := strings.Fields(line)
			if len(parts) < 2 {
				return time.Time{}, false
			}
			sec, err := strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return time.Time{}, false
			}
			return time.Unix(sec, 0).UTC(), true
		}
	}
	return time.Time{}, false
}

func linuxClockTicks() int64 {
	// sysconf(_SC_CLK_TCK) is typically 100; hard-code the portable default.
	// If wrong, start-time comparisons use startTimeTolerance as a buffer.
	return 100
}
