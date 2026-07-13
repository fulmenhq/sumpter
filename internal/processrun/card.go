package processrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
)

// Path-free process-card setup errors (never embed runtime/card paths).
var (
	ErrCardExists    = errors.New("process-run: process card run_id is live")
	ErrCardSetup     = errors.New("process-run: process card setup failed")
	ErrCardConfig    = errors.New("process-run: invalid process card configuration")
	ErrCardPlacement = errors.New("process-run: runtime dir not allowed")
	ErrCardSchema    = errors.New("process-run: process card schema validation failed")
)

// ClaimFileName is the exclusive ownership lease under a run directory.
// Only the process that creates this file with O_EXCL may publish the card.
const ClaimFileName = "claim.json"

// CardConfig opens a telemetry-only process card + event stream under a runtime dir.
//
// Layout:
//
//	<runtime>/proc/<run_id>/claim.json   (0600, exclusive lease)
//	<runtime>/proc/<run_id>/card.json    (0600, ephemeral, swept on clean exit / stream disable)
//	<runtime>/proc/<run_id>/events.ndjson (0600, durable; auto-placed when EventsPath empty)
//
// Live (pid, started_at) collision is fail-closed (ErrCardExists). Ordinary setup
// failures are fail-open for the caller (extract continues without process-run).
type CardConfig struct {
	// RuntimeDir is the already-resolved runtime root (must pass ValidateRuntimeDir).
	RuntimeDir string
	RunID      string
	PID        int
	StartedAt  time.Time
	Producer   Producer
	// EventsPath, when non-empty, is the exclusive stream path (must already pass
	// ValidateEventsPath). When empty, the stream is auto-placed under the run dir.
	EventsPath string
	// ContractBase optionally overrides the embedded process-run pin for schema
	// validation. When empty, the embedded Crucible v0.1.19 pin is used. Either
	// path must resolve against ProcessRunBaselineBundleSHA256; missing/wrong
	// material fails open (withholds card+stream) and never publishes unchecked.
	ContractBase string
	// HeartbeatInterval is forwarded to the event emitter (zero → default).
	HeartbeatInterval time.Duration
}

// Card is the published discovery root for one process-run.
// Call Sweep on clean exit to remove the card while retaining the event stream.
// The card is also withdrawn immediately if the event stream fail-open disables.
type Card struct {
	// Path is the published card.json path (empty after Sweep or failed setup).
	Path string
	// EventsPath is the stream path (may outlive the card).
	EventsPath string
	// RunDir is <runtime>/proc/<run_id>.
	RunDir string
	// Emitter is the single-writer event surface for this run.
	Emitter Emitter

	// published is true only after exclusive publish of a schema-valid card.
	published bool
	// claimPath is the exclusive lease we own for this slot.
	claimPath string
	// identity recorded on the claim/card.
	pid       int
	startedAt time.Time

	sweepMu sync.Mutex
	swept   bool
}

