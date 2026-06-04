# ADR-0005: Hybrid Streaming XML Architecture for Large File Processing

**Status:** Accepted
**Date:** 2025-10-12
**Deciders:** @3leapsdave (with `devlead` / `entarch` AI contribution)
**Context:** Alpha phase - addressing extreme memory usage on large XML files

## Context

Sumpter's extract command currently loads entire XML files into memory and builds a complete DOM tree for XPath-based field extraction. This architecture works well for moderate files (10-500MB) but fails catastrophically on large datasets:

### The ClinVar Problem

- **Input file**: ClinVar VCV Release (genomics variant classification data)
  - 4.7GB compressed (.xml.gz)
  - ~50GB uncompressed XML
  - ~2 million `<VariationArchive>` records
  - Individual record size: 2-50KB each

- **Current behavior**:
  - Load 50GB XML into memory
  - Build complete DOM tree: **111GB memory consumption**
  - System thrashes/crashes on 64GB machines
  - `--allow-large-files` flag warns but doesn't prevent OOM

- **User expectations**:
  - Process multi-GB biomedical/financial datasets
  - Run on standard dev machines (32-64GB RAM)
  - Extract structured records for analysis pipelines

### Why This Matters

ClinVar represents an **extreme but real** test case that validates architecture for all use cases:

1. **Biomedical**: ClinVar (50GB), UniProt (100GB+), PubMed XML (multi-TB)
2. **Financial**: XBRL instance documents (100MB-5GB)
3. **Retail**: POS transaction journals (50-200MB, 10K-100K transactions)
4. **Regulatory**: SEC EDGAR filings, clinical trial data, legal documents

If we can process ClinVar efficiently, we can handle **any** record-based XML at scale.

## Decision

We will implement a **Hybrid Streaming Architecture** that combines SAX-style streaming for record discovery with DOM parsing for individual records.

## Current contract (v0.1.8 development line)

The accepted architecture describes the intended bounded-memory extraction
model, but the current implementation does not yet make every extract mode
bounded end-to-end. XML input is tokenized incrementally where the streaming
path applies, seekable indexed reads avoid loading predecessor records, and
JSON/NDJSON file output writes through the record-sink path for sequential runs
and record-index parallel runs with bounded reorder/backpressure instead of
retaining the full output slice for that format.

The bounded claim is still intentionally narrow. DOM/non-streaming extraction
can load a whole document, and Parquet, mixed JSON+Parquet, record-index
parallel runs that request buffered formats, and `min_occurrences` recipes
remain buffered in v0.1.8. Public docs should therefore describe the present
contract as "streaming input parsing plus JSON/NDJSON output streaming" rather
than "constant-memory extraction" until the memory-regression fixture lands.

### Architecture Design

```
┌─────────────────────────────────────────────────────────────┐
│                    Input: Large XML File                     │
│                 (4.7GB .gz → 50GB uncompressed)              │
└────────────────────────────┬─────────────────────────────────┘
                             │
                             ▼
                  ┌──────────────────────┐
                  │  Streaming Decoder   │ ◄─── encoding/xml.Decoder
                  │   (SAX-style scan)   │      Token-by-token
                  └──────────┬───────────┘
                             │
                             ▼
              ┌──────────────────────────────┐
              │  Record Boundary Detection   │
              │  Find <VariationArchive>     │ ◄─── Match XPath selector
              │  tags in stream              │      from extract config
              └──────────┬───────────────────┘
                         │
                         ▼
              ┌──────────────────────┐
              │   Buffer Record XML   │
              │   (2-50KB subtree)    │ ◄─── Capture record only
              └──────────┬────────────┘      Not entire file
                         │
                         ▼
              ┌──────────────────────┐
              │  Parse Mini-DOM       │
              │  xmlquery.Parse(...)  │ ◄─── Small DOM: 2-50KB
              └──────────┬────────────┘
                         │
                         ▼
    ┌────────────────────────────────────────┐
    │   UNCHANGED: Existing Extract Engine   │
    │                                         │
    │  • extractRecords() with XPath         │
    │  • Field mappings (polymorphic, etc)   │ ◄─── XPath works on
    │  • Transforms & validation             │      mini-DOM nodes
    │  • Summary generation                  │
    │  • Output formatting                   │
    └────────────────────┬───────────────────┘
                         │
                         ▼
              ┌──────────────────────┐
              │   Emit Record JSON    │
              │   Discard Mini-DOM    │ ◄─── Free memory
              └──────────┬────────────┘      immediately
                         │
                         ▼
              ┌──────────────────────┐
              │   Repeat for Next     │
              │   Record in Stream    │
              └───────────────────────┘

Memory Footprint: ~10-50MB (streaming buffer + one mini-DOM)
vs. Current: 111GB (entire file as full DOM tree)
```

### Key Architectural Principles

1. **One-Pass Streaming**: Process file sequentially, never load complete document
2. **Record-Level DOM**: Parse only individual records as mini-DOM trees
3. **XPath Preservation**: Existing field extraction logic unchanged
4. **Transparent Compression**: Handle .gz/.bz2 streams natively
5. **Progressive Output**: Emit records as they're extracted (no batching)

