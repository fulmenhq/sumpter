package index

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Verifier validates record indexes against source XML files
type Verifier struct {
	opts VerifyOptions
}

// NewVerifier creates a new index verifier with the given options
func NewVerifier(opts VerifyOptions) *Verifier {
	return &Verifier{opts: opts}
}

// RecordProvider abstracts access to record metadata for verification.
// This interface allows the verifier to work with both JSON and seekable-zstd formats.
type RecordProvider interface {
	// Header returns the index header with source metadata
	Header() (*RecordIndex, error)
	// Records returns an iterator for streaming through records
	Records(ctx context.Context) (RecordIterator, error)
	// Close releases resources
	Close() error
}

// RecordIterator provides streaming access to record metadata.
type RecordIterator interface {
	// Next returns the next record, or io.EOF when done
	Next() (*RecordMetadata, error)
	// Close releases resources
	Close() error
}

// VerifyWithProvider validates the index against the source file using a RecordProvider.
// This method supports both JSON and seekable-zstd formats through the provider abstraction.
//
// IMPORTANT: The caller is responsible for closing the provider after verification completes.
// This method does not take ownership of the provider's lifecycle.
//
// Example:
//
//	store, err := store.Open(indexPath)
//	if err != nil { return err }
//	defer store.Close()  // Caller must close
//
//	result, err := verifier.VerifyWithProvider(store)
func (v *Verifier) VerifyWithProvider(provider RecordProvider) (*VerifyResult, error) {
	result := &VerifyResult{
		Valid:        true,
		RecordErrors: make([]string, 0),
	}

	// Get header metadata
	header, err := provider.Header()
	if err != nil {
		return nil, fmt.Errorf("failed to read index header: %w", err)
	}

	// Verify source file exists
	fileInfo, err := os.Stat(v.opts.InputPath)
	if err != nil {
		result.Valid = false
		result.ErrorMessage = fmt.Sprintf("source file not found: %v", err)
		return result, nil
	}

	// Verify file size
	if fileInfo.Size() != header.Source.SizeBytes {
		result.Valid = false
		result.SourceSizeMatch = false
		result.ErrorMessage = fmt.Sprintf(
			"source file size mismatch: expected %d bytes, got %d bytes",
			header.Source.SizeBytes,
			fileInfo.Size(),
		)
		return result, nil
	}
	result.SourceSizeMatch = true

	// Verify file SHA256
	sourceHash, err := computeFileSHA256(v.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}

	if sourceHash != header.Source.SHA256 {
		result.Valid = false
		result.SourceHashMatch = false
		result.ErrorMessage = fmt.Sprintf(
			"source file hash mismatch: expected %s, got %s",
			header.Source.SHA256,
			sourceHash,
		)
		return result, nil
	}
	result.SourceHashMatch = true

	// Optionally verify individual record hashes using streaming iterator
	if v.opts.VerifyRecords {
		ctx := context.Background()
		iter, err := provider.Records(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to create record iterator: %w", err)
		}
		defer func() { _ = iter.Close() }()

		count := 0
		for {
			record, err := iter.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read record %d: %w", count+1, err)
			}

			recordHash, err := computeRangeHashSHA256(
				v.opts.InputPath,
				record.StartOffset,
				record.EndOffset,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to compute hash for record %d: %w", record.RecordNum, err)
			}

			if recordHash != record.SHA256 {
				errMsg := fmt.Sprintf(
					"record %d hash mismatch: expected %s, got %s",
					record.RecordNum,
					record.SHA256,
					recordHash,
				)
				result.RecordErrors = append(result.RecordErrors, errMsg)
				result.Valid = false

				if v.opts.FailFast {
					result.ErrorMessage = errMsg
					return result, nil
				}
			}

			count++
			result.RecordsVerified = count
		}

		// Set error message if there were record errors
		if len(result.RecordErrors) > 0 && result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf(
				"%d record verification errors (see RecordErrors)",
				len(result.RecordErrors),
			)
		}
	}

	return result, nil
}

