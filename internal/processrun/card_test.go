package processrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
)

func processRunContractBase(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "tests", "fixtures", "process-run-contract", "v0")
}

func TestResolveRuntimeDirOrder(t *testing.T) {
	t.Setenv(EnvRuntimeDir, "")
	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("TMPDIR", t.TempDir())

	flagDir := filepath.Join(t.TempDir(), "flag-rt")
	got, err := ResolveRuntimeDir(flagDir)
	if err != nil {
		t.Fatalf("ResolveRuntimeDir(flag): %v", err)
	}
	want, _ := filepath.Abs(flagDir)
	if got != filepath.Clean(want) {
		t.Fatalf("flag: got %q want %q", got, want)
	}

	envDir := filepath.Join(t.TempDir(), "env-rt")
	t.Setenv(EnvRuntimeDir, envDir)
	got, err = ResolveRuntimeDir("")
	if err != nil {
		t.Fatalf("ResolveRuntimeDir(env): %v", err)
	}
	want, _ = filepath.Abs(envDir)
	if got != filepath.Clean(want) {
		t.Fatalf("env: got %q want %q", got, want)
	}

	t.Setenv(EnvRuntimeDir, "")
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	got, err = ResolveRuntimeDir("")
	if err != nil {
		t.Fatalf("ResolveRuntimeDir(xdg): %v", err)
	}
	want, _ = filepath.Abs(filepath.Join(xdg, runtimeSubdirXDG))
	if got != filepath.Clean(want) {
		t.Fatalf("xdg: got %q want %q", got, want)
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	got, err = ResolveRuntimeDir("")
	if err != nil {
		t.Fatalf("ResolveRuntimeDir(tmp): %v", err)
	}
	want, _ = filepath.Abs(filepath.Join(tmp, runtimeSubdirTmp))
	if got != filepath.Clean(want) {
		t.Fatalf("tmp: got %q want %q", got, want)
	}
}

func TestValidateRuntimeDirRejectsSumpterHome(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(work, "runtime")
	if err := ValidateRuntimeDir(bad, []string{home, work}); err == nil {
		t.Fatal("expected placement rejection under work root")
	} else if !errors.Is(err, ErrCardPlacement) {
		t.Fatalf("err = %v, want ErrCardPlacement", err)
	}
	ok := filepath.Join(t.TempDir(), "runtime")
	if err := ValidateRuntimeDir(ok, []string{home, work}); err != nil {
		t.Fatalf("allowed path rejected: %v", err)
	}
}

func TestOpenCardTelemetryOnlySchemaAndPerms(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	defer card.Close(true)

	if !card.Emitter.Enabled() {
		t.Fatal("emitter must be enabled")
	}
	assertMode(t, runtimeDir, 0o700)
	assertMode(t, card.RunDir, 0o700)
	assertMode(t, card.Path, 0o600)
	assertMode(t, card.EventsPath, 0o600)
	assertMode(t, filepath.Join(card.RunDir, ClaimFileName), 0o600)

	result, resolved, err := artifactcontract.ValidateProcessCardFile(processRunContractBase(t), card.Path)
	if err != nil {
		t.Fatalf("ValidateProcessCardFile: %v", err)
	}
	if resolved == nil || !result.Valid {
		t.Fatalf("card not schema-valid: %+v", result)
	}

	raw, err := os.ReadFile(card.Path) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["control"]; ok {
		t.Fatal("telemetry-only card must not include control")
	}

	card.Emitter.Started(1)
	card.Emitter.Completed(1, 1)
	eventsPath := card.EventsPath
	runDir := card.RunDir
	card.Close(true)

	if _, err := os.Stat(filepath.Join(runDir, CardFileName)); !os.IsNotExist(err) {
		t.Fatal("card must be swept on clean exit")
	}
	// Claim remains as exited tombstone for later stale reclaim.
	claim, err := readClaimFile(filepath.Join(runDir, ClaimFileName))
	if err != nil {
		t.Fatalf("claim tombstone must remain after clean exit: %v", err)
	}
	if claim.State != claimStateExited {
		t.Fatalf("claim state = %q, want %q", claim.State, claimStateExited)
	}
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("event stream must be retained after clean exit: %v", err)
	}
}

func TestOpenCardAlwaysValidatesWithoutContractBase(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c",
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	defer card.Close(true)
	if _, _, err := artifactcontract.ValidateProcessCardFile(processRunContractBase(t), card.Path); err != nil {
		t.Fatalf("embedded-pin published card must be schema-valid: %v", err)
	}
}

