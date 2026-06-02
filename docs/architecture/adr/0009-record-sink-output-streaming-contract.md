# ADR-0009: RecordSink Contract for Output Streaming

**Status:** Proposed (SUM-027 PR-A; entarch sign-off required)
**Date:** 2026-06-01
**Decision Makers:** @3leapsdave, India devlead/devrev, entarch review

## Context

Sumpter's extract paths now preserve source-document emission order and
`_runtime.record_num` semantics across regular DOM, streaming, and indexed
parallel extraction. That SUM-034 contract is part of the public emitted-record
surface:

- records are emitted in source-document order for single record-boundary
  selectors;
- `_runtime.record_num` is 1-based and assigned before filters run; and
- filtered or skipped records leave gaps rather than causing renumbering.

The remaining memory limitation is on the output side. XML input scanning is
incremental, and indexed parallel extraction can seek to records without loading
predecessor records, but command execution still collects emitted records in
memory before writing JSONL, manifests, and optional Parquet output. In the
parallel path, workers produce ordered results through an aggregator, and the
orchestrator still returns a full slice to the command layer.

SUM-027 changes that architecture. This ADR is PR-A: it defines the
`RecordSink` contract before implementation touches the extractor, command
writer, parallel aggregator, manifests, or Parquet adapter.

## Decision

Introduce an extraction output streaming contract centered on a `RecordSink`.
Implementation PRs must conform to this contract.

### Emitted Record Envelope

`RecordSink` receives an already-enriched emitted record envelope. It does not
receive a raw mutable extraction-data map.

The envelope must include the same sections the durable JSONL stream emits
today, including:

- `_runtime`, including `record_num`;
- optional `_validation`;
- `extract.summary`, when enabled; and
- `extract.data`.

The caller owns enrichment before handing the envelope to a sink. A sink may
serialize, count, validate, or project the envelope, but it must not mutate the
envelope in a way visible to later sinks or accounting.

The implementation may start with `map[string]interface{}` plus a defensive
copy if that is the least disruptive path, but the public contract is an
immutable emitted envelope. A later typed wrapper is acceptable if it preserves
the same JSON surface.

### Interface Shape

The implementation should use this shape, adjusted only for concrete package
names and existing error conventions:

```go
type RecordSink interface {
    OnRecord(ctx context.Context, record EmittedRecord) error
    OnFileBoundary(ctx context.Context, summary FileEmissionSummary) error
    Close(ctx context.Context) error
}
```

`OnRecord` is called once for each successfully extracted emitted record in
final output order.

`OnFileBoundary` is called after all records for one source file have either
been emitted or the file has failed. The summary carries counts and file
disposition, not buffered records.

`Close` finalizes durable output, manifests, and any buffered writer state. It
is idempotent and must be called on every return path after sink construction,
including extraction errors and sink errors.

### Ordering and `record_num`

The sink contract preserves SUM-034:

- single-selector extraction emits records in source-document order;
- `_runtime.record_num` is a 1-based source position assigned before filters;
- filtered records, oversized skipped records, and failed records consume their
  source position and leave gaps; and
- surviving emitted records are never renumbered by the sink.

For multi-selector recipes, the v0.1.7 contract remains selector-major unless a
future ADR changes global ordering semantics.

### Backpressure

Backpressure must be explicit and bounded. Either of these designs is allowed:

- synchronous `OnRecord` calls, where slow output blocks extraction directly; or
- a bounded queue between extractors/aggregators and sink writers, where a full
  queue blocks upstream work and cancellation propagates.

Unbounded result slices, unbounded maps of out-of-order records, and
fire-and-forget writer goroutines are not allowed. Slow output must throttle
extraction rather than allowing memory to grow with result count.

### Sequential Extraction

Sequential DOM and streaming extraction should enrich one record at a time and
call `OnRecord` immediately after validation, filtering, provenance enrichment,
and summary assembly for that record are complete.

The old `ExtractResult.Records` slice may remain for compatibility during a
transition, but the streaming command path must not depend on collecting every
record before writing JSONL.

