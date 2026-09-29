package index

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

	"github.com/fulmenhq/sumpter/internal/docnode"
	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
)

// jsonSource is an uncompressed JSON source held open for one build or
// verification: the source hash, the record scan and every record hash read
// the same open file, so they cannot come from different files.
type jsonSource struct {
	f    *os.File
	size int64
}

func openJSONSource(path string) (*jsonSource, error) {
	if compressed, format := DetectCompression(path); compressed {
		return nil, CompressedSourceIndexBuildError(path, format)
	}
	f, err := os.Open(path) // #nosec G304 - operator-selected source path
	if err != nil {
		return nil, fmt.Errorf("failed to open input file: %w", err)
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat input file: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("input %q is not a regular file", path)
	}
	return &jsonSource{f: f, size: info.Size()}, nil
}

func (s *jsonSource) Close() error { return s.f.Close() }

// hash returns the SHA-256 of the whole source.
func (s *jsonSource) hash() (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(s.f, 0, s.size)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// rangeHash returns the SHA-256 of source[start:end].
func (s *jsonSource) rangeHash(start, end int64) (string, error) {
	if start < 0 || end < start || end > s.size {
		return "", fmt.Errorf("range [%d,%d) is outside the %d-byte source", start, end, s.size)
	}
	return computeRangeHashSHA256FromReader(s.f, start, end)
}

// records scans the source for the selector's records, size only.
func (s *jsonSource) records(selector string) (docnode.RecordScanner, error) {
	return docjson.Format{}.NewScanner(io.NewSectionReader(s.f, 0, s.size), selector, true)
}
