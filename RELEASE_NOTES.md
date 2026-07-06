# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.2.5 (2026-07-06)

**Non-emitted recipe parameters for derive-only extraction inputs.**

**Released:** 2026-07-06 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.2.5 is a focused release for parameters that drive extraction logic but should not become output data. Recipes can now mark selected parameters as **internal**: the values remain available to XPath/DSL expressions, keep the existing scalar and JSON-list typing semantics, and still satisfy required-parameter checks, but are suppressed from emitted records, Parquet output, and the provenance argv sidecar. `extract-multi` also gains a run-level `--parameter-internal` flag for shared derive-only values that should be available across a whole multi-recipe pass without appearing as stray columns in recipes that do not consume them.

Everything in this release is additive. Existing recipes without internal parameters keep their current records and manifests byte-for-byte, and ordinary `--parameter` behavior is unchanged.

### Features

#### Per-recipe internal parameters — `parameters_internal`

Recipe defaults may now include a `parameters_internal` list:

```yaml
defaults:
  parameters:
    curated_prefixes: ["NM_", "NR_"]
  parameters_internal:
    - curated_prefixes
```

Each listed key remains in expression scope exactly like any other parameter. That matters for list-typed parameters: a JSON-array default or `--parameter key='["a","b"]'` override still resolves to a list and can be consumed by helpers such as `starts_with_any` and `value_in`. The difference is at emit time: Sumpter unwraps the value for expression evaluation and then skips it when writing output records.

This gives recipe authors a first-class way to pass classifier lists, lookup switches, or other derive-only run inputs through the normal parameter path without replicating those constants onto every row.

- **Record suppression.** Internal parameter values are omitted from NDJSON/JSON records and Parquet projections.
- **Provenance suppression.** CLI override values for internal parameters are redacted in `argv_sanitized` as `key=<internal>`. The key remains visible for replay and audit; the value does not.
- **Required checks still apply.** A key may be both required and internal. If it has no default and no CLI override, the run fails as before.
- **Collision checks still apply.** An internal parameter key still cannot collide with a mapped output field; Sumpter fails before writing output instead of silently replacing content-derived fields.

#### Run-level internal parameters — `--parameter-internal` on `extract-multi`

`extract-multi` already supports shared run-level `--parameter key=value`, layered over every recipe in the pass. v0.2.5 adds the derive-only twin:

```bash
sumpter recipes run extract-multi workspace/ \
  --parameter-internal 'curated_prefixes=["NM_","NR_"]'
```

The flag is repeatable and uses the same scalar/JSON-list parsing path as `--parameter`, but marks the supplied keys internal for every recipe in the pass. This is useful when one shared run value should be available to any recipe expression that needs it, while bystander recipes must not emit it as an unused output column.

- **Available everywhere, emitted nowhere.** The value is in every recipe's expression scope, but no recipe writes it to any sink.
- **Bystander-safe provenance.** Every per-recipe manifest redacts the run-level internal value in `argv_sanitized`, including recipes that do not declare or consume the parameter.
- **Composes with recipe declarations.** A recipe may also list the same key in `defaults.parameters_internal`; suppression is idempotent.
- **Required checks still apply.** A recipe's `parameters_required` entry is satisfied by a run-level internal value.
- **Conflict handling stays strict.** Supplying the same key through both `--parameter` and `--parameter-internal` in one `extract-multi` invocation is rejected because the repeatable flags do not preserve reliable cross-flag ordering. A shared key that collides with any recipe output field still fails plan loading.

`--parameter-internal` is intentionally scoped to `recipes run extract-multi`. Single-recipe `recipes run extract` already has the per-recipe `parameters_internal` declaration for this purpose.

### Compatibility & notes

- **All-additive.** Recipes with no `parameters_internal` declaration and `extract-multi` runs with no `--parameter-internal` flag keep current behavior.
- **Suppress-at-emit, not drop-from-scope.** Internal values stay available to expressions and retain list typing. Suppression happens only when records, Parquet columns, and provenance argv values are written.
- **Not a general secret-transport mechanism.** Internal parameters reduce output exposure for derive-only values, but recipe authors can still deliberately re-emit a derived result. Use credential handles for credentials and other secret-bearing integration paths.
- **Platforms:** unchanged from 0.2.0-0.2.4 — linux amd64/arm64, darwin **arm64**, windows amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull requests are welcome.

### Deferred / follow-ups

- **Namespace-aware XPath binding** is planned separately. Current recipes that need namespace portability should continue using documented namespace-safe forms until the binding surface lands.
- **Portable data-artifact contract support** remains on its own track.
- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, DuckDB/Arrow, service health endpoints, and repair modes** remain roadmap items, as in 0.2.4.

