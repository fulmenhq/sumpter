package processrun

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	// errClaimContention is retryable active contention (not a proven live identity).
	// After bounded retries, claimRunSlot may surface ErrCardExists as unresolved contention.
	errClaimContention = errors.New("process-run: claim slot contention")
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

	// Ownership of files created by this generation only (C1 no-clobber).
	// Never unlink an events/card path we did not create.
	ownedEvents bool
	ownedCard   bool

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
		// Open failed: we do not own the events path (including pre-existing collision).
		card.abandonSetup()
		return nil, mapStreamErr(err)
	}
	card.ownedEvents = true
	card.Emitter = emitter

	if err := card.publish(cfg); err != nil {
		// Close may fire OnWithhold → Sweep; abandonSetup only removes owned paths.
		emitter.Close()
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
// Existing slot: read claim identity; live → ErrCardExists; stale → link-CAS
// quarantine then exclusive new claim (only then may clear proven-stale residue).
// Claimless/no-identity: install claim without mutating pre-existing default
// residue — exclusive stream/card open fail-open without deletion.
//
// Error classes: ErrCardExists only for a proven live identity (or exhausted
// active contention). Filesystem/claim/link/write failures return ErrCardSetup
// so the dispatcher can fail-open telemetry without aborting extract.
func claimRunSlot(runDir, claimPath, cardPath, eventsPath string, pid int, startedAt time.Time) (string, error) {
	_ = eventsPath
	const attempts = 12
	sawContention := false
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
				if _, rerr := os.Stat(claimPath); errors.Is(rerr, os.ErrNotExist) {
					_ = removeIfEmpty(runDir)
				}
				if errors.Is(werr, ErrCardExists) {
					sawContention = true
					time.Sleep(time.Duration(2*(i+1)) * time.Millisecond)
					continue
				}
				// Write/fsync/setup failure — fail open, do not mask as live collision.
				return "", ErrCardSetup
			}
			return myToken, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", ErrCardSetup
		}

		// Recover durable mid-takeover crash state before reading ownership.
		// (dual-name quarantine+claim, or quarantine-only after claim.json was removed.)
		_ = recoverOrphanTakeoverState(runDir, claimPath)

		// Slot exists: inspect ownership via claim (preferred) or card fallback.
		owner, oerr := readSlotOwner(claimPath, cardPath)
		if oerr != nil && !errors.Is(oerr, os.ErrNotExist) {
			return "", ErrCardSetup
		}
		if owner != nil {
			if identityLive(owner.PID, owner.StartedAt) {
				return "", ErrCardExists
			}
			tok, terr := staleTakeover(runDir, claimPath, cardPath, owner, myToken, pid, startedAt)
			if terr == nil {
				return tok, nil
			}
			if errors.Is(terr, ErrCardExists) {
				return "", ErrCardExists
			}
			if errors.Is(terr, errClaimContention) {
				sawContention = true
				time.Sleep(time.Duration(2*(i+1)) * time.Millisecond)
				continue
			}
			// Permanent setup failure (link denied, write fail after quarantine, …).
			return "", ErrCardSetup
		}

		// No claim/card identity. Install claim only — do NOT delete pre-existing
		// default events/card residue (no proven-stale ownership). Exclusive Open
		// / Link will fail-open if residue collides.
		if _, cerr := os.Stat(claimPath); errors.Is(cerr, os.ErrNotExist) {
			// Last chance: adopt orphan quarantine-only state into claim.json.
			if recoverOrphanTakeoverState(runDir, claimPath) {
				continue
			}
			if werr := writeClaimExclusive(claimPath, pid, startedAt, myToken, claimStateLive); werr == nil {
				return myToken, nil
			} else if errors.Is(werr, ErrCardExists) {
				sawContention = true
			} else {
				return "", ErrCardSetup
			}
		}
		time.Sleep(time.Duration(5*(i+1)) * time.Millisecond)
	}
	if sawContention {
		// Bounded unresolved active contention — fail-closed identity gate.
		return "", ErrCardExists
	}
	return "", ErrCardSetup
}

