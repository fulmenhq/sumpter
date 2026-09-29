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

// EmptyContextsAsArrays returns a copy of contexts in which every empty
// declaration list is an empty array rather than nil.
func EmptyContextsAsArrays(contexts []NamespaceContext) []NamespaceContext {
	if contexts == nil {
		return nil
	}
	out := make([]NamespaceContext, len(contexts))
	for i, c := range contexts {
		if c.Declarations == nil {
			c.Declarations = []NamespaceDeclaration{}
		}
		out[i] = c
	}
	return out
}

// NormalizeRecordIndex fills backward-compatible defaults for older indexes.
func NormalizeRecordIndex(idx *RecordIndex) {
	if idx == nil {
		return
	}
	if strings.TrimSpace(idx.Source.OffsetKind) == "" {
		idx.Source.OffsetKind = OffsetKindSourceBytes
	}
	if (idx.Version == SchemaVersion || idx.Version == LegacySchemaVersionV012) && len(idx.NamespaceContexts) == 0 {
		idx.NamespaceContexts = []NamespaceContext{{ID: 0, Declarations: []NamespaceDeclaration{}}}
	}
}

// ValidateRecordIndexVersion rejects unsupported JSON record-index versions.
func ValidateRecordIndexVersion(version string) error {
	switch version {
	case SchemaVersion, LegacySchemaVersionV012, LegacySchemaVersion, LegacySchemaVersionV010:
		return nil
	default:
		return fmt.Errorf(
			"unsupported index version: %s (expected %s, %s, %s, or %s)",
			version,
			SchemaVersion,
			LegacySchemaVersionV012,
			LegacySchemaVersion,
			LegacySchemaVersionV010,
		)
	}
}

// SourceFormat returns the source syntax a record-index header declares.
// record-index/v0.1.3 must declare xml or json; earlier versions must declare
// nothing and mean xml. Any other combination is refused: a legacy header is
// never read as JSON, and a format is never guessed.
func SourceFormat(idx *RecordIndex) (string, error) {
	if idx == nil {
		return "", fmt.Errorf("record index header is missing")
	}
	format := idx.Source.Format
	if idx.Version == SchemaVersion || idx.Version == SzstStoreVersion {
		switch format {
		case SourceFormatXML, SourceFormatJSON:
			return format, nil
		case "":
			return "", fmt.Errorf("record index %s requires source.format (xml or json)", idx.Version)
		default:
			return "", fmt.Errorf("record index source.format %q is not supported (supported: xml, json)", format)
		}
	}
	switch idx.Version {
	case LegacySzstStoreVersion, LegacySzstStoreVersionV010:
	default:
		if err := ValidateRecordIndexVersion(idx.Version); err != nil {
			return "", err
		}
	}
	if format != "" {
		return "", fmt.Errorf("record index %s predates source.format and is XML only; it must not declare source.format %q", idx.Version, format)
	}
	return SourceFormatXML, nil
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

// ValidateNamespaceContextShape refuses a current header whose namespace
// context lists declarations as null or omits them: this release writes an
// empty context as an empty array, and a reader does not repair invalid new
// input. Legacy headers may carry null, which reads as an empty context.
func ValidateNamespaceContextShape(idx *RecordIndex) error {
	if idx == nil || (idx.Version != SchemaVersion && idx.Version != SzstStoreVersion) {
		return nil
	}
	for _, c := range idx.NamespaceContexts {
		if c.Declarations == nil {
			return fmt.Errorf("record index %s namespace context %d has no declarations array", idx.Version, c.ID)
		}
	}
	return nil
}

// requireWritableFormat refuses to write a current-version header that does
// not declare a closed source.format; a writer never supplies one by default.
func requireWritableFormat(idx *RecordIndex) error {
	switch idx.Source.Format {
	case SourceFormatXML, SourceFormatJSON:
		return nil
	case "":
		return fmt.Errorf("record index %s requires source.format (xml or json)", SchemaVersion)
	default:
		return fmt.Errorf("record index source.format %q is not supported (supported: xml, json)", idx.Source.Format)
	}
}