### Indexed Parallel Extraction

The parallel aggregator's responsibility changes from "collect all ordered
results" to "emit ordered results to a sink." It may buffer out-of-order worker
results only within a bounded policy. When an earlier record is skipped or
fails, the aggregator advances past that source position without waiting for a
missing emitted envelope and without renumbering later records.

Worker completion order must not affect output order.

### Failure Semantics

Extraction failures and output failures are distinct:

- `continue-on-error` may cover file applicability, source parsing, validation,
  record extraction, and min-occurrence failures according to the existing
  command semantics.
- Sink write, flush, manifest, and finalize errors are output failures and are
  fatal by default. The command must not report a successful extraction when
  durable output may be corrupt or incomplete.
- If `OnRecord` returns an error, upstream extraction stops through normal
  cancellation/error propagation.
- `Close` still runs after a sink error so file handles and partial output state
  can be finalized or cleaned up consistently.

### Manifest and Disposition Accounting

Manifest and disposition accounting is incremental. Implementations track
counts, source file summaries, output paths, and disposition rows. They do not
retain emitted records for end-of-run accounting.

The empty-output contract from ADR-0007 still applies: a successful zero-record
source writes the requested zero-record artifact and records `RecordCount: 0`
in the manifest.

### Parquet

JSONL/NDJSON is the primary SUM-027 bounded-memory target.

Parquet is an explicit exception unless an implementation PR adds true
incremental Parquet row-group writing. If Parquet remains buffered, public
memory claims must say bounded end-to-end memory applies to JSONL/NDJSON output
and that Parquet may buffer projection rows.

Parquet output continues to project `extract.data` only; it does not carry the
full emitted envelope.

### Relationship to SUM-032

`RecordSink` and SUM-032's planned index-writer contract should use compatible
patterns:

- immutable event/envelope input;
- bounded backpressure;
- idempotent close/finalize; and
- explicit fatal output errors.

They do not need to share one interface. Prematurely merging record-output and
index-output abstractions is out of scope.

## Consequences

### Positive

- JSONL/NDJSON output can become bounded in memory with respect to result count.
- Output order and `_runtime.record_num` stay stable while implementation moves
  from slice-returning APIs to writer callbacks.
- Slow or failing output becomes visible to extraction instead of being hidden
  behind post-processing after a full in-memory result set.
- Manifest and disposition writers can account incrementally.

### Negative / Tradeoffs

- The command layer needs a larger refactor because writing, manifest
  accounting, and disposition summaries are currently coupled to
  `ExtractResult.Records`.
- Parallel extraction needs a bounded ordering policy rather than the current
  unbounded map plus final slice collection.
- Parquet either needs its own incremental writer work or must remain a clearly
  documented buffered exception.

## Acceptance for Implementation PRs

- Sequential and streaming JSONL paths call a sink without full-result
  buffering.
- Indexed parallel extraction emits ordered records to a sink with bounded
  buffering and preserves `record_num` gaps for skipped or failed earlier
  records.
- Sink write/finalize failures stop the command and do not get hidden by
  `continue-on-error`.
- `Close` is idempotent and exercised on success, extraction error, and sink
  error return paths.
- Manifest, failure, and disposition outputs are written from incremental
  counters and summaries, not emitted-record slices.
- Tests assert final emitted envelopes for sequential, streaming, and indexed
  parallel paths, including ordering, filter gaps, skipped/failed early records,
  `record_num`, sink error propagation, and finalize behavior.
- A many-record fixture or benchmark demonstrates JSONL/NDJSON memory behavior
  without making RSS-gating mandatory in regular CI.
- Public docs scope bounded-memory claims to formats actually implemented.

## References

- SUM-027: RecordSink / output streaming
- SUM-034: document-order emission and `_runtime.record_num`
- SUM-032: streaming index writing and integrity
- ADR-0005: Hybrid Streaming XML Architecture for Large File Processing
- ADR-0006: Extraction Provenance
- ADR-0007: Empty Output Files Are the Zero-Record Extract Contract
