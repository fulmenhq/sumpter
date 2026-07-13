package processrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// CardConfig opens a telemetry-only process card + event stream under a runtime dir.
//
// Layout:
//
//	<runtime>/proc/<run_id>/card.json   (0600, ephemeral, swept on clean exit)
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
	// ContractBase, when non-empty, schema-validates the card against the pinned
	// process-run baseline before publication (fail-open if invalid / unreadable).
	ContractBase string
	// HeartbeatInterval is forwarded to the event emitter (zero → default).
	HeartbeatInterval time.Duration
}

// Card is the published discovery root for one process-run.
// Call Sweep on clean exit to remove the card while retaining the event stream.
type Card struct {
	// Path is the published card.json path (empty after Sweep or failed setup).
	Path string
	// EventsPath is the stream path (may outlive the card).
	EventsPath string
	// RunDir is <runtime>/proc/<run_id>.
	RunDir string
	// Emitter is the single-writer event surface for this run.
	Emitter Emitter

	// published is true only after atomic rename of a schema-valid card.
	published bool
	// ownedRunDir is true when we created the run directory this session
	// (or reclaimed a stale slot) and may remove an empty dir after Sweep.
	ownedRunDir bool
}

// OpenCard creates/reclaims the run directory, opens an exclusive event stream,
// validates a telemetry-only card, and atomically publishes it as the discovery root.
//
// On success the card is 0600 under a 0700 run directory. On any non-live error,
// partial state is removed and a path-free error is returned (caller fail-opens).
// ErrCardExists is the live-collision fail-closed signal — no partial card is left.
func OpenCard(cfg CardConfig) (*Card, error) {
	if err := validateCardConfig(&cfg); err != nil {
		return nil, err
	}

	runDir := RunDir(cfg.RuntimeDir, cfg.RunID)
	cardPath := filepath.Join(runDir, CardFileName)
	eventsPath := strings.TrimSpace(cfg.EventsPath)
	autoEvents := eventsPath == ""
	if autoEvents {
		eventsPath = filepath.Join(runDir, EventsFileName)
	}

	// Ensure runtime root exists with owner-only perms before the run slot.
	if err := ensureDir0700(cfg.RuntimeDir); err != nil {
		return nil, ErrCardSetup
	}
	if err := ensureDir0700(filepath.Join(cfg.RuntimeDir, "proc")); err != nil {
		return nil, ErrCardSetup
	}

	// Collision: live refuse (fail-closed); stale sweep; then exclusive create.
	if err := prepareRunDir(runDir, cardPath, cfg.RunID); err != nil {
		return nil, err
	}

	// Open exclusive stream first so the card only ever points at a live stream we own.
	emitter, err := Open(Config{
		Path:              eventsPath,
		RunID:             cfg.RunID,
		PID:               cfg.PID,
		Producer:          cfg.Producer,
		HeartbeatInterval: cfg.HeartbeatInterval,
	})
	if err != nil {
		// Stream collision/setup: remove empty reclaimed run dir; no card published.
		_ = removeIfEmpty(runDir)
		return nil, mapStreamErr(err)
	}

	card := &Card{
		Path:        cardPath,
		EventsPath:  eventsPath,
		RunDir:      runDir,
		Emitter:     emitter,
		ownedRunDir: true,
	}

	if err := card.publish(cfg); err != nil {
		emitter.Close()
		// Withhold partial stream we exclusively created.
		_ = os.Remove(eventsPath)
		_ = os.Remove(cardPath)
		_ = os.Remove(cardPath + ".tmp")
		_ = removeIfEmpty(runDir)
		card.Path = ""
		card.published = false
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

// prepareRunDir creates the run slot or reclaims a stale one.
// Live card → ErrCardExists. Stale → sweep slot contents. Missing → mkdir 0700.
func prepareRunDir(runDir, cardPath, runID string) error {
	_ = runID
	info, err := os.Lstat(runDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if mkErr := os.Mkdir(runDir, 0o700); mkErr != nil {
				return ErrCardSetup
			}
			_ = os.Chmod(runDir, 0o700)
			return nil
		}
		return ErrCardSetup
	}
	if !info.IsDir() {
		return ErrCardSetup
	}
	_ = os.Chmod(runDir, 0o700)

	// Existing slot: inspect card for liveness.
	existing, readErr := readExistingCard(cardPath)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		// Unreadable card — treat as setup failure (fail-open at caller).
		return ErrCardSetup
	}
	if existing != nil {
		if pidAlive(existing.PID) {
			return ErrCardExists
		}
		// Stale: reclaim the entire run slot (card + prior stream) for this run_id.
		if err := sweepRunSlot(runDir); err != nil {
			return ErrCardSetup
		}
		if err := os.Mkdir(runDir, 0o700); err != nil {
			return ErrCardSetup
		}
		_ = os.Chmod(runDir, 0o700)
		return nil
	}

	// Dir exists without a card (partial prior attempt): clear residue and reuse.
	if err := sweepRunSlot(runDir); err != nil {
		return ErrCardSetup
	}
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return ErrCardSetup
	}
	_ = os.Chmod(runDir, 0o700)
	return nil
}

