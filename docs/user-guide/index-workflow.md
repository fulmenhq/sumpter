# Record Index Workflow

Guide to building and using record indexes for seekable extraction and parallel processing.

## Overview

Record indexes map the byte boundaries of repeating XML elements in your source files. Sumpter's default index build path uses a streaming writer with memory bounded by parser state and writer buffering, allowing you to work with files that are too large to load into RAM. Once indexed, you can:

- Extract specific records by position without parsing the entire file
- Run parallel extraction with worker pools that seek to different file offsets
- Verify data integrity with SHA-256 checksums at both file and record level
- Process multi-GB files with minimal memory overhead

## When to Use Indexes

### Good Use Cases

**Large Files with Repeating Records**

```xml
<!-- Multi-GB file with thousands of repeating elements -->
<ClinicalData>
  <Patient>...</Patient>
  <Patient>...</Patient>
  <!-- 100,000+ more patients -->
</ClinicalData>
```

Indexing helps when:

- Source file is >100 MB
- File contains hundreds or thousands of records
- You need to extract subsets of records
- Parallel processing would speed up extraction
- File will be processed multiple times

**Compressed Archives**

```bash
# Decompress first, then build the index from source bytes
gunzip -c large-dataset.xml.gz > large-dataset.xml
sumpter index build large-dataset.xml --selector "//Record"
```

Record indexes are source-byte indexes. Build them from uncompressed XML so verification and parallel extraction can seek to the same bytes recorded in the index.

### When Indexes Don't Help

**Small Files**

- Files under 10 MB: Sequential extraction is faster
- Single-record files: No benefit to indexing
- Ad-hoc exploration: Use `sumpter inspect` instead

**Simple Transformations**

- One-time conversions where you'll extract all records anyway
- Files that change frequently (index becomes stale)
- Scenarios where build time exceeds extraction savings

## Basic Workflow

### Step 1: Inspect the Source

Before building an index, identify the record selector:

```bash
# Analyze XML structure
sumpter inspect data.xml --analyze-records

# Test a specific selector
sumpter inspect data.xml \
  --analyze-records \
  --record-selector "//Transaction"
```

Streaming record analysis and `index build --selector` support a single local
element name with exact case-sensitive matching. Use `Transaction` or
`//Transaction`. Predicate selectors, multi-segment paths, and
namespace-prefixed forms are rejected because they are not yet supported for
streaming/index mode.

The inspect command shows:

- How many records match the selector
- Average record size
- Total file size
- Estimated memory for DOM-based extraction

### Step 2: Build the Index

```bash
# Basic index build (JSON format, default)
sumpter index build data.xml \
  --selector "//Transaction" \
  --output data.recordindex.json

# With progress monitoring
sumpter index build large-file.xml \
  --selector "//Record" \
  --output indexes/large-file.recordindex.json \
  --progress

# Include exact percentiles when needed (retains record sizes during build)
sumpter index build large-file.xml \
  --selector "//Record" \
  --output indexes/large-file.recordindex.json \
  --p50 --p95 --p99

# Build with seekable-zstd format (v0.1.2+, requires CGO)
sumpter index build large-file.xml \
  --selector "//Record" \
  --emit-szst \
  --progress
# Creates: large-file.recordindex.header.json + large-file.recordindex.records.szst
```

**What Happens During Build:**

1. First pass: Compute SHA-256 hash of entire source file
2. Second pass: Stream through file to detect record boundaries
3. For each record: Capture start/end byte offsets and size
4. For each record: Compute SHA-256 over the original source byte range using the open source handle
5. Append each record to the selected index writer and finalize summary metadata

**Memory Usage:**

- Default path is bounded by XML parser state and writer buffering
- Exact percentile flags (`--p50`, `--p95`, `--p99`) retain record sizes to calculate exact values
- Tested with 59 GB XML file using <100 MB RAM
- Index size: ~25-35 bytes per record in JSON format

### Step 3: Verify the Index

```bash
# Quick verification (source file integrity only)
sumpter index verify data.xml \
  --index data.recordindex.json

# Deep verification (validates all record checksums)
sumpter index verify data.xml \
  --index data.recordindex.json \
  --verify-records
```

Verification checks:

- ✓ Source file size matches index metadata
- ✓ Source file SHA-256 matches index metadata
- ✓ Source file has not been modified since index creation
- ✓ (Deep mode) Each record's byte range checksum matches

**When to Verify:**

- Before parallel extraction runs
- After transferring files between systems
- When debugging extraction issues
- In automated pipelines (integrity gates)

### Step 4: Use the Index

#### Sequential Extraction (Index-Aware)

