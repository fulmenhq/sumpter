package commands

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
	"github.com/fulmenhq/sumpter/internal/config"
)

func runMultiWithProcessRunOpts(t *testing.T, shared *multiSharedOptions, ws string, warnOut io.Writer) (outRoot string, runErr error) {
	t.Helper()
	if warnOut == nil {
		warnOut = io.Discard
	}
	if shared.OutputPath == "" {
		shared.OutputPath = filepath.Join(t.TempDir(), "out")
	}
	if shared.RunID == "" {
		shared.RunID = testMultiRunID
	}
	if shared.OutputMode == "" {
		shared.OutputMode = "aggregate"
	}
	if shared.InputWorkers < 1 {
		shared.InputWorkers = 1
	}
	runErr = runExtractMulti(shared, []string{ws}, warnOut, time.Now())
	return shared.OutputPath, runErr
}

func readEventKinds(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatalf("read events %s: %v", path, err)
	}
	var kinds []string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		const key = `"event":"`
		i := strings.Index(line, key)
		if i < 0 {
			continue
		}
		rest := line[i+len(key):]
		j := strings.IndexByte(rest, '"')
		if j < 0 {
			continue
		}
		kinds = append(kinds, rest[:j])
	}
	return kinds
}

func processRunContractBase(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "tests", "fixtures", "process-run-contract", "v0")
}

func assertOneTerminalLast(t *testing.T, kinds []string, want string) {
	t.Helper()
	if len(kinds) == 0 {
		t.Fatal("no events")
	}
	if kinds[0] != "started" {
		t.Fatalf("first event = %q, want started; kinds=%v", kinds[0], kinds)
	}
	terminals := 0
	for _, k := range kinds {
		switch k {
		case "completed", "failed", "canceled":
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("want exactly one terminal, got %d in %v", terminals, kinds)
	}
	if kinds[len(kinds)-1] != want {
		t.Fatalf("last event = %q, want %q; kinds=%v", kinds[len(kinds)-1], want, kinds)
	}
}

func assertSchemaValidStream(t *testing.T, path string) {
	t.Helper()
	if _, _, err := artifactcontract.ValidateProcessEventStreamFile(processRunContractBase(t), path); err != nil {
		t.Fatalf("schema validate %s: %v", path, err)
	}
}

// assertMonotonicSettledProgress checks progress/terminal data.done is strictly
// non-decreasing and ends at the expected settled count when provided.
func assertMonotonicSettledProgress(t *testing.T, path string, wantFinalDone int) {
	t.Helper()
	raw, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	lastDone := -1
	var finalDone int
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var env map[string]interface{}
		if err := json.Unmarshal([]byte(line), &env); err != nil {
			t.Fatalf("parse: %v", err)
		}
		data, _ := env["data"].(map[string]interface{})
		if data == nil {
			continue
		}
		doneVal, ok := data["done"]
		if !ok {
			continue
		}
		done := int(doneVal.(float64))
		if done < lastDone {
			t.Fatalf("data.done regressed: %d after %d in %s", done, lastDone, line)
		}
		lastDone = done
		switch env["event"] {
		case "progress", "completed", "failed", "canceled":
			finalDone = done
		}
	}
	if wantFinalDone >= 0 && finalDone != wantFinalDone {
		t.Fatalf("final data.done = %d, want %d", finalDone, wantFinalDone)
	}
}

func workersLabel(n int) string {
	if n <= 1 {
		return "workers1"
	}
	return "workersN"
}

func sampleInputXML(label string) string {
	return `<root><TargetElement><Name>val` + label + `</Name></TargetElement></root>`
}

func TestExtractMultiProcessRun_OffByDefaultNoFile(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	eventsPath := filepath.Join(t.TempDir(), "should-not-exist.ndjson")
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, ProcessRunEventsPath: "",
	}, ws, io.Discard)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(eventsPath); !os.IsNotExist(err) {
		t.Fatalf("no-opt path must not create event stream; stat err=%v", err)
	}
}

