# Record Index Format

This document describes the durable record-index contract used by indexed and
parallel extraction.

## Current JSON Schema

The current JSON schema is `record-index/v0.1.2` at
`schemas/index/v0.1.2/record-index.schema.json`.

Sumpter continues to read `record-index/v0.1.0` and `record-index/v0.1.1` for
namespace-free extraction. Namespace-bound extraction requires namespace context
data and fails loudly on older indexes with rebuild guidance.

## Namespace Context Table

`record-index/v0.1.2` adds a compact namespace-context table:

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

## Stale Index Behavior

Namespace-bound recipes need context data. If an index lacks
`namespace_contexts`, Sumpter refuses the run and tells the user to rebuild the
index with `record-index/v0.1.2` or newer. Namespace-free recipes keep the
legacy behavior for v0.1.0 and v0.1.1 indexes.

## Seekable-Zstd Format

The seekable-zstd store has two files:

- `*.recordindex.header.json`
- `*.recordindex.records.szst`

For the namespace-context format, the header version is
`record-index-szst/v0.1.1`. The header includes the same `namespace_contexts`
table as the JSON schema. Each binary record row is 68 bytes:

| Offset | Width | Field                   |
| ------ | ----- | ----------------------- |
| 0      | 8     | `start_offset`          |
| 8      | 8     | `end_offset`            |
| 16     | 8     | `size_bytes`            |
| 24     | 4     | `depth`                 |
| 28     | 4     | `record_num`            |
| 32     | 32    | raw SHA-256             |
| 64     | 4     | `namespace_context_ref` |

Readers accept legacy 64-byte rows for namespace-free compatibility. A
namespace-bound run still requires the namespace context table in the header.