```bash
# Extract using record index for record-level integrity
sumpter extract files \
  --signature-config-path configs/signature.yaml \
  --extract-config-path configs/extract.yaml \
  --files data.xml \
  --record-index data.recordindex.json \
  --output-path outputs/
```

Even with sequential extraction, the index provides:

- Record-level tamper detection
- Progress tracking (N of M records)
- Ability to resume from specific record numbers

#### Parallel Extraction (Multi-Worker)

```bash
# Extract with 8 parallel workers
sumpter extract files \
  --signature-config-path configs/signature.yaml \
  --extract-config-path configs/extract.yaml \
  --files data.xml \
  --record-index data.recordindex.json \
  --workers 8 \
  --output-path outputs/
```

Parallel extraction:

- Spawns worker pool (default: 4 workers)
- Each worker seeks to specific record byte offsets
- No need to parse preceding records
- Linear speedup with CPU cores (up to I/O limits)

## Real-World Examples

### Example 1: Retail Transaction Data (3 MB, 588 Records)

```bash
# Build index for a daily point-of-sale journal
sumpter index build retail-journal.xml \
  --selector "//Transaction" \
  --output retail.recordindex.json
```

**Results:**

- Records: 588 transactions
- Total size: 3.08 MB
- Average record: 5.36 KB
- Build time: 73 ms
- Index size: 47 KB

**Verification:**

```bash
sumpter index verify retail-journal.xml \
  --index retail.recordindex.json \
  --verify-records
```

Output:

```
✓ Index verification passed
  Source size: match
  Source hash: match
  Records verified: 588
```

### Example 2: Large Public Variant Data (4.7 GB Archive, 3.7M Records)

```bash
# Decompress the public archive before indexing
gunzip -c ClinVarVCVRelease_00-latest.xml.gz > ClinVarVCVRelease_00-latest.xml

# Build index for ClinVar release (JSON format)
sumpter index build ClinVarVCVRelease_00-latest.xml \
  --selector "//VariationArchive" \
  --output ClinVarVCVRelease.recordindex.json \
  --progress \
  --allow-large-files

# Or use seekable-zstd for 10-20x smaller index (v0.1.2+)
sumpter index build ClinVarVCVRelease_00-latest.xml \
  --selector "//VariationArchive" \
  --emit-szst \
  --emit-json=false \
  --progress \
  --allow-large-files
# Creates: ~50-100 MB compressed index instead of 1 GB JSON
```

**Results:**

- Compressed: 4.7 GB (.gz)
- Uncompressed: 59.1 GB
- Records: 3,772,454 variants
- Average record: 16.05 KB
- Min/Max: 1.94 KB / 2.97 MB
- Build time: 17 minutes 45 seconds
- Throughput: ~213,000 records/minute
- Index file: 1.0 GB (JSON) or ~50-100 MB (seekable-zstd)

**Record Size Distribution:**

- P50 (median): 12.2 KB
- P95: 33.2 KB
- P99: 63.9 KB

**Memory Efficiency:**
Traditional DOM-based extraction of this file would require ~111 GB of RAM (1.88x the uncompressed size). Sumpter's streaming architecture with index uses <100 MB.

## Index File Format

Sumpter supports two index formats:

| Format        | Extension                   | Use Case                                |
| ------------- | --------------------------- | --------------------------------------- |
| JSON          | `*.recordindex.json`        | Default, human-readable, debuggable     |
| Seekable-Zstd | `*.recordindex.header.json` | Large indexes (1M+ records), compressed |

### JSON Format Structure

```json
{
  "version": "record-index/v0.1.1",
  "source": {
    "path": "/path/to/source.xml",
    "size_bytes": 3235840,
    "sha256": "a7f3e9d2...",
    "compressed": false,
    "compression_format": "none",
    "offset_kind": "source_bytes",
    "created_at": "2024-11-17T12:35:41Z"
  },
  "selector": {
    "xpath": "//SaleEvent",
    "element_name": "SaleEvent"
  },
  "records": [
    {
      "record_num": 1,
      "start_offset": 1247,
      "end_offset": 6891,
      "size_bytes": 5644,
      "sha256": "b3d9c1a8...",
      "element_name": "SaleEvent",
      "depth": 2
    }
  ],
  "summary": {
    "total_records": 588,
    "total_bytes": 3081472,
    "avg_record_size_bytes": 5238.7,
    "min_record_size_bytes": 2568,
    "max_record_size_bytes": 17345
  },
  "metadata": {
    "generator": "sumpter index build v0.1.2",
    "build_duration_ms": 73,
    "sumpter_version": "v0.1.2"
  }
}
```

### Fields

**source**: Immutable metadata about the indexed file

- Changing the source file invalidates the index
- Verification compares current file against these values

**selector**: XPath and extracted element name

