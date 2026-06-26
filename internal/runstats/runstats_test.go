package runstats

import (
	"strings"
	"testing"
	"time"
)

func TestFormatFullSample(t *testing.T) {
	s := Sample{
		Wall:         2 * time.Second,
		Inputs:       1000,
		InputBytes:   45 * 1024 * 1024,
		BytesKnown:   true,
		ParseWorkers: 4,
		GOMAXPROCS:   8,
		NumCPU:       8,
		CPU:          2800 * time.Millisecond, // 2.8 CPU-seconds over 2s wall => 1.4 cores
		CPUKnown:     true,
	}
	out := Format(s)
	for _, want := range []string{
		"wall:          2s",
		"inputs:        1000 (500/s)",
		"input bytes:   45 MiB (22.5 MiB/s)",
		"parse-workers: 4",
		"GOMAXPROCS:    8 (logical CPUs: 8)",
		"effective CPU: 1.4 cores (~35% of 4 parse-workers)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Format output missing %q:\n%s", want, out)
		}
	}
}

func TestFormatCPUUnavailable(t *testing.T) {
	s := Sample{Wall: time.Second, Inputs: 10, BytesKnown: false, ParseWorkers: 2, CPUKnown: false}
	out := Format(s)
	if !strings.Contains(out, "effective CPU: unavailable") {
		t.Errorf("want CPU unavailable line:\n%s", out)
	}
	if !strings.Contains(out, "input bytes:   unavailable") {
		t.Errorf("want bytes unavailable line:\n%s", out)
	}
}

// A sub-microsecond wall must not divide-by-zero or emit a per-second rate.
func TestFormatZeroWallNoDivideByZero(t *testing.T) {
	s := Sample{Wall: 0, Inputs: 5, BytesKnown: true, InputBytes: 1024, ParseWorkers: 4, CPU: time.Second, CPUKnown: true}
	out := Format(s)
	if strings.Contains(out, "/s)") {
		t.Errorf("zero wall must not emit a per-second rate:\n%s", out)
	}
	if !strings.Contains(out, "effective CPU: unavailable") {
		t.Errorf("zero wall must report CPU unavailable (no divide-by-zero):\n%s", out)
	}
	if !strings.Contains(out, "inputs:        5") {
		t.Errorf("want plain input count without rate:\n%s", out)
	}
}

// ParseWorkers == 0 must not divide-by-zero or print a percent.
func TestFormatZeroWorkersNoPercent(t *testing.T) {
	s := Sample{Wall: time.Second, Inputs: 1, ParseWorkers: 0, CPU: 500 * time.Millisecond, CPUKnown: true}
	out := Format(s)
	if strings.Contains(out, "% of") {
		t.Errorf("zero parse-workers must not print a percent:\n%s", out)
	}
	if !strings.Contains(out, "effective CPU: 0.5 cores") {
		t.Errorf("want bare cores line:\n%s", out)
	}
}

// The formatted block must carry no identity — only labels, counts, and units.
// This is structurally guaranteed (Sample has no string fields), but pin it.
func TestFormatCarriesNoIdentity(t *testing.T) {
	out := Format(Sample{Wall: time.Second, Inputs: 3, BytesKnown: true, InputBytes: 10, ParseWorkers: 1, CPUKnown: true, CPU: time.Second})
	for _, forbidden := range []string{"/Users", "/tmp", "s3://", "recipe", ".xml", "credential"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("stats block leaked %q:\n%s", forbidden, out)
		}
	}
}

func TestCollectorSampleObservesRuntime(t *testing.T) {
	c := Start(4)
	// Consume a little CPU so the delta is non-negative and wall advances.
	sum := 0
	for i := 0; i < 1_000_000; i++ {
		sum += i
	}
	_ = sum
	s := c.Sample(10, 2048, true)
	if s.Wall <= 0 {
		t.Errorf("wall should advance, got %v", s.Wall)
	}
	if s.GOMAXPROCS <= 0 || s.NumCPU <= 0 {
		t.Errorf("GOMAXPROCS/NumCPU should be positive, got %d/%d", s.GOMAXPROCS, s.NumCPU)
	}
	if s.Inputs != 10 || s.InputBytes != 2048 || !s.BytesKnown || s.ParseWorkers != 4 {
		t.Errorf("sample carried wrong counters: %+v", s)
	}
	// On darwin/linux (CI), process CPU is available and the delta is non-negative.
	if s.CPUKnown && s.CPU < 0 {
		t.Errorf("CPU delta must be non-negative, got %v", s.CPU)
	}
}
