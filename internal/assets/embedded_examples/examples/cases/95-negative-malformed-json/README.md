# 95 Negative: Malformed JSON

A JSON document whose second order is missing a comma, after a valid first
order. The input is parsed as one document, so the run fails and publishes
no records, including the valid first order.

Run:

```bash
examples/scripts/run-case.sh examples/cases/95-negative-malformed-json
```