- Used to locate record boundaries
- Must match the extraction recipe's record selector

**records**: Array of record metadata (can be very large)

- Byte offsets enable seekable access
- SHA-256 allows per-record integrity checking
- Sorted by `record_num` (1-indexed)

**summary**: Statistical overview

- Helps estimate extraction performance
- Exact percentiles show data distribution when explicitly requested
- Useful for capacity planning

## Seekable-Zstd Format (v0.1.2+)

For extreme-scale indexes (millions of records), Sumpter supports a compressed binary format using seekable-zstd. This format dramatically reduces disk usage while enabling parallel random access.

### Why Seekable-Zstd?

| Metric                   | JSON Format     | Seekable-Zstd | Improvement    |
| ------------------------ | --------------- | ------------- | -------------- |
| Disk Size (3.7M records) | ~1 GB           | ~50-100 MB    | 10-20x smaller |
| Memory (streaming)       | Constant        | Constant      | Same           |
| Random Access            | Sequential scan | O(1) seek     | Parallel-ready |

### Output Files

The seekable-zstd format uses two files:

```
source.recordindex.header.json   # Metadata (~1 KB)
source.recordindex.records.szst  # Binary records (compressed)
```

**Header file** contains:

- Source file metadata (path, size, SHA-256)
- Selector information
- Summary statistics
- Records encoding metadata (width, count, endianness)

**Records file** contains:

- Fixed-width binary records (64 bytes each)
- Seekable-zstd compression with frame-level random access

### Building Seekable-Zstd Indexes

```bash
# Build with seekable-zstd output
sumpter index build clinvar.xml \
  --selector "//VariationArchive" \
  --emit-szst \
  --progress

# Output:
#   clinvar.recordindex.header.json
#   clinvar.recordindex.records.szst

# Build both formats (JSON + szst) for transition
sumpter index build data.xml \
  --selector "//Record" \
  --emit-json \
  --emit-szst
```

### Using Seekable-Zstd Indexes

All commands auto-detect the format by extension:

```bash
# Stream-walk seekable-zstd index
sumpter index stream data.recordindex.header.json

# Verify with seekable-zstd index
sumpter index verify data.xml --index data.recordindex.header.json

# Extract with seekable-zstd index (parallel workers)
sumpter extract files \
  --record-index data.recordindex.header.json \
  --workers 8 \
  --output-path outputs/
```

### Build Requirements

Seekable-zstd requires CGO. Build with:

```bash
CGO_ENABLED=1 go build -tags seekablezstd ./cmd/sumpter
```

**Platform Support:**

- Linux glibc (amd64): Pre-built static library
- Linux musl (amd64): Pre-built static library
- macOS: Requires building seekable-zstd from source
- Windows: Not yet supported

Without CGO, Sumpter falls back to JSON-only mode.

### Container Deployment

```dockerfile
# Dockerfile with seekable-zstd support
FROM golang:1.26-bookworm AS builder
WORKDIR /build
COPY . .
RUN CGO_ENABLED=1 go build -tags seekablezstd -o sumpter ./cmd/sumpter

FROM debian:bookworm-slim
COPY --from=builder /build/sumpter /usr/local/bin/
ENTRYPOINT ["sumpter"]
```

## Compression Support

### Supported Formats

- **gzip** (.gz): Detected and rejected for index builds; decompress before indexing
- **bzip2** (.bz2): Detected and rejected for index builds
- **xz** (.xz): Detected and rejected for index builds

### Compression Workflow

```bash
# Decompress first, then index source bytes
gunzip -c compressed.xml.gz > compressed.xml
sumpter index build compressed.xml \
  --selector "//Record" \
  --output compressed.recordindex.json
```

**Process:**

1. Detect compression from file extension
2. Reject compressed input before hashing, scanning, or writing an index
3. Decompress to a normal XML file
4. Build index from source-byte offsets
5. Mark `source.compressed = false` and `source.offset_kind = "source_bytes"` in index

**Important:** Byte offsets in supported indexes refer to bytes in the indexed source file. Verification and parallel extraction refuse indexes marked as compressed or `offset_kind = "decompressed_bytes"`.

## Integration with Extract Workflow

### Recipe-Based Extraction

Indexes work alongside recipe manifests:

```yaml
# recipe.yaml
recipe_id: "retail_daily_sales_v1"
name: "Retail Daily Sales"
record_selector: "//Transaction"

extraction:
  signature_config: "signature/retail-journal-signature.yaml"
  extract_config: "extract/retail-journal-extract.yaml"

indexes:
  default: "indexes/retail.recordindex.json"
  # Reference pre-built indexes for test data
```

Run with an index:

```bash
sumpter recipes run extract ./recipes/retail-daily-sales \
  --input-path testdata/transactions.xml \
  --progress
```

