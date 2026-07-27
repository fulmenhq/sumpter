# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.3.3 (2026-07-27)

**Stock `github.com/antchfx/xpath` v1.3.8 — interim local pin retired.**

**Released:** 2026-07-27 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.3 retires the interim `./third_party/antchfx-xpath` tree that v0.3.2 used
for numeric operand-context isolation. Upstream merged and tagged the fix
(antchfx/xpath#124 → **v1.3.7** on the merge commit; **v1.3.8** is current).
Sumpter now depends on the stock module at **v1.3.8** with no `replace`.
`xmlquery` remains **v1.5.1**.

Behavior for predicated `sum(...) *` context-sensitive factors matches the
v0.3.2 pin. No new recipe surfaces; no process-run or data-artifact contract
pin changes.

**Start here:** [`docs/extract-workflow.md`](docs/extract-workflow.md). Full
narrative: [`docs/releases/v0.3.3.md`](docs/releases/v0.3.3.md).

### Changed

#### XPath pin retirement (`xpath-sum-multiply`)

- `go.mod` requires `github.com/antchfx/xpath v1.3.8`.
- Deleted `third_party/antchfx-xpath` and the `go.mod` `replace`.
- Hermetic extract regressions for the silent-wrong class stay green against
  the module cache.
- Operator docs: trailing factors are correct on this binary; factor-first
  remains a valid style (and the safer form on older Sumpter builds).

### Compatibility & notes

- **Corrective path unchanged** vs v0.3.2 for the operand-context isolation class.
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Nested item/polymorphic internals.
- Process-run control socket / run-steering (unchanged from v0.3.1 / v0.3.2).
- Richer data-artifact validation and full semantic L3 claims (unchanged).
- Cloud range-reads, GCS/Azure, DuckDB/Arrow, service health endpoints, and
  repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.3`. Binaries from this tag emit `v0.3.3`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.3`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.3.md`](docs/releases/v0.3.3.md) for the full release narrative.

---

## v0.3.2 (2026-07-15)

**Same-record helpers without stray columns, and no more silent-wrong XPath sign arithmetic.**

**Released:** 2026-07-15 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.2 is for recipe authors who need same-record intermediates without
polluting output schemas, and for anyone who multiplies a predicated `sum(...)`
by a context-sensitive factor in XPath:

- Mark a top-level `field_mappings` entry `internal: true` — compute a helper
  once, reuse it from later expressions, never emit a column or portable
  catalog field.
- Trailing context-sensitive factors now evaluate against the correct node —
  **no more silent-wrong sign totals** that still report a green extract.

The internal-mapping surface is additive and byte-compatible when unused. The
XPath fix changes results only for expressions that previously hit the wrong
operand context. No process-run or data-artifact contract pin changes.

**Start here:** [`docs/extract-workflow.md`](docs/extract-workflow.md). Full
narrative: [`docs/releases/v0.3.2.md`](docs/releases/v0.3.2.md).

### Features

#### Derive-only field mappings (`internal-field-mappings`)

**Minimal enable:**

```yaml
field_mappings:
  - output_field: sign_factor
    xpath: "1 - 2*count(self::RefundEvent)" # synthetic
    type: number
    internal: true
  - output_field: amount
    expression: "sign_factor * raw_amount"
    type: number
```

- Two-phase evaluation: all top-level XPath mappings, then expressions in order.
- Projected out before filters, schema fill, validation, enrichment,
  value_profile, and sinks.
- Omitted from record bodies, Parquet columns, field_provenance **entries**, and
  the portable field catalog.
- Plan-load rejects internal names in `value_profile.fields`, `output_schema`,
  and filters.
- Expression-only internals allowed; nested item/polymorphic internals deferred.
- **Names are not confidential** (may appear in recipe provenance and expression
  lineage) — do not put secrets in field names.

#### XPath numeric operand isolation (`xpath-sum-multiply`)

Predicated `sum(...) * factor` no longer silently mis-evaluates the factor.
v0.3.2 used an interim pin of `github.com/antchfx/xpath` under
`./third_party/antchfx-xpath` (`xmlquery` stayed v1.5.1). That pin is **retired
in v0.3.3** in favor of stock **v1.3.8**.

### Compatibility & notes

- **All-additive** for `internal: true` when unused; no-opt recipes unchanged.
- **Corrective** for XPath expressions that previously evaluated trailing
  context-sensitive factors against the wrong node.
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Nested item/polymorphic internals.
- ~~Upstream-tagged xpath release to retire the local pin~~ → done in **v0.3.3**.
- Process-run control socket / run-steering (unchanged from v0.3.1).
- Richer data-artifact validation and full semantic L3 claims (unchanged).
- Cloud range-reads, GCS/Azure, DuckDB/Arrow, service health endpoints, and
  repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.2`. Binaries from this tag emit `v0.3.2`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.2`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.2.md`](docs/releases/v0.3.2.md) for the full release narrative.

