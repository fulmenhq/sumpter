# ADR 0007: Empty Output Files Are the Zero-Record Extract Contract

Status: Accepted
Date: 2026-05-19

## Context

Extract consumers often run Sumpter from drivers that read the declared output
path after each successful command invocation. Before SUM-010, the sequential
extract path skipped output writing when a source matched the signature but
yielded zero records. That made a legitimate zero-record result
indistinguishable from a failed or skipped run.

Recipe authors can also declare `match_selectors[].min_occurrences`, but the
runtime did not enforce the declared floor. A selector that should have failed
loudly on zero matches could silently produce the same result as a selector
that explicitly allows zero.

## Decision

- A successful extract run writes the requested output artifact for every
  processed source, including zero-record sources.
- JSONL represents zero records as a zero-byte file.
- Parquet represents zero records as a schema-only file with zero rows.
- The provenance manifest records zero-record outputs with `RecordCount: 0`
  and includes the processed record type in `counts_by_record_type` with value
  `0`.
- `match_selectors[].min_occurrences` is enforced per selector at the command
  layer before output writing. Non-zero floors are opt-in; the schema default
  is `0`.
- A violated floor exits non-zero and writes no payload output or manifest for
  the failing run.

Command-layer enforcement is the CLI contract. Library consumers that call the
extract package directly can reproduce it by comparing each configured selector
floor with `ExtractResult.PerSelectorCounts`.

## Consequences

- Drivers can treat "output file exists" as part of the successful-run
  contract, even when the file contains zero rows.
- Absence of an expected output file or `manifest.json` indicates failure or a
  command that was not configured to write that artifact.
- Multi-selector recipes cannot pass by aggregate count when one selector
  violates its own floor.
- Streaming extraction may produce sparse per-selector counts when the scanner
  only tracks the record-boundary selector; strict multi-selector enforcement
  should use the regular path until streaming parity is extended.

## References

- Brief: `.plans/active/v0.1.4/SUM-010-extract-output-integrity.md`
- ADR 0001: Schema-First Programmatic Outputs
- ADR 0006: Extraction Provenance