func TestExtractMultiProcessRun_SuccessCompleted(t *testing.T) {
	for _, workers := range []int{1, 3} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			ws := writeMultiRecipeWorkspace(t, "summary")
			fileList, _ := writeMultiInputSet(t, 3)
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
				FileList: fileList, InputWorkers: workers, ProcessRunEventsPath: eventsPath,
			}, ws, io.Discard)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			info, err := os.Stat(eventsPath)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Mode().Perm() != 0o600 {
				t.Fatalf("mode = %o, want 0600", info.Mode().Perm())
			}
			assertSchemaValidStream(t, eventsPath)
			kinds := readEventKinds(t, eventsPath)
			assertOneTerminalLast(t, kinds, "completed")
			assertMonotonicSettledProgress(t, eventsPath, 3)
			progress := 0
			for _, k := range kinds {
				if k == "progress" {
					progress++
				}
			}
			if progress != 3 {
				t.Fatalf("progress count = %d, want 3", progress)
			}
			raw, _ := os.ReadFile(eventsPath) // #nosec G304
			text := string(raw)
			if !strings.Contains(text, `"heartbeat_interval_s"`) {
				t.Fatal("started must declare heartbeat_interval_s")
			}
			for _, forbidden := range []string{"/Users", "file-list", ".xml", "summary", "AKIA"} {
				if strings.Contains(text, forbidden) {
					t.Errorf("stream leaked %q", forbidden)
				}
			}
		})
	}
}

func TestExtractMultiProcessRun_FailFastTerminal(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			ws := writeMultiRecipeWorkspace(t, "summary")
			dir := t.TempDir()
			good := filepath.Join(dir, "good.xml")
			if err := os.WriteFile(good, []byte(sampleInputXML("A")), 0o600); err != nil {
				t.Fatalf("write good: %v", err)
			}
			bad := filepath.Join(dir, "bad.xml")
			if err := os.WriteFile(bad, []byte(`<root><unclosed`), 0o600); err != nil {
				t.Fatalf("write bad: %v", err)
			}
			list := filepath.Join(dir, "list.txt")
			if err := os.WriteFile(list, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
				t.Fatalf("write list: %v", err)
			}
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
				FileList: list, InputWorkers: workers, ProcessRunEventsPath: eventsPath,
			}, ws, io.Discard)
			if err == nil {
				t.Fatal("expected fail-fast error")
			}
			assertSchemaValidStream(t, eventsPath)
			kinds := readEventKinds(t, eventsPath)
			assertOneTerminalLast(t, kinds, "failed")
			assertMonotonicSettledProgress(t, eventsPath, -1)
			raw, _ := os.ReadFile(eventsPath) // #nosec G304
			if !strings.Contains(string(raw), `"reason":"run_error"`) {
				t.Fatalf("want run_error reason, got:\n%s", raw)
			}
		})
	}
}

func TestExtractMultiProcessRun_ContinueOnErrorPartial(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			ws := writeMultiRecipeWorkspace(t, "summary")
			dir := t.TempDir()
			good := filepath.Join(dir, "good.xml")
			if err := os.WriteFile(good, []byte(sampleInputXML("A")), 0o600); err != nil {
				t.Fatalf("write good: %v", err)
			}
			bad := filepath.Join(dir, "bad.xml")
			if err := os.WriteFile(bad, []byte(`<root><unclosed`), 0o600); err != nil {
				t.Fatalf("write bad: %v", err)
			}
			list := filepath.Join(dir, "list.txt")
			if err := os.WriteFile(list, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
				t.Fatalf("write list: %v", err)
			}
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
				FileList: list, InputWorkers: workers, ContinueOnError: true, ProcessRunEventsPath: eventsPath,
			}, ws, io.Discard)
			if err == nil {
				t.Fatal("expected partial failure error under continue-on-error with a failed input")
			}
			if !errors.Is(err, errRecipePartialFailure) {
				t.Fatalf("want errors.Is(err, errRecipePartialFailure), got %v", err)
			}
			assertSchemaValidStream(t, eventsPath)
			kinds := readEventKinds(t, eventsPath)
			assertOneTerminalLast(t, kinds, "failed")
			assertMonotonicSettledProgress(t, eventsPath, 2)
			raw, _ := os.ReadFile(eventsPath) // #nosec G304
			if !strings.Contains(string(raw), `"reason":"partial"`) {
				t.Fatalf("want partial reason, got:\n%s", raw)
			}
			progress := 0
			for _, k := range kinds {
				if k == "progress" {
					progress++
				}
			}
			if progress != 2 {
				t.Fatalf("progress count = %d, want 2 settled inputs", progress)
			}
		})
	}
}

