# 97 Negative: NDJSON Bad Line

Line-delimited JSON whose second line is a truncated object, after a valid
first line. The run fails, naming the record number; the case runner's staged
file output is withheld on this pre-commit parse failure. Stdout/library
delivery can already contain the first line's provisional record and cannot be
retracted by the producer. Consumers must stage/commit on success; `pipefail`
detects failure but does not undo consumed bytes or side effects. This is not
rollback of committed files after later validation/publish errors. This case
has only an `ndjson` variant. See
[publication boundaries](../../../docs/extract-workflow.md#recordsink-streaming-contract).

Run:

```bash
examples/scripts/run-case.sh examples/cases/97-negative-ndjson-bad-line --variant ndjson
```