func TestOpenCardLiveCollisionFailClosed(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	first, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("first OpenCard: %v", err)
	}
	defer first.Close(true)

	second, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err == nil {
		second.Close(true)
		t.Fatal("expected live collision ErrCardExists")
	}
	if !errors.Is(err, ErrCardExists) {
		t.Fatalf("err = %v, want ErrCardExists", err)
	}
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("live card clobbered: %v", err)
	}
}

func TestOpenCardConcurrentExclusiveClaim(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	const n = 16
	var (
		wg       sync.WaitGroup
		success  atomic.Int32
		liveHits atomic.Int32
		other    atomic.Int32
		mu       sync.Mutex
		winner   *Card
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			card, err := OpenCard(CardConfig{
				RuntimeDir: runtimeDir,
				RunID:      runID,
				PID:        os.Getpid(),
				StartedAt:  time.Now().UTC(),
				Producer:   Producer{Name: "sumpter", Version: "test"},
			})
			if err == nil {
				success.Add(1)
				mu.Lock()
				if winner == nil {
					winner = card
				} else {
					card.Close(true)
				}
				mu.Unlock()
				return
			}
			if errors.Is(err, ErrCardExists) {
				liveHits.Add(1)
				return
			}
			other.Add(1)
		}()
	}
	wg.Wait()
	if winner != nil {
		defer winner.Close(true)
	}
	if success.Load() != 1 {
		t.Fatalf("want exactly 1 successful OpenCard, got %d (live=%d other=%d)", success.Load(), liveHits.Load(), other.Load())
	}
}

func TestOpenCardConcurrentStaleReclaim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stale reclaim requires Unix liveness")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	deadPID := findDeadPID(t)
	staleStarted := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	staleToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, staleStarted, staleToken, claimStateExited); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, EventsFileName), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	const n = 12
	var (
		wg      sync.WaitGroup
		success atomic.Int32
		mu      sync.Mutex
		winner  *Card
	)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			card, err := OpenCard(CardConfig{
				RuntimeDir: runtimeDir,
				RunID:      runID,
				PID:        os.Getpid(),
				StartedAt:  time.Now().UTC(),
				Producer:   Producer{Name: "sumpter", Version: "test"},
			})
			if err != nil {
				return
			}
			success.Add(1)
			mu.Lock()
			if winner == nil {
				winner = card
			} else {
				card.Close(true)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if winner != nil {
		defer winner.Close(true)
	}
	if success.Load() != 1 {
		t.Fatalf("stale reclaim want exactly 1 winner, got %d", success.Load())
	}
	// Losers must not have deleted the winner's claim.
	if !winner.stillOwnClaim() {
		t.Fatal("winner lost claim ownership after concurrent stale reclaim")
	}
	if _, err := os.Stat(filepath.Join(runDir, "old")); !os.IsNotExist(err) && err == nil {
		t.Fatal("unexpected residue")
	}
	// Prior stream removed by reclaim under winner ownership.
	// Winner has its own events path open.
	if !winner.Emitter.Enabled() {
		t.Fatal("winner emitter must be enabled")
	}
}

func TestOpenCardStaleSweepReclaims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stale reclaim requires Unix liveness")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	deadPID := findDeadPID(t)
	staleStarted := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, staleStarted, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", claimStateExited); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "old.ndjson"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard stale reclaim: %v", err)
	}
	defer card.Close(true)
	if !card.Emitter.Enabled() {
		t.Fatal("reclaimed slot must open a fresh stream")
	}
}

func TestOpenCardPIDReuseMismatchedStartReclaims(t *testing.T) {
	if _, ok := processStartTime(os.Getpid()); !ok {
		t.Skip("process start time unavailable on this platform")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mismatched := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), os.Getpid(), mismatched, "cccccccccccccccccccccccccccccccc", claimStateLive); err != nil {
		t.Fatal(err)
	}
	if identityLive(os.Getpid(), mismatched) {
		t.Fatal("mismatched start time must not count as live identity")
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard PID-reuse reclaim: %v", err)
	}
	defer card.Close(true)
}

func TestOpenCardMatchingPairRefuses(t *testing.T) {
	if _, ok := processStartTime(os.Getpid()); !ok {
		t.Skip("process start time unavailable on this platform")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	pid, started := resolveIdentity(os.Getpid(), time.Now().UTC())
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), pid, started, "dddddddddddddddddddddddddddddddd", claimStateLive); err != nil {
		t.Fatal(err)
	}

	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if !errors.Is(err, ErrCardExists) {
		t.Fatalf("matching live pair must refuse, got %v", err)
	}
}

