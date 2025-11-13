# Record Index Schema v0.1.0

## Overview

The XML Record Index schema defines the structure for storing record boundary metadata, enabling seekable parallel extraction of large XML files with integrity verification.

## Purpose

- **Seekable Extraction**: Record byte offsets enable direct seeking to specific records without sequential scanning
- **Parallel Processing**: Multiple workers can process different records simultaneously
- **Integrity Verification**: SHA-256 hashes ensure source file and individual records haven't been tampered with
- **Performance Optimization**: Pre-computed statistics guide resource allocation and processing strategies

## Schema Structure

### Top-Level Fields

- `version`: Schema version identifier (currently `record-index/v0.1.0`)
- `source`: Source XML file metadata and integrity information
- `selector`: Record boundary selector configuration
- `records`: Array of record metadata (offsets, sizes, checksums)
- `summary`: Aggregate statistics across all records
- `metadata`: Index build process metadata

### Source Block

Contains source file integrity metadata:

- `path`: Path to source XML file
- `size_bytes`: Total file size
- `sha256`: SHA-256 hash of entire source file (hex-encoded, 64 characters)
- `compressed`: Boolean indicating if source is compressed
- `compression_format`: Format if compressed (`gzip`, `bzip2`, `xz`, `none`)
- `created_at`: ISO 8601 timestamp of index creation
- `encoding`: Detected character encoding (e.g., `UTF-8`, `UTF-16LE`)

### Selector Block

Defines how records were identified:

- `xpath`: XPath expression used to locate records (e.g., `//VariationArchive`)
- `element_name`: Extracted element name from XPath

### Records Array

Each record entry contains:

- `record_num`: Sequential 1-based record number
- `start_offset`: Byte offset where record begins
- `end_offset`: Byte offset where record ends
- `size_bytes`: Record size in bytes
- `sha256`: SHA-256 hash of record XML content
- `element_name`: Root element name
- `depth`: XML nesting depth (1 = top-level)

### Summary Block

Aggregate statistics:

- `total_records`: Count of indexed records
- `total_bytes`: Sum of all record sizes
- `avg_record_size_bytes`: Mean record size
- `min_record_size_bytes`: Smallest record size
- `max_record_size_bytes`: Largest record size
- `p50_record_size_bytes`: Median record size (optional)
- `p95_record_size_bytes`: 95th percentile record size (optional)
- `p99_record_size_bytes`: 99th percentile record size (optional)

### Metadata Block

Build process information:

- `generator`: Tool and version that created the index
- `build_duration_ms`: Index build time in milliseconds (optional)
- `sumpter_version`: Sumpter version used (optional)

## Usage

### Building an Index

```bash
sumpter index build input.xml --selector "//Record" --output index.json
```

### Verifying an Index

```bash
sumpter index verify input.xml --index index.json
```

### Using Index for Extraction

```bash
sumpter extract files input.xml --record-index index.json --workers 8
```

## Integrity Verification

The index includes two levels of SHA-256 hashing:

1. **Source File Hash**: Detects any modification to the source XML
2. **Record Hashes**: Detects tampering with individual records

Verification compares:
- Source file size and SHA-256 against recorded values
- Individual record checksums during extraction (optional)

## Compression Handling

- **Uncompressed files**: Offsets are true byte positions, enabling direct seeking
- **Compressed files** (.gz, .bz2): Offsets represent logical positions in decompressed stream
  - Seeking may require decompression from beginning (slower)
  - Index includes `compressed: true` flag and compression format
  - Phase 3 will support chunk-based seeking for compressed files

## Performance Characteristics

Index build performance (streaming, constant memory):

- 1k records (~25MB): <5 seconds
- 10k records (~250MB): <30 seconds
- 100k records (~2.5GB): <5 minutes
- 1M records (~25GB): <15 minutes

Memory usage: <100MB regardless of source file size (streaming architecture)

## Version History

### v0.1.0 (Current)

- Initial schema design
- SHA-256 integrity verification
- Record boundary metadata (offsets, sizes)
- Summary statistics
- Compression awareness
- JSON Schema 2020-12 compliance

## Example Files

See [examples/index/](../../../examples/index/) for complete example record index files including:
- ClinVar genomics data index (2.4M records, 50GB source)
- Performance benchmarking examples
- Usage demonstrations

## Related Documentation

- [High-Scale XML Processing MVP Plan](../../../.plans/active/v0.1.2/xml_highscale_mvp.md)
- [ADR-0005: Hybrid Streaming XML Architecture](../../../docs/architecture/adr/0005-hybrid-streaming-xml-architecture.md)
- [RecordScanner Implementation](../../../internal/extract/streaming/scanner.go)
