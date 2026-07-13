package processrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// Ensure no leftover test seams from other cases.
	ClaimWriteHook = nil
	staleAfterObserve = nil
	staleAfterQuarantine = nil
	publishAfterTemp = nil
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
	// Final path is absent while temp is complete (injected barrier before link).
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	finalPath := filepath.Join(runtimeDir, "proc", runID, CardFileName)
	var sawCompleteTemp bool
	prev := publishAfterTemp
	t.Cleanup(func() { publishAfterTemp = prev })
	publishAfterTemp = func(tmpPath, final string) {
		if final != finalPath {
			t.Fatalf("final path = %q, want %q", final, finalPath)
		}
		if _, err := os.Lstat(final); !os.IsNotExist(err) {
			t.Fatal("final discovery root must be absent until link completes")
		}
		raw, err := os.ReadFile(tmpPath) // #nosec G304
		if err != nil {
			t.Fatal(err)
		}
		if len(raw) < 20 {
			t.Fatalf("temp incomplete: %q", raw)
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatalf("temp must be complete JSON before link: %v", err)
		}
		sawCompleteTemp = true
	}

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
	if !sawCompleteTemp {
		t.Fatal("publishAfterTemp hook did not run")
	}
	raw, err := os.ReadFile(card.Path) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("final card must be complete JSON: %v", err)
	}
	// Existing card cannot be clobbered by link.
	tmp := filepath.Join(card.RunDir, "card.other.tmp")
	if err := writeFileExclusiveFull(tmp, []byte("{\"evil\":true}\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(tmp, card.Path); err == nil {
		t.Fatal("link over existing card must fail (no-replace)")
	}
	_ = os.Remove(tmp)
}

func TestOpenCardStaleTakeoverABA(t *testing.T) {
	// Deterministic schedule: A quarantines the stale claim (link-CAS), then B
	// attempts takeover while claim.json still names the quarantined object; B
	// must fail (no-replace dest) and must not delete A's eventual new claim.
	if runtime.GOOS == "windows" {
		t.Skip("needs unix liveness")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	claimPath := filepath.Join(runDir, ClaimFileName)
	cardPath := filepath.Join(runDir, CardFileName)
	deadPID := findDeadPID(t)
	oldToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	staleStarted := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := writeClaimExclusive(claimPath, deadPID, staleStarted, oldToken, claimStateExited); err != nil {
		t.Fatal(err)
	}
	old := &claimDoc{PID: deadPID, StartedAt: staleStarted, Token: oldToken, State: claimStateExited}

	aAtQuarantine := make(chan struct{})
	bFinished := make(chan struct{})
	prevQ := staleAfterQuarantine
	t.Cleanup(func() { staleAfterQuarantine = prevQ })

	var aRunning atomic.Bool
	staleAfterQuarantine = func() {
		if aRunning.Load() {
			close(aAtQuarantine)
			<-bFinished
		}
	}

	tokenA, errA := newClaimToken()
	if errA != nil {
		t.Fatal(errA)
	}
	tokenB, errB := newClaimToken()
	if errB != nil {
		t.Fatal(errB)
	}

	var bErr error
	go func() {
		<-aAtQuarantine
		// B observes the same stale token while A holds the quarantine link.
		_, bErr = staleTakeover(runDir, claimPath, cardPath, old, tokenB, os.Getpid(), time.Now().UTC())
		close(bFinished)
	}()

	aRunning.Store(true)
	gotA, aErr := staleTakeover(runDir, claimPath, cardPath, old, tokenA, os.Getpid(), time.Now().UTC())
	aRunning.Store(false)
	if aErr != nil {
		t.Fatalf("A staleTakeover: %v", aErr)
	}
	if gotA != tokenA {
		t.Fatalf("A token = %q", gotA)
	}
	if bErr == nil {
		t.Fatal("B must fail when quarantine dest already exists")
	}
	// A's claim must be live at claim.json.
	cur, err := readClaimFile(claimPath)
	if err != nil {
		t.Fatalf("A claim missing: %v", err)
	}
	if cur.Token != tokenA {
		t.Fatalf("A claim token = %q, want %q (B clobbered ownership)", cur.Token, tokenA)
	}
}

func TestOpenCardExplicitEventsCollisionNoClobber(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	// Pre-existing explicit events path (regular file).
	eventsPath := filepath.Join(t.TempDir(), "prior-events.ndjson")
	prior := []byte("PRIOR-BYTES-MUST-REMAIN\n")
	if err := os.WriteFile(eventsPath, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
		EventsPath: eventsPath,
	})
	if err == nil {
		t.Fatal("expected fail-open on existing events path")
	}
	got, err := os.ReadFile(eventsPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(prior) {
		t.Fatalf("prior events clobbered: got %q", got)
	}
}

func TestOpenCardExplicitEventsSymlinkNoClobber(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	target := filepath.Join(t.TempDir(), "real.ndjson")
	prior := []byte("SYMLINK-TARGET-BYTES\n")
	if err := os.WriteFile(target, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	linkPath := filepath.Join(t.TempDir(), "events-link.ndjson")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Fatal(err)
	}
	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
		EventsPath: linkPath,
	})
	if err == nil {
		t.Fatal("expected fail-open on existing symlink events path")
	}
	got, err := os.ReadFile(target) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(prior) {
		t.Fatalf("symlink target clobbered: got %q", got)
	}
}