// OpenCard creates/reclaims the run directory, opens an exclusive event stream,
// validates a telemetry-only card against the pinned process-run baseline, and
// publishes it as the discovery root under an exclusive ownership claim.
//
// On success the card is 0600 under a 0700 run directory. On any non-live error,
// partial state is removed and a path-free error is returned (caller fail-opens).
// ErrCardExists is the live-collision fail-closed signal — no partial card is left.
func OpenCard(cfg CardConfig) (*Card, error) {
	if err := validateCardConfig(&cfg); err != nil {
		return nil, err
	}
	// Resolve OS process identity so reclaim comparisons use the same pair.
	cfg.PID, cfg.StartedAt = resolveIdentity(cfg.PID, cfg.StartedAt)

	runDir := RunDir(cfg.RuntimeDir, cfg.RunID)
	cardPath := filepath.Join(runDir, CardFileName)
	claimPath := filepath.Join(runDir, ClaimFileName)
	eventsPath := strings.TrimSpace(cfg.EventsPath)
	if eventsPath == "" {
		eventsPath = filepath.Join(runDir, EventsFileName)
	}

	// Ensure runtime root exists with owner-only perms before the run slot.
	if err := ensureDir0700(cfg.RuntimeDir); err != nil {
		return nil, ErrCardSetup
	}
	if err := ensureDir0700(filepath.Join(cfg.RuntimeDir, "proc")); err != nil {
		return nil, ErrCardSetup
	}

	// Exclusive claim — live refuse fail-closed; stale reclaim; no blind RemoveAll.
	if err := claimRunSlot(runDir, claimPath, cardPath, cfg.PID, cfg.StartedAt); err != nil {
		return nil, err
	}

	// Open exclusive stream first so the card only ever points at a stream we own.
	// cardOpenStream is the production Open; tests may swap it to inject writers.
	emitter, err := cardOpenStream(Config{
		Path:              eventsPath,
		RunID:             cfg.RunID,
		PID:               cfg.PID,
		Producer:          cfg.Producer,
		HeartbeatInterval: cfg.HeartbeatInterval,
	})
	if err != nil {
		releaseClaim(runDir, claimPath)
		return nil, mapStreamErr(err)
	}

	card := &Card{
		Path:       cardPath,
		EventsPath: eventsPath,
		RunDir:     runDir,
		claimPath:  claimPath,
		pid:        cfg.PID,
		startedAt:  cfg.StartedAt,
	}
	// Wrap emitter so fail-open stream disable withdraws the discovery root.
	card.Emitter = &withdrawingEmitter{
		inner: emitter,
		onDisable: func() {
			card.Sweep()
		},
	}

	if err := card.publish(cfg); err != nil {
		emitter.Close()
		_ = os.Remove(eventsPath)
		card.Sweep()
		releaseClaim(runDir, claimPath)
		card.Emitter = Noop()
		return nil, err
	}
	return card, nil
}

func validateCardConfig(cfg *CardConfig) error {
	if strings.TrimSpace(cfg.RuntimeDir) == "" || strings.TrimSpace(cfg.RunID) == "" {
		return ErrCardConfig
	}
	if cfg.PID < 1 {
		cfg.PID = os.Getpid()
	}
	if cfg.StartedAt.IsZero() {
		cfg.StartedAt = time.Now().UTC()
	}
	if cfg.Producer.Name == "" {
		cfg.Producer.Name = "sumpter"
	}
	if cfg.Producer.Profile == "" {
		cfg.Producer.Profile = ProducerProfile
	}
	return nil
}

// claimRunSlot exclusively owns <runDir> for this (pid, started_at).
//
// Strategy:
//  1. Mkdir the slot; on success write claim.json with O_EXCL.
//  2. On EEXIST, read claim/card identity:
//     - live matching pair → ErrCardExists (fail-closed)
//     - proven stale → RemoveAll and retry
//     - no identity yet → brief retry then fail-closed ErrCardExists
//     (never RemoveAll without proven stale ownership; never fail-open on race)
func claimRunSlot(runDir, claimPath, cardPath string, pid int, startedAt time.Time) error {
	const attempts = 8
	for i := 0; i < attempts; i++ {
		err := os.Mkdir(runDir, 0o700)
		if err == nil {
			_ = os.Chmod(runDir, 0o700)
			if werr := writeClaimExclusive(claimPath, pid, startedAt); werr != nil {
				// Lost the claim write race or setup failure — release dir.
				_ = os.RemoveAll(runDir)
				if errors.Is(werr, ErrCardExists) {
					return ErrCardExists
				}
				// Another contender may have claimed; re-evaluate.
				continue
			}
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return ErrCardSetup
		}

		// Slot exists: inspect ownership.
		owner, oerr := readSlotOwner(claimPath, cardPath)
		if oerr != nil && !errors.Is(oerr, os.ErrNotExist) {
			return ErrCardSetup
		}
		if owner != nil {
			if identityLive(owner.PID, owner.StartedAt) {
				return ErrCardExists
			}
			// Proven stale: reclaim whole slot and retry exclusive create.
			if err := os.RemoveAll(runDir); err != nil {
				return ErrCardSetup
			}
			continue
		}

		// No claim/card yet — concurrent creator in progress. Wait and recheck.
		// Do NOT RemoveAll: that would clobber a live creator. After retries,
		// fail-closed (identity conflict) rather than fail-open.
		time.Sleep(time.Duration(5*(i+1)) * time.Millisecond)
	}
	return ErrCardExists
}