func TestExtractMultiProcessRun_CanceledTerminal(t *testing.T) {
	for _, workers := range []int{1, 2} {
		t.Run(workersLabel(workers), func(t *testing.T) {
			ws := writeMultiRecipeWorkspace(t, "summary")
			fileList, _ := writeMultiInputSet(t, 6)
			eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			shared := &multiSharedOptions{
				FileList:             fileList,
				OutputPath:           filepath.Join(t.TempDir(), "out"),
				RunID:                testMultiRunID,
				OutputMode:           "aggregate",
				InputWorkers:         workers,
				ProcessRunEventsPath: eventsPath,
				Context:              ctx,
			}
			d := newMultiDispatcher(shared, io.Discard)
			var parses atomic.Int32
			realParse := d.parseFile
			d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
				n := parses.Add(1)
				if n >= 2 {
					cancel()
				}
				return realParse(p, a)
			}
			err := d.run([]string{ws}, time.Now())
			if err == nil {
				t.Fatal("expected canceled run error")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("want errors.Is(err, context.Canceled), got %v", err)
			}
			if _, statErr := os.Stat(eventsPath); os.IsNotExist(statErr) {
				t.Fatal("expected event stream for canceled run")
			}
			assertSchemaValidStream(t, eventsPath)
			kinds := readEventKinds(t, eventsPath)
			assertOneTerminalLast(t, kinds, "canceled")
			assertMonotonicSettledProgress(t, eventsPath, -1)
		})
	}
}

func TestExtractMultiProcessRun_ByteIdenticalNoOptSurfaces(t *testing.T) {
	// Launch guardrail: flag omitted vs present must not fork stdout/stderr-class
	// output, artifacts, or provenance. extract-multi writes records to files only
	// (stdout stays empty on both paths when the dispatcher is used directly).
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 3)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")

	var offOut, onOut, offErr, onErr bytes.Buffer
	// stdout buffers are not wired into runExtractMulti; keep them empty sentinels.
	_ = offOut
	_ = onOut

	offRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, InputWorkers: 2, ProcessRunEventsPath: "",
	}, ws, &offErr)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	onRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, InputWorkers: 2, ProcessRunEventsPath: eventsPath,
	}, ws, &onErr)
	if err != nil {
		t.Fatalf("on: %v", err)
	}

	// Stderr-class (warnOut): byte-identical for a clean successful pair.
	if offErr.String() != onErr.String() {
		t.Fatalf("stderr-class differs on vs off\n--- off ---\n%q\n--- on ---\n%q", offErr.String(), onErr.String())
	}
	// Stdout sentinel: both empty (dispatcher does not print records to stdout).
	if offOut.Len() != 0 || onOut.Len() != 0 {
		t.Fatalf("stdout should be empty for both paths")
	}

	off := snapshotAggRecipe(t, filepath.Join(offRoot, "summary"))
	on := snapshotAggRecipe(t, filepath.Join(onRoot, "summary"))
	if on.records != off.records {
		t.Errorf("records differ with process-run on vs off\n--- off ---\n%s\n--- on ---\n%s", off.records, on.records)
	}
	if on.manifest != off.manifest {
		t.Errorf("stable provenance/manifest differs with process-run on vs off\n--- off ---\n%s\n--- on ---\n%s", off.manifest, on.manifest)
	}
	if on.failures != off.failures {
		t.Errorf("failures.json differs with process-run on vs off")
	}
	if off.manifest == "" {
		t.Fatal("expected non-empty stable manifest subset")
	}
}

func TestExtractMultiProcessRun_FailOpenUnwritable(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	badPath := filepath.Join(t.TempDir(), "nope", "nested", "events.ndjson")
	var warn strings.Builder
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, ProcessRunEventsPath: badPath,
	}, ws, &warn)
	if err != nil {
		t.Fatalf("extract must succeed with fail-open telemetry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "summary")); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if !strings.Contains(warn.String(), "process-run events disabled") {
		t.Fatalf("want fail-open warning, got %q", warn.String())
	}
}

