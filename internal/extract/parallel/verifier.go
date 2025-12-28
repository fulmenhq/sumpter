package parallel

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"go.uber.org/zap"
)

// SafetyVerifier performs pre-extraction safety checks
type SafetyVerifier struct {
	idx        *index.RecordIndex
	sourcePath string
	indexPath  string
	logger     *logging.ComponentLogger
}

// NewSafetyVerifier creates a new safety verifier.
//
// Deprecated: Use NewSafetyVerifierFromHeader for streaming mode.
func NewSafetyVerifier(idx *index.RecordIndex, sourcePath string, indexPath string) *SafetyVerifier {
	return &SafetyVerifier{
		idx:        idx,
		sourcePath: sourcePath,
		indexPath:  indexPath,
		logger:     logging.Component("parallel-verifier"),
	}
}

// NewSafetyVerifierFromHeader creates a safety verifier from header data.
// This is used in streaming mode where the full index is not loaded.
func NewSafetyVerifierFromHeader(header *index.RecordIndex, sourcePath string, indexPath string) *SafetyVerifier {
	return &SafetyVerifier{
		idx:        header,
		sourcePath: sourcePath,
		indexPath:  indexPath,
		logger:     logging.Component("parallel-verifier"),
	}
}

// isSeekableZstdIndex returns true if the index path indicates a seekable-zstd store.
func isSeekableZstdIndex(indexPath string) bool {
	return strings.HasSuffix(indexPath, ".recordindex.header.json")
}

// VerifyIntegrity performs SHA256 verification and compression checks
func (sv *SafetyVerifier) VerifyIntegrity() error {
	sv.logger.Info("Starting integrity verification",
		zap.String("source", sv.sourcePath),
		zap.String("index_version", sv.idx.Version))

	// Check compression - parallel extraction requires uncompressed or chunk-indexed files
	if sv.idx.Source.Compressed {
		return fmt.Errorf(
			"parallel extraction requires uncompressed files (source is %s compressed)\n"+
				"Hint: Decompress the file first, then rebuild the index:\n"+
				"  gunzip %s\n"+
				"  sumpter index build %s --selector %s\n"+
				"  sumpter extract files --record-index <index>",
			sv.idx.Source.CompressionFormat,
			sv.sourcePath,
			strings.TrimSuffix(sv.sourcePath, ".gz"),
			sv.idx.Selector.XPath,
		)
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
		return fmt.Errorf("source file not accessible: %w", err)
	}

	// Verify file size matches
	if fileInfo.Size() != sv.idx.Source.SizeBytes {
		return fmt.Errorf(
			"source file size mismatch: expected %d bytes, got %d bytes\n"+
				"The source file may have been modified since the index was created.\n"+
				"Rebuild the index with: sumpter index build %s --selector %s",
			sv.idx.Source.SizeBytes,
			fileInfo.Size(),
			sv.sourcePath,
			sv.idx.Selector.XPath,
		)
	}

	// Verify SHA256 hash
	file, err := os.Open(sv.sourcePath) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return fmt.Errorf("failed to open source file for verification: %w", err)
	}
	defer func() { _ = file.Close() }()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return fmt.Errorf("failed to compute source file hash: %w", err)
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
			sv.sourcePath,
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
		return fmt.Errorf("integrity verification failed: %w", err)
	}

	if !result.Valid {
		return fmt.Errorf(
			"source file integrity check failed: %s\n"+
				"The source file may have been modified since the index was created.\n"+
				"Rebuild the index with: sumpter index build %s --selector %s",
			result.ErrorMessage,
			sv.sourcePath,
			sv.idx.Selector.XPath,
		)
	}

	sv.logger.Info("Integrity verification passed",
		zap.Bool("source_hash_match", result.SourceHashMatch),
		zap.Bool("source_size_match", result.SourceSizeMatch))

	return nil
}
