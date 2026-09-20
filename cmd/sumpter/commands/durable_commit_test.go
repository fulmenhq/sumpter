package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

func runDurableProtocol(t *testing.T, fail map[string]error, empty, optOut bool) (string, []string, error) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "nested", "recipe")
	steps, err := runDurableProtocolAt(t, out, fail, empty, optOut)
	return out, steps, err
}

func runDurableProtocolAt(t *testing.T, out string, fail map[string]error, empty, optOut bool) ([]string, error) {
	t.Helper()
	steps := make([]string, 0, 32)
	occurrences := make(map[string]int)
	ops := osDurableCommitOps()
	ops.step = func(step string) error {
		steps = append(steps, step)
		occurrences[step]++
		occurrenceKey := fmt.Sprintf("%s#%d", step, occurrences[step])
		if err := fail[occurrenceKey]; err != nil {
			delete(fail, occurrenceKey) // fail once so cleanup can make a best effort
			return err
		}
		if err := fail[step]; err != nil {
			delete(fail, step) // fail once so cleanup can make a best effort
			return err
		}
		return nil
	}
	opts := &ExtractOptions{
		OutputPath:       out,
		OutputMode:       outputModeAggregate,
		NoDurableCommit:  optOut,
		durableCommitOps: &ops,
	}
	w := newAggregateWriter(opts, false)
	w.setCurrentInput(1)
	if !empty {
		w.beginInput()
		record := extract.NewEmittedRecord(map[string]interface{}{
			"_runtime": map[string]interface{}{"record_type": "item"},
			"extract":  map[string]interface{}{"data": map[string]interface{}{"id": "1"}},
		})
		if err := w.OnRecord(context.Background(), record); err != nil {
			return steps, errors.Join(err, w.abort())
		}
		if err := w.commitInput(); err != nil {
			return steps, errors.Join(err, w.abort())
		}
	}
	if err := w.commit(1); err != nil {
		return steps, errors.Join(err, w.abort())
	}
	if err := opts.aggregateCommit.writeSidecar(filepath.Join(out, "failures.json"), []byte("{}\n")); err != nil {
		return steps, errors.Join(err, w.abort())
	}
	if err := opts.aggregateCommit.writeSidecar(filepath.Join(out, "dispositions.json"), []byte("{}\n")); err != nil {
		return steps, errors.Join(err, w.abort())
	}
	manifest := provenance.Manifest{
		RunID:              testMultiRunID,
		SumpterVersion:     "test",
		StartedAt:          time.Unix(1, 0).UTC(),
		CompletedAt:        time.Unix(2, 0).UTC(),
		CLI:                provenance.CLI{Command: "test"},
		Inputs:             []provenance.Input{},
		Outputs:            []provenance.Output{},
		CountsByRecordType: map[string]int{},
	}
	if err := writeProvenanceManifest(opts, filepath.Join(out, provenance.ManifestFileName), manifest); err != nil {
		return steps, errors.Join(err, w.abort())
	}
	return steps, nil
}

