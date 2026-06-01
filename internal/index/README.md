# XML Record Index Package

Package `index` provides streaming XML record index building and verification with SHA-256 integrity checking.

## Overview

This package implements Phase 2 of the High-Scale XML Processing MVP, enabling:

- **Streaming index building** with constant memory usage (<100MB regardless of file size)
- **SHA-256 integrity verification** at both file and per-record levels
- **Statistical analysis** (min/max/avg/percentiles) of record sizes
- **Tamper detection** through hash verification

## Architecture

### Streaming Design

The builder uses a **two-pass streaming architecture**:

1. **First Pass**: Compute source file SHA-256 hash
2. **Second Pass**: Stream through records, collect metadata, compute per-record hashes

Memory usage remains constant because:

- Records are scanned using `streaming.RecordScannerSizeOnly` (no XML buffering)
- Per-record hashes are computed by seeking to byte ranges (no full record buffering)
- Statistics are calculated incrementally
- Index is written progressively to disk

### Key Types

#### RecordIndex

Complete index structure conforming to `record-index/v0.1.1` schema:

```go
type RecordIndex struct {
    Version  string           // Schema version
    Source   SourceInfo       // Source file metadata + integrity
    Selector SelectorInfo     // Record boundary selector
    Records  []RecordMetadata // Record boundary + integrity data
    Summary  SummaryStats     // Aggregate statistics
    Metadata IndexMetadata    // Build process metadata
}
```

#### Builder

Streaming index builder:

```go
builder := index.NewBuilder(index.BuildOptions{
    InputPath:      "/path/to/file.xml",
    Selector:       "//VariationArchive",
    IncludeP95:     true,
    IncludeP99:     true,
    SumpterVersion: "0.1.2",
})

idx, err := builder.Build()
if err != nil {
    return err
}

err = builder.WriteToFile(idx, "/path/to/output.recordindex.json")
```

The index builder uses the same record boundary grammar as streaming
extraction: `Name` and `//Name` are supported, matched exactly by local element
name. Predicates, multi-segment paths, and namespace-prefixed forms are not yet
supported for streaming/index mode and return an error before scanning.

#### Verifier

Index integrity verifier:

```go
verifier := index.NewVerifier(index.VerifyOptions{
    InputPath:     "/path/to/file.xml",
    IndexPath:     "/path/to/file.recordindex.json",
    VerifyRecords: true,  // Verify individual record hashes
    FailFast:      false, // Collect all errors
})

result, err := verifier.Verify()
if err != nil {
    return err
}

if !result.Valid {
    log.Fatalf("Verification failed: %s", result.ErrorMessage)
}
```

## Usage Examples

### Building an Index

```go
opts := index.BuildOptions{
    InputPath:      "clinvar.xml",
    Selector:       "//VariationArchive",
    IncludeP50:     true,
    IncludeP95:     true,
    IncludeP99:     true,
    SumpterVersion: "0.1.2",
}

builder := index.NewBuilder(opts)
idx, err := builder.Build()
if err != nil {
    return fmt.Errorf("build failed: %w", err)
}

err = builder.WriteToFile(idx, "clinvar.recordindex.json")
if err != nil {
    return fmt.Errorf("write failed: %w", err)
}

fmt.Printf("Indexed %d records in %dms\n",
    idx.Summary.TotalRecords,
    idx.Metadata.BuildDurationMs)
```

### Verifying an Index

```go
opts := index.VerifyOptions{
    InputPath:     "clinvar.xml",
    IndexPath:     "clinvar.recordindex.json",
    VerifyRecords: false, // Just verify file-level integrity
}

verifier := index.NewVerifier(opts)
result, err := verifier.Verify()
if err != nil {
    return err
}

if !result.Valid {
    fmt.Printf("Verification failed:\n")
    fmt.Printf("  Size match: %v\n", result.SourceSizeMatch)
    fmt.Printf("  Hash match: %v\n", result.SourceHashMatch)
    fmt.Printf("  Error: %s\n", result.ErrorMessage)
    return errors.New("index verification failed")
}

fmt.Println("Index verified successfully")
```

### Loading an Index

```go
idx, err := index.LoadIndex("clinvar.recordindex.json")
if err != nil {
    return err
}

fmt.Printf("Index contains %d records\n", idx.Summary.TotalRecords)
fmt.Printf("Average record size: %.2f KB\n",
    idx.Summary.AvgRecordSizeBytes/1024)
```

## Performance Characteristics

### Build Performance

Measured on streaming architecture with constant memory:

- **1k records** (~25MB): <5 seconds
- **10k records** (~250MB): <30 seconds
- **100k records** (~2.5GB): <5 minutes
- **1M records** (~25GB): <15 minutes

Memory usage: <100MB regardless of file size

### Verification Performance

- **File-level only**: Instant (single hash comparison)
- **With record hashes**: ~2x build time (re-hashing all records)

## Implementation Details

### SHA-256 Computation

**File Hash** (`computeFileSHA256`):

- Streams entire file through SHA-256 hasher
- Constant memory (streaming I/O)

**Record Hash** (`computeRangeHashSHA256`):

- Seeks to record start offset
- Reads exactly `endOffset - startOffset` bytes
- Computes SHA-256 of that range
- No buffering of full record in memory

### Percentile Calculation

Uses **linear interpolation** between sorted values:

```go
index = p * (len(sizes) - 1)
lower := int(index)
upper := lower + 1
weight := index - float64(lower)
return int64(float64(sizes[lower])*(1-weight) + float64(sizes[upper])*weight)
```

This matches standard statistical percentile definitions (R-7 method).

### Compression Detection

Detects compression based on file extension:

- `.gz` → gzip
- `.bz2` → bzip2
- `.xz` → xz
- Other → none

Note: JSON record indexes require source-byte offsets. Compressed source paths are rejected before hashing or scanning; decompress first with a pattern such as `gunzip -c input.xml.gz > input.xml`, then build the index from the uncompressed XML file.

## Testing

### Test Coverage

80.1% statement coverage across:

- Builder tests (build, write, helpers)
- Verifier tests (valid, tampered, missing file, record verification)
- Utility function tests (hash, percentile, compression detection)

### Running Tests

```bash
# Run all index tests
go test -v ./internal/index/...

# With coverage
go test -v -coverprofile=coverage.out ./internal/index/...
go tool cover -html=coverage.out

# Run specific test
go test -v ./internal/index/... -run TestBuilder_Build_SmallXML
```

## References

- **Schema**: `schemas/index/v0.1.1/record-index.schema.json`
- **Examples**: `examples/index/clinvar-sample.recordindex.json`
- **ADR-0005**: Hybrid Streaming XML Architecture
- **Design summary**: Record indexes persist byte ranges, hashes, and record
  metadata so extraction can seek directly to records without reparsing
  predecessor content.
- **RecordScanner**: `internal/extract/streaming/scanner.go`

## Future Enhancements (Phase 3)

- Explicit chunk-based source semantics for compressed inputs
- Parallel index building (multi-threaded record hashing)
- Index-driven parallel extraction
- Progressive index writing (streaming JSON output)
