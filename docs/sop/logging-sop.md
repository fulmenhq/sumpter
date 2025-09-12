# SOP: Logging Policy (stderr-first)

Status: Active

---

## Principle

- All logs go to stderr by default.
- Support two formats: `console` (pretty) and `json` (machine-readable), selectable via flags.
- Provide `--log-file` to tee logs to a file while retaining stderr output; append-safe.
- Timestamps MUST be RFC3339.

## Minimum Events for Long-Running Operations

- INFO start: operation, inputs, encoding (if relevant), options.
- INFO finish: bytes processed, elapsed_ms, throughput, notable counters (e.g., replacement_count).
- WARN: recoverable anomalies (encoding replacements, caps reached).
- ERROR: fatal failures with actionable messages.

## Configuration

- CLI flags (global): `--log-level`, `--log-format`, `--log-file`, `--log-color`, `--log-telemetry`.
- Default: `--log-format console`, level `info`.

## Implementation Notes

- Emit logs from commands (e.g., `inspect`) at the start and finish; avoid contaminating programmatic output (JSON/Markdown) which goes to stdout.
- When `--log-file` is provided, write identical entries to the file and stderr.

## References

- ADR: `docs/architecture/adr/0002-logging-stderr-json-pretty.md`