### Release notes

- **Version bump.** `VERSION` is `0.2.5`. Binaries from this tag emit `v0.2.5` via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.5` is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md): CI builds the tag, then the operator signing ceremony uploads checksums, signatures, public keys, and release notes before publishing.

See [`docs/releases/v0.2.5.md`](docs/releases/v0.2.5.md) for the full release narrative.

---

## v0.2.4 (2026-06-27)

**A performance + tunability minor for high-volume `extract-multi`: configurable parallel input processing across thousands of input files and beyond, the instrumentation to tune it, and contract-independent output hardening — all additive, with defaults byte-for-byte unchanged.**

v0.2.4 builds on v0.2.3's aggregate output with the performance follow-on it flagged. The headline is **configurable parallel input processing**: a single run over **thousands of input files and beyond** spreads each input's read+parse **plus** its full per-recipe application across N workers via `--input-workers N`, with a single ordered committer applying results in input order and **byte-identical output at every worker count**. Paired with it, **`--stats`** prints an end-of-run effective-CPU + throughput summary so you size `--input-workers` by measuring your own workload rather than guessing from core count. Around those, two contract-independent hardening items — `json`/`ndjson` naming clarity and additive manifest input-accounting integers — plus logging hardening round out the release. The v0.2.0–0.2.3 surface (cloud I/O, reference-table lookup, multi-recipe single-pass extract, aggregate output) is unchanged, and the supported platform matrix is identical to 0.2.3.

### What's new (summary)

- **Parallel input processing — `--input-workers N` (`input-parallelism`)** - run each `extract-multi` input's read+parse **plus** its full per-recipe application (signature/applicability, extraction, `min_occurrences`) across N workers, with a single ordered committer applying the worker-local bundles in input order so high file counts scale toward available cores. Output is **byte-identical at every worker count** — records emit in resolved input order and the per-invocation manifest is unchanged, because only the durable commit (writing, spool barrier, ledgers, manifest, shard rolling, cloud publish) stays single-owner. Memory is bounded (an over-limit input fails deterministically with guidance; records are never dropped). Local + cloud; default `1` is unchanged.
- **Tune by measuring — `--stats` (`run-stats`)** - opt-in end-of-run diagnostic summary to stderr (wall, inputs/s, best-effort MiB/s, input-workers, `GOMAXPROCS`/logical CPUs, and **effective CPU** as cores and % of input-workers) so you size `--input-workers` from Sumpter's own output. Observed counters only, off the deterministic path — no record, manifest, or `cli.argv_sanitized` change.
- **`json` / `ndjson` naming clarity (`format-naming`)** - `json` is documented as the canonical/legacy token and `ndjson` as a first-class accepted alias for the same newline-delimited JSON writer family; behavior-identical, zero breakage, no deprecation.
- **Manifest input-accounting integers (`manifest-completeness`)** - aggregate provenance manifests gain optional `inputs_total` / `inputs_applied` / `inputs_not_applicable` / `inputs_failed`, mirroring the closed disposition enum (`applied + failed + not_applicable == total == len(inputs)`). Required set unchanged; the `status` enum is deferred to v0.3.0.
- **Logging hardening** - nil-safe `logging.GetLogger()` (no-op when unconfigured) plus a `forbidigo` rule enforcing the package-level logging house style.

### Tuning `--input-workers` (the standout)

Input-parallelism and `--stats` are built to be used together: rather than setting N to your core count, run `--input-workers 1`, then `N`, then `2N` with `--stats` and stop when throughput plateaus. The right N depends on where your workload bottlenecks — CPU, storage bytes/sec, IOPS, or object-store/API quota — and **can exceed core count** for stall/latency-bound work; a low effective-CPU reading is headroom to test, not proof you should add workers. See the `--input-workers` tuning guidance in [`docs/extract-workflow.md`](docs/extract-workflow.md).

### Compatibility & notes

- **All-additive.** `--input-workers` defaults to `1` (serial, byte-identical to 0.2.3) and `--stats` is opt-in and off the deterministic path, so records and the provenance manifest are byte-identical with either on or off. `json`/`ndjson` behavior is unchanged. The manifest required set is unchanged; the new input-accounting integers are optional and appear only on aggregate manifests.
- **`--input-workers` is a throughput knob**, not a blanket speedup — it parallelizes per-input read+parse and application, while only the durable commit (writing, spool barrier, ledgers, manifest accounting, shard rolling, cloud publish) stays single-owner on the ordered committer to preserve byte-identical output. Gains track how CPU-bound per-input work is and whether storage serves concurrent reads.
- **Platforms:** unchanged from 0.2.0–0.2.3 — linux amd64/arm64, darwin **arm64**, windows amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull requests are welcome.

### Release notes

- `VERSION` is `0.2.4`. Binaries from this tag emit `v0.2.4` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.4` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.4.md`](docs/releases/v0.2.4.md) for the full release narrative.

---

## v0.2.3 (2026-06-25)

**Aggregate output: stream one NDJSON file per recipe across many inputs — local and cloud — with deterministic ordering, rolling shards, and per-shard provenance; plus a schema-backed record envelope and a whole-tree format gate.**

v0.2.3 is a feature minor driven by dogfooding of large many-small-input extraction runs. The headline is **aggregate output**: instead of one output file per input, a run can stream **one** NDJSON file per recipe per invocation, turning per-input file fan-out into a single streamed write — with deterministic record ordering, rolling shards, and per-shard provenance digests, for both local and `s3://` destinations. Around it, the emitted **record envelope** is now schema-backed, `extract-multi` gains the full `--file-list` / `--files` / `--input-path` input set with deterministic aggregate ordering, and a one-time whole-tree format normalization plus a full-tree format gate stop documentation/config drift from silently re-accumulating. The v0.2.0–0.2.2 surface (cloud I/O, reference-table lookup, multi-recipe single-pass extract) is unchanged.

