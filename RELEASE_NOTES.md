# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.2.4 (2026-06-27)

**A performance + tunability minor for high-volume `extract-multi`: parallel parsing, the instrumentation to tune it, and contract-independent output hardening — all additive, with defaults byte-for-byte unchanged.**

v0.2.4 builds on v0.2.3's aggregate output with the performance follow-on it flagged. The headline is **parallel parsing**: `--parse-workers N` parses one invocation's input set across N workers into the single ordered aggregate writer, with **byte-identical output at every worker count**. Paired with it, **`--stats`** prints an end-of-run effective-CPU + throughput summary so you size `--parse-workers` by measuring your own workload rather than guessing from core count. Around those, two contract-independent hardening items — `json`/`ndjson` naming clarity and additive manifest input-accounting integers — plus logging hardening round out the release. The v0.2.0–0.2.3 surface (cloud I/O, reference-table lookup, multi-recipe single-pass extract, aggregate output) is unchanged, and the supported platform matrix is identical to 0.2.3.

### What's new (summary)

- **Parallel parsing — `--parse-workers N` (`parse-parallelism`)** - parse one `extract-multi` invocation's input set across N workers, fanning parsed inputs into the single ordered aggregate writer so the parse (CPU) long pole at high file counts scales toward available cores. Output is **byte-identical at every worker count** — records emit in resolved input order and the per-invocation manifest is unchanged, because only read+parse is parallelized while extraction, writing, and shard rolling stay on a single ordered drain. Local + cloud; default `1` is unchanged.
- **Tune by measuring — `--stats` (`run-stats`)** - opt-in end-of-run diagnostic summary to stderr (wall, inputs/s, best-effort MiB/s, parse-workers, `GOMAXPROCS`/logical CPUs, and **effective CPU** as cores and % of parse-workers) so you size `--parse-workers` from Sumpter's own output. Observed counters only, off the deterministic path — no record, manifest, or `cli.argv_sanitized` change.
- **`json` / `ndjson` naming clarity (`format-naming`)** - `json` is documented as the canonical/legacy token and `ndjson` as a first-class accepted alias for the same newline-delimited JSON writer family; behavior-identical, zero breakage, no deprecation.
- **Manifest input-accounting integers (`manifest-completeness`)** - aggregate provenance manifests gain optional `inputs_total` / `inputs_applied` / `inputs_not_applicable` / `inputs_failed`, mirroring the closed disposition enum (`applied + failed + not_applicable == total == len(inputs)`). Required set unchanged; the `status` enum is deferred to v0.3.0.
- **Logging hardening** - nil-safe `logging.GetLogger()` (no-op when unconfigured) plus a `forbidigo` rule enforcing the package-level logging house style.

### Tuning `--parse-workers` (the standout)

Parse-parallelism and `--stats` are built to be used together: rather than setting N to your core count, run `--parse-workers 1`, then `N`, then `2N` with `--stats` and stop when throughput plateaus. The right N depends on where your workload bottlenecks — CPU, storage bytes/sec, IOPS, or object-store/API quota — and **can exceed core count** for stall/latency-bound work; a low effective-CPU reading is headroom to test, not proof you should add workers. See the `--parse-workers` tuning guidance in [`docs/extract-workflow.md`](docs/extract-workflow.md).

### Compatibility & notes

- **All-additive.** `--parse-workers` defaults to `1` (serial, byte-identical to 0.2.3) and `--stats` is opt-in and off the deterministic path, so records and the provenance manifest are byte-identical with either on or off. `json`/`ndjson` behavior is unchanged. The manifest required set is unchanged; the new input-accounting integers are optional and appear only on aggregate manifests.
- **Parse-parallelism is a parse-bound knob**, not a blanket speedup — it parallelizes read+parse only, while the ordered drain (extraction, writing, manifest accounting, shard rolling) stays serial to preserve byte-identical output. Gains track how parse-bound the workload is and whether storage serves concurrent reads.
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

## v0.2.2 (2026-06-23)

**Multi-recipe throughput: apply many extract recipes to one input set in a single parse-once pass, plus the run-level parameters and derive-only capture fields that make it adoptable.**

v0.2.2 is a feature minor driven by real-world dogfooding of large multi-projection extraction runs. The headline is **`extract-multi`**: when several recipes extract different projections from the **same** input set, the engine now reads and parses each input file once and dispatches the parsed document to every recipe — amortizing the per-recipe re-parse (the dominant cost at high file counts) from ~N× to 1× across N recipes. Two ergonomics features make it adoptable for production pipelines: a shared run-level `--parameter` passthrough, and derive-only `source_extraction` captures. The v0.2.0/0.2.1 surface (cloud I/O, reference-table lookup, list/file-list inputs) is unchanged.

### What's new (summary)

- **Multi-recipe single-pass extract (`extract-multi`)** - `recipes run extract-multi <workspace>...` parses each input file once, then fans the parsed document to every signature-matched recipe. Each recipe writes to its own isolated `<output-path>/<recipe-id>/` subdirectory; per-recipe output, formats, `defaults.parameters`, reference tables, and credential handles come from each recipe's own manifest, while the input set, output root, and run-level controls (and a single shared run id) are shared. Failure handling follows the input-vs-recipe boundary — a read/parse failure is input-level, while applicability/signature/extraction/`min_occurrences`/output failures are recipe-level and isolated under `--continue-on-error`. JSON/NDJSON output only in v0; the streaming/large-file path is not supported (a too-large file is rejected). See the worked example in [`docs/extract-workflow.md`](docs/extract-workflow.md).
- **Shared run-level `--parameter` (`multi-parameter`)** - a repeatable `--parameter key=value` on `extract-multi` layers over **every** recipe's `defaults.parameters` with the same override / collision / typed-value (scalar or JSON-list) semantics as single-recipe `recipes run extract --parameter`, satisfies each recipe's `parameters_required` independently, and is injected into every recipe's records — for the per-run keys every recipe shares (e.g. a per-run provenance/runtime stamp). A shared key colliding with any recipe's `field_mappings[].output_field` fails the run at plan-load preflight; secret-shaped parameter values are redacted by key in the provenance argv.
- **Derive-only `source_extraction` captures (`internal-fields`)** - a `source_extraction` pattern may set `internal: true`, making its named captures usable in `field_mappings[].expression` scope but **never emitted** into the record body on any sink — a true intermediate with no stray column. Defaults to `false`; internal captures still honor `source_extraction_required` and the collision checks. A capture name declared on both an internal and a non-internal pattern is rejected at plan validation.

### Behavior changes (please review before upgrading)

- **All-additive.** `extract-multi` is a new subcommand; the shared `--parameter` and `internal: true` are opt-in. Existing single-recipe `recipes run extract` behavior, output formats, and the provenance sidecar schema are unchanged. A recipe with no `internal: true` pattern and no `extract-multi` run behaves byte-for-byte as in 0.2.1.
- **`internal: true` is an output-shaping control, not a redaction mechanism** - the captured value still lives in expression scope and a recipe author can deliberately re-emit it; do not rely on it to keep a sensitive value out of output.

### Deferred / follow-ups

- **Input-handle provenance in multi runs** (record the effective shared input handle) and a **`relative_path` log-message tidy** are tracked fast-follows.
- **Parquet/other formats and the streaming/large-file path for `extract-multi`**, plus **cloud range-reads, cloud-side indexing, GCS/Azure providers, and repair modes**, remain roadmap items.

### Release notes

- `VERSION` is `0.2.2`. Binaries from this tag emit `v0.2.2` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.2` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.2.md`](docs/releases/v0.2.2.md) for the full release narrative.
