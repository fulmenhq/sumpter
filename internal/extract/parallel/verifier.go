package parallel

import (
	"fmt"
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

// NewSafetyVerifier creates a new safety verifier
func NewSafetyVerifier(idx *index.RecordIndex, sourcePath string, indexPath string) *SafetyVerifier {
	return &SafetyVerifier{
		idx:        idx,
		sourcePath: sourcePath,
		indexPath:  indexPath,
		logger:     logging.Component("parallel-verifier"),
	}
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

	// Verify SHA256 using existing index verifier
	verifier := index.NewVerifier(index.VerifyOptions{
		InputPath:     sv.sourcePath,
		IndexPath:     sv.indexPath,
		VerifyRecords: false, // Only file-level check
		FailFast:      true,
	})

	// Load the index and verify file-level integrity
	result, err := sv.verifyFileIntegrity(verifier)
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

// verifyFileIntegrity performs file-level verification using index.Verifier
func (sv *SafetyVerifier) verifyFileIntegrity(verifier *index.Verifier) (*index.VerifyResult, error) {
	// Use the index verifier to check file integrity
	result, err := verifier.Verify()
	if err != nil {
		return nil, fmt.Errorf("failed to run verification: %w", err)
	}

	return result, nil
}
