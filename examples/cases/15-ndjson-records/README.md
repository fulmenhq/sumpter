# 15 NDJSON Records

The same three WidgetCo orders as one JSON document (the default run) and as
line-delimited JSON (the `ndjson` variant), extracted to the same records.

The two runs need different recipes:

- The JSON recipe reads `input.json` as one document. Its signature matches
  the document root `/Orders`, and the extract selects each `//Order`.
- The NDJSON recipe sets `defaults.input.format: ndjson` and, in the
  signature, `format_type: ndjson` and `match_scope: record`. Each line is one
  record, so the signature pattern `/Order/id` and the field mappings are
  relative to that record.

`input.ndjson` includes a blank line and a whitespace-only line; both are
skipped and do not count as records. Numbers and booleans are JSON values,
not strings, in both inputs.

Run:

```bash
examples/scripts/run-case.sh examples/cases/15-ndjson-records
examples/scripts/run-case.sh examples/cases/15-ndjson-records --variant ndjson
```