func TestDurableAggregateCommitSequence(t *testing.T) {
	out, steps, err := runDurableProtocol(t, map[string]error{}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{
		"shard-write", "shard-sync", "shard-close", "shard-rename", "shards-dir-sync",
		"sidecar-write", "sidecar-flush", "sidecar-sync", "sidecar-close", "sidecar-rename",
		"manifest-temp-create", "manifest-write", "manifest-flush", "manifest-sync", "manifest-close", "ownership-remove", "manifest-rename", "manifest-dir-sync",
	}
	last := -1
	for _, want := range wantOrder {
		idx := slices.Index(steps, want)
		if idx < 0 {
			t.Fatalf("missing step %q in %v", want, steps)
		}
		if idx <= last {
			t.Fatalf("step %q at %d is not after prior step at %d: %v", want, idx, last, steps)
		}
		last = idx
	}
	sidecarSyncs := 0
	lastSidecarSync := -1
	for i, step := range steps {
		if step == "sidecar-sync" {
			sidecarSyncs++
			lastSidecarSync = i
		}
	}
	if sidecarSyncs != 2 {
		t.Fatalf("sidecar sync count = %d, want failures + dispositions", sidecarSyncs)
	}
	if manifestTemp := slices.Index(steps, "manifest-temp-create"); lastSidecarSync >= manifestTemp {
		t.Fatalf("last sidecar sync at %d is not before manifest temp at %d: %v", lastSidecarSync, manifestTemp, steps)
	}
	for _, name := range []string{"records.jsonl", "failures.json", "dispositions.json", provenance.ManifestFileName} {
		info, statErr := os.Stat(filepath.Join(out, name))
		if statErr != nil {
			t.Fatalf("%s missing: %v", name, statErr)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s permissions = %o, want owner-only", name, info.Mode().Perm())
		}
	}
	if _, err := os.Stat(filepath.Join(out, aggregateCommitLockName)); !os.IsNotExist(err) {
		t.Fatalf("ownership marker remains after commit: %v", err)
	}
}

func TestDurableAggregateCommitFailureMatrix(t *testing.T) {
	steps := []string{
		"ancestor-dir-sync-open", "ancestor-dir-sync", "ancestor-dir-sync-close", "directory-create", "parent-dir-sync-open", "parent-dir-sync", "parent-dir-sync-close", "output-dir-sync-open", "output-dir-sync", "output-dir-sync-close", "ownership-create", "ownership-sync", "ownership-close", "ownership-dir-sync-open", "ownership-dir-sync", "ownership-dir-sync-close",
		"shard-open", "shard-write", "shard-sync", "shard-close", "shard-rename", "shards-dir-sync",
		"sidecar-temp-create", "sidecar-write", "sidecar-flush", "sidecar-sync", "sidecar-close", "sidecar-rename",
		"ownership-remove", "manifest-temp-create", "manifest-write", "manifest-flush", "manifest-sync", "manifest-close", "manifest-rename", "manifest-dir-sync-open", "manifest-dir-sync", "manifest-dir-sync-close",
		"sidecar-temp-create#2", "sidecar-write#2", "sidecar-flush#2", "sidecar-sync#2", "sidecar-close#2", "sidecar-rename#2",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			out, _, err := runDurableProtocol(t, map[string]error{step: errors.New("injected " + step)}, false, false)
			if err == nil || !strings.Contains(err.Error(), "injected "+step) {
				t.Fatalf("error = %v, want injected %s", err, step)
			}
			if _, statErr := os.Stat(filepath.Join(out, provenance.ManifestFileName)); !os.IsNotExist(statErr) {
				t.Fatalf("failure %s left a reader commit marker: %v", step, statErr)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatalf("failure %s left owned output ancestry: %v", step, statErr)
			}
			if _, retryErr := runDurableProtocolAt(t, out, map[string]error{}, false, false); retryErr != nil {
				t.Fatalf("retry after %s: %v", step, retryErr)
			}
		})
	}
}

func TestDurableAggregateCommitEmptyOutputAndOptOut(t *testing.T) {
	out, steps, err := runDurableProtocol(t, map[string]error{}, true, false)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(out, "records.jsonl"))
	if err != nil || info.Size() != 0 {
		t.Fatalf("empty aggregate shard = info:%v err:%v", info, err)
	}
	if !slices.Contains(steps, "shard-sync") || !slices.Contains(steps, "manifest-sync") {
		t.Fatalf("empty durable commit skipped sync boundaries: %v", steps)
	}

	_, optSteps, err := runDurableProtocol(t, map[string]error{}, false, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range optSteps {
		if strings.HasSuffix(step, "-sync") {
			t.Fatalf("--no-durable-commit unexpectedly executed sync step %q", step)
		}
	}
}

func TestDurableAggregateCommitReportsCleanupFailure(t *testing.T) {
	_, _, err := runDurableProtocol(t, map[string]error{
		"shard-sync":     errors.New("injected shard-sync"),
		"cleanup-remove": errors.New("injected cleanup-remove"),
	}, false, false)
	if err == nil || !strings.Contains(err.Error(), "injected shard-sync") || !strings.Contains(err.Error(), "injected cleanup-remove") {
		t.Fatalf("combined failure = %v", err)
	}
}

