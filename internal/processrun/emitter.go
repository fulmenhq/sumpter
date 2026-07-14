// Package processrun implements opt-in process-run/v0 flight-recorder emission.
//
// It is the runtime surface for long-running extract-multi telemetry: append-only
// NDJSON events and an optional telemetry-only process card (discovery root) under
// a platform runtime directory. Contract resolution and schema validation stay in
// internal/artifactcontract; this package never vendors alternate identities.
//
// Invariants:
//   - Fail-open: ordinary setup/write/flush failures disable emission and never fail
//     the extract. Live run_id card collision is the exception (fail-closed).
//   - Single-writer: all emitter methods are mutex-serialized; call only from the
//     orchestrator/committer path (never worker goroutines).
//   - Exactly one terminal event (Completed / Failed / Canceled) when Enabled.
//   - Event data is an explicit allow-list (counts, timing, closed reasons) — no paths,
//     xpaths, record content, or secrets.
//   - Stream open is exclusive create (no truncate, no merge into an existing path).
//   - Process card is 0600 under a 0700 run dir, validated before atomic publish,
//     swept on clean exit; the durable event stream is retained.
package processrun

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// Capability is the host-less process-run contract token.
	Capability = "contract: process-run/v0"
	// ProducerProfile is the Sumpter extract process-run producer profile id.
	ProducerProfile = "sumpter.extract-process-run/v0"
	// DefaultHeartbeatInterval is the cadence declared on started and used for
	// producer-owned heartbeats when the caller does not override it.
	DefaultHeartbeatInterval = 5 * time.Second

	// data key allow-list (producer-owned payload; keep in sync with emit methods).
	dataKeyDone               = "done"
	dataKeyTotal              = "total"
	dataKeyHeartbeatIntervalS = "heartbeat_interval_s"
	dataKeyReason             = "reason"
)

// Path-free setup errors for fail-open operator warnings (never embed the
// operator-chosen stream path — withheld like provenance argv omission).
var (
	ErrStreamExists    = errors.New("process-run: event stream path already exists")
	ErrStreamSetup     = errors.New("process-run: event stream setup failed")
	ErrStreamConfig    = errors.New("process-run: invalid configuration")
	ErrStreamPlacement = errors.New("process-run: events path not allowed")
)

// Producer identifies the emitting process on started (and optionally other events).
type Producer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Profile string `json:"profile,omitempty"`
}

// Config opens a fresh exclusive event stream. Empty Path yields a no-op Emitter.
type Config struct {
	Path              string
	RunID             string
	PID               int
	Producer          Producer
	HeartbeatInterval time.Duration // zero → DefaultHeartbeatInterval
	// OnWithhold is invoked (at most once) when fail-open disables emission and
	// removes the owned stream file. Used by the process card to withdraw the
	// discovery root for heartbeat and Sync/Close failures that bypass callers.
	// Must not block; must not call back into the emitter under its lock.
	OnWithhold func()
}

// streamWriter is the narrow I/O surface for the append-only event file.
// Tests inject failing implementations to prove mid-run fail-open.
type streamWriter interface {
	Write([]byte) (int, error)
	Sync() error
	Close() error
}

// Emitter is the single-writer process-run event surface.
type Emitter interface {
	// Enabled reports whether emission is active (false for no-op / fail-open disabled).
	Enabled() bool
	// Path returns the stream path when Enabled, else "".
	Path() string
	Started(total int)
	Progress(done, total int)
	Heartbeat(done int)
	Completed(done, total int)
	Failed(done, total int, reason string)
	Canceled(done, total int, reason string)
	// Close stops heartbeats and closes the stream file. Fail-open: errors are ignored.
	// After a clean terminal, the owned stream is retained; after fail-open disable,
	// any partial owned stream is removed so it is not publishable.
	Close()
}