type existingCard struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	RunID     string `json:"run_id"`
}

func readExistingCard(cardPath string) (*existingCard, error) {
	data, err := os.ReadFile(cardPath) // #nosec G304 - process-run card path under runtime dir
	if err != nil {
		return nil, err
	}
	var c existingCard
	if err := json.Unmarshal(data, &c); err != nil {
		// Corrupt card: treat as absent for reclaim (caller sweeps slot).
		return nil, os.ErrNotExist
	}
	if c.PID < 1 {
		return nil, os.ErrNotExist
	}
	return &c, nil
}

func sweepRunSlot(runDir string) error {
	// Remove the whole slot so a reclaimed run_id starts clean.
	// Durable streams from *other* run_ids are never under this directory.
	if err := os.RemoveAll(runDir); err != nil {
		return err
	}
	return nil
}

func (c *Card) publish(cfg CardConfig) error {
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
			"path":   c.EventsPath,
			"format": "ndjson",
		},
	}
	// Absolute path required by schema intent (discovery root).
	absEvents, err := filepath.Abs(c.EventsPath)
	if err != nil {
		return ErrCardSetup
	}
	doc["telemetry"].(map[string]interface{})["path"] = absEvents
	c.EventsPath = absEvents

	raw, err := json.Marshal(doc)
	if err != nil {
		return ErrCardSetup
	}
	raw = append(raw, '\n')

	if base := strings.TrimSpace(cfg.ContractBase); base != "" {
		resolved, rerr := artifactcontract.ResolveProcessRunBaseline(base)
		if rerr != nil {
			// Base unreadable / pin mismatch: fail-open (no partial card).
			return ErrCardSchema
		}
		result, verr := artifactcontract.ValidateProcessCardBytes(resolved, raw, CardFileName)
		if verr != nil || result == nil || !result.Valid {
			return ErrCardSchema
		}
	}

	// Atomic publish: write temp in the same directory, fsync, rename.
	tmp := c.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return ErrCardSetup
	}
	if _, werr := f.Write(raw); werr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ErrCardSetup
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ErrCardSetup
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return ErrCardSetup
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return ErrCardSetup
	}
	// Refuse to clobber an unexpected existing card (race with another publisher).
	if _, lerr := os.Lstat(c.Path); lerr == nil {
		_ = os.Remove(tmp)
		return ErrCardExists
	}
	if err := os.Rename(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		return ErrCardSetup
	}
	_ = os.Chmod(c.Path, 0o600)
	c.published = true
	return nil
}

// Sweep removes the published card on clean exit. The durable event stream is
// retained (contract: stream outlives the card). Safe to call multiple times.
// After a crash/kill the card is intentionally left so operators can discover
// the retained stream.
func (c *Card) Sweep() {
	if c == nil {
		return
	}
	if c.published && c.Path != "" {
		_ = os.Remove(c.Path)
		c.published = false
	}
	c.Path = ""
}

// Close tears down the emitter and, when clean is true, sweeps the card.
// clean=false leaves the card for crash/post-mortem discovery.
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
		// Existing stream under a reclaimed/new slot — surface as setup (fail-open).
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
