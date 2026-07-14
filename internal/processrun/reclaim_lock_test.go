package processrun

import (
	"bufio"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReclaimLockSameProcessContention(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("advisory lock unsupported")
	}
	dir := t.TempDir()
	a, err := tryAcquireReclaimLock(dir)
	if err != nil {
		t.Fatalf("A acquire: %v", err)
	}
	defer a.Release()

	b, err := tryAcquireReclaimLock(dir)
	if !errors.Is(err, errReclaimLockBusy) {
		if b != nil {
			b.Release()
		}
		t.Fatalf("B acquire err=%v, want busy", err)
	}

	a.Release()
	b, err = tryAcquireReclaimLock(dir)
	if err != nil {
		t.Fatalf("B acquire after A release: %v", err)
	}
	b.Release()
}

func TestReclaimLockStableInodeUnderResidueCleanup(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" && runtime.GOOS != "windows" {
		t.Skip("advisory lock unsupported")
	}
	dir := t.TempDir()
	a, err := tryAcquireReclaimLock(dir)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	defer a.Release()

	infoBefore, err := os.Lstat(a.Path())
	if err != nil {
		t.Fatal(err)
	}
	// Residue cleanup must not remove/recreate reclaim.lock while held.
	clearSlotOwnedResidue(dir, filepath.Join(dir, CardFileName))
	infoAfter, err := os.Lstat(a.Path())
	if err != nil {
		t.Fatalf("reclaim.lock removed by residue cleanup: %v", err)
	}
	if !sameFile(infoBefore, infoAfter) {
		t.Fatal("reclaim.lock inode changed under cleanup — breaks flock exclusion")
	}
	// Peer still excluded.
	if _, err := tryAcquireReclaimLock(dir); !errors.Is(err, errReclaimLockBusy) {
		t.Fatalf("peer acquire after cleanup err=%v, want busy", err)
	}
}

func TestReclaimLockCrashReleaseCrossProcess(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unix flock crash-release test")
	}
	if os.Getenv("RECLAIM_LOCK_CHILD") == "1" {
		dir := os.Getenv("RECLAIM_LOCK_DIR")
		l, err := tryAcquireReclaimLock(dir)
		if err != nil {
			os.Exit(2)
		}
		_ = l // hold until killed; do not Release
		_, _ = os.Stdout.WriteString("READY\n")
		_ = os.Stdout.Sync()
		select {} // park
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestReclaimLockCrashReleaseCrossProcess$")
	cmd.Env = append(os.Environ(),
		"RECLAIM_LOCK_CHILD=1",
		"RECLAIM_LOCK_DIR="+dir,
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Read READY
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || line != "READY\n" {
		_ = cmd.Process.Kill()
		t.Fatalf("child ready: line=%q err=%v", line, err)
	}
	// Kill without unlock — kernel must release flock.
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = cmd.Process.Wait()

	// Parent acquires successfully after child death.
	deadline := time.Now().Add(2 * time.Second)
	var last error
	for time.Now().Before(deadline) {
		l, err := tryAcquireReclaimLock(dir)
		if err == nil {
			l.Release()
			return
		}
		last = err
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("parent could not acquire after child kill: %v", last)
}

func TestReclaimLockProductionTakeoverOneWinner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs unix liveness")
	}
	runtimeDir := filepath.Join(t.TempDir(), "rt")
	runID := "018f3c2a-7b4e-7c1d-9a2b-0d5e6f7a8b9c"
	runDir := RunDir(runtimeDir, runID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	deadPID := findDeadPID(t)
	staleToken := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := writeClaimExclusive(filepath.Join(runDir, ClaimFileName), deadPID, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), staleToken, claimStateExited); err != nil {
		t.Fatal(err)
	}

	const n = 8
	var (
		wg      sync.WaitGroup
		success atomic.Int32
		mu      sync.Mutex
		winner  *Card
	)
	pid, started := resolveIdentity(os.Getpid(), time.Now().UTC())
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			card, err := OpenCard(CardConfig{
				RuntimeDir: runtimeDir,
				RunID:      runID,
				PID:        pid,
				StartedAt:  started,
				Producer:   Producer{Name: "sumpter", Version: "test", Profile: ProducerProfile},
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
		t.Fatalf("want exactly 1 winner under reclaim.lock, got %d", success.Load())
	}
	// reclaim.lock must still exist (stable inode).
	if _, err := os.Lstat(reclaimLockPath(runDir)); err != nil {
		t.Fatalf("reclaim.lock missing after takeover: %v", err)
	}
}