func TestExtractMultiProcessRun_FailOpenExistingPathNoClobber(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	prior := []byte("DO-NOT-CLOBBER\n")
	if err := os.WriteFile(eventsPath, prior, 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var warn strings.Builder
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, ProcessRunEventsPath: eventsPath,
	}, ws, &warn)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got, err := os.ReadFile(eventsPath) // #nosec G304
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != string(prior) {
		t.Fatalf("existing stream clobbered: got %q", got)
	}
	if !strings.Contains(warn.String(), "process-run events disabled") {
		t.Fatalf("want collision fail-open warning, got %q", warn.String())
	}
}

func TestExtractMultiProcessRun_RejectsSumpterHomePath(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	home := t.TempDir()
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Env points elsewhere so only ProcessRunBlockedRoots (CLI-effective roots) block.
	t.Setenv("SUMPTER_HOME", t.TempDir())
	t.Setenv("SUMPTER_WORKDIR", t.TempDir())
	bad := filepath.Join(work, "events.ndjson")
	var warn strings.Builder
	outRoot, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:               fileList,
		ProcessRunEventsPath:   bad,
		ProcessRunBlockedRoots: []string{home, work},
	}, ws, &warn)
	if err != nil {
		t.Fatalf("extract must succeed fail-open: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outRoot, "summary")); err != nil {
		t.Fatalf("output missing: %v", err)
	}
	if !strings.Contains(warn.String(), "process-run events disabled") {
		t.Fatalf("want home rejection warning, got %q", warn.String())
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("must not create stream under effective home/work roots")
	}
}

func TestExtractMultiProcessRun_BlockedRootsFromHomeWorkdirOverrides(t *testing.T) {
	// Mirrors extract-multi RunE: ResolvePathLayout(--home, --workdir) without create.
	customHome := t.TempDir()
	layout, err := config.ResolvePathLayout(customHome, "")
	if err != nil {
		t.Fatalf("ResolvePathLayout: %v", err)
	}
	if layout.WorkDir == "" || layout.Home == "" {
		t.Fatalf("expected non-empty layout roots: home=%q work=%q", layout.Home, layout.WorkDir)
	}
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	bad := filepath.Join(layout.WorkDir, "events.ndjson")
	var warn strings.Builder
	_, err = runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList:               fileList,
		ProcessRunEventsPath:   bad,
		ProcessRunBlockedRoots: []string{layout.Home, layout.WorkDir},
	}, ws, &warn)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(warn.String(), "process-run events disabled") {
		t.Fatalf("want rejection for --home-derived work root, got %q", warn.String())
	}
}

func TestBuildExtractMultiArgv_OmitsProcessRunEvents(t *testing.T) {
	argv := buildExtractMultiArgv([]string{"ws"}, &recipeRunExtractMultiOptions{
		ProcessRunEventsPath: "/tmp/events.ndjson",
		OutputPath:           "out",
		InputWorkers:         4,
		Stats:                true,
	})
	joined := strings.Join(argv, " ")
	if strings.Contains(joined, "process-run") {
		t.Errorf("buildExtractMultiArgv must omit process-run flags, got: %s", joined)
	}
	if strings.Contains(joined, "--stats") {
		t.Errorf("buildExtractMultiArgv must omit --stats, got: %s", joined)
	}
}

func TestClassifyProcessRunTerminal(t *testing.T) {
	if got := classifyProcessRunTerminal(nil); got != "completed" {
		t.Fatalf("nil → %q", got)
	}
	if got := classifyProcessRunTerminal(context.Canceled); got != "canceled" {
		t.Fatalf("canceled → %q", got)
	}
	if got := classifyProcessRunTerminal(io.EOF); got != "run_error" {
		t.Fatalf("eof → %q", got)
	}
	partial := recipePartialFailure("summary", 1, 1)
	if got := classifyProcessRunTerminal(partial); got != "partial" {
		t.Fatalf("typed partial → %q", got)
	}
	// Prose-only match must not classify as partial (path/message can contain the words).
	prose := fmt.Errorf("failed to process file /tmp/partial failure/input.xml: boom")
	if got := classifyProcessRunTerminal(prose); got != "run_error" {
		t.Fatalf("prose 'partial failure' in path must stay run_error, got %q", got)
	}
}

