# Record Index Schema v0.1.1

## Overview

The XML Record Index schema stores record boundary metadata for seekable parallel extraction of large XML files with integrity verification. Version v0.1.1 makes the record offset coordinate space explicit with `source.offset_kind`.

## Source Offset Semantics

- `offset_kind: "source_bytes"` means `start_offset` and `end_offset` address bytes in the indexed source file.
- `offset_kind: "decompressed_bytes"` is reserved for legacy or future decompressed-stream indexes and is refused by seekable verification and parallel extraction.
- New Sumpter JSON indexes emit `record-index/v0.1.1` with `offset_kind: "source_bytes"`.
- Legacy `record-index/v0.1.0` indexes without `offset_kind` are read as `source_bytes`.

Build indexes from uncompressed XML. For gzip archives, use:

```bash
gunzip -c input.xml.gz > input.xml
sumpter index build input.xml --selector "//Record"
```

## Top-Level Fields

- `version`: Schema version identifier (`record-index/v0.1.1`)
- `source`: Source XML file metadata, integrity information, and offset semantics
- `selector`: Record boundary selector configuration
- `records`: Array of record metadata (offsets, sizes, checksums)
- `summary`: Aggregate statistics across all records
- `metadata`: Index build process metadata

## Source Block

- `path`: Path to source XML file
- `size_bytes`: Total file size
- `sha256`: SHA-256 hash of entire source file
- `compressed`: Whether the indexed source file was compressed
- `compression_format`: Compression format (`gzip`, `bzip2`, `xz`, `none`)
- `offset_kind`: Offset coordinate space (`source_bytes`, `decompressed_bytes`)
- `created_at`: ISO 8601 timestamp of index creation
- `encoding`: Detected character encoding

## Record Fields

- `record_num`: Sequential 1-based record number
- `start_offset`: Byte offset where record begins in `source.offset_kind`
- `end_offset`: Byte offset where record ends in `source.offset_kind`
- `size_bytes`: Record size in bytes
- `sha256`: SHA-256 hash of record XML content
- `element_name`: Root element name
- `depth`: XML nesting depth