## Rationale

### Why Hybrid (Not Pure SAX)?

We evaluated three approaches:

#### Option 1: Current DOM (Status Quo)

```
✗ Entire file → Full DOM → Extract all records
Memory: O(file_size × 2-3) = 111GB for 50GB file
```

**Rejected**: Unsustainable for multi-GB files.

#### Option 2: Pure SAX Streaming

```
✓ Token-by-token → Manual state tracking → Build records
Memory: O(max_record_size) = ~50KB constant
```

**Rejected**: Would require:

- Rewriting all XPath-based field mapping logic
- Manual XML path tracking for nested elements
- State machines for complex record structures
- Rewriting every extract config
- High maintenance burden for marginal memory gain (50MB → 10MB)

#### Option 3: Hybrid Streaming (Selected)

```
✓ Stream to records → Mini-DOM per record → Existing XPath extraction
Memory: O(stream_buffer + max_record_size) = ~50MB
```

**Accepted**: Best trade-off:

- ✅ 99.95% memory reduction (111GB → 50MB)
- ✅ Zero changes to extract configs
- ✅ Preserves XPath field mapping logic
- ✅ One-pass streaming (no temp files)
- ✅ Manageable implementation scope (~8-10 hours)

### Memory Analysis

**Current Architecture**:

```
50GB XML file
→ 50GB in memory (file content)
→ +61GB DOM structure (nodes, pointers, attributes)
→ = 111GB peak memory
```

**Hybrid Architecture**:

```
50GB XML file
→ 20MB streaming buffer (decompression + record buffering)
→ 50KB mini-DOM (largest single record)
→ +10MB extraction overhead (field mapping, transforms)
→ = ~30-50MB peak memory
```

**Reduction**: 111GB → 50MB = **99.95% memory savings**

### Why This Works for All Use Cases

The hybrid approach succeeds because:

1. **Record Size Reality**: XML records are typically small
   - ClinVar `<VariationArchive>`: 2-50KB
   - XBRL facts/contexts: 1-20KB
   - Retail POS transactions: 0.5-5KB
   - Even complex nested records: <500KB

2. **DOM-per-Record is Cheap**:
   - 50KB record → 100-200KB mini-DOM
   - Parse time: <1ms per record
   - Memory freed immediately after extraction

3. **XPath Needs Context**:
   - Field extraction: `./GeneList/Gene/@Symbol`
   - Polymorphic arrays: `./Entry/TypeA | ./Entry/TypeB`
   - These require navigable tree structure (DOM provides this)

### Single-Pass Streaming

**Common Misconception**: "Streaming requires multiple file passes"

**Reality**: Hybrid uses **one sequential pass**:

```go
Open file stream
↓
for each record in stream {
    buffer = captureNextRecord(stream)  // Still streaming
    miniDOM = parse(buffer)             // Small memory spike
    result = extract(miniDOM)           // Existing logic
    emit(result)                        // Output record
    free(miniDOM)                       // Immediate cleanup
}
↓
Close stream (never loaded full file)
```

**File is never** rewound, never loaded completely, never buffered to disk.

## Implementation Plan

### Phase 1: Streaming Infrastructure

**New Package**: `internal/extract/streaming/`

**Files**:

- `scanner.go` - Record boundary scanner using `encoding/xml.Decoder`
- `types.go` - Data structures for streaming
- `scanner_test.go` - Unit tests

**Key Types**:

```go
type RecordScanner struct {
    decoder        *xml.Decoder
    recordSelector string  // XPath for record boundary
    buffer         *bytes.Buffer
    depth          int     // Track XML nesting depth
}

func NewRecordScanner(reader io.Reader, recordSelector string) *RecordScanner
func (s *RecordScanner) Next() (recordXML string, error)
func (s *RecordScanner) Close() error
```

### Phase 2: Extractor Integration

**Modify**: `internal/extract/extractor.go`

**Changes**:

1. Add `ProcessFileStreaming()` function (new)
2. Modify `ProcessFile()` to conditionally use streaming for large files
3. Keep all existing extraction logic unchanged

**Decision Logic**:

```go
if allowLargeFiles && estimatedSize > 1GB {
    return ProcessFileStreaming(...)  // New path
} else {
    return ProcessFile(...)  // Existing path unchanged
}
```

### Phase 3: Configuration Support

**Update**: `schemas/extract/v0.1.0/extract-record-match-schema.yaml`

**Add streaming hints** (optional optimization):

```yaml
streaming_hints:
  record_selector: "//VariationArchive" # Override auto-detection
  estimated_record_size_kb: 50 # Buffer sizing hint
```

**Default behavior**: Use first `match_selector` XPath as record boundary.

### Phase 4: Testing Strategy

**Unit Tests**:

- `TestRecordScanner_BasicScan` - Scan 10 records from test XML
- `TestRecordScanner_CompressedStream` - Handle .gz input
- `TestRecordScanner_MalformedBoundary` - Error handling
- `TestRecordScanner_LargeRecords` - 500KB record buffer

