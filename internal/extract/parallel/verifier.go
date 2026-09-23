package parallel

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"go.uber.org/zap"
)

// SafetyVerifier performs pre-extraction safety checks
type SafetyVerifier struct {
	idx         *index.RecordIndex
	sourcePath  string
	sourceLabel string
	indexPath   string
	logger      *logging.ComponentLogger
}

// NewSafetyVerifier creates a new safety verifier.
//
// Deprecated: Use NewSafetyVerifierFromHeader for streaming mode.
func NewSafetyVerifier(idx *index.RecordIndex, sourcePath string, indexPath string, diagnosticLabel ...string) *SafetyVerifier {
	return &SafetyVerifier{
		idx:         idx,
		sourcePath:  sourcePath,
		sourceLabel: diagnosticLabelValue(sourcePath, diagnosticLabel...),
		indexPath:   indexPath,
		logger:      logging.Component("parallel-verifier"),
	}
}

// NewSafetyVerifierFromHeader creates a safety verifier from header data.
// This is used in streaming mode where the full index is not loaded.
func NewSafetyVerifierFromHeader(header *index.RecordIndex, sourcePath string, indexPath string, diagnosticLabel ...string) *SafetyVerifier {
	return &SafetyVerifier{
		idx:         header,
		sourcePath:  sourcePath,
		sourceLabel: diagnosticLabelValue(sourcePath, diagnosticLabel...),
		indexPath:   indexPath,
		logger:      logging.Component("parallel-verifier"),
	}
}

func diagnosticLabelValue(sourcePath string, labels ...string) string {
	if len(labels) > 0 && labels[0] != "" {
		return labels[0]
	}
	return sourcePath
}

func (sv *SafetyVerifier) diagnosticError(err error) error {
	if sv == nil {
		return err
	}
	return (provenance.RuntimeOptions{DiagnosticLabel: sv.sourceLabel}).DiagnosticError(err, sv.sourcePath)
}

// isSeekableZstdIndex returns true if the index path indicates a seekable-zstd store.
func isSeekableZstdIndex(indexPath string) bool {
	return strings.HasSuffix(indexPath, ".recordindex.header.json")
}

// VerifyIntegrity performs SHA256 verification and source offset semantic checks.
func (sv *SafetyVerifier) VerifyIntegrity() error {
	sv.logger.Info("Starting integrity verification",
		zap.String("source", sv.sourceLabel),
		zap.String("index_version", sv.idx.Version))

	if err := index.ValidateSourceByteOffsets(sv.idx, sv.sourcePath); err != nil {
		return sv.diagnosticError(err)
	}
	if err := index.ValidateRecordIndexHeaderVersion(sv.idx.Version); err != nil {
		return err
	}

	// For seekable-zstd indexes (.header.json), use header-based verification
	// since index.Verifier expects the full JSON format
	if isSeekableZstdIndex(sv.indexPath) {
		return sv.verifyFromHeader()
	}

	// For standard JSON indexes, use the existing verifier
	return sv.verifyFromFullIndex()
}

// verifyFromHeader performs verification using only header data.
// This is used for seekable-zstd indexes where we don't have a full JSON index.
func (sv *SafetyVerifier) verifyFromHeader() error {
	sv.logger.Info("Using header-based verification for seekable-zstd index")

	// Verify source file exists
	fileInfo, err := os.Stat(sv.sourcePath)
	if err != nil {
		return fmt.Errorf("source file not accessible: %w", sv.diagnosticError(err))
	}

	// Verify file size matches
	if fileInfo.Size() != sv.idx.Source.SizeBytes {
		return fmt.Errorf(
			"source file size mismatch: expected %d bytes, got %d bytes\n"+
				"The source file may have been modified since the index was created.\n"+
				"Rebuild the index with: sumpter index build %s --selector %s",
			sv.idx.Source.SizeBytes,
			fileInfo.Size(),
			sv.sourceLabel,
			sv.idx.Selector.XPath,
		)
	}

	// Verify SHA256 hash
	file, err := os.Open(sv.sourcePath) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return fmt.Errorf("failed to open source file for verification: %w", sv.diagnosticError(err))
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("failed to compute source file hash: %w", sv.diagnosticError(err))
	}

	actualHash := fmt.Sprintf("%x", hasher.Sum(nil))
	if actualHash != sv.idx.Source.SHA256 {
		return fmt.Errorf(
			"source file SHA256 mismatch:\n"+
				"  expected: %s\n"+
				"  actual:   %s\n"+
				"The source file may have been modified since the index was created.\n"+
				"Rebuild the index with: sumpter index build %s --selector %s",
			sv.idx.Source.SHA256,
			actualHash,
			sv.sourceLabel,
			sv.idx.Selector.XPath,
		)
	}

	sv.logger.Info("Header-based verification passed",
		zap.Bool("source_hash_match", true),
		zap.Bool("source_size_match", true))

	return nil
}

// verifyFromFullIndex uses the existing index.Verifier for full JSON indexes.
func (sv *SafetyVerifier) verifyFromFullIndex() error {
	verifier := index.NewVerifier(index.VerifyOptions{
		InputPath:     sv.sourcePath,
		IndexPath:     sv.indexPath,
		VerifyRecords: false, // Only file-level check
		FailFast:      true,
	})

	result, err := verifier.Verify()
	if err != nil {
		return fmt.Errorf("integrity verification failed: %w", sv.diagnosticError(err))
	}

	if !result.Valid {
		detail := result.ErrorMessage
		if detail != "" {
			detail = sv.diagnosticError(errors.New(detail)).Error()
		}
		return fmt.Errorf(
			"source file integrity check failed: %s\n"+
				"The source file may have been modified since the index was created.\n"+
				"Rebuild the index with: sumpter index build %s --selector %s",
			detail,
			sv.sourceLabel,
			sv.idx.Selector.XPath,
		)
	}

	sv.logger.Info("Integrity verification passed",
		zap.Bool("source_hash_match", result.SourceHashMatch),
		zap.Bool("source_size_match", result.SourceSizeMatch))

	return nil
}