// Test hooks for deterministic reclaim interleaving (nil in production).
var (
	// staleAfterObserve runs after the stale claim is re-read and validated, before election.
	staleAfterObserve func()
	// staleAfterLease runs after the reclaim lease is durable, before quarantine/link.
	staleAfterLease func()
	// staleAfterQuarantine runs after successful quarantine link, before unlinking claim.json.
	staleAfterQuarantine func()
	// publishAfterTemp runs after the complete card temp is written, before link into final path.
	publishAfterTemp func(tmpPath, finalPath string)
	// electAfterTemp runs after a complete lease temp is written, before hard-link into claim.taking.
	// Final claim.taking must still be absent (atomic publication).
	electAfterTemp func(tmpPath, finalPath string)
	// cleanDeadAfterObserve runs after observing a complete dead lease (token+inode),
	// before object-bound unlink — used to interleave replacement with live election.
	cleanDeadAfterObserve func(path string, lease *reclaimLease, info os.FileInfo)
	// removeAfterSameInodeCheck runs after the final same-inode authorization to unlink
	// claim.taking, immediately before the pathname Remove. Used to plant a replacement
	// live lease in the late check-to-unlink ABA window.
	removeAfterSameInodeCheck func(path, trash string, observed *reclaimLease, trashInfo os.FileInfo)
	// cleanLockAfterTemp runs after a complete clean-lock temp is written, before
	// hard-link into claim.taking.clean.<token>. Final path must still be absent.
	cleanLockAfterTemp func(tmpPath, finalPath string)
	// cleanLockReapBeforeUnlink runs after a reaper holds the exclusive .reaping
	// marker and has re-validated the dead clean lock, immediately before Remove.
	cleanLockReapBeforeUnlink func(lockPath, markerPath string, observed *reclaimCleanLock, info os.FileInfo)
)

// reclaimLeaseFile is a single per-slot election file (not per-token) so only one
// reclaimer can hold the durable takeover lease at a time.
const reclaimLeaseFile = "claim.taking"

// reclaimLease holds the active reclaimer's identity on disk so another process
// can distinguish an in-flight takeover from a dead orphan.
type reclaimLease struct {
	PID       int
	StartedAt time.Time
	Token     string
	OldToken  string
	Path      string
}

// recoverOrphanTakeoverState repairs durable mid-takeover crash residues only when
// no live reclaimer lease is present. An active reclaimer's claim.taking
// (with live pid,started_at) blocks recovery so B cannot remove/adopt A's marker.
//
// Boundary 1: dual-name same dead inode + dead/no lease → drop quarantine name.
// Boundary 2: quarantine-only dead claim + dead/no lease → restore claim.json.
//
// Returns true when state changed.
func recoverOrphanTakeoverState(runDir, claimPath string) bool {
	// Never touch residues while a live reclaimer holds a lease.
	if hasLiveReclaimLease(runDir) {
		return false
	}
	// Drop dead reclaim leases first so orphan cleanup can proceed.
	_ = cleanDeadReclaimLeases(runDir)

	// Boundary 1: dual-name same inode of a dead claim.
	if claimInfo, err := os.Lstat(claimPath); err == nil {
		doc, derr := readClaimFile(claimPath)
		if derr != nil || identityLive(doc.PID, doc.StartedAt) || !validClaimToken(doc.Token) {
			return false
		}
		staleName := filepath.Join(runDir, "claim.stale."+doc.Token)
		destInfo, lerr := os.Lstat(staleName)
		if lerr != nil || !destInfo.Mode().IsRegular() || !sameFile(claimInfo, destInfo) {
			return false
		}
		_ = os.Remove(staleName)
		return true
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}

	// Boundary 2: claim.json missing — restore a dead quarantine marker.
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "claim.stale.") {
			continue
		}
		token := strings.TrimPrefix(name, "claim.stale.")
		if !validClaimToken(token) {
			continue
		}
		staleName := filepath.Join(runDir, name)
		doc, derr := readClaimFile(staleName)
		if derr != nil || identityLive(doc.PID, doc.StartedAt) {
			continue
		}
		restoreClaimFromQuarantine(claimPath, staleName)
		if _, err := os.Lstat(claimPath); err == nil {
			return true
		}
	}
	return false
}

func reclaimLeasePath(runDir string) string {
	return filepath.Join(runDir, reclaimLeaseFile)
}

func hasLiveReclaimLease(runDir string) bool {
	path := reclaimLeasePath(runDir)
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if !info.Mode().IsRegular() {
		return false
	}
	lease, err := readReclaimLease(path)
	if err != nil {
		// Incomplete/partial/malformed final path: treat as active contention
		// (may still be publishing). Never report "not live" in a way that
		// authorizes deletion — that is handled separately in cleanDead.
		return true
	}
	return identityLive(lease.PID, lease.StartedAt)
}

// cleanDeadReclaimLeases removes a complete, proven-dead lease object only.
// Incomplete/partial/malformed claim.taking is never deleted (may be mid-publish).
// Removal is token+inode bound so a cleaner cannot unlink a later live election
// at the same pathname (pathname ABA).
func cleanDeadReclaimLeases(runDir string) int {
	path := reclaimLeasePath(runDir)
	info, err := os.Lstat(path)
	if err != nil {
		return 0
	}
	if !info.Mode().IsRegular() {
		return 0
	}
	lease, err := readReclaimLease(path)
	if err != nil {
		// Incomplete or corrupt — do NOT delete (active publisher may still hold the inode).
		return 0
	}
	if identityLive(lease.PID, lease.StartedAt) {
		return 0
	}
	if cleanDeadAfterObserve != nil {
		cleanDeadAfterObserve(path, lease, info)
	}
	if removeReclaimLeaseObject(path, lease, info) {
		return 1
	}
	return 0
}

