# 11 Parquet Secondary Output

Shows recipe-level dual output where JSONL remains the canonical audit stream
and Parquet is written as an analytics-friendly projection of `extract.data`.

The recipe uses:

```yaml
defaults:
  output:
    formats: [json, parquet]
    patterns:
      json: records.jsonl
      parquet: records.parquet
```

Expected outputs:

- `records.jsonl` contains the full extract envelope, including `_runtime`,
  `_validation`, and summaries.
- `records.parquet` contains only `extract.data` columns plus Sumpter metadata.
