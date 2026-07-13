package processrun

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
)

func TestNoopWhenPathEmpty(t *testing.T) {
	e, err := Open(Config{Path: "", RunID: "run-1"})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if e.Enabled() {
		t.Fatal("empty path must yield disabled emitter")
	}
	if e.Path() != "" {
		t.Fatalf("Path() = %q, want empty when disabled", e.Path())
	}
	e.Started(1)
	e.Progress(1, 1)
	e.Completed(1, 1)
	e.Close()
}

func TestEmitterLifecycleSchemaAndMonotonicSeq(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	e, err := Open(Config{
		Path:              path,
		RunID:             "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c",
		PID:               4242,
		Producer:          Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
		HeartbeatInterval: time.Hour,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !e.Enabled() {
		t.Fatal("expected enabled emitter")
	}
	if e.Path() != path {
		t.Fatalf("Path() = %q, want %q", e.Path(), path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("stream mode = %o, want 0600", info.Mode().Perm())
	}

	e.Started(3)
	e.Progress(1, 3)
	e.Heartbeat(1)
	e.Progress(2, 3)
	e.Progress(3, 3)
	e.Completed(3, 3)
	e.Failed(3, 3, "run_error") // second terminal no-op
	e.Close()

	if e.Path() != "" {
		t.Fatalf("Path() after close must be empty, got %q", e.Path())
	}

	raw, err := os.ReadFile(path) // #nosec G304 - test path
	if err != nil {
		t.Fatalf("read stream: %v", err)
	}
	base := filepath.Join("..", "..", "tests", "fixtures", "process-run-contract", "v0")
	resolved, err := artifactcontract.ResolveProcessRunBaseline(base)
	if err != nil {
		t.Fatalf("ResolveProcessRunBaseline: %v", err)
	}
	schema, err := artifactcontract.LoadPinnedProcessEventSchema(resolved)
	if err != nil {
		t.Fatalf("LoadPinnedProcessEventSchema: %v", err)
	}
	results, err := artifactcontract.ValidateProcessEventStreamBytes(schema, raw, path)
	if err != nil {
		t.Fatalf("stream schema validation: %v", err)
	}
	if len(results) < 4 {
		t.Fatalf("want ≥4 events, got %d", len(results))
	}

	var seqs []int
	var kinds []string
	sc := bufio.NewScanner(strings.NewReader(string(raw)))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse line: %v", err)
		}
		if strings.Contains(line, dir) || strings.Contains(line, "/Users") {
			t.Fatalf("stream leaked filesystem path: %s", line)
		}
		seqs = append(seqs, int(env["seq"].(float64)))
		kinds = append(kinds, env["event"].(string))
	}
	for i := 1; i < len(seqs); i++ {
		if seqs[i] <= seqs[i-1] {
			t.Fatalf("seq not strictly increasing: %v", seqs)
		}
	}
	if kinds[0] != "started" {
		t.Fatalf("first event = %q, want started", kinds[0])
	}
	if kinds[len(kinds)-1] != "completed" {
		t.Fatalf("last event = %q, want completed", kinds[len(kinds)-1])
	}
	var started map[string]interface{}
	_ = json.Unmarshal([]byte(strings.Split(string(raw), "\n")[0]), &started)
	data, _ := started["data"].(map[string]interface{})
	if data["heartbeat_interval_s"] == nil {
		t.Fatalf("started.data missing heartbeat_interval_s: %#v", data)
	}
}

func TestOpenNoClobberExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.ndjson")
	prior := []byte("PRIOR-STREAM-BYTES\n")
	if err := os.WriteFile(path, prior, 0o600); err != nil {
		t.Fatalf("seed prior: %v", err)
	}
	e, err := Open(Config{Path: path, RunID: "run-1", PID: 1})
	if err == nil {
		t.Fatal("Open should fail-open on existing path")
	}
	if e.Enabled() {
		t.Fatal("collision must return disabled emitter")
	}
	got, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatalf("read prior: %v", err)
	}
	if !bytes.Equal(got, prior) {
		t.Fatalf("existing file was mutated; got %q want %q", got, prior)
	}
}

func TestOpenNoClobberSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.ndjson")
	prior := []byte("SYMLINK-TARGET-BYTES\n")
	if err := os.WriteFile(target, prior, 0o600); err != nil {
		t.Fatalf("seed target: %v", err)
	}
	link := filepath.Join(dir, "events.ndjson")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	e, err := Open(Config{Path: link, RunID: "run-1", PID: 1})
	if err == nil {
		t.Fatal("Open should fail-open when path is an existing symlink")
	}
	if e.Enabled() {
		t.Fatal("symlink collision must return disabled emitter")
	}
	got, err := os.ReadFile(target) // #nosec G304
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if !bytes.Equal(got, prior) {
		t.Fatalf("symlink target was mutated; got %q want %q", got, prior)
	}
}

func TestOpenFailOpenMissingParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "nested", "events.ndjson")
	e, err := Open(Config{Path: path, RunID: "run-1", PID: 1})
	if err == nil {
		t.Fatal("expected open error for missing parent")
	}
	if e.Enabled() {
		t.Fatal("failed open must return disabled emitter")
	}
}

func TestExactlyOneTerminal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "e.ndjson")
	e, err := Open(Config{Path: path, RunID: "r", PID: 1, HeartbeatInterval: time.Hour})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	e.Started(1)
	e.Failed(0, 1, "run_error")
	e.Completed(1, 1)
	e.Canceled(1, 1, "canceled")
	e.Close()
	raw, _ := os.ReadFile(path) // #nosec G304
	count := 0
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Contains(line, `"event":"failed"`) || strings.Contains(line, `"event":"completed"`) || strings.Contains(line, `"event":"canceled"`) {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("want exactly one terminal event, got %d in:\n%s", count, raw)
	}
}

func TestClosedReasonNeverEchoesPaths(t *testing.T) {
	if got := closedReason("/tmp/secret.xml: boom"); got != "run_error" {
		t.Fatalf("closedReason = %q", got)
	}
	if got := closedReason("partial"); got != "partial" {
		t.Fatalf("closedReason partial = %q", got)
	}
	if got := closedReason("canceled"); got != "canceled" {
		t.Fatalf("closedReason canceled = %q", got)
	}
}

type scriptedWriter struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	failAt    int // 0 = never; n = fail on nth Write
	writes    int
	failSync  bool
	failClose bool
	closed    bool
}

func (w *scriptedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	if w.failAt > 0 && w.writes >= w.failAt {
		return 0, errors.New("injected write failure")
	}
	return w.buf.Write(p)
}

func (w *scriptedWriter) Sync() error {
	if w.failSync {
		return errors.New("injected sync failure")
	}
	return nil
}

func (w *scriptedWriter) Close() error {
	w.closed = true
	if w.failClose {
		return errors.New("injected close failure")
	}
	return nil
}

func TestMidRunWriteFailureDisablesAndWithholds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.ndjson")
	// Create exclusive owned file first, then wrap with failing writer via OpenWithWriter
	// by writing through a scripted writer that pretends to own path.
	w := &scriptedWriter{failAt: 2} // started ok, progress fails
	e, err := OpenWithWriter(Config{
		Path: path, RunID: "r", PID: 1, HeartbeatInterval: time.Hour,
	}, w, path)
	if err != nil {
		t.Fatalf("OpenWithWriter: %v", err)
	}
	// Seed an on-disk partial so remove-on-disable is observable.
	if err := os.WriteFile(path, []byte("partial\n"), 0o600); err != nil {
		t.Fatalf("seed partial: %v", err)
	}
	e.Started(2)
	e.Progress(1, 2)
	if e.Enabled() {
		t.Fatal("emitter must disable after write failure")
	}
	if e.Path() != "" {
		t.Fatalf("Path() after disable must be empty, got %q", e.Path())
	}
	e.Completed(2, 2) // no-op
	e.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial owned stream must be withheld/removed after fail-open disable; err=%v", err)
	}
}

func TestFlushFailureOnCloseWithholdsPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flush.ndjson")
	w := &scriptedWriter{failSync: true}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	e, err := OpenWithWriter(Config{Path: path, RunID: "r", PID: 1, HeartbeatInterval: time.Hour}, w, path)
	if err != nil {
		t.Fatalf("OpenWithWriter: %v", err)
	}
	e.Started(1)
	// No terminal — Close flush fails → disable + remove partial.
	e.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("partial stream must be removed after flush fail-open; err=%v", err)
	}
}

func TestTerminalThenSyncFailureWithholdsStream(t *testing.T) {
	// Production teardown: terminal write succeeds, then Close Sync fails.
	path := filepath.Join(t.TempDir(), "term-sync.ndjson")
	if err := os.WriteFile(path, []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := &scriptedWriter{failSync: true}
	e, err := OpenWithWriter(Config{Path: path, RunID: "r", PID: 1, HeartbeatInterval: time.Hour}, w, path)
	if err != nil {
		t.Fatalf("OpenWithWriter: %v", err)
	}
	e.Started(1)
	e.Completed(1, 1)
	e.Close() // production sequence
	if e.Enabled() {
		t.Fatal("must be disabled after close")
	}
	if e.Path() != "" {
		t.Fatalf("Path after close must be empty")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stream must be withheld after terminal+Sync failure; err=%v", err)
	}
}

func TestTerminalThenCloseFailureWithholdsStream(t *testing.T) {
	path := filepath.Join(t.TempDir(), "term-close.ndjson")
	if err := os.WriteFile(path, []byte("seed\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := &scriptedWriter{failClose: true}
	e, err := OpenWithWriter(Config{Path: path, RunID: "r", PID: 1, HeartbeatInterval: time.Hour}, w, path)
	if err != nil {
		t.Fatalf("OpenWithWriter: %v", err)
	}
	e.Started(1)
	e.Failed(0, 1, "run_error")
	e.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stream must be withheld after terminal+Close failure; err=%v", err)
	}
}

func TestShortWriteFailsOpen(t *testing.T) {
	w := &shortWriter{}
	e, err := OpenWithWriter(Config{Path: "x", RunID: "r", PID: 1, HeartbeatInterval: time.Hour}, w, "")
	if err != nil {
		t.Fatalf("OpenWithWriter: %v", err)
	}
	e.Started(1)
	if e.Enabled() {
		t.Fatal("short write must disable emitter")
	}
}

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) / 2, nil }
func (shortWriter) Sync() error                 { return nil }
func (shortWriter) Close() error                { return nil }

func TestValidateEventsPathRejectsSumpterHome(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
	bad := filepath.Join(work, "events.ndjson")
	if err := ValidateEventsPath(bad, []string{home, work}); err == nil {
		t.Fatal("expected rejection under blocked roots")
	}
	// Env fallback when no explicit roots.
	t.Setenv("SUMPTER_HOME", home)
	t.Setenv("SUMPTER_WORKDIR", work)
	if err := ValidateEventsPath(bad, nil); err == nil {
		t.Fatal("expected env-based rejection")
	}
	ok := filepath.Join(t.TempDir(), "events.ndjson")
	if err := ValidateEventsPath(ok, []string{home, work}); err != nil {
		t.Fatalf("temp path should be allowed: %v", err)
	}
}

func TestValidateEventsPathHonorsExplicitOverrideRoots(t *testing.T) {
	// CLI --home/--workdir resolve to custom roots that are not in env.
	customHome := t.TempDir()
	customWork := filepath.Join(customHome, "work")
	if err := os.MkdirAll(customWork, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Env points elsewhere so only explicit roots should block.
	t.Setenv("SUMPTER_HOME", t.TempDir())
	t.Setenv("SUMPTER_WORKDIR", t.TempDir())
	bad := filepath.Join(customWork, "events.ndjson")
	if err := ValidateEventsPath(bad, []string{customHome, customWork}); err == nil {
		t.Fatal("expected rejection under explicit CLI override roots")
	}
	// Same path with env-only roots (different) should be allowed.
	if err := ValidateEventsPath(bad, nil); err != nil {
		t.Fatalf("path outside env roots should be allowed without explicit roots: %v", err)
	}
}

// Ensure streamWriter is satisfied by *os.File.
var _ streamWriter = (*os.File)(nil)
var _ io.Writer = (*scriptedWriter)(nil)
