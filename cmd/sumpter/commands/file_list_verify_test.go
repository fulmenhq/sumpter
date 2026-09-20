package commands

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// TestSnapshotAndVerifyDeclaredInput pins the declared-input snapshot gate: the
// declaration is verified against a private copy of the source bytes, the
// snapshot is the byte image the caller parses, and the copy is removed when the
// caller is done.
func TestSnapshotAndVerifyDeclaredInput(t *testing.T) {
	dir := t.TempDir()
	content := []byte("<root><item><Name>A</Name></item></root>")
	path := filepath.Join(dir, "input.xml")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	size := int64(len(content))
	decl := &fileListDeclaration{URI: path, Size: size, SHA256: sum}

	t.Run("match returns a removable snapshot of the verified bytes", func(t *testing.T) {
		snap, err := snapshotAndVerifyDeclaredInput(path, decl)
		if err != nil {
			t.Fatalf("snapshotAndVerifyDeclaredInput: %v", err)
		}
		if snap.SHA256 != sum || snap.Size != size {
			t.Fatalf("identity = (%s, %d), want (%s, %d)", snap.SHA256, snap.Size, sum, size)
		}
		got, rerr := os.ReadFile(snap.Path) // #nosec G304 - test-controlled snapshot path
		if rerr != nil {
			t.Fatalf("read snapshot: %v", rerr)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("snapshot bytes differ from the source bytes")
		}
		snapshotPath := snap.Path
		if err := snap.Remove(); err != nil {
			t.Fatalf("remove snapshot: %v", err)
		}
		if _, statErr := os.Stat(snapshotPath); !os.IsNotExist(statErr) {
			t.Errorf("snapshot still present after Remove: %v", statErr)
		}
		if err := snap.Remove(); err != nil { // idempotent
			t.Fatalf("remove snapshot again: %v", err)
		}
	})

	t.Run("source mutation after snapshot cannot affect the verified bytes", func(t *testing.T) {
		snap, err := snapshotAndVerifyDeclaredInput(path, decl)
		if err != nil {
			t.Fatalf("snapshotAndVerifyDeclaredInput: %v", err)
		}
		t.Cleanup(func() {
			if err := snap.Remove(); err != nil {
				t.Errorf("remove snapshot: %v", err)
			}
		})

		// Concurrent same-size mutation of the source between verify and parse:
		// the parser reads the snapshot, so the mutation cannot reach the run.
		mutated := bytes.Repeat([]byte("X"), len(content))
		if werr := os.WriteFile(path, mutated, 0o600); werr != nil {
			t.Fatalf("mutate source: %v", werr)
		}
		gotSum, gotSize, herr := provenance.HashLocalInput(snap.Path)
		if herr != nil {
			t.Fatalf("hash snapshot: %v", herr)
		}
		if gotSum != sum || gotSize != size {
			t.Fatalf("snapshot identity changed after source mutation: (%s, %d), want (%s, %d)", gotSum, gotSize, sum, size)
		}
		got, rerr := os.ReadFile(snap.Path) // #nosec G304 - test-controlled snapshot path
		if rerr != nil {
			t.Fatalf("read snapshot: %v", rerr)
		}
		if !bytes.Equal(got, content) {
			t.Fatalf("snapshot bytes changed after source mutation")
		}
	})

	t.Run("size mismatch fails before copying", func(t *testing.T) {
		_, err := snapshotAndVerifyDeclaredInput(path, &fileListDeclaration{URI: path, Size: size + 1, SHA256: sum})
		if err == nil || !strings.Contains(err.Error(), "declared size") {
			t.Fatalf("err = %v, want declared-size mismatch", err)
		}
	})

	t.Run("digest mismatch fails and leaves no snapshot", func(t *testing.T) {
		before, gerr := filepath.Glob(filepath.Join(os.TempDir(), "sumpter-declared-*"))
		if gerr != nil {
			t.Fatalf("glob snapshots before mismatch: %v", gerr)
		}
		wrong := "sha256:" + strings.Repeat("b", 64)
		_, err := snapshotAndVerifyDeclaredInput(path, &fileListDeclaration{URI: path, Size: size, SHA256: wrong})
		if err == nil || !strings.Contains(err.Error(), "declared sha256") {
			t.Fatalf("err = %v, want declared-digest mismatch", err)
		}
		after, rerr := filepath.Glob(filepath.Join(os.TempDir(), "sumpter-declared-*"))
		if rerr != nil {
			t.Fatalf("glob snapshots after mismatch: %v", rerr)
		}
		if len(after) != len(before) {
			t.Errorf("snapshot count changed after mismatch: before=%d after=%d", len(before), len(after))
		}
	})

	t.Run("digest mismatch reports snapshot cleanup failure without leaking path", func(t *testing.T) {
		previousRemove := inputSnapshotRemove
		var leftover string
		t.Cleanup(func() {
			inputSnapshotRemove = previousRemove
			if leftover != "" {
				_ = os.Remove(leftover)
			}
		})
		inputSnapshotRemove = func(path string) error {
			leftover = path
			return errors.New("injected snapshot cleanup failure")
		}
		wrong := "sha256:" + strings.Repeat("c", 64)
		_, err := snapshotAndVerifyDeclaredInput(path, &fileListDeclaration{URI: path, Size: size, SHA256: wrong})
		if err == nil || !strings.Contains(err.Error(), "declared sha256") || !strings.Contains(err.Error(), "injected snapshot cleanup failure") {
			t.Fatalf("combined mismatch/cleanup error = %v", err)
		}
		if leftover == "" {
			t.Fatal("cleanup seam did not observe snapshot path")
		}
		if strings.Contains(err.Error(), leftover) || strings.Contains(err.Error(), "sumpter-declared-") {
			t.Fatalf("private snapshot path leaked in error: %v", err)
		}
	})

	t.Run("missing file fails", func(t *testing.T) {
		_, err := snapshotAndVerifyDeclaredInput(filepath.Join(dir, "absent.xml"), &fileListDeclaration{URI: "absent.xml", Size: 1, SHA256: sum})
		if err == nil || !strings.Contains(err.Error(), "stat declared input") {
			t.Fatalf("err = %v, want stat failure", err)
		}
	})

	t.Run("directory fails", func(t *testing.T) {
		_, err := snapshotAndVerifyDeclaredInput(dir, &fileListDeclaration{URI: dir, Size: 0, SHA256: sum})
		if err == nil || !strings.Contains(err.Error(), "directory") {
			t.Fatalf("err = %v, want directory rejection", err)
		}
	})
}

func TestSnapshotAndIdentifyURIOnlyInputBindsParsedBytes(t *testing.T) {
	dir := t.TempDir()
	original := []byte("<root><item><Name>original</Name></item></root>")
	path := filepath.Join(dir, "input.xml")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := snapshotAndIdentifyInput(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := snap.Remove(); err != nil {
			t.Errorf("remove snapshot: %v", err)
		}
	})
	wantSum := fmt.Sprintf("sha256:%x", sha256.Sum256(original))
	if snap.SHA256 != wantSum || snap.Size != int64(len(original)) {
		t.Fatalf("snapshot identity = (%s,%d), want (%s,%d)", snap.SHA256, snap.Size, wantSum, len(original))
	}
	mutated := bytes.Repeat([]byte("X"), len(original))
	if err := os.WriteFile(path, mutated, 0o600); err != nil {
		t.Fatal(err)
	}
	parsedBytes, err := os.ReadFile(snap.Path) // #nosec G304 - test-owned immutable snapshot
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(parsedBytes, original) {
		t.Fatal("URI-only identity snapshot followed source mutation")
	}
}
