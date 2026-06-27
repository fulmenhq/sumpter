// Package runstats collects and formats opt-in, observed-only run statistics for
// diagnostic output. It exists to help a user tune --parse-workers empirically by
// comparing runs, not to prescribe a worker count.
//
// It is deliberately free of any run identity: no input paths, logical URIs,
// recipe IDs, credential handles, environment values, or error text ever flow
// through it — only aggregate counts, durations, and CPU/throughput derived from
// them. The formatted output is a human-readable end-of-run block intended for
// stderr, and never the data stream or the provenance manifest (stats are
// nondeterministic and must stay off the byte-identical artifact path).
package runstats

import (
	"fmt"
	"strings"
	"time"
)

// Sample is the observed-only set of metrics for one run. Every field is an
// aggregate counter, a duration, or a machine-capability integer — nothing here
// can identify an input, recipe, or destination.
type Sample struct {
	Wall         time.Duration // monotonic wall-clock elapsed for the run
	Inputs       int           // resolved inputs processed
	InputBytes   int64         // best-effort total source bytes (see BytesKnown)
	BytesKnown   bool          // false when source sizes were not cheaply available
	ParseWorkers int           // configured --parse-workers value
	GOMAXPROCS   int           // runtime.GOMAXPROCS(0) at sample time
	NumCPU       int           // runtime.NumCPU()
	CPU          time.Duration // process user+sys CPU consumed during the run
	CPUKnown     bool          // false when process CPU could not be read
}

// Format renders the diagnostic stats block. The leading newline separates it
// from any preceding warning output. The block is intentionally diagnostic, not
// prescriptive: it reports observed counters and leaves worker-count tuning to
// the caller comparing runs.
func Format(s Sample) string {
	var b strings.Builder
	b.WriteString("\nextract-multi --stats (diagnostic; observed counters, not a recommendation)\n")
	fmt.Fprintf(&b, "  wall:          %s\n", formatDuration(s.Wall))

	wallSec := s.Wall.Seconds()
	if wallSec > 0 {
		fmt.Fprintf(&b, "  inputs:        %d (%s/s)\n", s.Inputs, trimFloat(float64(s.Inputs)/wallSec))
	} else {
		fmt.Fprintf(&b, "  inputs:        %d\n", s.Inputs)
	}

	if s.BytesKnown {
		mib := float64(s.InputBytes) / (1024 * 1024)
		if wallSec > 0 {
			fmt.Fprintf(&b, "  input bytes:   %s MiB (%s MiB/s)\n", trimFloat(mib), trimFloat(mib/wallSec))
		} else {
			fmt.Fprintf(&b, "  input bytes:   %s MiB\n", trimFloat(mib))
		}
	} else {
		b.WriteString("  input bytes:   unavailable\n")
	}

	fmt.Fprintf(&b, "  parse-workers: %d\n", s.ParseWorkers)
	fmt.Fprintf(&b, "  GOMAXPROCS:    %d (logical CPUs: %d)\n", s.GOMAXPROCS, s.NumCPU)

	if s.CPUKnown && wallSec > 0 {
		cores := s.CPU.Seconds() / wallSec
		if s.ParseWorkers > 0 {
			pct := cores / float64(s.ParseWorkers) * 100
			fmt.Fprintf(&b, "  effective CPU: %s cores (~%.0f%% of %d parse-workers)\n", trimFloat(cores), pct, s.ParseWorkers)
		} else {
			fmt.Fprintf(&b, "  effective CPU: %s cores\n", trimFloat(cores))
		}
	} else {
		b.WriteString("  effective CPU: unavailable\n")
	}

	return b.String()
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	return d.Round(time.Millisecond).String()
}

// trimFloat formats a float with two decimals and trims trailing zeros so common
// values read cleanly (e.g. "4" and "1.4" rather than "4.00" and "1.40").
func trimFloat(f float64) string {
	s := fmt.Sprintf("%.2f", f)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	if s == "" || s == "-" {
		return "0"
	}
	return s
}