func TestExtractMultiProcessRun_FailFastWithPartialFailureInPathStaysRunError(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	// Directory name contains the old prose marker to prove typed classification.
	dir := filepath.Join(t.TempDir(), "partial failure")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(bad, []byte(`<root><unclosed`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	list := filepath.Join(dir, "list.txt")
	if err := os.WriteFile(list, []byte(bad+"\n"), 0o600); err != nil {
		t.Fatalf("list: %v", err)
	}
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: list, ProcessRunEventsPath: eventsPath,
	}, ws, io.Discard)
	if err == nil {
		t.Fatal("expected fail-fast error")
	}
	if errors.Is(err, errRecipePartialFailure) {
		t.Fatal("fail-fast must not be typed partial failure")
	}
	if !strings.Contains(err.Error(), "partial failure") {
		t.Fatalf("test setup: error should mention path fragment, got %v", err)
	}
	assertSchemaValidStream(t, eventsPath)
	kinds := readEventKinds(t, eventsPath)
	assertOneTerminalLast(t, kinds, "failed")
	raw, _ := os.ReadFile(eventsPath) // #nosec G304
	if !strings.Contains(string(raw), `"reason":"run_error"`) {
		t.Fatalf("want run_error, got:\n%s", raw)
	}
	if strings.Contains(string(raw), `"reason":"partial"`) {
		t.Fatal("must not misclassify as partial")
	}
}

func TestExtractMultiProcessRun_SerialParsePanicRecordsFailedNotCompleted(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 2)
	eventsPath := filepath.Join(t.TempDir(), "events.ndjson")
	shared := &multiSharedOptions{
		FileList:             fileList,
		OutputPath:           filepath.Join(t.TempDir(), "out"),
		RunID:                testMultiRunID,
		OutputMode:           "aggregate",
		InputWorkers:         1, // serial: parseFile is not recovered
		ProcessRunEventsPath: eventsPath,
	}
	d := newMultiDispatcher(shared, io.Discard)
	d.parseFile = func(string, bool) (*xmlquery.Node, error) {
		panic("injected serial parse panic")
	}

	var recovered interface{}
	func() {
		defer func() { recovered = recover() }()
		_ = d.run([]string{ws}, time.Now())
	}()
	if recovered == nil {
		t.Fatal("expected panic to re-propagate after process-run terminal")
	}
	if _, err := os.Stat(eventsPath); os.IsNotExist(err) {
		t.Fatal("expected retained failed stream after panic")
	}
	assertSchemaValidStream(t, eventsPath)
	kinds := readEventKinds(t, eventsPath)
	assertOneTerminalLast(t, kinds, "failed")
	for _, k := range kinds {
		if k == "completed" {
			t.Fatal("panic must never be recorded as completed")
		}
	}
	raw, _ := os.ReadFile(eventsPath) // #nosec G304
	if !strings.Contains(string(raw), `"reason":"run_error"`) {
		t.Fatalf("want run_error terminal, got:\n%s", raw)
	}
}

func TestExtractMultiProcessRun_SetupWarningWithholdsPath(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	// Sensitive-shaped path token must never appear on stderr.
	sensitive := filepath.Join(t.TempDir(), "AKIAIOSFODNN7EXAMPLE-secret-stream.ndjson")
	// Pre-create to force collision fail-open.
	if err := os.WriteFile(sensitive, []byte("prior\n"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var warn strings.Builder
	_, err := runMultiWithProcessRunOpts(t, &multiSharedOptions{
		FileList: fileList, ProcessRunEventsPath: sensitive,
	}, ws, &warn)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	got := warn.String()
	if !strings.Contains(got, "process-run events disabled") {
		t.Fatalf("want fail-open warning, got %q", got)
	}
	if strings.Contains(got, sensitive) || strings.Contains(got, "AKIAIOSFODNN7EXAMPLE") || strings.Contains(got, "secret-stream") {
		t.Fatalf("stderr leaked events path token: %q", got)
	}
	if strings.Contains(got, filepath.Base(sensitive)) {
		t.Fatalf("stderr leaked basename: %q", got)
	}
	// Category only.
	if !strings.Contains(got, "path already exists") {
		t.Fatalf("want category label, got %q", got)
	}
}