---

## v0.3.1 (2026-07-14)

**Opt-in `process-run/v0` flight recorder for long-running `extract-multi` — discover the run, watch settled progress, read the sole terminal, optionally bridge to published data-artifact descriptors.**

**Released:** 2026-07-14 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.1 gives operators of long-running `recipes run extract-multi` batches a
portable local **flight recorder** under host-less `process-run/v0`: an
owner-only process card for discovery, an append-only NDJSON event stream for
progress and the authoritative terminal, and a reference-only bridge from that
terminal to successfully published `data-artifact/v0` descriptors when both
surfaces are enabled. Watch the *process* without parsing workdir trees or
coupling observers to output paths.

The release is additive and opt-in. Runs that never enable process-run telemetry
keep extract outputs, exit codes, and provenance manifests byte-compatible with
v0.3.0. Process-run flags are omitted from the sanitized provenance argv. This
ships **observe-only** process-run surfaces — the card is telemetry-only; there
is no control socket in this release.

Operator notes: [`docs/process-run.md`](docs/process-run.md). Full narrative:
[`docs/releases/v0.3.1.md`](docs/releases/v0.3.1.md).

### Features

#### Host-less process-run contract baseline

`process-run/v0` reuses the same host-less contract resolution discipline as
`data-artifact/v0` (shared primitive). Pin checks cover the process-run
entry-bundle and sibling event-schema digests (Crucible **`v0.1.19`**:
entry-bundle
`sha256:4589befc1d0d3485744c7eea3dfb569ff79457f99996f2ee8313595489a7091b`,
event-schema
`sha256:7138fba72fea862d7964d6c235b1b93da0047e9eb76862be4d111701f887b12d`).
`make process-run-contract-check` is wired into `make check-all`.

#### Event stream

`extract-multi --process-run-events <path>` (or the process-run enable path)
emits a single-writer NDJSON stream: `started`, settled `progress`, heartbeat,
and exactly one terminal (`completed` / `failed` / `canceled`). Exclusive
create, owner-only `0600`, fail-open setup/write. Placement under home/workdir
roots is rejected. CLI cancel uses SIGINT/SIGTERM via context cancellation.
The terminal event is authoritative for run outcome — `done == total` alone is
not success.

#### Process card and reclaim

With process-run enabled (not stream-only), an owner-only discovery root is
published under `<runtime>/proc/<run_id>/` (`card.json`, `claim.json`,
`events.ndjson`, kernel `reclaim.lock`). Cards are pin-validated before publish
and appear only after atomic temp+hard-link publish. Clean exit withdraws the
card and retains the stream; crash leaves the discovery root. Stale reclaim is
fail-closed on live `(pid, started_at)` and serialized by `reclaim.lock`.

#### Terminal → data-artifact bridge

When process-run telemetry and `--artifact-descriptor` are both enabled,
successfully published descriptors appear on the sole terminal as
`data.artifacts[]` with exact `artifact_id` and `lifecycle` plus portable
non-locator `descriptor` (`<artifact_id>#descriptor`). Refs register only after
output Publish succeeds; multi-recipe runs list only successful publications in
plan order. Reference-only — no paths, cloud URIs, or recipe identity in the
event stream.

### Compatibility & notes

- **All-additive.** Default paths stay byte-compatible when process-run is off.
- **Telemetry vs durable output.** Event/card failures fail open; descriptor
  publish failures remain extract-fatal.
- **Two contracts.** `process-run/v0` (run telemetry) composes optionally with
  `data-artifact/v0` (portable extract output).
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Process-run control socket / run-steering surface (later release track).
- Contract graduation and broader process-run control vocabulary.
- Event rotation, OTLP/forwarders, and WAN/TLS profiles.
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3
  claims (unchanged from v0.3.0 posture).
- Cloud range-reads, cloud-side indexing, GCS/Azure, DuckDB/Arrow, service health
  endpoints, and repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.1`. Binaries from this tag emit `v0.3.1`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.1`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.1.md`](docs/releases/v0.3.1.md) for the full release narrative.
