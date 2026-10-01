# JSON and NDJSON extraction

This page is the first-run path for JSON documents and line-delimited JSON
(`ndjson`). It uses the bundled WidgetCo orders examples. No external download
is required.

Recipe-driven extraction from XML, JSON, and NDJSON uses one DSL and XPath
grammar with format-specific recipes. Declare the input format; Sumpter does
not infer it from the file name. See the
[input-route matrix](../extract-workflow.md#input-route-support) for which
commands accept which formats.

## Inspect a JSON document

```bash
sumpter inspect examples/cases/14-json-basic-extraction/input.json \
  --input-format json
```

The report lists key paths as extraction sees them, with counts, samples, and
value kinds. JSON members become elements; arrays become repeated siblings
named by their key. There are no attributes, so an XML `@id` is the member
`id`. NDJSON inspection is refused in this release.

To write a loadable starter extract config from that profile:

```bash
sumpter inspect examples/cases/14-json-basic-extraction/input.json \
  --input-format json \
  --generate-config \
  --output extract.yaml
```

Copy the commented signature block from the header into a signature file, then
run the command the header names. See
[inspect JSON input](commands/inspect.md#input-format).

## Extract a JSON document

Case 14 is the JSON twin of the basic WidgetCo order. The recipe sets
`defaults.input.format: json` and `format_type: json`, and uses `id` in place
of `@id`.

```bash
sumpter recipes run extract examples/cases/14-json-basic-extraction/recipe \
  --files examples/cases/14-json-basic-extraction/input.json \
  --output-path out/

jq .extract.data out/records.jsonl
```

Expected `extract.data`:

```json
{
  "customer": "WidgetCo North",
  "order_id": "ORDER-1001",
  "status": "open",
  "total_amount": 42.5
}
```

JSON `null` and empty arrays bind absent. Parsing keeps number lexemes;
XPath numeric operations use floating point, so identifiers above 2^53 need
string mappings and `value_text`. The
[document node model](../standards/document-node-model.md) is the selector
contract.

## Extract line-delimited JSON

Each nonblank line is one JSON object and one record. The recipe sets
`defaults.input.format: ndjson` with `format_type: ndjson` and
`match_scope: record`. Field paths are relative to that object.

```bash
sumpter recipes run extract \
  examples/cases/15-ndjson-records/variants/ndjson/recipe \
  --files examples/cases/15-ndjson-records/variants/ndjson/input.ndjson \
  --output-path out/
```

Blank lines are skipped. A line that is not one object fails the input. Case
15 also has a JSON-document run of the same three orders.

## Stream or index a JSON source

JSON files below the large-file threshold load as one document. Above it, a
record-scoped recipe with an eligible selector and output is read record by
record; otherwise the run fails with `route_unsupported`.
`--allow-large-files` parses the file as one document.

Uncompressed JSON sources can be indexed:

```bash
sumpter index build events.json \
  --input-format json \
  --selector "results" \
  --output events.recordindex.json
```

Indexed extraction needs a `match_scope: record` signature whose match
selector equals the index selector. NDJSON is not an index input. See the
[index workflow](index-workflow.md) and
[extract workflow](../extract-workflow.md#input-route-support).

## Next

- [Worked examples](../../examples/README.md) — XML/JSON twins, NDJSON, and
  refusal cases
- [Extract workflow](../extract-workflow.md) — routes, outputs, and
  publication boundaries
- [Public-data examples](public-data-examples.md) — optional public-domain
  recipes
