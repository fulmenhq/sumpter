# 96 Negative: Truncated JSON

A JSON document that ends inside its order array, after a valid first order.
The case runner's file output is withheld on this pre-commit parse failure.
This does not promise that record-stream stdout/library delivery never began:
such deliveries are provisional and non-retractable. Already committed files
can remain after later validation/publish failures; see
[publication boundaries](../../../docs/extract-workflow.md#recordsink-streaming-contract).

Run:

```bash
examples/scripts/run-case.sh examples/cases/96-negative-truncated-json
```