func TestDurableAggregateCommitPostMarkerSyncIsUnconfirmed(t *testing.T) {
	out, _, err := runDurableProtocol(t, map[string]error{"manifest-dir-sync": errors.New("injected final directory sync")}, false, false)
	if err == nil || !strings.Contains(err.Error(), "unconfirmed after manifest rename") {
		t.Fatalf("post-marker sync error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, provenance.ManifestFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("unconfirmed marker was not invalidated: %v", statErr)
	}
}

func TestDurableAggregateCommitReportsCleanupCloseFailure(t *testing.T) {
	_, _, err := runDurableProtocol(t, map[string]error{
		"shard-write":   errors.New("injected shard-write"),
		"cleanup-close": errors.New("injected cleanup-close"),
	}, false, false)
	if err == nil || !strings.Contains(err.Error(), "injected shard-write") || !strings.Contains(err.Error(), "injected cleanup-close") {
		t.Fatalf("combined close failure = %v", err)
	}
}

func TestDurableAggregateCommitRejectsStaleAndConcurrentDestinations(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(out, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, provenance.ManifestFileName), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	opts := &ExtractOptions{OutputPath: out, OutputMode: outputModeAggregate}
	if err := newLocalAggregateCommit(opts).prepare(); err == nil || !strings.Contains(err.Error(), "prior or interrupted") {
		t.Fatalf("stale manifest prepare error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(out, provenance.ManifestFileName)); err != nil || string(got) != "old" {
		t.Fatalf("stale manifest was changed: %q err=%v", got, err)
	}

	concurrentOut := filepath.Join(t.TempDir(), "out")
	one := newLocalAggregateCommit(&ExtractOptions{OutputPath: concurrentOut, OutputMode: outputModeAggregate})
	if err := one.prepare(); err != nil {
		t.Fatal(err)
	}
	two := newLocalAggregateCommit(&ExtractOptions{OutputPath: concurrentOut, OutputMode: outputModeAggregate})
	if err := two.prepare(); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("concurrent prepare error = %v", err)
	}
	if err := one.abort(); err != nil {
		t.Fatal(err)
	}

	unrelatedOut := filepath.Join(t.TempDir(), "out")
	if err := os.MkdirAll(unrelatedOut, 0o750); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(unrelatedOut, "keep.txt")
	if err := os.WriteFile(keep, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	owner := newLocalAggregateCommit(&ExtractOptions{OutputPath: unrelatedOut, OutputMode: outputModeAggregate})
	if err := owner.prepare(); err != nil {
		t.Fatalf("unrelated artifact blocked ownership: %v", err)
	}
	if err := owner.abort(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(keep); err != nil || string(got) != "unrelated" {
		t.Fatalf("unrelated artifact changed during abort: %q err=%v", got, err)
	}
}

func BenchmarkLocalAggregateCommit(b *testing.B) {
	for _, tc := range []struct {
		name   string
		optOut bool
	}{
		{name: "durable-default"},
		{name: "explicit-opt-out", optOut: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			root := b.TempDir()
			payload := []byte("{\"record\":1}\n")
			sidecar := []byte("{}\n")
			for i := 0; i < b.N; i++ {
				out := filepath.Join(root, fmt.Sprintf("run-%06d", i))
				opts := &ExtractOptions{OutputPath: out, OutputMode: outputModeAggregate, NoDurableCommit: tc.optOut}
				commit := newLocalAggregateCommit(opts)
				final := filepath.Join(out, "records.jsonl")
				f, stage, err := commit.openShard(final)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := f.Write(payload); err != nil {
					b.Fatal(err)
				}
				if err := commit.finalizeShard(f); err != nil {
					b.Fatal(err)
				}
				if err := commit.commitShards([]string{stage}, []string{final}); err != nil {
					b.Fatal(err)
				}
				if err := commit.writeSidecar(filepath.Join(out, "failures.json"), sidecar); err != nil {
					b.Fatal(err)
				}
				if err := commit.writeSidecar(filepath.Join(out, "dispositions.json"), sidecar); err != nil {
					b.Fatal(err)
				}
				if err := commit.writeManifest(filepath.Join(out, provenance.ManifestFileName), sidecar); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