**Integration Tests**:

- `TestProcessFileStreaming_vs_ProcessFile` - Identical outputs
- `TestProcessFileStreaming_MemoryUsage` - Verify <100MB for 2GB file
- `TestProcessFileStreaming_Progress` - Stream progress reporting

**Real-World Validation**:

```bash
# ClinVar extraction (50GB uncompressed)
sumpter --allow-large-files recipes run extract clinvar-recipe

# Expected: <50MB memory, completes successfully
# vs Current: 111GB memory, crashes
```

## Consequences

### Positive

- ✅ **99.95% memory reduction** (111GB → 50MB for ClinVar)
- ✅ **Process files 50x larger than RAM** (process 50GB on 1GB machine)
- ✅ **Zero extract config changes** (all XPath mappings work unchanged)
- ✅ **One-pass streaming** (no temp files, no multi-pass overhead)
- ✅ **Works for all record-based XML** (XBRL, clinical, genomics, retail journals)
- ✅ **Progressive output** (start emitting records immediately)
- ✅ **Transparent compression** (handles .gz/.bz2 natively)

### Negative

- ⚠️ **Slight latency increase** (~10-20% slower due to streaming overhead)
- ⚠️ **Requires record structure** (doesn't help with document-oriented XML)
- ⚠️ **Signature matching complexity** (need to check first record, not full document)
- ⚠️ **Additional code path** (streaming vs non-streaming - two modes to maintain)

### Neutral

- 🔄 **Documentation needed** for record selector configuration
- 🔄 **`--allow-large-files` semantics change** (now enables streaming, not just warning)
- 🔄 **Testing burden** (need to validate streaming vs non-streaming equivalence)

## Alternatives Considered

### Alternative 1: Memory-Mapped Files

**Approach**: Use `mmap()` to page file contents on-demand.

**Rejected**:

- Still requires DOM building (doesn't solve 111GB problem)
- OS page cache thrashing with multi-GB files
- Not portable across platforms
- Doesn't help with compressed files

### Alternative 2: External Database (SQLite/DuckDB)

**Approach**: Load XML into embedded database, query with SQL.

**Rejected**:

- Requires two-pass processing (load DB, then query)
- Disk I/O becomes bottleneck
- Temporary storage requirements (50GB → 100GB on disk)
- Doesn't align with streaming philosophy

### Alternative 3: Split File Pre-Processing

**Approach**: Split large XML into smaller files, process separately.

**Rejected**:

- Requires preprocessing step (user workflow friction)
- Loses atomicity (partial failures harder to handle)
- Doesn't solve general problem (what if one record is huge?)

## Migration Path

### For Existing Users

**No action required**:

- Streaming automatically enabled for files >1GB when `--allow-large-files` set
- Existing extract configs work unchanged
- Output format identical to non-streaming mode

### For New Users

**Best practices**:

- Test extract configs on sample files (<1GB) without streaming
- Enable `--allow-large-files` for production-scale data
- Monitor memory usage to verify streaming is active

### Future Enhancements

**Potential optimizations**:

- Parallel record processing (worker pool)
- Adaptive buffering based on detected record sizes
- Streaming signature matching (partial document checks)
- Support for non-record-based streaming (pure SAX fallback)

## Success Metrics

1. ✅ **Memory**: <100MB for 50GB ClinVar file (target: 50MB)
2. ✅ **Correctness**: Streaming output === non-streaming output (byte-for-byte)
3. ✅ **Performance**: <2x slowdown vs full-file loading
4. ✅ **Scale**: Successfully process 100GB synthetic XML on 8GB machine
5. ✅ **Compatibility**: All existing extract configs work unchanged

## References

- **Test Case**: ClinVar VCVRelease XML (ncbi.nlm.nih.gov/clinvar)
  - Real-world genomics dataset: 4.7GB compressed, 50GB uncompressed
  - 2M+ variant classification records
  - Complex nested structure with XPath requirements

- **Similar Datasets**:
  - XBRL instance documents (100MB-5GB financial filings)
  - POS transaction journals (50-200MB retail data)
  - PubMed XML (multi-TB biomedical literature)

- **Go Streaming APIs**:
  - `encoding/xml.Decoder` - SAX-style token parser
  - `compress/gzip.Reader` - Streaming decompression
  - `github.com/antchfx/xmlquery` - DOM parsing for subtrees

## Notes

This ADR reflects Alpha phase architectural evolution: We built a working extract engine with XPath-based field mapping that handles moderate files beautifully. ClinVar's extreme scale (50GB) validates our architecture can scale to **any** real-world use case while preserving the elegant XPath-based configuration model that makes Sumpter powerful and maintainable.

The hybrid approach is the **pragmatic choice**: massive memory savings with minimal code complexity, no user-facing changes, and a clear path to pure SAX if needed in the future.

---

**Generated by an AI agent under supervision of @3leapsdave (see `docs/standards/agentic-attribution.md`)**
