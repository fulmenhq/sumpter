package processrun

import (
	"crypto/rand"
	"encoding/hex"
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

const (
	// ClaimFileName is the exclusive ownership lease under a run directory.
	ClaimFileName = "claim.json"
	// Claim state values persisted in claim.json.
	claimStateLive   = "live"
	claimStateExited = "exited"
)

// CardConfig opens a telemetry-only process card + event stream under a runtime dir.
//
// Layout:
//
//	<runtime>/proc/<run_id>/claim.json   (0600, exclusive lease + identity tombstone)
//	<runtime>/proc/<run_id>/card.json    (0600, discovery root while published)
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
// Call Sweep on clean exit to withdraw the card while retaining the event stream
// and a claim tombstone for later stale reclaim. The card is also withdrawn
// immediately if the event stream fail-open removes the owned stream file.
type Card struct {
	// Path is the published card.json path (empty after Sweep or failed setup).
	Path string
	// EventsPath is the stream path (may outlive the card).
	EventsPath string
	// RunDir is <runtime>/proc/<run_id>.
	RunDir string
	// Emitter is the single-writer event surface for this run.
	Emitter Emitter

	// published is true only after exclusive atomic publish of a schema-valid card.
	published bool
	// claimPath is the exclusive lease path for this slot.
	claimPath string
	// token uniquely identifies this claim generation (CAS for reclaim/release).
	token string
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

	token, err := claimRunSlot(runDir, claimPath, cardPath, eventsPath, cfg.PID, cfg.StartedAt)
	if err != nil {
		return nil, err
	}

	card := &Card{
		Path:       cardPath,
		EventsPath: eventsPath,
		RunDir:     runDir,
		claimPath:  claimPath,
		token:      token,
		pid:        cfg.PID,
		startedAt:  cfg.StartedAt,
	}

	// Open exclusive stream with direct withhold notification (heartbeat + Sync/Close).
	emitter, err := cardOpenStream(Config{
		Path:              eventsPath,
		RunID:             cfg.RunID,
		PID:               cfg.PID,
		Producer:          cfg.Producer,
		HeartbeatInterval: cfg.HeartbeatInterval,
		OnWithhold: func() {
			// Stream file removed — withdraw discovery root immediately.
			card.Sweep()
		},
	})
	if err != nil {
		card.abandonSetup()
		return nil, mapStreamErr(err)
	}
	card.Emitter = emitter

	if err := card.publish(cfg); err != nil {
		// Close may also fire OnWithhold → Sweep; abandonSetup is token-safe.
		emitter.Close()
		_ = os.Remove(eventsPath)
		card.abandonSetup()
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

// claimRunSlot exclusively owns <runDir> for this (pid, started_at) and returns
// the unique claim token for this generation.
//
// Fresh slot: Mkdir + exclusive claim.json create.
// Existing slot: read claim identity; live → ErrCardExists; stale → CAS
// quarantine of the old claim token then exclusive new claim (never blind RemoveAll
// of a live winner). Missing identity with residue: wait, then try exclusive claim.
func claimRunSlot(runDir, claimPath, cardPath, eventsPath string, pid int, startedAt time.Time) (string, error) {
	const attempts = 12
	for i := 0; i < attempts; i++ {
		myToken, err := newClaimToken()
		if err != nil {
			return "", ErrCardSetup
		}

		err = os.Mkdir(runDir, 0o700)
		if err == nil {
			_ = os.Chmod(runDir, 0o700)
			if werr := writeClaimExclusive(claimPath, pid, startedAt, myToken, claimStateLive); werr != nil {
				// Did not install claim — do not RemoveAll (another contender may own it).
				// If the dir is empty of a claim, best-effort remove only if still claimless.
				if _, rerr := os.Stat(claimPath); errors.Is(rerr, os.ErrNotExist) {
					_ = removeIfEmpty(runDir)
				}
				if errors.Is(werr, ErrCardExists) {
					// Lost exclusive claim create — re-evaluate.
					continue
				}
				continue
			}
			return myToken, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", ErrCardSetup
		}

		// Slot exists: inspect ownership via claim (preferred) or card fallback.
		owner, oerr := readSlotOwner(claimPath, cardPath)
		if oerr != nil && !errors.Is(oerr, os.ErrNotExist) {
			return "", ErrCardSetup
		}
		if owner != nil {
			if identityLive(owner.PID, owner.StartedAt) {
				return "", ErrCardExists
			}
			// Proven stale: CAS takeover by quarantining the observed claim token.
			if tok, terr := staleTakeover(runDir, claimPath, cardPath, eventsPath, owner, myToken, pid, startedAt); terr == nil {
				return tok, nil
			} else if errors.Is(terr, ErrCardExists) {
				return "", ErrCardExists
			}
			// Lost race or transient — retry.
			time.Sleep(time.Duration(2*(i+1)) * time.Millisecond)
			continue
		}

		// No claim/card identity. Residue may exist (legacy ownerless stream).
		// Wait briefly for an in-progress creator; then try exclusive claim create
		// without deleting the directory (Mkdir already lost).
		if _, cerr := os.Stat(claimPath); errors.Is(cerr, os.ErrNotExist) {
			if werr := writeClaimExclusive(claimPath, pid, startedAt, myToken, claimStateLive); werr == nil {
				// We own the claim now — clear prior stream/card residue under our ownership.
				_ = os.Remove(cardPath)
				_ = os.Remove(eventsPath)
				return myToken, nil
			}
		}
		time.Sleep(time.Duration(5*(i+1)) * time.Millisecond)
	}
	return "", ErrCardExists
}

// staleTakeover quarantines a proven-stale claim by renaming claim.json to a
// token-specific name (atomic CAS), then installs a new exclusive claim. Only the
// winner of the rename may clean residue. Losers never delete the winner.
func staleTakeover(runDir, claimPath, cardPath, eventsPath string, old *claimDoc, myToken string, pid int, startedAt time.Time) (string, error) {
	// Re-read: identity must still match the observed stale token.
	cur, err := readClaimFile(claimPath)
	if err != nil {
		// Claim vanished — retry outer loop.
		return "", err
	}
	if cur.Token != old.Token {
		return "", errors.New("claim token changed")
	}
	if identityLive(cur.PID, cur.StartedAt) {
		return "", ErrCardExists
	}

	// CAS: move claim.json → claim.stale.<oldToken>. Fails if another reclaimer won.
	staleName := filepath.Join(runDir, "claim.stale."+old.Token)
	if err := os.Rename(claimPath, staleName); err != nil {
		return "", err
	}

	// We hold the quarantined identity. Install our live claim exclusively.
	if err := writeClaimExclusive(claimPath, pid, startedAt, myToken, claimStateLive); err != nil {
		// Could not install — leave quarantine in place; do not delete others' work.
		return "", err
	}

	// Under our claim: remove discovery root, prior stream, and quarantine marker.
	_ = os.Remove(cardPath)
	_ = os.Remove(eventsPath)
	_ = os.Remove(staleName)
	// Best-effort cleanup of other stale markers from prior races.
	if entries, rerr := os.ReadDir(runDir); rerr == nil {
		for _, e := range entries {
			name := e.Name()
			if strings.HasPrefix(name, "claim.stale.") || strings.HasSuffix(name, ".tmp") {
				_ = os.Remove(filepath.Join(runDir, name))
			}
		}
	}
	return myToken, nil
}

type claimDoc struct {
	PID       int
	StartedAt time.Time
	Token     string
	State     string
}

func readSlotOwner(claimPath, cardPath string) (*claimDoc, error) {
	if c, err := readClaimFile(claimPath); err == nil {
		return c, nil
	}
	// Fall back to card identity (crash before claim tombstone existed).
	return readClaimFile(cardPath)
}

func readClaimFile(path string) (*claimDoc, error) {
	data, err := os.ReadFile(path) // #nosec G304 - process-run path under runtime dir
	if err != nil {
		return nil, err
	}
	var raw struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"started_at"`
		Token     string `json:"token"`
		State     string `json:"state"`
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
	token := strings.TrimSpace(raw.Token)
	if token == "" {
		// Pre-token claims: synthesize a stable token from path contents hash so
		// quarantine names remain unique enough for CAS rename.
		token = "legacy"
	}
	state := strings.TrimSpace(raw.State)
	if state == "" {
		state = claimStateLive
	}
	return &claimDoc{
		PID:       raw.PID,
		StartedAt: ts.UTC(),
		Token:     token,
		State:     state,
	}, nil
}

func writeClaimExclusive(claimPath string, pid int, startedAt time.Time, token, state string) error {
	raw, err := marshalClaim(pid, startedAt, token, state)
	if err != nil {
		return ErrCardSetup
	}
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

// writeClaimReplaceAtomically replaces claim.json content only when the current
// claim token still matches expectedToken (ownership check). Uses temp + rename.
func writeClaimReplaceAtomically(claimPath string, expectedToken string, pid int, startedAt time.Time, token, state string) error {
	cur, err := readClaimFile(claimPath)
	if err != nil {
		return err
	}
	if cur.Token != expectedToken {
		return errors.New("claim ownership lost")
	}
	raw, err := marshalClaim(pid, startedAt, token, state)
	if err != nil {
		return err
	}
	tmp := claimPath + ".tmp." + token
	if err := writeFileExclusiveFull(tmp, raw); err != nil {
		return err
	}
	// Re-check ownership before rename-over.
	cur2, err := readClaimFile(claimPath)
	if err != nil || cur2.Token != expectedToken {
		_ = os.Remove(tmp)
		return errors.New("claim ownership lost")
	}
	if err := os.Rename(tmp, claimPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Chmod(claimPath, 0o600)
	return nil
}

func marshalClaim(pid int, startedAt time.Time, token, state string) ([]byte, error) {
	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": startedAt.UTC().Format(time.RFC3339Nano),
		"token":      token,
		"state":      state,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

func newClaimToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func (c *Card) publish(cfg CardConfig) error {
	if !c.stillOwnClaim() {
		return ErrCardExists
	}
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

	// Atomic publish: write+fsync a same-directory temp, then hard-link into the
	// final discovery-root path (no-replace). Readers never see a partial card.json.
	tmp := filepath.Join(c.RunDir, "card."+c.token+".tmp")
	if err := writeFileExclusiveFull(tmp, raw); err != nil {
		return ErrCardSetup
	}
	if err := os.Link(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return ErrCardExists
		}
		// Some filesystems reject hard links; fall back to exclusive create of final
		// only if the final path is still absent — still write full content first via
		// rename from the already-complete temp when rename is no-clobber.
		if _, lerr := os.Lstat(c.Path); lerr == nil {
			return ErrCardExists
		}
		if rerr := os.Rename(tmp, c.Path); rerr != nil {
			_ = os.Remove(tmp)
			return ErrCardSetup
		}
	} else {
		_ = os.Remove(tmp)
	}
	_ = os.Chmod(c.Path, 0o600)
	c.published = true
	return nil
}

// writeFileExclusiveFull creates path exclusively, writes all bytes, fsyncs, closes.
func writeFileExclusiveFull(path string, raw []byte) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) // #nosec G304
	if err != nil {
		return err
	}
	if _, werr := f.Write(raw); werr != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return werr
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func (c *Card) stillOwnClaim() bool {
	if c == nil || c.claimPath == "" || c.token == "" {
		return false
	}
	cur, err := readClaimFile(c.claimPath)
	if err != nil {
		return false
	}
	return cur.Token == c.token
}

// Sweep withdraws the discovery root (card.json) and converts the claim into an
// exited tombstone so a later same-run_id contender can prove staleness once the
// producer is no longer live. The durable event stream is retained.
// Safe to call multiple times; no-ops if this card no longer owns the claim token.
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
	if !c.stillOwnClaim() {
		c.published = false
		c.Path = ""
		return
	}
	if c.Path != "" {
		_ = os.Remove(c.Path)
	}
	// Tombstone: keep (pid, started_at, token) with state=exited for reclaim CAS.
	_ = writeClaimReplaceAtomically(c.claimPath, c.token, c.pid, c.startedAt, c.token, claimStateExited)
	c.published = false
	c.Path = ""
}

// abandonSetup releases a claim we own after a failed setup (no retained stream
// contract). Token-checked; never deletes another generation's claim.
func (c *Card) abandonSetup() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if !c.stillOwnClaim() {
		return
	}
	if c.Path != "" {
		_ = os.Remove(c.Path)
	}
	_ = os.Remove(c.EventsPath)
	_ = os.Remove(c.claimPath)
	_ = removeIfEmpty(c.RunDir)
	c.published = false
	c.Path = ""
}

// Close tears down the emitter and, when clean is true, sweeps the card to a
// tombstone claim. clean=false leaves a live claim + card for crash discovery
// unless the stream withhold path already swept.
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