// removeReclaimLeaseObject unlinks claim.taking only when it still names the exact
// proven-dead lease object previously observed (matching token + inode).
func removeReclaimLeaseObject(path string, observed *reclaimLease, observedInfo os.FileInfo) bool {
	return removeReclaimLeaseObjectMode(path, observed, observedInfo, false)
}

// removeReclaimLeaseObjectMode unlinks the observed lease object by token+inode.
// When allowLive is false (cleaners), a live identity aborts. When true (owner
// release), the holder may drop their own still-live lease without allowing a
// different live election at the same pathname to be unlinked.
//
// Single-winner cleanup:
//  1. Exclusive cleaner lock claim.taking.clean.<token> carries the cleaner's
//     (pid, started_at), published via temp+fsync+hard-link no-replace so the
//     final name is never partial. A second cleaner that sees a live lock holder
//     returns immediately and never joins the unlink path.
//  2. Dead clean locks are reaped only under an exclusive claim.taking.clean.<token>.reaping
//     marker (also temp+link, reaper identity). Multi-reaper unique-probe Remove
//     races are not used — only the reaping-marker holder may unlink the dead lock.
//  3. Dead object pin claim.taking.dead.<token> is hard-link no-replace only.
//     Probe collision while holding the clean lock may finish crash residue.
func removeReclaimLeaseObjectMode(path string, observed *reclaimLease, observedInfo os.FileInfo, allowLive bool) bool {
	if observed == nil || !validClaimToken(observed.Token) {
		return false
	}
	lockPath := path + ".clean." + observed.Token
	lock, ok := acquireReclaimCleanLock(lockPath)
	if !ok {
		return false
	}
	defer releaseReclaimCleanLock(lock)

	trash := path + ".dead." + observed.Token
	// Exclusive pin: hard-link no-replace. Do NOT remove an existing probe first.
	if err := os.Link(path, trash); err != nil {
		if errors.Is(err, os.ErrExist) {
			// Only the clean-lock holder may complete residue against this probe.
			return completeReclaimLeaseProbeCollision(path, trash, observed, observedInfo, allowLive)
		}
		return false
	}
	return finishReclaimLeaseUnlink(path, trash, observed, observedInfo, allowLive)
}

type reclaimCleanLock struct {
	Path      string
	PID       int
	StartedAt time.Time
}

// acquireReclaimCleanLock publishes claim.taking.clean.<token> as a complete
// fsynced object (temp + hard-link no-replace) with this process's live identity.
// Live holders block. Dead holders are reaped under an exclusive .reaping marker
// before retrying publication — never via multi-winner unique-probe Removes.
func acquireReclaimCleanLock(lockPath string) (*reclaimCleanLock, bool) {
	pid, started := resolveIdentity(os.Getpid(), time.Now().UTC())
	if pid < 1 {
		return nil, false
	}
	startedStr := started.UTC().Format(time.RFC3339Nano)
	ts, perr := time.Parse(time.RFC3339Nano, startedStr)
	if perr != nil {
		ts = started.UTC()
	}
	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": startedStr,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	raw = append(raw, '\n')

	for attempt := 0; attempt < 6; attempt++ {
		tmp := fmt.Sprintf("%s.%d.%d.tmp", lockPath, pid, time.Now().UnixNano())
		_ = os.Remove(tmp)
		if err := writeFileExclusiveFull(tmp, raw); err != nil {
			return nil, false
		}
		if cleanLockAfterTemp != nil {
			cleanLockAfterTemp(tmp, lockPath)
		}
		lerr := os.Link(tmp, lockPath)
		_ = os.Remove(tmp)
		if lerr == nil {
			_ = os.Chmod(lockPath, 0o600)
			return &reclaimCleanLock{Path: lockPath, PID: pid, StartedAt: ts.UTC()}, true
		}
		if !errors.Is(lerr, os.ErrExist) {
			return nil, false
		}
		holder, _, rerr := readReclaimCleanLock(lockPath)
		if rerr != nil {
			// Unreadable final (legacy partial / corrupt). Clear only under
			// exclusive reaping marker so we do not race a live publisher.
			if !reapUnreadableCleanLock(lockPath) {
				return nil, false
			}
			continue
		}
		if identityLive(holder.PID, holder.StartedAt) {
			return nil, false
		}
		if !reapDeadCleanLock(lockPath, holder) {
			return nil, false
		}
	}
	return nil, false
}

