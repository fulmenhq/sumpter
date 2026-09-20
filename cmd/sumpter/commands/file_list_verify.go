package commands

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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

// inputSnapshotReadyHook is a test-only seam invoked after the immutable copy
// and digest are complete but before callers parse the snapshot. Production
// leaves it nil; tests use it to prove source-path mutation cannot alter the
// row bytes bound to the emitted digest.
var inputSnapshotReadyHook func(sourcePath, snapshotPath string)

// inputSnapshotRemove is a test-only filesystem seam for snapshot cleanup.
// Production uses os.Remove.
var inputSnapshotRemove = os.Remove

type privateInputError struct {
	err          error
	display      string
	privatePaths []string
}

func (e *privateInputError) Error() string {
	return sanitizePrivateInputText(e.err.Error(), e.display, e.privatePaths...)
}

func (e *privateInputError) Unwrap() error { return e.err }

func sanitizePrivateInputError(err error, display string, privatePaths ...string) error {
	if err == nil {
		return nil
	}
	return &privateInputError{err: err, display: display, privatePaths: privatePaths}
}

func sanitizePrivateInputText(text, display string, privatePaths ...string) string {
	for _, path := range privatePaths {
		if path == "" || path == display {
			continue
		}
		text = strings.ReplaceAll(text, path, display)
	}
	return text
}

func removeInputSnapshot(snap *declaredInputSnapshot, logical string) error {
	if snap == nil {
		return nil
	}
	privatePath := snap.Path
	if err := snap.Remove(); err != nil {
		return sanitizePrivateInputError(fmt.Errorf("remove input snapshot: %w", err), logical, privatePath)
	}
	return nil
}

func removeInputSnapshotPath(path string) error {
	if path == "" {
		return nil
	}
	if err := inputSnapshotRemove(path); err != nil && !os.IsNotExist(err) {
		return sanitizePrivateInputError(fmt.Errorf("remove input snapshot: %w", err), "private input snapshot", path)
	}
	return nil
}

func inputSnapshotOperationError(operation string, err error, privatePaths ...string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, sanitizePrivateInputError(err, "private input", privatePaths...))
}

// Remove deletes the snapshot. It is idempotent and safe on a nil receiver;
// callers that create identity snapshots must treat cleanup failure as terminal.
func (s *declaredInputSnapshot) Remove() error {
	if s == nil || s.Path == "" {
		return nil
	}
	if err := inputSnapshotRemove(s.Path); err != nil && !os.IsNotExist(err) {
		return err
	}
	s.Path = ""
	return nil
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
// a stat-size pre-check (fast fail before copying), then one SHA-256 pass while
// writing the snapshot — the same digest the manifest ledger records. On success the
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
	return snapshotAndIdentifyInput(src, decl)
}

// snapshotAndIdentifyInput creates one immutable byte image, hashes it while it
// is copied, and returns the identity the parser and ledger must share. A
// declaration, when present, is verified against that same image. The optional
// row-identity path calls this with nil so URI-only inputs are also bound before
// row emission instead of being hashed after a mutable-path parse.
func snapshotAndIdentifyInput(src string, decl *fileListDeclaration) (*declaredInputSnapshot, error) {
	info, err := os.Stat(src)
	if err != nil {
		if decl != nil {
			return nil, fmt.Errorf("stat declared input: %w", sanitizePrivateInputError(err, "input", src))
		}
		return nil, fmt.Errorf("stat input: %w", sanitizePrivateInputError(err, "input", src))
	}
	if info.IsDir() {
		return nil, fmt.Errorf("input is a directory")
	}
	if decl != nil && info.Size() != decl.Size {
		return nil, fmt.Errorf("declared size %d does not match actual size %d", decl.Size, info.Size())
	}

	srcFile, err := os.Open(src) // #nosec G304 - operator-provided input path
	if err != nil {
		return nil, fmt.Errorf("open input: %w", sanitizePrivateInputError(err, "input", src))
	}

	prefix := "sumpter-identity-*"
	if decl != nil {
		prefix = "sumpter-declared-*"
	}
	snapFile, err := os.CreateTemp("", prefix+filepath.Ext(src))
	if err != nil {
		return nil, errors.Join(
			fmt.Errorf("create input snapshot: %w", err),
			inputSnapshotOperationError("close input", srcFile.Close(), src),
		)
	}
	snapPath := snapFile.Name()
	hasher := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(snapFile, hasher), srcFile)
	snapshotCloseErr := snapFile.Close()
	sourceCloseErr := srcFile.Close()
	if copyErr != nil {
		return nil, errors.Join(
			inputSnapshotOperationError("snapshot input", copyErr, src, snapPath),
			inputSnapshotOperationError("close input snapshot", snapshotCloseErr, snapPath),
			inputSnapshotOperationError("close input", sourceCloseErr, src),
			removeInputSnapshotPath(snapPath),
		)
	}
	if snapshotCloseErr != nil || sourceCloseErr != nil {
		return nil, errors.Join(
			inputSnapshotOperationError("close input snapshot", snapshotCloseErr, snapPath),
			inputSnapshotOperationError("close input", sourceCloseErr, src),
			removeInputSnapshotPath(snapPath),
		)
	}
	if decl != nil && written != decl.Size {
		return nil, errors.Join(
			fmt.Errorf("declared size %d does not match snapshot size %d", decl.Size, written),
			removeInputSnapshotPath(snapPath),
		)
	}

	sum := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if decl != nil && sum != decl.SHA256 {
		return nil, errors.Join(
			fmt.Errorf("declared sha256 %s does not match actual %s", decl.SHA256, sum),
			removeInputSnapshotPath(snapPath),
		)
	}
	if inputSnapshotReadyHook != nil {
		inputSnapshotReadyHook(src, snapPath)
	}
	return &declaredInputSnapshot{Path: snapPath, SHA256: sum, Size: written}, nil
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