type slotOwner struct {
	PID       int
	StartedAt time.Time
}

func readSlotOwner(claimPath, cardPath string) (*slotOwner, error) {
	owner, err := readOwnerFile(claimPath)
	if err == nil {
		return owner, nil
	}
	// Missing or corrupt claim: fall through to card.json.
	return readOwnerFile(cardPath)
}

func readOwnerFile(path string) (*slotOwner, error) {
	data, err := os.ReadFile(path) // #nosec G304 - process-run path under runtime dir
	if err != nil {
		return nil, err
	}
	var raw struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"started_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, os.ErrNotExist
	}
	if raw.PID < 1 || strings.TrimSpace(raw.StartedAt) == "" {
		return nil, os.ErrNotExist
	}
	ts, err := time.Parse(time.RFC3339Nano, raw.StartedAt)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, raw.StartedAt)
		if err != nil {
			return nil, os.ErrNotExist
		}
	}
	return &slotOwner{PID: raw.PID, StartedAt: ts.UTC()}, nil
}

func writeClaimExclusive(claimPath string, pid int, startedAt time.Time) error {
	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": startedAt.UTC().Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return ErrCardSetup
	}
	raw = append(raw, '\n')
	f, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrCardExists
		}
		return ErrCardSetup
	}
	if _, werr := f.Write(raw); werr != nil {
		_ = f.Close()
		_ = os.Remove(claimPath)
		return ErrCardSetup
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(claimPath)
		return ErrCardSetup
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(claimPath)
		return ErrCardSetup
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(claimPath)
		return ErrCardSetup
	}
	return nil
}

func releaseClaim(runDir, claimPath string) {
	_ = os.Remove(claimPath)
	_ = removeIfEmpty(runDir)
	// If only claim was present and stream/card gone, try remove dir.
	_ = os.Remove(runDir)
}

func (c *Card) publish(cfg CardConfig) error {
	absEvents, err := filepath.Abs(c.EventsPath)
	if err != nil {
		return ErrCardSetup
	}
	c.EventsPath = absEvents

	doc := map[string]interface{}{
		"capabilities": []string{Capability},
		"run_id":       cfg.RunID,
		"pid":          cfg.PID,
		"started_at":   cfg.StartedAt.UTC().Format(time.RFC3339Nano),
		"producer": map[string]interface{}{
			"name":    cfg.Producer.Name,
			"version": cfg.Producer.Version,
			"profile": cfg.Producer.Profile,
		},
		"telemetry": map[string]interface{}{
			"path":   absEvents,
			"format": "ndjson",
		},
	}

	raw, err := json.Marshal(doc)
	if err != nil {
		return ErrCardSetup
	}
	raw = append(raw, '\n')

	// Always validate against the pinned process-run baseline before publication.
	resolved, rerr := resolveCardValidator(strings.TrimSpace(cfg.ContractBase))
	if rerr != nil {
		return ErrCardSchema
	}
	result, verr := artifactcontract.ValidateProcessCardBytes(resolved, raw, CardFileName)
	if verr != nil || result == nil || !result.Valid {
		return ErrCardSchema
	}

	// Exclusive create of the discovery root — never rename-over an existing card.
	// (Platforms where rename replaces would otherwise clobber a concurrent publisher.)
	f, err := os.OpenFile(c.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return ErrCardExists
		}
		return ErrCardSetup
	}
	if _, werr := f.Write(raw); werr != nil {
		_ = f.Close()
		_ = os.Remove(c.Path)
		return ErrCardSetup
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(c.Path)
		return ErrCardSetup
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(c.Path)
		return ErrCardSetup
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(c.Path)
		return ErrCardSetup
	}
	c.published = true
	return nil
}