func readReclaimCleanLock(path string) (*reclaimCleanLock, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("invalid reclaim clean lock")
	}
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, nil, err
	}
	var raw struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"started_at"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, nil, err
	}
	if raw.PID < 1 {
		return nil, nil, errors.New("invalid reclaim clean lock")
	}
	ts, err := time.Parse(time.RFC3339Nano, raw.StartedAt)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, raw.StartedAt)
		if err != nil {
			return nil, nil, err
		}
	}
	return &reclaimCleanLock{Path: path, PID: raw.PID, StartedAt: ts.UTC()}, info, nil
}

func cleanIdentityMatch(a, b *reclaimCleanLock) bool {
	if a == nil || b == nil {
		return false
	}
	if a.PID != b.PID {
		return false
	}
	return a.StartedAt.UTC().Format(time.RFC3339Nano) == b.StartedAt.UTC().Format(time.RFC3339Nano)
}

// acquireReapingMarker publishes lockPath+".reaping" with this process's identity
// via temp+link. Live reapers block; abandoned (dead) markers are claimed by
// rename (single-winner) then discarded.
func acquireReapingMarker(markerPath string) (*reclaimCleanLock, bool) {
	pid, started := resolveIdentity(os.Getpid(), time.Now().UTC())
	if pid < 1 {
		return nil, false
	}
	startedStr := started.UTC().Format(time.RFC3339Nano)
	ts, perr := time.Parse(time.RFC3339Nano, startedStr)
	if perr != nil {
		ts = started.UTC()
	}
	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": startedStr,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, false
	}
	raw = append(raw, '\n')

	for attempt := 0; attempt < 4; attempt++ {
		tmp := fmt.Sprintf("%s.%d.%d.tmp", markerPath, pid, time.Now().UnixNano())
		_ = os.Remove(tmp)
		if err := writeFileExclusiveFull(tmp, raw); err != nil {
			return nil, false
		}
		lerr := os.Link(tmp, markerPath)
		_ = os.Remove(tmp)
		if lerr == nil {
			_ = os.Chmod(markerPath, 0o600)
			return &reclaimCleanLock{Path: markerPath, PID: pid, StartedAt: ts.UTC()}, true
		}
		if !errors.Is(lerr, os.ErrExist) {
			return nil, false
		}
		holder, _, rerr := readReclaimCleanLock(markerPath)
		if rerr != nil {
			// Partial marker should not exist with atomic publish; fail closed.
			return nil, false
		}
		if identityLive(holder.PID, holder.StartedAt) {
			return nil, false
		}
		// Abandoned marker: rename is the exclusive claim (second renamer loses).
		trash := fmt.Sprintf("%s.gone.%d.%d", markerPath, os.Getpid(), time.Now().UnixNano())
		if rerr := os.Rename(markerPath, trash); rerr != nil {
			continue
		}
		th, _, err := readReclaimCleanLock(trash)
		if err != nil || identityLive(th.PID, th.StartedAt) || !cleanIdentityMatch(th, holder) {
			// Moved unexpected object — restore if path free.
			if _, e := os.Lstat(markerPath); errors.Is(e, os.ErrNotExist) {
				_ = os.Rename(trash, markerPath)
			} else {
				_ = os.Remove(trash)
			}
			return nil, false
		}
		_ = os.Remove(trash)
	}
	return nil, false
}

func releaseReapingMarker(marker *reclaimCleanLock) {
	releaseReclaimCleanLock(marker)
}

// reapDeadCleanLock removes a proven-dead clean lock under an exclusive .reaping
// marker so only one reaper can unlink the pathname (no multi-winner Remove ABA).
func reapDeadCleanLock(lockPath string, observed *reclaimCleanLock) bool {
	if observed == nil || observed.PID < 1 {
		return false
	}
	markerPath := lockPath + ".reaping"
	marker, ok := acquireReapingMarker(markerPath)
	if !ok {
		return false
	}
	defer releaseReapingMarker(marker)

	cur, info, err := readReclaimCleanLock(lockPath)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	if identityLive(cur.PID, cur.StartedAt) || !cleanIdentityMatch(cur, observed) {
		return false
	}
	if cleanLockReapBeforeUnlink != nil {
		cleanLockReapBeforeUnlink(lockPath, markerPath, observed, info)
	}
	// Re-bind immediately before Remove: a replacement live lock must survive.
	cur2, info2, err2 := readReclaimCleanLock(lockPath)
	if errors.Is(err2, os.ErrNotExist) {
		return true
	}
	if err2 != nil {
		return false
	}
	if identityLive(cur2.PID, cur2.StartedAt) || !cleanIdentityMatch(cur2, observed) {
		return false
	}
	if info != nil && info2 != nil && !sameFile(info, info2) {
		return false
	}
	if rerr := os.Remove(lockPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return false
	}
	return true
}

