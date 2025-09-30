# ADR 0002: Logging to stderr with JSON or Pretty Formats

Status: Accepted
Date: 2025-09-12

## Context

We need consistent logging behavior that avoids polluting programmatic outputs (stdout) while supporting both
human-readable and machine-readable logs.

## Decision

- Logs go to stderr by default.
- Two formats are supported: `console` (pretty) and `json`.
- `--log-file` allows teeing logs to a file while preserving stderr output; appends by default.
- Timestamps must be RFC3339.

## Consequences

- Commands must emit start/finish/warn/error events to stderr.
- Programmatic outputs (reports) write to stdout.

## References

- SOP: `docs/sop/logging-sop.md`
