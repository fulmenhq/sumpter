package runstats

import (
	"runtime"
	"time"
)

// Collector captures the monotonic wall-clock and process-CPU baseline at run
// start so Sample can report deltas. Process CPU is cumulative since process
// start, so only the delta between Start and Sample is meaningful — reporting the
// absolute value would be wrong in tests and long-lived processes.
type Collector struct {
	parseWorkers int
	wallStart    time.Time
	cpuStart     time.Duration
	cpuStartOK   bool
}

// Start captures the baseline. parseWorkers is the configured --parse-workers
// value, used only to express effective CPU as a fraction of workers. A failed
// CPU read here is remembered so Sample reports CPU as unavailable rather than a
// wrong delta.
func Start(parseWorkers int) *Collector {
	cpu, ok := processCPU()
	return &Collector{
		parseWorkers: parseWorkers,
		wallStart:    time.Now(),
		cpuStart:     cpu,
		cpuStartOK:   ok,
	}
}

// Sample reads the end-of-run wall and CPU and returns the observed metrics.
// inputs and inputBytes are aggregate counters; bytesKnown is false when source
// sizes were not cheaply available (no extra reads are ever performed to populate
// them). CPU is reported only when both the start and end reads succeeded.
func (c *Collector) Sample(inputs int, inputBytes int64, bytesKnown bool) Sample {
	wall := time.Since(c.wallStart)
	cpuEnd, endOK := processCPU()
	s := Sample{
		Wall:         wall,
		Inputs:       inputs,
		InputBytes:   inputBytes,
		BytesKnown:   bytesKnown,
		ParseWorkers: c.parseWorkers,
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
		NumCPU:       runtime.NumCPU(),
	}
	if c.cpuStartOK && endOK {
		s.CPU = cpuEnd - c.cpuStart
		s.CPUKnown = true
	}
	return s
}
