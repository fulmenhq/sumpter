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
| Recipe manifest | `defaults.input.format` | `xml` (default), `json`, `ndjson` |
| File signature | `format_type` | `xml` (default), `json`, `ndjson`; `protobuf` is reserved and rejected |
| File signature | `match_scope` | `document` (default), `record`; see [Signature scope](#signature-scope) |

- In a recipe, `defaults.input.format` decides the format, and the signature's
  `format_type` must declare the same value. An omitted `format_type` means
  `xml`, so a JSON recipe must set `format_type: json` explicitly.
- Outside a recipe (`sumpter extract --signature … --extract …`), the
  signature's `format_type` decides.
- File names and extensions are never used to choose a format. An unknown
  token is a load error.
- Path-mode discovery defaults to `*.json` for `json` input, `*.ndjson` for
  `ndjson` input, and `*.xml` otherwise. An explicit include pattern always
  wins; a tree of `.jsonl` files sets `--include-pattern '*.jsonl'`.

`json` and `ndjson` mean different things for input and output:

| Setting | `json` | `ndjson` |
| --- | --- | --- |
| `defaults.input.format` | one JSON document per file | one JSON object per line; each line is one record |
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

## Selecting keys that are not XML names

A key that is an XML NCName (an XML name without `:`) is selected with a
plain step: `Order`, or `a.b` for the key `"a.b"`, which is distinct from the
nested keys `a` then `b` (`a/b`). Every other key is selected with
`*[local-name()=LIT]`, where `LIT` is an XPath 1.0 string literal. XPath 1.0
has no escapes, so a key without `'` is quoted with `'`, a key with `'` but
without `"` is quoted with `"`, and a key with both is built with `concat()`.
`inspect --generate-config` builds its selectors this way.

<!-- key-steps:begin -->
| Key | Step |
| --- | --- |
| `first name` | `*[local-name()='first name']` |
| `123` | `*[local-name()='123']` |
| `$ref` | `*[local-name()='$ref']` |
| `@id` | `*[local-name()='@id']` |
| `a:b` | `*[local-name()='a:b']` |
| `a\b` | `*[local-name()='a\b']` |
| (empty key) | `*[local-name()='']` |
| `it's` | `*[local-name()="it's"]` |
| `say "hi"` | `*[local-name()='say "hi"']` |
| `both'and"` | `*[local-name()=concat('both',"'",'and"')]` |
<!-- key-steps:end -->

A plain step `a:b` selects nothing: XPath reads `a` as a namespace prefix.

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

Below the large-file threshold (100 MB), a `json` input is parsed as one
document. Above it, the input is read record by record on the streaming route,
unless something needs the whole document: a signature with
`match_scope: document` (the default), an applicability predicate, or a
buffered output such as Parquet. Then the input fails with disposition reason
`route_unsupported`, and the error names what blocks streaming.
`--allow-large-files` parses any input as one document instead.

An `ndjson` input is always read record by record, at any size; it has no
whole-document route.

Record-index (parallel) extraction reads `json` input under a signature with
`match_scope: record`; each indexed record is read and parsed as on the
streaming route (see the [record index format](../technical/record-index-format.md)).
Record indexes over `ndjson` input, `extract-multi`, `inspect` of `ndjson`
input, and `inspect --analyze-records` of `json` or `ndjson` input are not
available in this release; each is refused with `route_unsupported` or a load
error.

## Streaming records

On the streaming route the record selector (the single extract match
selector, `Name` or `//Name`) picks the same elements the same XPath picks on
the whole document: `//Name` every element named `Name`, and `Name` only the
elements directly under the document node. Each record is evaluated as its
own document, holding that one element; its string-values, numbers, nulls,
and child elements are exactly the whole-document route's.

- Records are numbered in document order. An element named `Name` inside
  another record is also a record: the outer record comes first, then the
  records inside it.
- A record's source range is the value's own bytes in the input file: from its
  first byte to its last, never including surrounding whitespace, array
  separators, line terminators, or the byte order mark. Offsets count from
  the first byte of the file, byte order mark included, so `raw[start:end]`
  parsed on its own gives the record back.
- A record whose value is a scalar or `null` is a record, as on the
  whole-document route.
- Memory depends on the span of the outermost open record, the records nested
  in it, the nesting depth, and the keys of each open object checked for
  duplicates. A selected record that spans the input, or a very wide object,
  can need memory on the scale of the input.
- A fault anywhere in an input fails that input, and none of its records are
  published, including records read before the fault; see
  [What JSON input rejects](#what-json-input-rejects).

## Line-delimited JSON (`ndjson`)

- Each line holds one JSON object, and each object is one record, named by
  the record selector: field mappings are relative to the line's object, as
  for one item of `"Name": [...]`.
- A line of only whitespace is skipped and is not counted. A line ending in
  `\r\n` or `\n` and a last line with no terminator are read the same way.
- A line holding an array, a scalar, a truncated value, or more than one value
  fails the input, naming the record number and byte offset.
- An input with no JSON value at all (empty, or only blank lines) fails with
  "input contains no JSON value".
- The signature must declare `match_scope: record`. Applicability is not
  supported.

## Signature scope

A signature's `match_scope` sets what its match patterns are evaluated
against:

- `document` (the default) evaluates them against the whole input document,
  as in earlier releases.
- `record` evaluates them against each record, as its own document holding
  that one record element, with the same weights and `confidence_threshold`.
  Every record must reach the threshold: the first that does not fails the
  input with disposition reason `signature_mismatch`, naming the record
  number. The records scored are the ones the single extract match selector
  picks, on the whole-document and streaming routes alike, so a recipe admits
  the same inputs whichever route reads them. When no record is selected,
  nothing is scored.

Write `record`-scoped patterns relative to the record, for example `/Order`
or `/Order/id`, not the document root: a pattern such as `/data` never
matches a record's document. `record` requires exactly one match selector of
the form `Name` or `//Name`, is required for `ndjson`, and is not supported
for `xml` in this release. The XML streaming route does not evaluate the
signature, and it takes only the outermost of nested same-name elements.

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