// reapUnreadableCleanLock clears a non-parseable final lock only under the
// exclusive reaping marker (legacy partial residue / corruption).
func reapUnreadableCleanLock(lockPath string) bool {
	markerPath := lockPath + ".reaping"
	marker, ok := acquireReapingMarker(markerPath)
	if !ok {
		return false
	}
	defer releaseReapingMarker(marker)

	_, _, err := readReclaimCleanLock(lockPath)
	if err == nil {
		// Became readable (complete lock appeared) — do not delete.
		return false
	}
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	// Still unreadable / invalid: remove pathname only while marker held.
	if rerr := os.Remove(lockPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		return false
	}
	return true
}

func releaseReclaimCleanLock(lock *reclaimCleanLock) {
	if lock == nil || lock.Path == "" {
		return
	}
	cur, info, err := readReclaimCleanLock(lock.Path)
	if err != nil || !cleanIdentityMatch(cur, lock) {
		return
	}
	// Owner release of our still-live clean lock (identity + inode bound).
	pathInfo, err := os.Lstat(lock.Path)
	if err != nil || (info != nil && !sameFile(pathInfo, info)) {
		return
	}
	_ = os.Remove(lock.Path)
}

// completeReclaimLeaseProbeCollision handles Link EEXIST on the exclusive dead
// probe while this cleaner already holds the clean lock. Without the clean lock,
// callers never reach here via removeReclaimLeaseObjectMode. A second cleaner
// blocked on a live clean lock never joins this path.
//
// Dual-name same-inode completion is therefore single-winner: only the clean-lock
// holder may finish crash residue left by an abandoned cleaner that linked the
// probe then died before unlinking path.
func completeReclaimLeaseProbeCollision(path, trash string, observed *reclaimLease, observedInfo os.FileInfo, allowLive bool) bool {
	tInfo, terr := os.Lstat(trash)
	if terr != nil {
		return false
	}
	tLease, trerr := readReclaimLease(trash)
	if trerr != nil || tLease.Token != observed.Token {
		return false
	}
	if !allowLive && identityLive(tLease.PID, tLease.StartedAt) {
		return false
	}
	// Probe must still name the object we observed when known.
	if observedInfo != nil && !sameFile(tInfo, observedInfo) {
		// Stale probe for this token name but different inode — reap probe only if
		// path no longer references it (safe residue cleanup).
		if pInfo, perr := os.Lstat(path); perr != nil || !sameFile(pInfo, tInfo) {
			_ = os.Remove(trash)
		}
		return false
	}
	pInfo, perr := os.Lstat(path)
	if errors.Is(perr, os.ErrNotExist) {
		// Path already cleared; reap orphan probe for the observed dead object.
		_ = os.Remove(trash)
		return true
	}
	if perr != nil {
		return false
	}
	if sameFile(pInfo, tInfo) {
		// Dual-name same dead object under our exclusive clean lock: finish unlink.
		return finishReclaimLeaseUnlinkWithProbe(path, trash, tInfo, observed, allowLive)
	}
	// Path names a different object; probe is crash residue of the dead object.
	_ = os.Remove(trash)
	return false
}

func finishReclaimLeaseUnlink(path, trash string, observed *reclaimLease, observedInfo os.FileInfo, allowLive bool) bool {
	trashInfo, err := os.Lstat(trash)
	if err != nil {
		return false
	}
	// Must still be the observed object (same inode when known).
	if observedInfo != nil && !sameFile(trashInfo, observedInfo) {
		_ = os.Remove(trash)
		return false
	}
	return finishReclaimLeaseUnlinkWithProbe(path, trash, trashInfo, observed, allowLive)
}

func finishReclaimLeaseUnlinkWithProbe(path, trash string, trashInfo os.FileInfo, observed *reclaimLease, allowLive bool) bool {
	cur, err := readReclaimLease(trash)
	if err != nil || cur.Token != observed.Token {
		_ = os.Remove(trash)
		return false
	}
	if !allowLive && identityLive(cur.PID, cur.StartedAt) {
		_ = os.Remove(trash)
		return false
	}

	// Authorize pathname unlink only while path still names the pinned object.
	removed := false
	pathInfo, err := os.Lstat(path)
	if err == nil && sameFile(pathInfo, trashInfo) {
		if removeAfterSameInodeCheck != nil {
			removeAfterSameInodeCheck(path, trash, observed, trashInfo)
		}
		// Re-bind immediately before Remove so a late replacement at the pathname
		// survives even if the barrier planted it after authorization.
		pathInfo2, err2 := os.Lstat(path)
		if err2 == nil && sameFile(pathInfo2, trashInfo) {
			if rerr := os.Remove(path); rerr == nil {
				removed = true
			}
		} else if errors.Is(err2, os.ErrNotExist) {
			// Observed object no longer at final name; probe cleanup is enough.
			removed = true
		}
		// else: path now names a different object (e.g. live election) — leave it.
	} else if errors.Is(err, os.ErrNotExist) {
		removed = true
	}
	_ = os.Remove(trash)
	return removed
}

