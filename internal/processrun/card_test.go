package processrun

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

	// Flag wins over everything.
	flagDir := filepath.Join(t.TempDir(), "flag-rt")
	got, err := ResolveRuntimeDir(flagDir)
	if err != nil {
		t.Fatalf("ResolveRuntimeDir(flag): %v", err)
	}
	want, _ := filepath.Abs(flagDir)
	if got != filepath.Clean(want) {
		t.Fatalf("flag: got %q want %q", got, want)
	}

	// Env wins when flag empty.
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

	// XDG when flag+env empty.
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

	// TMPDIR fallback.
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
		RuntimeDir:   runtimeDir,
		RunID:        runID,
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC(),
		Producer:     Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
		ContractBase: processRunContractBase(t),
	})
	if err != nil {
		t.Fatalf("OpenCard: %v", err)
	}
	defer card.Close(true)

	if !card.Emitter.Enabled() {
		t.Fatal("emitter must be enabled")
	}
	// Runtime + run dir 0700, card + stream 0600.
	assertMode(t, runtimeDir, 0o700)
	assertMode(t, card.RunDir, 0o700)
	assertMode(t, card.Path, 0o600)
	assertMode(t, card.EventsPath, 0o600)

	// Schema-valid card document.
	result, resolved, err := artifactcontract.ValidateProcessCardFile(processRunContractBase(t), card.Path)
	if err != nil {
		t.Fatalf("ValidateProcessCardFile: %v", err)
	}
	if resolved == nil || !result.Valid {
		t.Fatalf("card not schema-valid: %+v", result)
	}

	// No control surface.
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
	card.Close(true)

	// Clean exit: card gone, stream retained.
	if _, err := os.Stat(card.Path); !os.IsNotExist(err) {
		// Path cleared after Sweep — check original path via RunDir.
		if _, err := os.Stat(filepath.Join(card.RunDir, CardFileName)); !os.IsNotExist(err) {
			t.Fatal("card must be swept on clean exit")
		}
	}
	if _, err := os.Stat(card.EventsPath); err != nil {
		t.Fatalf("event stream must be retained after clean exit: %v", err)
	}
}

func TestOpenCardLiveCollisionFailClosed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("live pid check is conservative on non-Unix")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	first, err := OpenCard(CardConfig{
		RuntimeDir:   runtimeDir,
		RunID:        runID,
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC(),
		Producer:     Producer{Name: "sumpter", Version: "test"},
		ContractBase: processRunContractBase(t),
	})
	if err != nil {
		t.Fatalf("first OpenCard: %v", err)
	}
	// Keep first card published (don't sweep) to simulate a live peer.
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
	// First card and stream must remain intact.
	if _, err := os.Stat(first.Path); err != nil {
		t.Fatalf("live card clobbered: %v", err)
	}
	prior, err := os.ReadFile(first.EventsPath) // #nosec G304
	if err != nil {
		t.Fatal(err)
	}
	// Exclusive create means second never wrote; file may be empty still.
	_ = prior
}

func TestOpenCardStaleSweepReclaims(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stale pid sweep requires Unix signal-0 liveness")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Dead pid (unlikely to be alive): 1 is usually init/launchd and IS alive.
	// Use a high pid that is almost certainly dead.
	deadPID := 999999
	for pidAlive(deadPID) && deadPID > 900000 {
		deadPID--
	}
	if pidAlive(deadPID) {
		t.Skip("could not find a dead pid for stale test")
	}
	staleCard := map[string]interface{}{
		"capabilities": []string{Capability},
		"run_id":       runID,
		"pid":          deadPID,
		"started_at":   "2020-01-01T00:00:00Z",
		"producer":     map[string]interface{}{"name": "sumpter", "version": "old"},
		"telemetry":    map[string]interface{}{"path": filepath.Join(runDir, "old.ndjson"), "format": "ndjson"},
	}
	raw, _ := json.Marshal(staleCard)
	if err := os.WriteFile(filepath.Join(runDir, CardFileName), append(raw, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "old.ndjson"), []byte("stale\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	card, err := OpenCard(CardConfig{
		RuntimeDir:   runtimeDir,
		RunID:        runID,
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC(),
		Producer:     Producer{Name: "sumpter", Version: "test"},
		ContractBase: processRunContractBase(t),
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

func TestOpenCardCrashLeavesCard(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	card, err := OpenCard(CardConfig{
		RuntimeDir:   runtimeDir,
		RunID:        runID,
		PID:          os.Getpid(),
		StartedAt:    time.Now().UTC(),
		Producer:     Producer{Name: "sumpter", Version: "test"},
		ContractBase: processRunContractBase(t),
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

func TestOpenCardFailOpenNoPartialOnSchemaBaseMissing(t *testing.T) {
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	// Nonexistent contract base → schema path fails open (no published card).
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
	// No discovery root left behind.
	if _, err := os.Stat(CardPath(runtimeDir, "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c")); !os.IsNotExist(err) {
		t.Fatal("partial card must not remain after schema failure")
	}
}

func TestOpenCardWithoutContractBaseStillPublishes(t *testing.T) {
	// Production path may not have a local pin; structural card still publishes.
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
	// Validate post-hoc with fixture base.
	if _, _, err := artifactcontract.ValidateProcessCardFile(processRunContractBase(t), card.Path); err != nil {
		t.Fatalf("published card should be schema-valid: %v", err)
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
