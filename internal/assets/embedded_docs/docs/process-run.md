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
<runtime>/proc/<run_id>/claim.json    # exclusive ownership lease + identity tombstone (0600)
<runtime>/proc/<run_id>/card.json     # discovery root while published (0600)
<runtime>/proc/<run_id>/events.ndjson # durable event stream (0600)
```

Directories are owner-only (`0700`). The claim carries `(pid, started_at)`, a
unique claim token, and a state (`live` or `exited`). Cleanup and reclaim always
verify the claim token so a loser never deletes a winner's slot.

## Card lifecycle

- The card is schema-validated against the pinned process-run/v0 baseline
  **before** it is published. The final `card.json` appears only after a complete
  temp write (atomic no-replace hard-link only; no rename fallback); readers never observe a partial
  discovery root. Invalid pin material withholds both card and stream.
- The card is **telemetry-only** in this release (no control socket).
- **Clean exit** (including a normal failed extract): the card is withdrawn; the
  claim becomes an `exited` tombstone with the same identity; the event stream is
  **retained**. A later same-`run_id` open reclaims only when that identity is no
  longer live (fail-closed while the producer process is still alive).
- **Crash / kill / unrecovered panic**: the live claim and card remain so
  operators can discover the retained stream. Reclaim requires a proven-stale
  `(pid, started_at)` pair via atomic claim-token quarantine.
- If the event stream fail-open disables for any reason (caller write, autonomous
  heartbeat, or Sync/Close failure), the owned stream is removed and the card is
  withdrawn immediately so the discovery root never points at a missing partial.

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
