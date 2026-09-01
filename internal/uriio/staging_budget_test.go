package uriio_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/uriio"
)

func TestStagingBudgetOversizeFailsBeforeAdmit(t *testing.T) {
	b := uriio.NewStagingBudget(uriio.StagingBudgetConfig{MaxBytes: 1000, MaxFiles: 4, ObjectMax: 100})
	err := b.Admit(context.Background(), 101)
	if err == nil || !strings.Contains(err.Error(), "per-object cap") {
		t.Fatalf("object-max: err=%v", err)
	}
	err = b.Admit(context.Background(), 1001)
	if err == nil || !strings.Contains(err.Error(), "per-object cap") {
		t.Fatalf("object-max also binds objects above the global cap: err=%v", err)
	}
	wide := uriio.NewStagingBudget(uriio.StagingBudgetConfig{MaxBytes: 1000, MaxFiles: 4, ObjectMax: 10000})
	err = wide.Admit(context.Background(), 1001)
	if err == nil || !strings.Contains(err.Error(), "staging budget") {
		t.Fatalf("global cap: err=%v", err)
	}
	if st := b.Stats(); st.PeakFiles != 0 || st.AcquiredCount != 0 {
		t.Fatalf("oversize must not reserve: %+v", st)
	}
}

func TestStagingBudgetBackpressureAndRelease(t *testing.T) {
	b := uriio.NewStagingBudget(uriio.StagingBudgetConfig{MaxBytes: 100, MaxFiles: 1, ObjectMax: 100})
	if err := b.Admit(context.Background(), 60); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := b.Admit(ctx, 60); err == nil {
		t.Fatal("expected wait/cancel while slot full")
	}

	done := make(chan error, 1)
	go func() {
		done <- b.Admit(context.Background(), 40)
	}()
	time.Sleep(20 * time.Millisecond)
	b.Release(60)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("admit did not proceed after release")
	}
	st := b.Stats()
	if st.PeakFiles != 1 || st.PeakBytes != 60 || st.AcquiredCount != 2 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStagedBytesExceedAdmit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "obj")
	if err := os.WriteFile(p, []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := uriio.StagedBytesExceedAdmit(p, 10); err != nil {
		t.Fatal(err)
	}
	if err := uriio.StagedBytesExceedAdmit(p, 9); err == nil {
		t.Fatal("want exceed error")
	}
	// Delta would breach a 10-byte global budget if 1 extra byte landed.
	if err := os.WriteFile(p, []byte("0123456789X"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := uriio.StagedBytesExceedAdmit(p, 10)
	if err == nil || !strings.Contains(err.Error(), "exceeds admitted") {
		t.Fatalf("err=%v", err)
	}
}

func TestStagingGetSizeMismatch(t *testing.T) {
	if err := uriio.StagingGetSizeMismatch(100, 100); err != nil {
		t.Fatal(err)
	}
	if err := uriio.StagingGetSizeMismatch(100, 80); err != nil {
		t.Fatal(err)
	}
	err := uriio.StagingGetSizeMismatch(100, 101)
	if err == nil || !strings.Contains(err.Error(), "size changed") {
		t.Fatalf("err=%v", err)
	}
}

func TestStagingBudgetCleanupFailureBlocksAdmit(t *testing.T) {
	b := uriio.NewStagingBudget(uriio.StagingBudgetConfig{MaxBytes: 1000, MaxFiles: 4, ObjectMax: 1000})
	if err := b.Admit(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	b.NoteCleanupFailure()
	err := b.Admit(context.Background(), 10)
	if err == nil || !strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("err=%v", err)
	}
	st := b.Stats()
	if st.CleanupFailures != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestStagingBudgetCancelUnblocks(t *testing.T) {
	b := uriio.NewStagingBudget(uriio.StagingBudgetConfig{MaxBytes: 10, MaxFiles: 1, ObjectMax: 10})
	if err := b.Admit(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- b.Admit(ctx, 1) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("want ctx error")
		}
	case <-time.After(time.Second):
		t.Fatal("admit did not unblock on cancel")
	}
}