### Parallel Extraction Workflow

**Full parallel workflow:**

```bash
# 1. Build index (one-time)
sumpter index build source.xml \
  --selector "//Transaction" \
  --output source.recordindex.json

# 2. Verify index integrity
sumpter index verify source.xml \
  --index source.recordindex.json

# 3. Extract with parallel workers
sumpter extract files \
  --signature-config-path configs/signature.yaml \
  --extract-config-path configs/extract.yaml \
  --files source.xml \
  --record-index source.recordindex.json \
  --workers 8 \
  --output-path outputs/
```

**Worker Pool Sizing:**

| Workers | Use Case                                |
| ------- | --------------------------------------- |
| 1       | Sequential processing (default)         |
| 4       | Balanced for most workloads             |
| 8-16    | CPU-bound extraction logic              |
| 16+     | Very large files with simple extraction |

Performance scales linearly up to I/O saturation or CPU core count.

## Best Practices

### Index Storage

**Location:**

```
project/
├── data/
│   └── source.xml
├── indexes/
│   └── source.recordindex.json    # Keep indexes separate
└── recipes/
    └── extraction-recipe/
```

**Version Control:**

- Commit indexes for test data (small, stable files)
- Exclude indexes for production data (.gitignore)
- Document index rebuild process in README

### Index Lifecycle

**When to Rebuild:**

- Source file modified (hash mismatch)
- Selector changed (different record boundaries)
- Sumpter version upgrade (format compatibility)
- Compression format changed

**When to Keep:**

- Source file unchanged
- Multiple extraction runs planned
- Sharing work with teammates
- Archival/audit requirements

### Performance Tips

**Index Build:**

- Build on local SSD (not network storage)
- Enable `--progress` for large files
- Use seekable-zstd index storage for large record maps

**Index Usage:**

- Verify before long extraction runs
- Use `--workers` based on available CPU cores
- Monitor memory usage with large indexes (1M+ records)

## Troubleshooting

### Build Failures

**Error:** `XML syntax error on line 1: illegal character code U+001F`

**Cause:** Compressed file was passed where uncompressed XML was expected

**Solution:** Decompress first, then rebuild the index:

```bash
gunzip -c input.xml.gz > input.xml
sumpter index build input.xml --selector "//Record"
```

---

**Error:** `failed to stat input file`

**Cause:** File path incorrect or file doesn't exist

**Solution:** Use absolute paths or verify working directory

### Verification Failures

**Error:** `Source size mismatch`

**Cause:** File modified since index creation

**Solution:** Rebuild index with current file version

---

**Error:** `Source hash mismatch`

**Cause:** File content changed (but size might match)

**Solution:** Rebuild index; investigate if file should have changed

### Extraction Issues

**Error:** `failed to open file: open : no such file or directory`

**Cause:** Index created with relative path, now invalid

**Solution:** Build indexes with absolute paths, or ensure working directory consistency

---

**Error:** Empty records `{}`

**Cause:** XPath expressions in extract config might be document-scoped, not record-scoped

**Solution:** Verify extract config XPaths work within individual record context

## Advanced Usage

### Custom Output Paths

```bash
# Organize indexes by date
sumpter index build data-2024-11-17.xml \
  --selector "//Record" \
  --output indexes/2024/11/data-2024-11-17.recordindex.json
```

### Batch Index Building

```bash
# Index multiple files
for file in data/*.xml; do
  base=$(basename "$file" .xml)
  sumpter index build "$file" \
    --selector "//Transaction" \
    --output "indexes/${base}.recordindex.json" \
    --progress
done
```

### Pipeline Integration

```bash
#!/bin/bash
# Daily extraction pipeline with index verification

INDEX="indexes/daily.recordindex.json"
SOURCE="data/daily.xml"

# Verify index is current
if ! sumpter index verify "$SOURCE" --index "$INDEX" 2>/dev/null; then
  echo "Index stale or missing, rebuilding..."
  sumpter index build "$SOURCE" \
    --selector "//Transaction" \
    --output "$INDEX"
fi

# Extract with verified index
sumpter extract files \
  --signature-config-path configs/signature.yaml \
  --extract-config-path configs/extract.yaml \
  --files "$SOURCE" \
  --record-index "$INDEX" \
  --workers 8 \
  --output-path "outputs/$(date +%Y%m%d).json"
```

## Summary

Record indexes enable efficient processing of large XML files by:

- Mapping record boundaries once, using many times
- Enabling random access without full file parsing
- Supporting parallel extraction with worker pools
- Providing integrity verification at file and record level

Use indexes when file size, record count, or repeated processing justify the one-time build cost. For small files or one-off extractions, sequential processing without an index is simpler and faster.
