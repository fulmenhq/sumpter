# Document Node Model

Sumpter evaluates every recipe XPath against one node tree, whatever the input
syntax. This standard defines how a JSON document maps onto that tree, so that
a recipe written for the XML shape of a document carries over to its JSON
shape with one mechanical change: JSON has no attribute axis, so `@k` becomes
`k`. Paths, predicates, `text()`, functions, mappings, transforms, and
validation mean the same thing on both trees.

The rules below are normative and versioned with the recipe schema. They are
pinned by the conformance fixtures in
[`tests/fixtures/docnode/json/`](../../tests/fixtures/docnode/json/) and the
table in `conformance.json`; the examples at the end of this page are checked
against that table in CI.

## Declaring the input format

| Where | Field | Values |
| --- | --- | --- |
| Recipe manifest | `defaults.input.format` | `xml` (default), `json` |
| File signature | `format_type` | `xml` (default), `json`; `protobuf` is reserved and rejected |

- In a recipe, `defaults.input.format` decides the format, and the signature's
  `format_type` must declare the same value. An omitted `format_type` means
  `xml`, so a JSON recipe must set `format_type: json` explicitly.
- Outside a recipe (`sumpter extract --signature … --extract …`), the
  signature's `format_type` decides.
- File names and extensions are never used to choose a format. An unknown
  token is a load error.
- Path-mode discovery defaults to `*.json` for JSON input and `*.xml`
  otherwise. An explicit include pattern always wins.

`json` and `ndjson` mean different things for input and output:

| Setting | `json` | `ndjson` |
| --- | --- | --- |
| `defaults.input.format` | one JSON document per file | line-delimited input; arrives in a later release |
| `defaults.output.format` | newline-delimited JSON records | the same writer as `json` |

## Tree shape

The tree has a document node, element nodes, and text nodes. It has no
attribute or namespace nodes.

| JSON | Node model |
| --- | --- |
| top-level object | its members are the document node's element children; there is no synthetic root, so `/WidgetCoData` matches `{"WidgetCoData": {...}}` as it matches `<WidgetCoData>` |
| top-level array | each item is an element named `item` directly under the document node; select them with `/item` |
| member `"k": v` | an element whose local name is `k`, verbatim |
| member whose value is an array | one element named `k` per item, as repeated siblings in source order (`"tags": ["a","b"]` ≡ `<tags>a</tags><tags>b</tags>`); an empty array yields no element |
| array item that is itself an array | an element with the same name, whose children are the inner items (`"m": [[1,2],[3]]` gives two `m` elements holding three `m` elements) |
| member whose value is an object | element `k` whose children are the object's members |
| scalar value | the element has one text-node child carrying the string-value below |

## Encoding

JSON input is UTF-8. One leading UTF-8 byte order mark (`EF BB BF`) is
accepted and ignored; the input digest still covers the raw bytes, byte order
mark included. Any other encoding is refused.

## Scalars

| JSON value | String-value | Scalar kind |
| --- | --- | --- |
| string | the string | string |
| number | the source lexeme, byte for byte (`1.0`, `1e3`, `2.50`, `9007199254740993`) | number |
| `true` / `false` | `true` / `false` | bool |
| `null` | empty, with no text child | null |

Numbers are never converted to floating point while parsing. A field mapping's
`type` converts the text exactly as it does for XML.

A field mapping over a `null` value binds **absent**, not the empty string.
This is the one deliberate difference from XML, where an empty element binds
`""`. In an array of scalars, null items are dropped the same way, and an
empty array binds absent.

## Names and order

- Keys are kept verbatim as local names, including keys that are not XML
  names (`"first name"`, `"@id"`, `"1st"`). Select them with
  `*[local-name()='first name']`. Keys are never renamed or escaped.
- Document order is source order: `*[1]` is the first member in the bytes.
- The string-value of an object or array element is the concatenation of its
  descendant text in document order, as in XML.

## What JSON input rejects

These are hard errors. Parse errors produce no records, even when well-formed
records come before the bad byte. Load errors are raised before any input is
read. Byte offsets are relative to the start of the file.

When an input has more than one fault, the first fault in byte order is
reported, whether the input is extracted or inspected. Every byte is checked,
including bytes after a complete top-level value.

| Condition | When | Error names |
| --- | --- | --- |
| input not UTF-8 (a UTF-16 or UTF-32 byte order mark, or UTF-16 text without one) | parse | "JSON input must be UTF-8" |
| invalid UTF-8 | parse | the byte offset of the first invalid byte |
| duplicate key in one object | parse | the key and the byte offset of its opening quote; a key longer than 64 bytes is shown as its first 64 bytes or fewer, cut on a character boundary, followed by `…(N bytes)` with its length in bytes |
| nesting deeper than 1024 | parse | the limit |
| data after the top-level value | parse | the offset |
| input ends inside an open object or array | parse | — |
| top-level scalar | parse | — |
| attribute axis (`@k`, `attribute::`) in any recipe XPath | load | the expression; "JSON has no attributes: use k instead of @k" |
| namespace axis, prefixed name, or a `namespaces` map | load | the expression or map |
| `polymorphic_mapping` by `element_type` that matches no item while items exist | extraction | the wrapper idiom |

## Polymorphic arrays: the wrapper idiom

Items of a JSON array all carry their parent key's name, so `element_type`
cannot tell bare items apart. Wrap each item in an object keyed by its element
type:

```json
{"Events": [{"OrderLine": {"sku": "A"}}, {"ReturnLine": {"sku": "R"}}]}
```

The wrapper key is the element name the mapping matches. A bare array under an
`element_type` mapping is an error, never an empty result.

## Routes

JSON input uses the whole-document route. Above the large-file threshold it
parses as one document only with `--allow-large-files`; without the flag the
input fails with disposition reason `route_unsupported`. There is no streaming
fallback. Record-index
(parallel) extraction and `extract-multi` accept XML input only in this
release.

## Conformance examples

Each row is a `(fixture, expression)` pair from `conformance.json`.

<!-- conformance:begin -->
| Fixture | Expression | Result |
| --- | --- | --- |
| widget.json | `/WidgetCoData` | the root member |
| widget.json | `count(//Record)` | `2` |
| widget.json | `count(//empty)` | `0` |
| widget.json | `/WidgetCoData/Customer/text()` | `Acme Corp` |
| widget.json | `/WidgetCoData/*[local-name()='first name']` | `Ada` |
| widget.json | `count(//@*)` | `0` |
| widget.json | `count(//missing/text())` | `0` |
| toplevel-array.json | `/item` | three `item` elements |
| order.json | `/*[1]` | `z` |
| numbers.json | `/n/big` | `9007199254740993` |
| numbers.json | `/n/exp` | `1e3` |
| numbers.json | `/n/dec` | `2.50` |
| nested-array.json | `count(//m)` | `5` |
| concat.json | `string(/o/b)` | `y12` |
<!-- conformance:end -->
