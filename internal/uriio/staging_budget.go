package uriio

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// StagingBudget is a run-global cap on staged cloud payload: peak concurrent
// object count and bytes, plus a per-object maximum. Count-only is not safe for
// variable-size objects. Stats are aggregate-only and never carry URIs.
//
// Admit waits (backpressure) when the working set is full but the object would
// fit after a release. An object larger than the per-object max or the total
// byte cap fails immediately — no one-object exception.
type StagingBudget struct {
	maxBytes  int64
	maxFiles  int
	objectMax int64

	mu              sync.Mutex
	cond            *sync.Cond
	usedBytes       int64
	usedFiles       int
	peakBytes       int64
	peakFiles       int
	acquiredBytes   int64
	acquiredCount   int64
	cleanupFailures int64
	retriesThrottle int64
	retriesUnavail  int64
	admitBlocked    bool
}

// StagingBudgetConfig is the operator-declared run-global staging cap.
type StagingBudgetConfig struct {
	MaxBytes  int64
	MaxFiles  int
	ObjectMax int64
}

// NewStagingBudget builds a run-global staging budget. All three caps must be
// positive; zero/negative is a programming error at the CLI validation layer.
func NewStagingBudget(cfg StagingBudgetConfig) *StagingBudget {
	b := &StagingBudget{
		maxBytes:  cfg.MaxBytes,
		maxFiles:  cfg.MaxFiles,
		objectMax: cfg.ObjectMax,
	}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// StagingStats is the aggregate-only snapshot for --stats / diagnostics.
type StagingStats struct {
	PeakBytes       int64
	PeakFiles       int
	AcquiredBytes   int64
	AcquiredCount   int64
	CleanupFailures int64
	RetriesThrottle int64
	RetriesUnavail  int64
}

// Admit reserves size bytes and one file slot. It fails before any reservation
// if size exceeds the per-object max or the total byte cap. Otherwise it waits
// until capacity is free or ctx is done.
func (b *StagingBudget) Admit(ctx context.Context, size int64) error {
	if b == nil {
		return nil
	}
	if size < 0 {
		size = 0
	}
	if b.objectMax > 0 && size > b.objectMax {
		return fmt.Errorf("uriio: object is %d bytes, exceeding the %d-byte per-object cap; not staged", size, b.objectMax)
	}
	if b.maxBytes > 0 && size > b.maxBytes {
		return fmt.Errorf("uriio: object is %d bytes, exceeding the %d-byte staging budget; not staged", size, b.maxBytes)
	}

	stop := context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer stop()

	b.mu.Lock()
	defer b.mu.Unlock()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if b.admitBlocked {
			return fmt.Errorf("uriio: staging cleanup failed; refusing further admits")
		}
		filesOK := b.maxFiles <= 0 || b.usedFiles < b.maxFiles
		bytesOK := b.maxBytes <= 0 || b.usedBytes+size <= b.maxBytes
		if filesOK && bytesOK {
			b.usedFiles++
			b.usedBytes += size
			b.acquiredCount++
			b.acquiredBytes += size
			if b.usedFiles > b.peakFiles {
				b.peakFiles = b.usedFiles
			}
			if b.usedBytes > b.peakBytes {
				b.peakBytes = b.usedBytes
			}
			return nil
		}
		b.cond.Wait()
	}
}

// Release frees a previously admitted object. size must match the admitted size.
func (b *StagingBudget) Release(size int64) {
	if b == nil {
		return
	}
	if size < 0 {
		size = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.usedFiles--
	if b.usedFiles < 0 {
		b.usedFiles = 0
	}
	b.usedBytes -= size
	if b.usedBytes < 0 {
		b.usedBytes = 0
	}
	b.cond.Broadcast()
}

// NoteCleanupFailure increments the aggregate cleanup-failure counter.
func (b *StagingBudget) NoteCleanupFailure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.cleanupFailures++
	b.admitBlocked = true
	b.cond.Broadcast()
	b.mu.Unlock()
}

// NoteRetry records a classified GET retry (throttle vs unavailable).
func (b *StagingBudget) NoteRetry(throttled bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	if throttled {
		b.retriesThrottle++
	} else {
		b.retriesUnavail++
	}
	b.mu.Unlock()
}

// Stats returns an aggregate-only snapshot. Never includes URIs.
func (b *StagingBudget) Stats() StagingStats {
	if b == nil {
		return StagingStats{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return StagingStats{
		PeakBytes:       b.peakBytes,
		PeakFiles:       b.peakFiles,
		AcquiredBytes:   b.acquiredBytes,
		AcquiredCount:   b.acquiredCount,
		CleanupFailures: b.cleanupFailures,
		RetriesThrottle: b.retriesThrottle,
		RetriesUnavail:  b.retriesUnavail,
	}
}

// acquireRetryLimit is the small cap on classified throttle/unavailable GET retries.
const acquireRetryLimit = 3

// StagingGetSizeMismatch rejects a GET whose reported size exceeds the Head
// size already admitted. Extra bytes would undercount the run-global budget.
// StagedBytesExceedAdmit reports when the on-disk staged file is larger than
// the Head/Get size already reserved in the run-global budget.
func StagedBytesExceedAdmit(path string, admitted int64) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.Size() > admitted {
		return fmt.Errorf("uriio: staged %d bytes exceeds admitted %d; not kept", fi.Size(), admitted)
	}
	return nil
}

func StagingGetSizeMismatch(admitted, got int64) error {
	if got > admitted {
		return fmt.Errorf("uriio: object size changed during get (%d -> %d); not staged", admitted, got)
	}
	return nil
}

func acquireBackoff(attempt int) time.Duration {
	// 50ms, 100ms, 200ms
	d := 50 * time.Millisecond
	for i := 0; i < attempt; i++ {
		d *= 2
	}
	return d
}