// Sweep removes the published card (and claim lease) so the discovery root is
// withdrawn. The durable event stream is retained. Safe to call multiple times.
// After a crash/kill without Sweep the card remains for post-mortem discovery.
func (c *Card) Sweep() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if c.swept {
		return
	}
	c.swept = true
	if c.Path != "" {
		_ = os.Remove(c.Path)
	}
	if c.claimPath != "" {
		_ = os.Remove(c.claimPath)
	}
	c.published = false
	c.Path = ""
}

// Close tears down the emitter and, when clean is true, sweeps the card.
// clean=false leaves the card for crash/post-mortem discovery (stream retained either way).
func (c *Card) Close(clean bool) {
	if c == nil {
		return
	}
	if c.Emitter != nil {
		c.Emitter.Close()
	}
	if clean {
		c.Sweep()
	}
}

// withdrawingEmitter proxies an Emitter and withdraws the card as soon as the
// underlying stream fail-open disables mid-run (so a published card never points
// at a removed/partial stream). Intentional Close does not auto-sweep — the
// Card.Close(clean) path owns clean-exit vs crash-persistence semantics.
type withdrawingEmitter struct {
	inner     Emitter
	onDisable func()
	once      sync.Once
	closed    bool
}

func (w *withdrawingEmitter) check() {
	if w.closed {
		return
	}
	if w.inner == nil || !w.inner.Enabled() {
		w.once.Do(func() {
			if w.onDisable != nil {
				w.onDisable()
			}
		})
	}
}

func (w *withdrawingEmitter) Enabled() bool {
	if w.inner == nil {
		return false
	}
	ok := w.inner.Enabled()
	if !ok {
		w.check()
	}
	return ok
}

func (w *withdrawingEmitter) Path() string {
	if w.inner == nil || w.closed {
		return ""
	}
	return w.inner.Path()
}

func (w *withdrawingEmitter) Started(total int) {
	if w.inner != nil {
		w.inner.Started(total)
	}
	w.check()
}

func (w *withdrawingEmitter) Progress(done, total int) {
	if w.inner != nil {
		w.inner.Progress(done, total)
	}
	w.check()
}

func (w *withdrawingEmitter) Heartbeat(done int) {
	if w.inner != nil {
		w.inner.Heartbeat(done)
	}
	w.check()
}

func (w *withdrawingEmitter) Completed(done, total int) {
	if w.inner != nil {
		w.inner.Completed(done, total)
	}
	w.check()
}

func (w *withdrawingEmitter) Failed(done, total int, reason string) {
	if w.inner != nil {
		w.inner.Failed(done, total, reason)
	}
	w.check()
}

func (w *withdrawingEmitter) Canceled(done, total int, reason string) {
	if w.inner != nil {
		w.inner.Canceled(done, total, reason)
	}
	w.check()
}

func (w *withdrawingEmitter) Close() {
	w.closed = true
	if w.inner != nil {
		w.inner.Close()
	}
}

func ensureDir0700(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	return os.Chmod(path, 0o700)
}

func removeIfEmpty(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return nil
	}
	return os.Remove(dir)
}

func mapStreamErr(err error) error {
	switch {
	case errors.Is(err, ErrStreamExists):
		return ErrCardSetup
	case errors.Is(err, ErrStreamPlacement):
		return ErrCardPlacement
	case errors.Is(err, ErrStreamConfig):
		return ErrCardConfig
	case errors.Is(err, ErrStreamSetup):
		return ErrCardSetup
	default:
		return ErrCardSetup
	}
}

// cardOpenStream opens the event stream for a card. Tests replace this to inject
// failing writers while keeping the claim/publish path production-shaped.
var cardOpenStream = func(cfg Config) (Emitter, error) {
	return Open(cfg)
}
