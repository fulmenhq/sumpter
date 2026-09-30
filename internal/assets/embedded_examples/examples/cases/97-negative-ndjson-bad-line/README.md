# 97 Negative: NDJSON Bad Line

Line-delimited JSON whose second line is a truncated object, after a valid
first line. The run fails, naming the record number, and publishes no
records. This case has only an `ndjson` variant.

Run:

```bash
examples/scripts/run-case.sh examples/cases/97-negative-ndjson-bad-line --variant ndjson
```
