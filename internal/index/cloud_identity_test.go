package index

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifySourceIntegrity covers the cloud read-boundary integrity guard used
// by parallel/record-index extraction: the staged local copy of a (mutable)
// cloud source must match the size and SHA-256 recorded in the index header, or
// the recorded byte offsets can no longer be trusted.
func TestVerifySourceIntegrity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "source.xml")
	payload := []byte(`<root><item><name>A</name></item></root>`)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	sum := sha256.Sum256(payload)
	good := SourceInfo{
		Path:      "s3://bucket/source.xml",
		SizeBytes: int64(len(payload)),
		SHA256:    hex.EncodeToString(sum[:]),
	}

	t.Run("match", func(t *testing.T) {
		if err := VerifySourceIntegrity(path, good); err != nil {
			t.Errorf("VerifySourceIntegrity(match) = %v, want nil", err)
		}
	})

	t.Run("size mismatch", func(t *testing.T) {
		bad := good
		bad.SizeBytes = good.SizeBytes + 1
		err := VerifySourceIntegrity(path, bad)
		if err == nil || !strings.Contains(err.Error(), "size mismatch") {
			t.Errorf("VerifySourceIntegrity(size mismatch) = %v, want size mismatch error", err)
		}
	})

	t.Run("hash mismatch", func(t *testing.T) {
		bad := good
		bad.SHA256 = strings.Repeat("0", 64)
		err := VerifySourceIntegrity(path, bad)
		if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Errorf("VerifySourceIntegrity(hash mismatch) = %v, want SHA-256 mismatch error", err)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if err := VerifySourceIntegrity(filepath.Join(dir, "absent.xml"), good); err == nil {
			t.Error("VerifySourceIntegrity(missing) = nil, want stat error")
		}
	})
}

// TestBuilderRecordsSourceIdentity proves the index header records the logical
// source identity (SourceIdentity) while byte content is read from InputPath, so
// a staged cloud copy's local path never lands in the index. With SourceIdentity
// empty the header falls back to InputPath (unchanged local behavior).
func TestBuilderRecordsSourceIdentity(t *testing.T) {
	dir := t.TempDir()
	xmlPath := filepath.Join(dir, "staged.xml")
	if err := os.WriteFile(xmlPath, []byte(`<root><item><name>A</name></item><item><name>B</name></item></root>`), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	t.Run("records logical identity", func(t *testing.T) {
		logicalURI := "s3://bucket/prefix/staged.xml"
		idx, err := NewBuilder(BuildOptions{
			InputPath:      xmlPath,
			SourceIdentity: logicalURI,
			Selector:       "//item",
		}).Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if idx.Source.Path != logicalURI {
			t.Errorf("source.path = %q, want logical URI %q", idx.Source.Path, logicalURI)
		}
		if strings.Contains(idx.Source.Path, dir) {
			t.Errorf("source.path leaked the local staging dir: %q", idx.Source.Path)
		}
		if idx.Summary.TotalRecords != 2 {
			t.Errorf("total_records = %d, want 2 (built from the staged bytes)", idx.Summary.TotalRecords)
		}
	})

	t.Run("falls back to InputPath", func(t *testing.T) {
		idx, err := NewBuilder(BuildOptions{
			InputPath: xmlPath,
			Selector:  "//item",
		}).Build()
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if idx.Source.Path != xmlPath {
			t.Errorf("source.path = %q, want InputPath %q", idx.Source.Path, xmlPath)
		}
	})
}
