# Process-run producer notes (extract-multi)

Sumpter can emit an opt-in **process-run/v0** flight recorder from
`recipes run extract-multi`: an owner-only process card (discovery root) and an
append-only NDJSON event stream. This is additive telemetry. Turning it off
leaves extract outputs, exit codes, and provenance unchanged.

## Enabling

| Control | Effect |
| --- | --- |
| `--process-run` | Enable card + stream; runtime dir from env/platform default |
| `--process-run-runtime-dir <path>` | Override runtime root (implies process-run) |
| `SUMPTER_PROCESS_RUN_RUNTIME_DIR` | Env override when the flag is empty |
| `--process-run-events <path>` | Explicit stream path; alone enables stream-only (no card) |

Runtime directory resolution order:

1. `--process-run-runtime-dir`
2. `SUMPTER_PROCESS_RUN_RUNTIME_DIR`
3. `$XDG_RUNTIME_DIR/sumpter`
4. `$TMPDIR/sumpter-process-run` (or the OS temp dir)

The runtime directory must **not** sit under `SUMPTER_HOME` or the workdir.
Ordinary placement/setup failures disable process-run and warn on stderr;
extraction continues (fail-open). A live reused `run_id` is fail-closed.

Layout under the runtime root:

```text
<runtime>/proc/<run_id>/claim.json    # exclusive ownership lease (0600)
<runtime>/proc/<run_id>/card.json     # discovery root while the run is live (0600)
<runtime>/proc/<run_id>/events.ndjson # durable event stream (0600)
```

Directories are owner-only (`0700`).

## Card lifecycle

- The card is schema-validated against the pinned process-run/v0 baseline
  **before** it is published. Invalid or unreadable pin material withholds both
  card and stream (never publishes an unchecked discovery root).
- The card is **telemetry-only** in this release (no control socket).
- **Clean exit** (including a normal failed extract): the card and claim are
  removed; the event stream is **retained** for post-run inspection.
- **Crash / kill / unrecovered panic**: the card is left in place so operators
  can discover the retained stream. A later start with the same `run_id`
  reclaims the slot only when the recorded `(pid, started_at)` is not live.
- If the event stream fail-open disables mid-run (for example a write error),
  the card is withdrawn immediately so it never points at a removed partial.

## How to read the stream

The terminal event is **authoritative** for run outcome:

| Terminal event | Meaning |
| --- | --- |
| `completed` | Run finished successfully |
| `failed` with `reason=run_error` | Hard failure |
| `failed` with `reason=partial` | Continue-on-error partial outcome |
| `canceled` | Context cancellation (for example SIGINT/SIGTERM) |

Progress counters (`data.done` / `data.total`) count **settled** inputs, not
necessarily successful ones. After a hard failure, `done` may equal `total`
alongside a `failed` or `canceled` terminal. Consumers must not treat
`done == total` alone as success — always read the final terminal event.

Process-complete is also independent of artifact completeness: a process may
finish (or cancel) while output lifecycle is incomplete. That composition is
formalized when terminal events link to data-artifact descriptors.

## Provenance

Process-run flags and paths are omitted from the sanitized provenance argv.
They are operator telemetry surfaces, not portable replay inputs.
