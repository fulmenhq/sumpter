# 95 Negative: Malformed JSON

A JSON document whose second order is missing a comma, after a valid first
order. This case parses the whole document and fails before committing the
case runner's file output; that output contains no records, including the
valid first order. This is not a universal no-delivery or rollback promise:
record-stream stdout/library deliveries can be provisional and non-retractable,
and committed files can remain after later validation/publish errors. See
[publication boundaries](../../../docs/extract-workflow.md#recordsink-streaming-contract).

Run:

```bash
examples/scripts/run-case.sh examples/cases/95-negative-malformed-json
```