func TestOpenCardCleanExitTombstoneBlocksWhileAlive(t *testing.T) {
	// After clean Sweep the claim is an exited tombstone with our live identity —
	// a second open in the same still-running process must fail-closed.
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	first, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	eventsPath := first.EventsPath
	first.Emitter.Started(1)
	first.Emitter.Completed(1, 1)
	first.Close(true)

	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("stream retained: %v", err)
	}
	_, err = OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if !errors.Is(err, ErrCardExists) {
		t.Fatalf("live producer after clean sweep must refuse same run_id, got %v", err)
	}
}

func TestOpenCardExitedTombstoneReclaimWhenDead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs dead pid")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	deadPID := findDeadPID(t)
	// Simulate clean-exit tombstone from a dead producer + retained stream.
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, time.Date(2021, 2, 2, 0, 0, 0, 0, time.UTC), "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", claimStateExited); err != nil {
		t.Fatal(err)
	}
	oldStream := filepath.Join(runDir, EventsFileName)
	if err := os.WriteFile(oldStream, []byte("retained\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("reclaim after dead exited tombstone: %v", err)
	}
	defer card.Close(true)
	if !card.Emitter.Enabled() {
		t.Fatal("expected fresh stream after reclaim")
	}
}

func TestOpenCardCrashLeavesCard(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	cardPath := card.Path
	card.Close(false)
	if _, err := os.Stat(cardPath); err != nil {
		t.Fatalf("crash must leave card for discovery: %v", err)
	}
	claim, err := readClaimFile(filepath.Join(card.RunDir, ClaimFileName))
	if err != nil {
		t.Fatal(err)
	}
	if claim.State != claimStateLive {
		t.Fatalf("crash claim state = %q, want live", claim.State)
	}
}

func TestOpenCardBadContractBaseWithholds(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	_, err := OpenCard(CardConfig{
		RuntimeDir:   runtimeDir,
		RunID:        "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c",
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC(),
		Producer:     Producer{Name: "sumpter", Version: "test"},
		ContractBase: filepath.Join(t.TempDir(), "missing-base"),
	})
	if !errors.Is(err, ErrCardSchema) {
		t.Fatalf("err = %v, want ErrCardSchema", err)
	}
	if _, err := os.Stat(CardPath(runtimeDir, "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c")); !os.IsNotExist(err) {
		t.Fatal("partial card must not remain after schema failure")
	}
}