// Verify validates the index against the source file.
// It checks:
// 1. Source file size matches index metadata
// 2. Source file SHA256 matches index metadata
// 3. (Optional) Individual record SHA256 hashes match
//
// Note: This method only supports JSON format. For seekable-zstd format,
// use VerifyWithProvider() with a store.IndexStore.
func (v *Verifier) Verify() (*VerifyResult, error) {
	result := &VerifyResult{
		Valid:        true,
		RecordErrors: make([]string, 0),
	}

	// Load index from JSON
	index, err := v.loadIndex()
	if err != nil {
		return nil, fmt.Errorf("failed to load index: %w", err)
	}

	// Verify source file exists
	fileInfo, err := os.Stat(v.opts.InputPath)
	if err != nil {
		result.Valid = false
		result.ErrorMessage = fmt.Sprintf("source file not found: %v", err)
		return result, nil
	}

	// Verify file size
	if fileInfo.Size() != index.Source.SizeBytes {
		result.Valid = false
		result.SourceSizeMatch = false
		result.ErrorMessage = fmt.Sprintf(
			"source file size mismatch: expected %d bytes, got %d bytes",
			index.Source.SizeBytes,
			fileInfo.Size(),
		)
		return result, nil
	}
	result.SourceSizeMatch = true

	// Verify file SHA256
	sourceHash, err := computeFileSHA256(v.opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}

	if sourceHash != index.Source.SHA256 {
		result.Valid = false
		result.SourceHashMatch = false
		result.ErrorMessage = fmt.Sprintf(
			"source file hash mismatch: expected %s, got %s",
			index.Source.SHA256,
			sourceHash,
		)
		return result, nil
	}
	result.SourceHashMatch = true

	// Optionally verify individual record hashes
	if v.opts.VerifyRecords {
		for i, record := range index.Records {
			recordHash, err := computeRangeHashSHA256(
				v.opts.InputPath,
				record.StartOffset,
				record.EndOffset,
			)
			if err != nil {
				return nil, fmt.Errorf("failed to compute hash for record %d: %w", record.RecordNum, err)
			}

			if recordHash != record.SHA256 {
				errMsg := fmt.Sprintf(
					"record %d hash mismatch: expected %s, got %s",
					record.RecordNum,
					record.SHA256,
					recordHash,
				)
				result.RecordErrors = append(result.RecordErrors, errMsg)
				result.Valid = false

				if v.opts.FailFast {
					result.ErrorMessage = errMsg
					return result, nil
				}
			}

			result.RecordsVerified = i + 1
		}

		// Set error message if there were record errors
		if len(result.RecordErrors) > 0 && result.ErrorMessage == "" {
			result.ErrorMessage = fmt.Sprintf(
				"%d record verification errors (see RecordErrors)",
				len(result.RecordErrors),
			)
		}
	}

	return result, nil
}

// loadIndex loads a record index from a JSON file
func (v *Verifier) loadIndex() (*RecordIndex, error) {
	file, err := os.Open(v.opts.IndexPath) // #nosec G304 - IndexPath is user-provided CLI argument
	if err != nil {
		return nil, fmt.Errorf("failed to open index file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var index RecordIndex
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&index); err != nil {
		return nil, fmt.Errorf("failed to decode index JSON: %w", err)
	}

	// Validate schema version
	if index.Version != SchemaVersion {
		return nil, fmt.Errorf(
			"unsupported index version: %s (expected %s)",
			index.Version,
			SchemaVersion,
		)
	}

	return &index, nil
}

// LoadIndex loads a record index from a JSON file (public utility function)
func LoadIndex(path string) (*RecordIndex, error) {
	file, err := os.Open(path) // #nosec G304 - path is user-provided CLI argument
	if err != nil {
		return nil, fmt.Errorf("failed to open index file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var index RecordIndex
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&index); err != nil {
		return nil, fmt.Errorf("failed to decode index JSON: %w", err)
	}

	// Validate schema version
	if index.Version != SchemaVersion {
		return nil, fmt.Errorf(
			"unsupported index version: %s (expected %s)",
			index.Version,
			SchemaVersion,
		)
	}

	return &index, nil
}
