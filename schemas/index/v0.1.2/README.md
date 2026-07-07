# Record Index Schema v0.1.2

## Overview

The XML Record Index schema stores record boundary metadata for seekable
parallel extraction. Version v0.1.2 adds namespace-context capture so indexed
record fragments can be parsed with the same namespace URI semantics as
whole-document and streaming extraction.

## Changes from v0.1.1

- `namespace_contexts`: a compact, index-level table of in-scope namespace
  declarations captured at record boundaries.
- `records[].namespace_context_ref`: a per-record integer reference into that
  table.
- Namespace-bound extraction against an older index fails with rebuild guidance.
  Namespace-free extraction can continue to read v0.1.0 and v0.1.1 indexes.

## Namespace Context Semantics

Record-boundary selectors remain local-name-only (`Record` or `//Record`).
Namespace binding applies inside the selected record. During index build,
Sumpter records the namespace declarations in scope at each record root,
deduplicates those declaration sets, and persists them in `namespace_contexts`.

During indexed extraction, Sumpter adds missing declarations from the referenced
context onto the sliced record root before parsing it. Declarations already
present on the record root win, so root-local prefix shadowing and default
namespace undeclarations are preserved. Namespace URI values are XML-escaped
before reinjection; they are inert match keys and are never dereferenced.

## Top-Level Fields

- `version`: Schema version identifier (`record-index/v0.1.2`)
- `source`: Source XML file metadata, integrity information, and offset semantics
- `selector`: Local-name record-boundary selector configuration
- `namespace_contexts`: Deduplicated namespace contexts
- `records`: Record metadata with offsets, checksums, and namespace context refs
- `summary`: Aggregate statistics across all records
- `metadata`: Index build process metadata

## Record Fields

- `record_num`: Sequential 1-based record number
- `start_offset`: Byte offset where record begins in `source.offset_kind`
- `end_offset`: Byte offset where record ends in `source.offset_kind`
- `size_bytes`: Record size in bytes
- `sha256`: SHA-256 hash of record XML content
- `element_name`: Root element local name
- `depth`: XML nesting depth
- `namespace_context_ref`: Reference into `namespace_contexts`

## Seekable-Zstd Store

The seekable-zstd companion store uses header version
`record-index-szst/v0.1.1` with the same `namespace_contexts` table in
`*.recordindex.header.json`. Binary record rows are 68 bytes:

- 8 bytes `start_offset`
- 8 bytes `end_offset`
- 8 bytes `size_bytes`
- 4 bytes `depth`
- 4 bytes `record_num`
- 32 bytes raw SHA-256
- 4 bytes `namespace_context_ref`

Readers accept legacy 64-byte rows for namespace-free compatibility. Namespace-
bound extraction requires the v0.1.2 JSON schema or a seekable-zstd header that
contains `namespace_contexts`.
