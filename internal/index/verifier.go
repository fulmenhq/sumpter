package index

import (
	"encoding/json"
	"fmt"
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

// Verify validates the index against the source file.
// It checks:
// 1. Source file size matches index metadata
// 2. Source file SHA256 matches index metadata
// 3. (Optional) Individual record SHA256 hashes match
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
