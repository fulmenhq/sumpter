# Record Index Format

This document describes the durable record-index contract used by indexed and
parallel extraction.

## Current JSON Schema

The current JSON schema is `record-index/v0.1.3` at
`schemas/index/v0.1.3/record-index.schema.json`. Every index built by this
release uses it.

`record-index/v0.1.3` requires `source.format`, a closed token `xml` or
`json`, and readers route on it; a format is never inferred from a file name.
Sumpter continues to read `record-index/v0.1.0` through `v0.1.2`, which carry
no `source.format` and are XML only. A legacy header that declares a format,
an unknown version, or an unknown format is refused when the index is opened.
Namespace-bound extraction requires namespace context data and fails loudly
on older indexes with rebuild guidance.

A current header must carry a non-empty `namespace_contexts` table with
unique context ids, writes an empty namespace context as `"declarations": []`,
and is refused if the table is missing or empty or a context carries `null`.
Headers from earlier releases may omit the table or carry `null`, which reads
as an empty context.

## Namespace Context Table

`record-index/v0.1.2` added a compact namespace-context table:

```json
{
  "namespace_contexts": [
    {
      "id": 0,
      "declarations": [
        { "prefix": "", "uri": "urn:example:sumpter-records" },
        { "prefix": "ext", "uri": "urn:example:sumpter-records-ext" }
      ]
    }
  ],
  "records": [
    {
      "record_num": 1,
      "start_offset": 1247,
      "end_offset": 6891,
      "size_bytes": 5644,
      "sha256": "b3d9c1a8...",
      "element_name": "Record",
      "depth": 2,
      "namespace_context_ref": 0
    }
  ]
}
```

The table is deduplicated across records. Large files commonly have one root
namespace context shared by millions of records, so each record stores only a
small integer reference. Prefix shadowing creates additional table entries only
for the records that need them.

## Indexed Extraction Semantics

Record-boundary selection remains local-name-only in streaming and indexed
paths: `Record` and `//Record` are supported. Namespace URI binding applies to
match selectors and field mappings inside the selected record.

When indexed extraction reads a record slice, Sumpter looks up
`namespace_context_ref` and adds any missing namespace declarations to the
fragment root before parsing. Existing declarations on the fragment root are not
duplicated or overridden, so record-local declarations preserve shadowing and
default namespace undeclarations. Namespace URI values are XML-escaped before
insertion and are treated only as match keys.

## JSON Sources

`sumpter index build --input-format json` indexes an uncompressed JSON
source. The selector is a record selector (`Name` or `//Name`) with the same
meaning as on the streaming route, and each selected element is a record.
`start_offset` and `end_offset` are absolute offsets in the source file (a
leading byte order mark counts toward offsets but is never inside a record),
and `sha256` covers exactly `source[start_offset:end_offset]`. Records are
numbered in document order, and records of the same name may nest.
`namespace_contexts` is exactly the single empty context `0`, and every
`namespace_context_ref` is `0`; any other table is refused when the index is
opened. `ndjson` sources are not indexed in this
release.

`sumpter index verify --input-format json` scans the source again with the
index's selector and requires every record's number, range, size, name and
depth, the record count, and every record hash to match.

`extract --record-index` over a JSON source needs a signature with
`match_scope: record` and a match selector equal to the index's selector.
Each record's bytes are read from the source file opened once for the run
and checked against the index hash before they are parsed; the record-scoped
signature is scored on each record; and the first failure in record order
fails the input with no rows published. A JSON record above the size limit
fails the input: `--max-record-size-mb` defaults to 100 MiB for JSON when it
is 0, and `--skip-large-records` does not apply. `--verify-index` also hashes
the whole source before and after the run. It proves integrity, not
selection: an index whose selection was forged consistently is detected by
`index verify`, not by extraction.

## Stale Index Behavior

Namespace-bound recipes need context data. If an index lacks
`namespace_contexts`, Sumpter refuses the run and tells the user to rebuild the
index with `record-index/v0.1.2` or newer. Namespace-free recipes keep the
legacy behavior for v0.1.0 and v0.1.1 indexes.

## Seekable-Zstd Format

The seekable-zstd store has two files:

- `*.recordindex.header.json`
- `*.recordindex.records.szst`

Headers written by this release use `record-index-szst/v0.1.2`, described by
`schemas/index/v0.1.3/szst-header.schema.json`. The header carries the same
`source.format` and `namespace_contexts` as the JSON schema and describes the
binary rows in a nested object:

```json
"records": {
  "record_count": 3700000,
  "layout": { "width_bytes": 68, "sha_encoding": "raw32", "endianness": "little" },
  "records_file": "clinvar.recordindex.records.szst"
}
```

A v0.1.2 header carries none of the flat `record_width_bytes`,
`sha_encoding` or `endianness` fields of earlier headers. `records_file` must
be a single file name in the header's directory; it is opened relative to
that directory without following a symbolic link and must be a regular file.
The binary rows carry no element name, so readers take it from
`selector.element_name`. Each binary record row is 68 bytes:

| Offset | Width | Field                   |
| ------ | ----- | ----------------------- |
| 0      | 8     | `start_offset`          |
| 8      | 8     | `end_offset`            |
| 16     | 8     | `size_bytes`            |
| 24     | 4     | `depth`                 |
| 28     | 4     | `record_num`            |
| 32     | 32    | raw SHA-256             |
| 64     | 4     | `namespace_context_ref` |

Headers from earlier releases (`record-index-szst/v0.1.1` and `v0.1.0`) use
the flat layout fields, are XML only, and remain readable. Readers accept
legacy 64-byte rows for namespace-free compatibility. A namespace-bound run
still requires the namespace context table in the header.

## Compatibility

Indexes built by this release are not readable by earlier releases; rebuild
them with the earlier release's `sumpter index build` to use one.

- Earlier releases refuse a new seekable-zstd header in `index stream`,
  `index verify` and `extract --record-index`: it has no flat
  `record_width_bytes`, so it fails their header check before any record is
  read.
- Earlier releases refuse a new `record-index/v0.1.3` JSON index in
  `index verify` and `extract --record-index` on its version.
- An earlier release's `index stream` does not check the version of a JSON
  index. It may print the index's header metadata and record-size statistics
  and may exit 0; its exit status is not a refusal. It reads no source bytes.