// Open exclusively creates a new owner-only (0600) event stream at Path.
// It never truncates or appends to an existing path (file or symlink): collision
// degrades fail-open (noop emitter + error). Empty Path yields a silent no-op.
//
// Returned errors are path-free (ErrStreamExists / ErrStreamSetup / ErrStreamConfig)
// so fail-open warnings cannot disclose the operator-chosen telemetry path.
func Open(cfg Config) (Emitter, error) {
	if strings.TrimSpace(cfg.Path) == "" {
		return noopEmitter{}, nil
	}
	if err := validateConfig(&cfg); err != nil {
		return noopEmitter{}, err
	}

	// Refuse to clobber: any pre-existing path (including symlink) is a collision.
	if _, err := os.Lstat(cfg.Path); err == nil {
		return noopEmitter{}, ErrStreamExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return noopEmitter{}, ErrStreamSetup
	}

	// Exclusive create — never O_TRUNC / O_APPEND into someone else's stream.
	f, err := os.OpenFile(cfg.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304 - caller-selected telemetry path
	if err != nil {
		return noopEmitter{}, ErrStreamSetup
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(cfg.Path)
		return noopEmitter{}, ErrStreamSetup
	}
	return newFileEmitter(cfg, f, cfg.Path, true), nil
}

// OpenWithWriter is a test seam that injects a streamWriter for mid-run I/O failure.
// ownedPath, when non-empty, is removed on fail-open disable (partial stream withheld).
func OpenWithWriter(cfg Config, w streamWriter, ownedPath string) (Emitter, error) {
	if err := validateConfig(&cfg); err != nil {
		return noopEmitter{}, err
	}
	if w == nil {
		return noopEmitter{}, ErrStreamConfig
	}
	return newFileEmitter(cfg, w, ownedPath, ownedPath != ""), nil
}

func validateConfig(cfg *Config) error {
	if strings.TrimSpace(cfg.RunID) == "" {
		return ErrStreamConfig
	}
	if cfg.PID < 1 {
		cfg.PID = os.Getpid()
	}
	if cfg.HeartbeatInterval <= 0 {
		cfg.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if cfg.Producer.Name == "" {
		cfg.Producer.Name = "sumpter"
	}
	if cfg.Producer.Profile == "" {
		cfg.Producer.Profile = ProducerProfile
	}
	return nil
}

func newFileEmitter(cfg Config, w streamWriter, path string, removeOnDisable bool) *fileEmitter {
	return &fileEmitter{
		w:                 w,
		path:              path,
		runID:             cfg.RunID,
		pid:               cfg.PID,
		producer:          cfg.Producer,
		heartbeatInterval: cfg.HeartbeatInterval,
		removeOnDisable:   removeOnDisable,
		onWithhold:        cfg.OnWithhold,
		stopHeartbeat:     make(chan struct{}),
	}
}

type fileEmitter struct {
	mu                sync.Mutex
	w                 streamWriter
	path              string
	runID             string
	pid               int
	producer          Producer
	heartbeatInterval time.Duration
	removeOnDisable   bool
	onWithhold        func()
	withholdFired     bool
	seq               int
	disabled          bool
	terminal          bool
	started           bool
	stopHeartbeat     chan struct{}
	heartbeatOnce     sync.Once
	closeOnce         sync.Once
	// doneSnapshot is updated on Progress for heartbeat reads.
	doneSnapshot atomic.Int64
}

func (e *fileEmitter) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enabledLocked()
}

func (e *fileEmitter) enabledLocked() bool {
	return !e.disabled && e.w != nil
}

func (e *fileEmitter) Path() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.enabledLocked() {
		return ""
	}
	return e.path
}

func (e *fileEmitter) Started(total int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabled || e.terminal || e.started {
		return
	}
	intervalS := int(e.heartbeatInterval / time.Second)
	if intervalS < 1 {
		intervalS = 1
	}
	data := map[string]interface{}{
		dataKeyTotal:              total,
		dataKeyHeartbeatIntervalS: intervalS,
	}
	if !e.writeLocked("started", "INFO", data, true) {
		return
	}
	e.started = true
	e.startHeartbeatLocked()
}

func (e *fileEmitter) Progress(done, total int) {
	e.doneSnapshot.Store(int64(done))
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabled || e.terminal || !e.started {
		return
	}
	_ = e.writeLocked("progress", "INFO", map[string]interface{}{
		dataKeyDone:  done,
		dataKeyTotal: total,
	}, false)
}

func (e *fileEmitter) Heartbeat(done int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabled || e.terminal || !e.started {
		return
	}
	_ = e.writeLocked("heartbeat", "INFO", map[string]interface{}{
		dataKeyDone: done,
	}, false)
}

func (e *fileEmitter) Completed(done, total int) {
	e.terminalLocked("completed", "INFO", map[string]interface{}{
		dataKeyDone:  done,
		dataKeyTotal: total,
	})
}

func (e *fileEmitter) Failed(done, total int, reason string) {
	data := map[string]interface{}{
		dataKeyDone:  done,
		dataKeyTotal: total,
	}
	if r := closedReason(reason); r != "" {
		data[dataKeyReason] = r
	}
	e.terminalLocked("failed", "ERROR", data)
}

func (e *fileEmitter) Canceled(done, total int, reason string) {
	data := map[string]interface{}{
		dataKeyDone:  done,
		dataKeyTotal: total,
	}
	if r := closedReason(reason); r != "" {
		data[dataKeyReason] = r
	}
	e.terminalLocked("canceled", "WARN", data)
}

