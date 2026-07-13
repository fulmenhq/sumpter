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

	// Schema-valid via embedded pin (no ContractBase required).
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
	tel, _ := doc["telemetry"].(map[string]interface{})
	if tel["format"] != "ndjson" {
		t.Fatalf("format = %v", tel["format"])
	}
	if tel["path"] != card.EventsPath {
		t.Fatalf("telemetry.path = %v, want %v", tel["path"], card.EventsPath)
	}

	card.Emitter.Started(1)
	card.Emitter.Completed(1, 1)
	eventsPath := card.EventsPath
	runDir := card.RunDir
	card.Close(true)

	if _, err := os.Stat(filepath.Join(runDir, CardFileName)); !os.IsNotExist(err) {
		t.Fatal("card must be swept on clean exit")
	}
	if _, err := os.Stat(filepath.Join(runDir, ClaimFileName)); !os.IsNotExist(err) {
		t.Fatal("claim must be released on clean exit")
	}
	if _, err := os.Stat(eventsPath); err != nil {
		t.Fatalf("event stream must be retained after clean exit: %v", err)
	}
}

func TestOpenCardAlwaysValidatesWithoutContractBase(t *testing.T) {
	// Production path uses the embedded pin — never publishes unchecked.
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
	if liveHits.Load()+other.Load() != n-1 {
		t.Fatalf("unexpected error tally live=%d other=%d", liveHits.Load(), other.Load())
	}
	// Losers must not leave partial discovery roots besides the winner.
	entries, err := os.ReadDir(filepath.Join(runtimeDir, "proc", runID))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		switch e.Name() {
		case CardFileName, ClaimFileName, EventsFileName:
			// expected
		default:
			t.Fatalf("unexpected entry in run slot: %s", e.Name())
		}
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
	deadPID := 999999
	for pidAlive(deadPID) && deadPID > 900000 {
		deadPID--
	}
	if pidAlive(deadPID) {
		t.Skip("could not find a dead pid for stale test")
	}
	staleStarted := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	writeOwnerFile(t, filepath.Join(runDir, ClaimFileName), deadPID, staleStarted)
	writeOwnerFile(t, filepath.Join(runDir, CardFileName), deadPID, staleStarted)
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

	if _, err := os.Stat(filepath.Join(runDir, "old.ndjson")); !os.IsNotExist(err) {
		t.Fatal("stale sweep must remove prior stream under reclaimed run_id slot")
	}
	if !card.Emitter.Enabled() {
		t.Fatal("reclaimed slot must open a fresh stream")
	}
}

func TestOpenCardPIDReuseMismatchedStartReclaims(t *testing.T) {
	// Live pid with a started_at that cannot match OS process start → reclaimable.
	if _, ok := processStartTime(os.Getpid()); !ok {
		t.Skip("process start time unavailable on this platform")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Same live pid, started_at far in the past (PID-reuse simulation).
	mismatched := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	writeOwnerFile(t, filepath.Join(runDir, ClaimFileName), os.Getpid(), mismatched)
	writeOwnerFile(t, filepath.Join(runDir, CardFileName), os.Getpid(), mismatched)

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
	if !card.Emitter.Enabled() {
		t.Fatal("expected reclaim success")
	}
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
	if !identityLive(pid, started) {
		t.Fatal("current process identity must be live")
	}
	writeOwnerFile(t, filepath.Join(runDir, ClaimFileName), pid, started)
	writeOwnerFile(t, filepath.Join(runDir, CardFileName), pid, started)

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
	// Simulate crash teardown: close emitter without sweeping card.
	card.Close(false)
	if _, err := os.Stat(cardPath); err != nil {
		t.Fatalf("crash must leave card for discovery: %v", err)
	}
	if _, err := os.Stat(card.EventsPath); err != nil {
		t.Fatalf("crash must leave stream: %v", err)
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
	if err == nil {
		t.Fatal("expected schema/setup failure")
	}
	if !errors.Is(err, ErrCardSchema) {
		t.Fatalf("err = %v, want ErrCardSchema", err)
	}
	if _, err := os.Stat(CardPath(runtimeDir, "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c")); !os.IsNotExist(err) {
		t.Fatal("partial card must not remain after schema failure")
	}
}

func TestOpenCardStreamFailOpenWithdrawsCard(t *testing.T) {
	// Inject a writer that fails on the first event write after publish.
	prev := cardOpenStream
	t.Cleanup(func() { cardOpenStream = prev })

	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	var ownedPath string
	cardOpenStream = func(cfg Config) (Emitter, error) {
		ownedPath = cfg.Path
		// Seed the path so remove-on-disable is observable; writer fails first write.
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
	if cardPath == "" {
		t.Fatal("expected published card path before stream failure")
	}
	// Started write fails → emitter disables → card withdrawn.
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
	// Clean close should not revive the card.
	card.Close(true)
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
		// started ok (write 1), progress fails (write 2)
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
	if !card.Emitter.Enabled() {
		t.Fatal("started must succeed")
	}
	if _, err := os.Stat(cardPath); err != nil {
		t.Fatalf("card must remain after successful started: %v", err)
	}
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

func writeOwnerFile(t *testing.T, path string, pid int, started time.Time) {
	t.Helper()
	doc := map[string]interface{}{
		"pid":        pid,
		"started_at": started.UTC().Format(time.RFC3339Nano),
		// Minimal extras so a card-shaped file also works as owner source.
		"capabilities": []string{Capability},
		"run_id":       "seed",
		"producer":     map[string]interface{}{"name": "sumpter", "version": "seed"},
		"telemetry":    map[string]interface{}{"path": filepath.Join(filepath.Dir(path), "events.ndjson"), "format": "ndjson"},
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
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
