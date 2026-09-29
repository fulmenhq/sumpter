# Record Index Schema v0.1.3

## Overview

Version v0.1.3 makes the record index self-describing about its source
syntax, so one index container serves XML and JSON sources. Every index built
by this release uses v0.1.3.

## Changes from v0.1.2

- `source.format`: required, closed token `xml` or `json`. Readers route on it
  and never infer a format from a file name. Indexes from v0.1.0 through v0.1.2
  carry no `source.format` and are XML only; a v0.1.0–v0.1.2 header that
  declares a format is refused.
- The seekable-zstd store header moves to `record-index-szst/v0.1.2` and is
  described by `szst-header.schema.json` in this directory.

## JSON Sources

For `source.format: json`, each record is a JSON value in the uncompressed
source. `start_offset` and `end_offset` are absolute offsets in the source
file (a leading byte order mark counts toward offsets but is never inside a
record), and `records[].sha256` covers exactly `source[start_offset:end_offset]`.
Records are numbered in document order; a record may contain other records of
the same name, and their ranges then nest. `element_name` is the name the
selector matched and `depth` its depth in the document node model. JSON
records have no XML namespaces: `namespace_contexts` holds the single empty
context `0`, and every `namespace_context_ref` is `0`.

## Seekable-Zstd Store

`record-index-szst/v0.1.2` headers describe the binary rows in a nested
`records.layout` object (`width_bytes: 68`, `sha_encoding: raw32`,
`endianness: little`) and carry none of the flat `record_width_bytes`,
`sha_encoding` or `endianness` fields of earlier headers, so releases before
v0.1.3 refuse them at open. `records.records_file` is a single file name in
the header's directory. The 68-byte row layout is unchanged:

- 8 bytes `start_offset`
- 8 bytes `end_offset`
- 8 bytes `size_bytes`
- 4 bytes `depth`
- 4 bytes `record_num`
- 32 bytes raw SHA-256
- 4 bytes `namespace_context_ref`

The binary rows carry no per-record element name; readers take it from
`selector.element_name`.

## Compatibility

Indexes built by this release are not readable by earlier releases; rebuild
them with `sumpter index build` to use an earlier release. This release reads
indexes from v0.1.0 through v0.1.3 and `record-index-szst/v0.1.0` through
`v0.1.2`.
