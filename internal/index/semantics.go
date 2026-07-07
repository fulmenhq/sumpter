package index

import (
	"fmt"
	"path/filepath"
	"strings"
)

const decompressHint = "gunzip -c input.xml.gz > input.xml"

// DetectCompression determines if a file is compressed based on extension.
func DetectCompression(path string) (bool, string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".gz":
		return true, "gzip"
	case ".bz2":
		return true, "bzip2"
	case ".xz":
		return true, "xz"
	default:
		return false, "none"
	}
}

func detectCompression(path string) (bool, string) {
	return DetectCompression(path)
}

// NormalizeRecordIndex fills backward-compatible defaults for older indexes.
func NormalizeRecordIndex(idx *RecordIndex) {
	if idx == nil {
		return
	}
	if strings.TrimSpace(idx.Source.OffsetKind) == "" {
		idx.Source.OffsetKind = OffsetKindSourceBytes
	}
	if idx.Version == SchemaVersion && len(idx.NamespaceContexts) == 0 {
		idx.NamespaceContexts = []NamespaceContext{{ID: 0, Declarations: []NamespaceDeclaration{}}}
	}
}

// ValidateRecordIndexVersion rejects unsupported JSON record-index versions.
func ValidateRecordIndexVersion(version string) error {
	switch version {
	case SchemaVersion, LegacySchemaVersion, LegacySchemaVersionV010:
		return nil
	default:
		return fmt.Errorf(
			"unsupported index version: %s (expected %s, %s, or %s)",
			version,
			SchemaVersion,
			LegacySchemaVersion,
			LegacySchemaVersionV010,
		)
	}
}

// ValidateRecordIndexHeaderVersion validates JSON record-index versions while
// leaving alternate store container versions to their own readers.
func ValidateRecordIndexHeaderVersion(version string) error {
	if strings.HasPrefix(version, "record-index/") {
		return ValidateRecordIndexVersion(version)
	}
	return nil
}

// ValidateSourceByteOffsets ensures a record index can be used for seekable
// source-byte verification and extraction.
func ValidateSourceByteOffsets(idx *RecordIndex, sourcePath string) error {
	if idx == nil {
		return fmt.Errorf("record index header is missing")
	}
	NormalizeRecordIndex(idx)

	if idx.Source.OffsetKind != OffsetKindSourceBytes {
		return fmt.Errorf(
			"record index offset_kind %q is not supported for seekable source-byte access; rebuild from an uncompressed source (example: %s)",
			idx.Source.OffsetKind,
			decompressHint,
		)
	}

	if idx.Source.Compressed {
		format := idx.Source.CompressionFormat
		if format == "" {
			format = "unknown"
		}
		return fmt.Errorf(
			"record index source is marked compressed (%s); record-index offsets must address uncompressed source bytes; decompress first (example: %s) and rebuild the index",
			format,
			decompressHint,
		)
	}

	if sourcePath == "" {
		sourcePath = idx.Source.Path
	}
	if compressed, format := DetectCompression(sourcePath); compressed {
		return fmt.Errorf(
			"source path %q appears %s compressed; record-index offsets require an uncompressed source; decompress first (example: %s) and rebuild the index",
			sourcePath,
			format,
			decompressHint,
		)
	}

	return nil
}

// CompressedSourceIndexBuildError returns the build-time error for compressed
// inputs before any source hashing, scanning, or index output occurs.
func CompressedSourceIndexBuildError(path, format string) error {
	return fmt.Errorf(
		"record index build requires an uncompressed source; %q appears %s compressed. Decompress first (example: %s) and build the index from the uncompressed file",
		path,
		format,
		decompressHint,
	)
}