func TestOpenCardUnexpectedFinalCardNoClobber(t *testing.T) {
	// Plant card.json after temp is ready but before link — Link must fail and
	// abandonSetup must not delete the unowned final path.
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	finalPath := filepath.Join(runtimeDir, "proc", runID, CardFileName)
	priorCard := []byte("{\"not\":\"ours\"}\n")

	prev := publishAfterTemp
	t.Cleanup(func() { publishAfterTemp = prev })
	publishAfterTemp = func(tmpPath, final string) {
		if err := os.WriteFile(final, priorCard, 0o600); err != nil {
			t.Fatalf("plant card: %v", err)
		}
		_ = tmpPath
	}

	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
	})
	if err == nil {
		t.Fatal("expected publish fail on pre-existing final card")
	}
	if !errors.Is(err, ErrCardExists) {
		t.Fatalf("err = %v, want ErrCardExists", err)
	}
	got, rerr := os.ReadFile(finalPath) // #nosec G304
	if rerr != nil {
		t.Fatalf("unowned card removed: %v", rerr)
	}
	if string(got) != string(priorCard) {
		t.Fatalf("unowned card clobbered: %q", got)
	}
}

func TestOpenCardClaimlessSlotPreservesDefaultEvents(t *testing.T) {
	// Run dir with pre-existing default events and no claim: must not delete residue.
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(runDir, EventsFileName)
	prior := []byte("CLAIMLESS-DEFAULT-EVENTS\n")
	if err := os.WriteFile(eventsPath, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	// Malformed card residue without a claim.
	if err := os.WriteFile(filepath.Join(runDir, CardFileName), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if err == nil {
		t.Fatal("expected fail-open when default events already exist")
	}
	// Setup / stream collision — not a live identity failure.
	if errors.Is(err, ErrCardExists) {
		t.Fatalf("claimless residue must not surface as live collision: %v", err)
	}
	got, rerr := os.ReadFile(eventsPath) // #nosec G304
	if rerr != nil {
		t.Fatalf("default events removed: %v", rerr)
	}
	if string(got) != string(prior) {
		t.Fatalf("default events clobbered: %q", got)
	}
	// Malformed card may remain or be left alone — never replaced with our card on fail.
	if raw, err := os.ReadFile(filepath.Join(runDir, CardFileName)); err == nil {
		if string(raw) != "not-json" && !strings.Contains(string(raw), "capabilities") {
			// if still there as not-json good; if our full card was published that's wrong on fail
			_ = raw
		}
		if strings.Contains(string(raw), `"run_id"`) {
			t.Fatal("must not publish card when stream open failed over pre-existing events")
		}
	}
}

func TestOpenCardClaimWriteSetupFailureNotLiveCollision(t *testing.T) {
	prev := ClaimWriteHook
	t.Cleanup(func() { ClaimWriteHook = prev })
	ClaimWriteHook = func(string) error { return ErrCardSetup }

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c",
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if !errors.Is(err, ErrCardSetup) {
		t.Fatalf("err = %v, want ErrCardSetup", err)
	}
	if errors.Is(err, ErrCardExists) {
		t.Fatal("setup failure must not be ErrCardExists")
	}
}

func TestOpenCardQuarantineLinkSetupFailureNotLiveCollision(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs dead pid + hard links")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	deadPID := findDeadPID(t)
	oldToken := "ffffffffffffffffffffffffffffffff"
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), oldToken, claimStateExited); err != nil {
		t.Fatal(err)
	}
	// Block hard-link quarantine dest with a directory → Link fails as setup.
	if err := os.Mkdir(filepath.Join(runDir, "claim.stale."+oldToken), 0o700); err != nil {
		t.Fatal(err)
	}

	_, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
	})
	if !errors.Is(err, ErrCardSetup) {
		t.Fatalf("err = %v, want ErrCardSetup (not live collision)", err)
	}
	if errors.Is(err, ErrCardExists) {
		t.Fatal("quarantine link failure must not be ErrCardExists")
	}
	// Stale claim must remain (no successful takeover).
	if _, err := os.Stat(filepath.Join(runDir, ClaimFileName)); err != nil {
		t.Fatalf("stale claim removed after setup failure: %v", err)
	}
}

func TestOpenCardStaleReclaimDoesNotDeleteExplicitEvents(t *testing.T) {
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
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), "cccccccccccccccccccccccccccccccc", claimStateExited); err != nil {
		t.Fatal(err)
	}
	// Unrelated allowed events file outside the slot — must not be deleted on reclaim.
	external := filepath.Join(t.TempDir(), "external.ndjson")
	prior := []byte("EXTERNAL-UNRELATED\n")
	if err := os.WriteFile(external, prior, 0o600); err != nil {
		t.Fatal(err)
	}
	card, err := OpenCard(CardConfig{
		RuntimeDir: runtimeDir,
		RunID:      runID,
		PID:        os.Getpid(),
		StartedAt:  time.Now().UTC(),
		Producer:   Producer{Name: "sumpter", Version: "test"},
		EventsPath: external, // will fail exclusive create — and must not delete prior
	})
	if err == nil {
		card.Close(true)
		t.Fatal("expected events collision fail-open")
	}
	got, err := os.ReadFile(external) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(prior) {
		t.Fatalf("external events deleted/clobbered: %q", got)
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