### What's new (summary)

- **Aggregate output mode (`aggregate-output`)** - `--output-mode aggregate` streams all of a recipe's records into a **single** NDJSON file per invocation instead of one file per input. Records emit in **input order × intra-input `record_num`** (deterministic for a fixed input subset); `--aggregate-max-records` / `--aggregate-max-bytes` roll the stream into sequential shards proactively. The provenance manifest is authoritative (not filename parsing): a gap-free `inputs[]` inventory plus a per-shard `aggregate_outputs[]` entry (shard `sha256`, `record_count`, contributing input-ordinal span). `--continue-on-error` and recipe `min_occurrences` floors work for **both local and cloud** via a per-input spool barrier — an input commits to the shard only on success, so a failed/floor-missing input never contributes partial records. Cloud aggregate publishes each shard via the existing single-PUT boundary, **requires `--aggregate-max-bytes` ≤ 5 GiB**, and on partial publish marks the manifest `incomplete:true` enumerating the committed shards. Applies per-recipe under `extract-multi`. See [`docs/extract-workflow.md`](docs/extract-workflow.md) "Aggregate output mode."
- **Batch input selection on `extract-multi` (`--file-list` / `--files` / `--input-path`)** - `extract-multi` accepts the same mutually-exclusive input set as single-recipe `extract`. `--file-list` is the batch input for large or precisely-scoped sets (no directory walk, no argv ceiling, listed order preserved). For aggregate runs the order is load-bearing — **aggregate ordinals are assigned in `--file-list` / `--files` order** — so output ordering is operator-controlled and reproducible.
- **Schema-backed record envelope (`record-envelope`)** - a Draft 2020-12 JSON Schema validates one emitted NDJSON row (closed top-level envelope over an open, additively-extensible `_runtime`/payload), and each row self-identifies via the additive `_runtime.envelope_schema = "extract-record-envelope/v0"`. A build-time meta-validation target (wired into `make check-all`) and a live-row round-trip test keep the schema honest. Contract-independent: schematizes existing output, no behavior change.

### Behavior changes (please review before upgrading)

- **All-additive.** `--output-mode aggregate` and the aggregate cap flags are opt-in; the default remains one output file per input, byte-for-byte as in 0.2.2. The record-envelope schema describes existing output (the new `_runtime.envelope_schema` field is additive). The format sweep touches only whitespace/EOF, not content.
- **Aggregate is NDJSON-only**; JSON-array and Parquet outputs continue to write per-input files. Cloud aggregate requires `--aggregate-max-bytes` ≤ 5 GiB.
- **Provenance sidecar (`sumpter.provenance/v1`)** gains an additive `aggregate_outputs[]` block and a gap-free `inputs[]` inventory on aggregate runs; existing manifests remain valid.

### Deferred / follow-ups

- **An aggregate-output performance follow-on**, the **`json` / `ndjson` naming** clarification, and a **manifest-completeness signal** are sequenced for later (v0.2.4 / a separate follow-up).
- **Optional runtime envelope validation** (a `--validate-output` mode and the full validation ladder) is deferred to the v0.3.0 data-artifact-contract work.
- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, DuckDB/Arrow, service health endpoints, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.2.3`. Binaries from this tag emit `v0.2.3` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.3` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.3.md`](docs/releases/v0.2.3.md) for the full release narrative.

---