func (e *fileEmitter) Close() {
	e.closeOnce.Do(func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.stopHeartbeatLocked()
		if e.w == nil {
			e.disabled = true
			return
		}
		if e.disabled {
			_ = e.w.Close()
			e.w = nil
			return
		}
		// Production teardown always Sync+Close after an optional terminal write.
		// Sync/Close failures are fail-open telemetry: preserve extract semantics,
		// disable the emitter, and withhold the exclusively owned stream (even if
		// a terminal line was already written — durability is not proven).
		syncErr := e.w.Sync()
		closeErr := e.w.Close()
		e.w = nil
		if syncErr != nil || closeErr != nil {
			if e.removeOnDisable && e.path != "" {
				_ = os.Remove(e.path)
				e.fireWithholdLocked()
			}
			e.terminal = false
		}
		e.disabled = true
	})
}

func (e *fileEmitter) terminalLocked(event, severity string, data map[string]interface{}) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.disabled || e.terminal {
		return
	}
	if !e.started {
		return
	}
	if e.writeLocked(event, severity, data, false) {
		e.terminal = true
	}
	e.stopHeartbeatLocked()
}

// writeLocked appends one NDJSON line. On failure, disables the emitter (fail-open)
// and withholds the partial owned stream.
func (e *fileEmitter) writeLocked(event, severity string, data map[string]interface{}, includeProducer bool) bool {
	if e.w == nil || e.disabled {
		return false
	}
	env := map[string]interface{}{
		"ts":       time.Now().UTC().Format(time.RFC3339Nano),
		"event":    event,
		"run_id":   e.runID,
		"seq":      e.seq,
		"severity": severity,
		"pid":      e.pid,
	}
	if includeProducer {
		env["producer"] = map[string]interface{}{
			"name":    e.producer.Name,
			"version": e.producer.Version,
			"profile": e.producer.Profile,
		}
	}
	if len(data) > 0 {
		env["data"] = data
	}
	line, err := json.Marshal(env)
	if err != nil {
		e.disableLocked()
		return false
	}
	line = append(line, '\n')
	n, err := e.w.Write(line)
	if err != nil || n != len(line) {
		if err == nil {
			err = io.ErrShortWrite
		}
		_ = err
		e.disableLocked()
		return false
	}
	e.seq++
	return true
}

func (e *fileEmitter) disableLocked() {
	if e.disabled {
		return
	}
	e.disabled = true
	e.stopHeartbeatLocked()
	if e.w != nil {
		_ = e.w.Close()
		e.w = nil
	}
	// No partial publishable stream: remove the file we exclusively created.
	if e.removeOnDisable && e.path != "" && !e.terminal {
		_ = os.Remove(e.path)
		e.fireWithholdLocked()
	}
}

// fireWithholdLocked notifies the process card (or other owner) that the stream
// file was removed. Must be called only while e.mu is held and only when the owned
// stream was actually deleted. The callback runs outside the mutex.
func (e *fileEmitter) fireWithholdLocked() {
	if e.withholdFired || e.onWithhold == nil {
		return
	}
	e.withholdFired = true
	cb := e.onWithhold
	e.onWithhold = nil
	e.mu.Unlock()
	cb()
	e.mu.Lock()
}

func (e *fileEmitter) stopHeartbeatLocked() {
	if e.stopHeartbeat == nil {
		return
	}
	select {
	case <-e.stopHeartbeat:
	default:
		close(e.stopHeartbeat)
	}
}

func (e *fileEmitter) startHeartbeatLocked() {
	e.heartbeatOnce.Do(func() {
		interval := e.heartbeatInterval
		stop := e.stopHeartbeat
		go func() {
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				select {
				case <-stop:
					return
				case <-t.C:
					e.Heartbeat(int(e.doneSnapshot.Load()))
				}
			}
		}()
	})
}

// closedReason maps free-form reasons to a small allow-list (no paths/xpaths).
func closedReason(reason string) string {
	switch strings.TrimSpace(reason) {
	case "", "error", "run_error":
		return "run_error"
	case "canceled", "cancelled":
		return "canceled"
	case "partial":
		return "partial"
	default:
		// Never echo arbitrary error text (may contain paths).
		return "run_error"
	}
}

// noopEmitter is used when telemetry is off or fail-open disabled.
type noopEmitter struct{}

func (noopEmitter) Enabled() bool             { return false }
func (noopEmitter) Path() string              { return "" }
func (noopEmitter) Started(int)               {}
func (noopEmitter) Progress(int, int)         {}
func (noopEmitter) Heartbeat(int)             {}
func (noopEmitter) Completed(int, int)        {}
func (noopEmitter) Failed(int, int, string)   {}
func (noopEmitter) Canceled(int, int, string) {}
func (noopEmitter) Close()                    {}

// Noop returns a disabled emitter.
func Noop() Emitter { return noopEmitter{} }
