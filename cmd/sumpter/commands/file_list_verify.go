package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
)

// inputIdentity is a verified declared-input identity captured before parse so
// the manifest ledger and the recipe applications reuse the same single hash
// (declared inputs are hashed once; the verify hash IS the ledger hash).
type inputIdentity struct {
	sha256 string
	size   int64
}

// declaredInputSnapshot is a private, owner-only copy of a declared input's
// stored bytes. Verification hashes the snapshot and parsing reads the same
// snapshot path, so a concurrent mutation of the source cannot separate the
// bytes that were verified from the bytes that get parsed. Callers must Remove
// the snapshot on every path once parsing is done.
type declaredInputSnapshot struct {
	Path   string
	SHA256 string
	Size   int64
}

// Remove deletes the snapshot. It is idempotent and safe on a nil receiver.
func (s *declaredInputSnapshot) Remove() {
	if s == nil || s.Path == "" {
		return
	}
	_ = os.Remove(s.Path)
	s.Path = ""
}

// declarationAt returns the declaration for an input ordinal (1-based), or nil
// when the input is URI-only (or the list carried no declarations). Declarations
// ride the ordinal position; they are never looked up by path, so duplicate
// references cannot cross-apply an expected digest.
func declarationAt(decls []*fileListDeclaration, ordinal int) *fileListDeclaration {
	if ordinal < 1 || ordinal > len(decls) {
		return nil
	}
	return decls[ordinal-1]
}

// snapshotAndVerifyDeclaredInput copies the declared input's stored bytes into a
// private owner-only snapshot and verifies the declaration against that copy:
// a stat-size pre-check (fast fail before copying), then one HashLocalInput over
// the snapshot — the same digest the manifest ledger records. On success the
// returned snapshot is the byte image that was verified; callers parse the
// snapshot path and Remove it afterwards, so verification and parsing cannot be
// separated by a concurrent same-size mutation of the source.
//
// The snapshot keeps the source's extension so transparent decompression and the
// large-file routing decide exactly as they would have for the source path. On
// any mismatch the snapshot is removed and a loud error is returned carrying no
// staging-path content; callers attribute it to the input's ordinal and logical
// URI. The failed input contributes no records; its provenance entry follows the
// existing failed-input contract for the calling path.
func snapshotAndVerifyDeclaredInput(src string, decl *fileListDeclaration) (*declaredInputSnapshot, error) {
	if decl == nil {
		return nil, fmt.Errorf("declared input has no declaration")
	}
	info, err := os.Stat(src)
	if err != nil {
		return nil, fmt.Errorf("stat declared input: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("declared input is a directory")
	}
	if info.Size() != decl.Size {
		return nil, fmt.Errorf("declared size %d does not match actual size %d", decl.Size, info.Size())
	}

	srcFile, err := os.Open(src) // #nosec G304 - declared input path from the operator-provided file list
	if err != nil {
		return nil, fmt.Errorf("open declared input: %w", err)
	}
	defer func() { _ = srcFile.Close() }()

	snapFile, err := os.CreateTemp("", "sumpter-declared-*"+filepath.Ext(src))
	if err != nil {
		return nil, fmt.Errorf("create declared-input snapshot: %w", err)
	}
	snapPath := snapFile.Name()
	written, copyErr := io.Copy(snapFile, srcFile)
	closeErr := snapFile.Close()
	if copyErr != nil {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("snapshot declared input: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("snapshot declared input: %w", closeErr)
	}
	if written != decl.Size {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("declared size %d does not match snapshot size %d", decl.Size, written)
	}

	sum, size, herr := provenance.HashLocalInput(snapPath)
	if herr != nil {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("hash declared input: %w", herr)
	}
	if size != decl.Size {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("declared size %d does not match actual size %d", decl.Size, size)
	}
	if sum != decl.SHA256 {
		_ = os.Remove(snapPath)
		return nil, fmt.Errorf("declared sha256 %s does not match actual %s", decl.SHA256, sum)
	}
	return &declaredInputSnapshot{Path: snapPath, SHA256: sum, Size: size}, nil
}

// ledgerInputFor builds the provenance input ledger for a processed input,
// reusing a precomputed declared-input identity when present: the declaration
// verify hash is the ledger hash, so a declared input is hashed once. Undeclared
// inputs keep the historical BuildInputLedger behavior (hash now). The recorded
// path is always the logical URI/path — never a staging path.
func ledgerInputFor(opts *ExtractOptions, result extract.ExtractResult, ident *inputIdentity, roots ...string) (provenance.Input, error) {
	if ident != nil {
		return provenance.BuildInputLedgerHashed(result.LogicalURI, ident.sha256, ident.size, resolvedInputHandle(opts), roots...)
	}
	return provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), roots...)
}