func TestOpenCardPublishAtomicNoPartialFinal(t *testing.T) {
	// Final card path must not exist until publish completes (temp+link strategy).
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	defer card.Close(true)
	// Published card is complete JSON and schema-valid (not empty/partial).
	raw, err := os.ReadFile(card.Path) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 20 {
		t.Fatalf("card too small to be complete: %q", raw)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("final card must be complete JSON: %v", err)
	}
	// Existing card cannot be clobbered by a second exclusive publish attempt.
	tmp := filepath.Join(card.RunDir, "card.other.tmp")
	if err := writeFileExclusiveFull(tmp, []byte("{\"evil\":true}\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(tmp, card.Path); err == nil {
		t.Fatal("link over existing card must fail (no-replace)")
	}
	_ = os.Remove(tmp)
	raw2, _ := os.ReadFile(card.Path) // #nosec G304
	if string(raw2) != string(raw) {
		t.Fatal("existing card was clobbered")
	}
}

func TestOpenCardStreamFailOpenWithdrawsCard(t *testing.T) {
	prev := cardOpenStream
	t.Cleanup(func() { cardOpenStream = prev })

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	var ownedPath string
	cardOpenStream = func(cfg Config) (Emitter, error) {
		ownedPath = cfg.Path
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.Path, []byte("seed\n"), 0o600); err != nil {
			return nil, err
		}
		w := &scriptedWriter{failAt: 1}
		return OpenWithWriter(cfg, w, cfg.Path)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	cardPath := card.Path
	card.Emitter.Started(1)
	if card.Emitter.Enabled() {
		t.Fatal("emitter must be disabled after write failure")
	}
	if _, err := os.Stat(cardPath); !os.IsNotExist(err) {
		t.Fatal("discovery root must be withdrawn when stream fail-open disables")
	}
	if ownedPath != "" {
		if _, err := os.Stat(ownedPath); !os.IsNotExist(err) {
			t.Fatal("partial stream must be withheld")
		}
	}
	// Claim becomes exited tombstone (not deleted).
	claim, err := readClaimFile(filepath.Join(card.RunDir, ClaimFileName))
	if err != nil {
		t.Fatalf("tombstone claim: %v", err)
	}
	if claim.State != claimStateExited {
		t.Fatalf("state = %q, want exited", claim.State)
	}
	card.Close(true)
}

func TestOpenCardHeartbeatFailOpenWithdrawsCard(t *testing.T) {
	prev := cardOpenStream
	t.Cleanup(func() { cardOpenStream = prev })

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	// started ok (1), first heartbeat fails (2) via autonomous ticker path.
	cardOpenStream = func(cfg Config) (Emitter, error) {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.Path, []byte("seed\n"), 0o600); err != nil {
			return nil, err
		}
		cfg.HeartbeatInterval = 20 * time.Millisecond
		w := &scriptedWriter{failAt: 2}
		return OpenWithWriter(cfg, w, cfg.Path)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir:        runtimeDir,
		RunID:             runID,
		PID:               os.Getpid(),
		StartedAt:         time.Now().UTC(),
		Producer:          Producer{Name: "sumpter", Version: "test"},
		HeartbeatInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	cardPath := card.Path
	eventsPath := card.EventsPath
	card.Emitter.Started(1)
	// Wait for autonomous heartbeat to fail (bypasses withdrawing wrapper).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !card.Emitter.Enabled() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if card.Emitter.Enabled() {
		t.Fatal("expected heartbeat fail-open to disable emitter")
	}
	// Allow OnWithhold to run.
	time.Sleep(20 * time.Millisecond)
	if _, err := os.Stat(cardPath); !os.IsNotExist(err) {
		t.Fatal("card must be withdrawn after autonomous heartbeat failure")
	}
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatal("stream must be withheld after heartbeat failure")
	}
	card.Close(false)
}

func TestOpenCardSyncFailOnCrashCloseWithdrawsCard(t *testing.T) {
	prev := cardOpenStream
	t.Cleanup(func() { cardOpenStream = prev })

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	cardOpenStream = func(cfg Config) (Emitter, error) {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.Path, []byte("seed\n"), 0o600); err != nil {
			return nil, err
		}
		w := &scriptedWriter{failSync: true}
		return OpenWithWriter(cfg, w, cfg.Path)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	cardPath := card.Path
	eventsPath := card.EventsPath
	card.Emitter.Started(1)
	card.Emitter.Completed(1, 1)
	// Crash teardown: clean=false, but Sync failure removes stream → must withdraw card.
	card.Close(false)
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatal("sync failure must withhold stream")
	}
	if _, err := os.Stat(cardPath); !os.IsNotExist(err) {
		t.Fatal("card must be withdrawn when stream is removed on crash Close Sync failure")
	}
}

func TestOpenCardMidRunFailOpenWithdrawsCard(t *testing.T) {
	prev := cardOpenStream
	t.Cleanup(func() { cardOpenStream = prev })

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	cardOpenStream = func(cfg Config) (Emitter, error) {
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o700); err != nil {
			return nil, err
		}
		if err := os.WriteFile(cfg.Path, []byte("seed\n"), 0o600); err != nil {
			return nil, err
		}
		w := &scriptedWriter{failAt: 2}
		return OpenWithWriter(cfg, w, cfg.Path)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	cardPath := card.Path
	eventsPath := card.EventsPath
	card.Emitter.Started(2)
	card.Emitter.Progress(1, 2)
	if card.Emitter.Enabled() {
		t.Fatal("progress failure must disable emitter")
	}
	if _, err := os.Stat(cardPath); !os.IsNotExist(err) {
		t.Fatal("card must be withdrawn after mid-run stream disable")
	}
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatal("partial stream must be withheld after mid-run disable")
	}
	card.Close(false)
}

func findDeadPID(t *testing.T) int {
	t.Helper()
	deadPID := 999999
	for pidAlive(deadPID) && deadPID > 900000 {
		deadPID--
	}
	if pidAlive(deadPID) {
		t.Skip("could not find a dead pid")
	}
	return deadPID
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Mode().Perm() != want {
		t.Fatalf("%s mode = %o, want %o", path, info.Mode().Perm(), want)
	}
}