func readReclaimLease(path string) (*reclaimLease, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		return nil, err
	}
	var raw struct {
		PID       int    `json:"pid"`
		StartedAt string `json:"started_at"`
		Token     string `json:"token"`
		OldToken  string `json:"old_token"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if raw.PID < 1 || !validClaimToken(raw.Token) {
		return nil, errors.New("invalid reclaim lease")
	}
	ts, err := time.Parse(time.RFC3339Nano, raw.StartedAt)
	if err != nil {
		ts, err = time.Parse(time.RFC3339, raw.StartedAt)
		if err != nil {
			return nil, err
		}
	}
	return &reclaimLease{
		PID:       raw.PID,
		StartedAt: ts.UTC(),
		Token:     raw.Token,
		OldToken:  strings.TrimSpace(raw.OldToken),
		Path:      path,
	}, nil
}

// electReclaimLease publishes a complete claim.taking via temp+fsync+hard-link
// no-replace so the final pathname never appears partial. Fails with ErrCardExists
// if another live reclaimer already holds a complete lease.
func electReclaimLease(runDir, myToken, oldToken string, pid int, startedAt time.Time) (*reclaimLease, error) {
	if !validClaimToken(myToken) {
		return nil, ErrCardSetup
	}
	if hasLiveReclaimLease(runDir) {
		return nil, ErrCardExists
	}
	_ = cleanDeadReclaimLeases(runDir)
	if hasLiveReclaimLease(runDir) {
		return nil, ErrCardExists
	}

	path := reclaimLeasePath(runDir)
	// Final path present but incomplete/malformed: active publisher or corrupt —
	// do not delete; treat as contention.
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return nil, ErrCardSetup
		}
		lease, rerr := readReclaimLease(path)
		if rerr != nil {
			return nil, errClaimContention
		}
		if identityLive(lease.PID, lease.StartedAt) {
			return nil, ErrCardExists
		}
		// Complete dead lease left behind — object-bound clean, then continue.
		if !removeReclaimLeaseObject(path, lease, info) {
			return nil, errClaimContention
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, ErrCardSetup
	}

	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": startedAt.UTC().Format(time.RFC3339Nano),
		"token":      myToken,
		"old_token":  oldToken,
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		return nil, ErrCardSetup
	}
	raw = append(raw, '\n')

	// Atomic publication: complete temp first, then hard-link into claim.taking
	// (final path appears only when fully written/fsynced).
	tmp := filepath.Join(runDir, "claim.taking."+myToken+".tmp")
	_ = os.Remove(tmp)
	if err := writeFileExclusiveFull(tmp, raw); err != nil {
		return nil, ErrCardSetup
	}
	if electAfterTemp != nil {
		electAfterTemp(tmp, path)
	}
	if err := os.Link(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			if hasLiveReclaimLease(runDir) {
				return nil, ErrCardExists
			}
			return nil, errClaimContention
		}
		return nil, ErrCardSetup
	}
	_ = os.Remove(tmp)
	_ = os.Chmod(path, 0o600)

	// Confirm the published object is ours.
	got, err := readReclaimLease(path)
	if err != nil || got.Token != myToken {
		return nil, ErrCardSetup
	}
	return &reclaimLease{
		PID:       pid,
		StartedAt: startedAt.UTC(),
		Token:     myToken,
		OldToken:  oldToken,
		Path:      path,
	}, nil
}

func releaseReclaimLease(lease *reclaimLease) {
	if lease == nil || lease.Path == "" || !validClaimToken(lease.Token) {
		return
	}
	info, err := os.Lstat(lease.Path)
	if err != nil {
		return
	}
	cur, err := readReclaimLease(lease.Path)
	if err != nil || cur.Token != lease.Token {
		return
	}
	// Owner release: token+inode bound, allowed while still live (unlike cleaners).
	_ = removeReclaimLeaseObjectMode(lease.Path, cur, info, true)
}

// staleTakeover elects a reclaim lease (reclaimer identity), quarantines the proven-
// stale claim, then installs a new exclusive claim.
//
// An active reclaimer's lease is fail-closed to other callers. A dead reclaimer's
// lease is cleaned and residues are recovered. Losers never remove a live winner's
// quarantine/lease.
//
// Returns ErrCardExists for proven live identity or live reclaimer lease,
// errClaimContention for retryable races, and ErrCardSetup for permanent failures.
func staleTakeover(runDir, claimPath, cardPath string, old *claimDoc, myToken string, pid int, startedAt time.Time) (string, error) {
	// If claim.json is missing, try restoring a dead quarantine only when no live reclaimer.
	if _, err := os.Lstat(claimPath); errors.Is(err, os.ErrNotExist) {
		_ = recoverOrphanTakeoverState(runDir, claimPath)
	}
	if !validClaimToken(old.Token) {
		if doc, err := readClaimFile(claimPath); err == nil && validClaimToken(doc.Token) {
			old = doc
		} else {
			return "", ErrCardSetup
		}
	}

	// Observe the live pathname and its object identity.
	info, err := os.Lstat(claimPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errClaimContention
		}
		return "", ErrCardSetup
	}
	cur, err := readClaimFile(claimPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errClaimContention
		}
		return "", ErrCardSetup
	}
	if validClaimToken(cur.Token) {
		old = cur
	} else if cur.Token != old.Token {
		return "", errClaimContention
	}
	if identityLive(cur.PID, cur.StartedAt) {
		return "", ErrCardExists
	}
	if staleAfterObserve != nil {
		staleAfterObserve()
	}

	// Elect durable reclaimer ownership before mutating claim/quarantine.
	lease, lerr := electReclaimLease(runDir, myToken, old.Token, pid, startedAt)
	if lerr != nil {
		// A live reclaim lease means another reclaimer is mid-takeover — retryable
		// contention, not a permanent live process-run identity conflict. Only a
		// live claim.json identity is fail-closed ErrCardExists.
		if errors.Is(lerr, ErrCardExists) {
			if cur2, cerr := readClaimFile(claimPath); cerr == nil && identityLive(cur2.PID, cur2.StartedAt) {
				return "", ErrCardExists
			}
			return "", errClaimContention
		}
		return "", lerr
	}
	// Ensure lease is released on all failure paths; success path clears it too.
	defer func() {
		// Success path removes lease explicitly; defer is no-op if already gone.
		releaseReclaimLease(lease)
	}()
	if staleAfterLease != nil {
		staleAfterLease()
	}

	staleName := filepath.Join(runDir, "claim.stale."+old.Token)
	alreadyQuarantined := false

	// No-replace quarantine: Link fails if dest exists.
	if err := os.Link(claimPath, staleName); err != nil {
		if destInfo, lerr := os.Lstat(staleName); lerr == nil && destInfo.Mode().IsRegular() {
			if dest, derr := readClaimFile(staleName); derr == nil &&
				dest.Token == old.Token &&
				sameFile(info, destInfo) {
				// Same object already linked under our elected lease (restart after Link
				// while we still hold claim.taking). Proceed with uninstall+install.
				if !identityLive(dest.PID, dest.StartedAt) {
					alreadyQuarantined = true
				} else {
					return "", ErrCardExists
				}
			} else {
				return "", ErrCardSetup
			}
		} else {
			return "", ErrCardSetup
		}
	}

	if !alreadyQuarantined {
		q, qerr := readClaimFile(staleName)
		if qerr != nil || q.Token != old.Token {
			_ = os.Remove(staleName)
			return "", errClaimContention
		}
		infoAfter, aerr := os.Lstat(claimPath)
		staleInfo, serr := os.Lstat(staleName)
		if aerr != nil || serr != nil || !sameFile(info, infoAfter) || !sameFile(info, staleInfo) {
			if q2, e2 := readClaimFile(staleName); e2 == nil && q2.Token == old.Token {
				if aerr == nil && !sameFile(info, infoAfter) {
					_ = os.Remove(staleName)
					return "", errClaimContention
				}
			}
			return "", errClaimContention
		}
	}
	if staleAfterQuarantine != nil {
		staleAfterQuarantine()
	}

	// Drop the claim.json name; quarantine hardlink retains the old object.
	if err := os.Remove(claimPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		if _, e := os.Lstat(staleName); e != nil {
			return "", ErrCardSetup
		}
	}

	// Install our live claim exclusively at claim.json.
	if err := writeClaimExclusive(claimPath, pid, startedAt, myToken, claimStateLive); err != nil {
		if errors.Is(err, ErrCardExists) {
			return "", errClaimContention
		}
		// Setup failure: restore claim.json from quarantine for retryability.
		restoreClaimFromQuarantine(claimPath, staleName)
		return "", ErrCardSetup
	}

	// Proven-stale ownership only: clear slot-default residue + quarantine + lease.
	clearSlotOwnedResidue(runDir, cardPath)
	_ = os.Remove(staleName)
	releaseReclaimLease(lease)
	return myToken, nil
}

// restoreClaimFromQuarantine re-links claim.json from a quarantine hardlink when
// the path is still free. Best-effort; leaves quarantine in place if restore fails.
func restoreClaimFromQuarantine(claimPath, staleName string) {
	if _, err := os.Lstat(claimPath); err == nil {
		// claim.json already present — do not overwrite.
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := os.Link(staleName, claimPath); err != nil {
		return
	}
	// Prefer a single claim.json name after successful restore.
	_ = os.Remove(staleName)
	_ = os.Chmod(claimPath, 0o600)
}

// clearSlotOwnedResidue removes the slot card and the default events.ndjson under
// runDir only. It never deletes an arbitrary caller-selected events path outside
// the slot default name.
func clearSlotOwnedResidue(runDir, cardPath string) {
	_ = os.Remove(cardPath)
	_ = os.Remove(filepath.Join(runDir, EventsFileName))
	_ = os.Remove(reclaimLeasePath(runDir))
	if entries, rerr := os.ReadDir(runDir); rerr == nil {
		for _, e := range entries {
			name := e.Name()
			// claim.taking.* covers .tmp, .dead.<token>, .clean.<token> sidecars
			// without removing the final claim.taking election file itself.
			if strings.HasPrefix(name, "claim.stale.") ||
				strings.HasPrefix(name, "claim.taking.") ||
				strings.HasSuffix(name, ".tmp") {
				_ = os.Remove(filepath.Join(runDir, name))
			}
		}
	}
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
		// Pre-token claims: fixed 32-hex sentinel (filename-safe).
		token = "00000000000000000000000000000000"
	}
	if !validClaimToken(token) {
		// Reject arbitrary disk text in token (path traversal / unexpected names).
		return nil, os.ErrNotExist
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

// validClaimToken restricts claim tokens used in filesystem names to hex only.
var claimTokenPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validClaimToken(token string) bool {
	return claimTokenPattern.MatchString(token)
}

// ClaimWriteHook is a test seam that short-circuits writeClaimExclusive.
// Production must leave it nil.
var ClaimWriteHook func(claimPath string) error

func writeClaimExclusive(claimPath string, pid int, startedAt time.Time, token, state string) error {
	if ClaimWriteHook != nil {
		if err := ClaimWriteHook(claimPath); err != nil {
			return err
		}
	}
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
	// If Link is unavailable or fails for non-existence reasons, fail open — do not
	// use rename-over (TOCTOU clobber) as a fallback.
	tmp := filepath.Join(c.RunDir, "card."+c.token+".tmp")
	if err := writeFileExclusiveFull(tmp, raw); err != nil {
		return ErrCardSetup
	}
	if publishAfterTemp != nil {
		publishAfterTemp(tmp, c.Path)
	}
	if err := os.Link(tmp, c.Path); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, os.ErrExist) {
			return ErrCardExists
		}
		return ErrCardSetup
	}
	_ = os.Remove(tmp)
	_ = os.Chmod(c.Path, 0o600)
	c.published = true
	c.ownedCard = true
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
	if c.ownedCard && c.Path != "" {
		_ = os.Remove(c.Path)
		c.ownedCard = false
	}
	// Tombstone: keep (pid, started_at, token) with state=exited for reclaim CAS.
	_ = writeClaimReplaceAtomically(c.claimPath, c.token, c.pid, c.startedAt, c.token, claimStateExited)
	c.published = false
	c.Path = ""
}

// abandonSetup releases a claim we own after a failed setup. Token-checked; only
// unlinks files this generation created (ownedCard / ownedEvents). Never deletes
// a pre-existing events target or an unowned card.json.
func (c *Card) abandonSetup() {
	if c == nil {
		return
	}
	c.sweepMu.Lock()
	defer c.sweepMu.Unlock()
	if !c.stillOwnClaim() {
		// Still drop owned files if claim was already tombstoned by OnWithhold.
		if c.ownedEvents && c.EventsPath != "" {
			_ = os.Remove(c.EventsPath)
			c.ownedEvents = false
		}
		return
	}
	if c.ownedCard && c.Path != "" {
		_ = os.Remove(c.Path)
		c.ownedCard = false
	}
	if c.ownedEvents && c.EventsPath != "" {
		_ = os.Remove(c.EventsPath)
		c.ownedEvents = false
	}
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

// sameFile reports whether two FileInfo values refer to the same underlying object.
// Implemented in samefile_*.go (inode/dev on Unix; basenames elsewhere).

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
