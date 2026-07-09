# Sumpter Extract Workflow

The extract command tokenizes XML inputs incrementally through recipe-driven field mappings and produces structured JSON, NDJSON, and optional Parquet outputs. JSON/NDJSON file output uses the record-sink path for sequential runs and record-index parallel runs, streaming records as they are produced instead of retaining the full output slice for that format. Memory for those routes is bounded with respect to emitted result count by parser state, active record work, writer buffers, and the configured reorder window for parallel runs. Unambiguous record-index parallel runs enforce `min_occurrences` from index counts before publishing output and can still use the streaming route. Parquet, mixed JSON+Parquet, sequential `min_occurrences`, and ambiguous indexed-floor recipes remain buffered in v0.2.0. Recipes control both the business payload and optional metadata so downstream consumers can decide what to retain.

## Output Formats

| Mode                 | Description                                                                                                                                    | When to use                                                      |
| -------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------- |
| Structured (default) | A single JSON object containing metadata and recipe-authored data                                                                              | Interactive runs, debugging, consumers that expect a single file |
| NDJSON               | Records emitted as newline-delimited JSON with sidecar manifests; sequential and record-index parallel file output stream through `RecordSink` | Pipeline ingestion and append-friendly record processing         |
| Parquet              | Buffered secondary columnar projection declared by recipe output settings                                                                      | Analytics engines and columnar downstream storage                |

NDJSON is the default durable record output for recipe examples. Parquet is a secondary output path, still requires recipe configuration for the projected columns, and remains buffered in v0.2.0. A run that requests both JSON/NDJSON and Parquet stays on the buffered path so both outputs are produced from the same completed record set.

## Portable Artifact Descriptor

`--artifact-descriptor` writes an opt-in `artifact-descriptor.json` sidecar and
its referenced `fields/records.fields.json` field-catalog sidecar beside the
normal provenance manifest. The descriptor declares conformance to the host-less
`contract: data-artifact/v0` capability and describes the run's record-stream
grain plus its emitted JSON/NDJSON or Parquet representations. Descriptor output
requires `--output-path`, a normal manifest, and an explicit local
`--contract-base`; Sumpter validates the generated descriptor against that pinned
contract base before publishing it.

```bash
sumpter extract files \
  --files ./input.xml \
  --signature-config-path ./signature.yaml \
  --extract-config-path ./extract.yaml \
  --output-path ./out \
  --artifact-descriptor \
  --contract-base ./contracts/data-artifact/v0
```

This descriptor surface covers only the record-stream grain. The field catalog
sidecar is default-deny protection metadata: source-structure-derived field keys
are withheld by count rather than disclosed. The sidecar is validated against
the pinned data-artifact field-catalog shape before publish. With the pinned
Crucible `v0.1.19` baseline, a fully withheld catalog is valid as `fields: []`
plus a positive `withheld_field_count`, so all-XPath XML extraction recipes can
emit descriptors without disclosing source-structure field keys. Object-index
grains, aggregate grains, value profiles, and protection enforcement metadata
are separate follow-on surfaces.

**Descriptor `lifecycle`.** The portable data-artifact/v0 lifecycle field is
mapped from existing provenance completeness signals — no new accounting is
invented:

| Provenance signal | `lifecycle` |
| --- | --- |
| `incomplete: true` (hard failure; orphans may exist) | `incomplete` |
| Any failed inputs (`inputs_failed > 0` or `inputs[].disposition == "failed"`) | `partial` |
| Otherwise (applied and/or not_applicable only) | `complete` |

`draft`, `building`, and `retired` are reserved by the contract. Sumpter extract
does not emit them for finished runs (`building` would apply only to an
explicitly exposed in-progress descriptor, which is not part of this surface).

### Opt-in `--validate-output` ladder

After extract writes its durable sidecars (and optional artifact descriptor),
`--validate-output` can re-check them against embedded schemas and the host-less
data-artifact contract base. Default is `off` so high-volume runs stay
performance-oriented and byte-compatible with prior behavior.

| Mode | Checks |
| --- | --- |
| `off` (default) | No extra output validation |
| `sidecars` | Provenance `manifest.json`; `failures.json` / `dispositions.json` when present |
| `artifact` | `sidecars` plus generated `artifact-descriptor.json` and `fields/records.fields.json` (requires `--artifact-descriptor` and `--contract-base`) |
| `envelope-sample` | `artifact` plus sampled NDJSON record envelopes (first, every 100th, last) against the extract-record-envelope schema |
| `strict` | `artifact` plus **every** NDJSON record envelope |

```bash
sumpter extract files \
  --files ./input.xml \
  --signature-config-path ./signature.yaml \
  --extract-config-path ./extract.yaml \
  --output-path ./out \
  --artifact-descriptor \
  --contract-base ./contracts/data-artifact/v0 \
  --validate-output strict
```

The flag is also available on `recipes run extract` and `recipes run extract-multi`.
Payload-schema validation (`extract.data` against recipe `output_schema`) remains a
follow-on strictness level; this ladder covers sidecars, the portable artifact
bundle, and record envelopes.

## Structured Output Layout

```
{
  "_runtime": { ... },
  "_validation": { ... },        // optional audit block
  "extract": {
    "summary": { ... },          // optional recipe-defined summaries
    "data": { ... }              // recipe’s business payload
  }
}
```

- `_runtime` is generated by Sumpter and captures runtime metadata (record type, source file, signature used, timestamps, and `record_num`). Consumers can ignore it if they only need the payload.
- `_validation` contains the validation/audit metadata produced by `validation_metadata`. It appears only when validation is enabled and the recipe has not disabled it.
- `extract.summary` is populated when the recipe defines summaries and `output_options.show_summaries` remains true. The layout of summary content is 100% recipe controlled.
- `extract.data` is always present and contains the values produced by the recipe’s `field_mappings`.

## Document Order and `record_num`

For recipes with a single record-boundary `match_selectors[]` entry, Sumpter
emits extracted records in source-document order on the regular DOM,
streaming, and indexed parallel extraction paths. This order is stable across
reruns when the source content and recipe/selector semantics are unchanged.
Future output-path refactors, including the record-sink streaming work, must
preserve this contract.

Every emitted record includes `_runtime.record_num`, a 1-based integer assigned
at record-boundary match, scan, or index time before record-level filters run.
It is the source-document position among records matched by the record-boundary
selector. Filtered or skipped records consume their number and leave a gap;
surviving records are not renumbered when filters change.

For example, a recipe that matches `//Item` and filters out canceled items may
emit record numbers `1, 3, 4, 5` when the second source item is filtered. A
downstream driver can sort surviving records within its own parent group by
`_runtime.record_num` and assign any per-parent ordinal it needs.

Recipes with multiple record-boundary selectors are not contracted to use
global document order in v0.1.7. They currently iterate selector-major, so
their `record_num` values are stable for the current recipe semantics but must
not be interpreted as global source-document order across selectors.

## Aggregate output mode (`--output-mode aggregate`)

By default Sumpter writes **one output file per input** (`--output-mode per-input`).
When a run extracts many small single-record inputs, that one-file-per-input fan-out
makes output **file creation** — not parsing — the bottleneck. `--output-mode aggregate`
removes it: every input's records are **streamed to one open NDJSON writer per
invocation** instead of a file per input, so the per-input file-create cost drops from
~N× to 1×.

```bash
sumpter recipes run extract ./recipes/orders \
  --input-path ./orders \
  --output-path ./out \
  --output-mode aggregate
```

```text
out/
├── records.jsonl     # every input's records, one envelope per line
└── manifest.json     # input-set provenance (see below)
```

`aggregate` changes **record-file fan-out, not record shape** — each line is the same
emitted envelope as per-input output. Key properties:

- **Streamed, bounded memory.** Records flow to the open writer as they are extracted;
  there is no per-input staging and no buffer-all-then-flush, so memory stays bounded as
  the input count grows.
- **Deterministic order within an invocation.** Records appear in resolved
  **input-list order × intra-input `record_num`**. For `--file-list` / `--files`, the
  given order is authoritative; for `--input-path` discovery, inputs are sorted before
  ordinals are assigned. Payload is byte-stable for the same input subset (runtime
  provenance like `run_id`/timestamps still varies).
- **Rolling shards.** `--aggregate-max-records <n>` and `--aggregate-max-bytes <bytes>`
  roll to the next lexically ordered shard (`records-00001.jsonl`, `records-00002.jsonl`,
  …) **before** a record would exceed the cap. Default is uncapped (single `records.jsonl`).
- **Input-set provenance.** Because an aggregate file no longer maps to one input by name,
  `manifest.json` records the resolved input inventory — each input's path, content
  `sha256`, `record_count`, and disposition (ordinal = position in `inputs[]`) — plus, per
  shard, an `aggregate_outputs[]` entry with the shard's own `sha256`, `record_count`, and
  contributing input-ordinal span. The manifest, not filename parsing, is authoritative for
  shard order. Completeness is a **global** invariant — Σ shard `record_count` == Σ input
  `record_count` == the run's total — not a per-shard sum: a single multi-record input
  whose records straddle a shard cap appears in **both** adjacent shards' ordinal spans, so
  the spans locate coverage rather than partition the inputs.

**Scope (v0).** Aggregate is opt-in, **NDJSON/JSON only** (Parquet/mixed formats are
rejected), and requires `--output-path` and a manifest. The aggregate **writer** is a
single serial, deterministic streamed sink (records are emitted in input-ordinal order,
one ordered committer owns every durable write) — that ordering is the load-bearing
contract and is never parallelized. The **per-input processing** (parse + per-recipe
application), however, can be parallelized within one `extract-multi` invocation; see
[`--input-workers`](#parallel-input-processing-with---input-workers).

**`--continue-on-error` and `min_occurrences` floors.** Both are supported for **local
and cloud** aggregate output via a per-input barrier. Each input's records are buffered and
only flushed into the shared shard once the input extracts cleanly **and** meets its
`match_selectors[].min_occurrences` floors, so a failed or floor-missing input's rows are
discarded before they ever reach (or, for cloud, are published to) the shard — the shared
shard only ever holds whole, qualifying inputs. A failed input is recorded in
`failures.json` and the manifest (disposition `failed`, `record_count` 0), and the run
exits with a partial-failure error. Memory stays bounded by the largest single input's
records (independent of input count). Without `--continue-on-error` aggregate is
**fail-fast**: any input failure (or floor miss) aborts the whole run, leaving no partial
**local** output (for cloud, already-published shards are recorded `incomplete: true`; see
below).

**Cloud (`s3://`) output.** Aggregate publishes each shard and the sidecar manifest to
the object store through the same credential-handle write boundary as per-input cloud
output. Because each shard is one object subject to the single-PUT size limit (5 GiB), a
cloud aggregate run **requires `--aggregate-max-bytes`** at or below that limit and
rejects an uncapped or over-limit run **before any input is read**, so shards roll
proactively rather than failing a multi-gigabyte upload. Cloud shards publish
**incrementally** (each is a separate, atomic PUT) and cannot be un-published. A
**terminal output failure** (a shard PUT or finalize error — fatal even under
`--continue-on-error`, per ADR-0009) after some shards are already uploaded writes the
manifest with `incomplete: true` recording the committed shards — a manifest carrying that
flag denotes a **failed** run whose listed objects are discoverable for cleanup or
idempotent rerun. A run that **completes** with some inputs failed under
`--continue-on-error` is different: it writes a **normal** manifest (no `incomplete` flag)
plus `failures.json`, because the published shards hold only the successful inputs' rows.
`incomplete: true` therefore means "hard output failure / possible orphans", **not** "some
inputs failed" — see the completeness contract below.

### Knowing a multi-input run completed — and not silently dropping inputs

A common aggregate use case is a single operation over **many small inputs** (dozens to
hundreds). Use these signals to verify every input was applied, in order of authority:

1. **Exit code — authoritative.** A zero exit means **every resolved input was applied**
   (no failures, no hard error). Any non-zero exit means inspect further. The simplest,
   safest pipeline gate is `sumpter … || handle_failure` — never assume success without
   checking it.
2. **Fail-fast (the default) is the never-silently-drop mode.** Without
   `--continue-on-error`, the first input failure (or floor miss) aborts the whole run and
   exits non-zero — there is **no partial local output**, so you cannot accidentally
   consume a short dataset. Use the default when completeness is non-negotiable.
3. **`--continue-on-error` is an explicit opt-in to drop-tolerance**, and it moves the
   completeness burden to you. The run still exits non-zero if anything failed;
   `failures.json` enumerates **every** dropped input (path + reason); and the manifest
   `inputs[]` inventory is **gap-free** — every resolved input ordinal appears exactly once
   with a `disposition` (`applied` / `failed`) and a `record_count`. A pipeline can assert
   completeness positively: `len(inputs)` equals the expected count and no input has
   `disposition: failed`.
4. **`incomplete: true` is not the completeness signal.** It flags a hard output failure
   with possibly-orphaned cloud objects (R8), a different condition from "some inputs
   failed". Do not gate completeness on this flag alone.

**Input-accounting summary (aggregate manifests).** A completed aggregate
manifest carries four optional top-level integers derived from the gap-free
`inputs[]` inventory, so a consumer can reconcile completeness from a single
place rather than walking every entry. They mirror the closed input-disposition
enum exactly:

- `inputs_total` — total resolved inputs, equal to `len(inputs)`.
- `inputs_applied` — inputs with disposition `applied` (including zero-record successes).
- `inputs_not_applicable` — inputs skipped by signature/applicability dispatch.
- `inputs_failed` — inputs with disposition `failed`. A floor miss stays here
  (disposition `failed`, reason `min_occurrences_violation`), not a separate count.

The reconciliation invariant is `applied + failed + not_applicable == total == len(inputs)`.
These counts are present only on aggregate manifests with an authoritative
inventory — omitted on per-input/default manifests and on `incomplete: true`
manifests, which are not a completeness signal. The exit code remains
authoritative; the counts are a convenience over the `inputs[]` inventory, not a
replacement for checking it. When `--artifact-descriptor` is enabled, the
portable descriptor `lifecycle` field maps these same signals (plus
`incomplete: true` and per-input `disposition`) onto the data-artifact/v0
lifecycle enum — see [Portable Artifact Descriptor](#portable-artifact-descriptor).

**With `extract-multi`.** `--output-mode aggregate` applies to
[`recipes run extract-multi`](#run-multiple-recipes-in-one-pass-extract-multi) too: each
recipe gets its **own** aggregate writer and shard sequence under its
`<output-path>/<recipe-id>/` directory (`records.jsonl` / `records-00001.jsonl…` +
`manifest.json`). Per-recipe isolation is unchanged — one writer per recipe, never shared
— and the shared input set is still parsed once. The same contract is enforced per recipe
across the pass: fail-fast by default, or `--continue-on-error` (local and cloud) where each
recipe discards a failed input's buffered rows and records it in its own `failures.json`.

## Recipe Controls

### Scaffold a Recipe Workspace

Use the CLI to create a new recipe directory structure:

```bash
sumpter recipes init \
  --path ./recipes/customer/retail-daily-sales \
  --id retail_daily_sales_v1 \
  --git-init
```

This command creates the standard folders (`signature/`, `extract/`, `validation/`, `testdata/`, `outputs/`) and drops templated `README.md` and `recipe.yaml` files filled with your recipe ID and timestamp. The manifest captures metadata, asset paths, and execution defaults. The `--git-init` flag initializes an empty repository, making it easy to track proprietary recipes in private Git remotes. Run the command in an empty path; it will refuse to overwrite existing content.

### Generate a Starter Extract Config

Use `inspect --generate-config` on a representative XML sample to produce a starter `extract.yaml`:

```bash
sumpter inspect --generate-config ./testdata/sample.xml \
  --output ./recipes/customer/retail-daily-sales/extract/extract.yaml
```

The generated file is a first draft. Sumpter infers a record selector, field names, relative XPaths, scalar types, and simple array `item_mapping` blocks from the sample structure. Review the generated TODO comments before using the recipe for routine extraction.

If the inferred record selector is too broad or ambiguous, provide the selector explicitly:

```bash
sumpter inspect --generate-config ./testdata/sample.xml \
  --record-selector "//OrderEvent" \
  --min-occurrence 1 \
  --output ./recipes/customer/retail-daily-sales/extract/extract.yaml
```

`--min-occurrence` controls sparse-path filtering. `--optional-threshold` controls when generated fields receive optionality review comments. Identifier-shaped values with leading zeroes are inferred as strings; other digit-only values may still need human review when they are operational identifiers rather than quantities.

### Run from the Manifest

Once the manifest points to real signature and extract configs, execute the recipe directly:

```bash
sumpter recipes run extract ./recipes/customer/retail-daily-sales \
  --input-path ./recipes/customer/retail-daily-sales/testdata \
  --output-path ./recipes/customer/retail-daily-sales/outputs/retail.json \
  --progress
```

The runner resolves relative paths via `recipe.yaml`, applies defaults for include/exclude patterns, and delegates to the extract engine. Override any option at the command line when experimenting.

### Run multiple recipes in one pass (`extract-multi`)

When several recipes extract different projections from the **same** input set — say a per-record summary, a line-item detail view, and a financial rollup — running them separately re-reads and re-parses every input file once per recipe. At high file counts (many small files) that redundant parse is the dominant cost. `recipes run extract-multi` applies all of them in a single pass: each input file is read and **parsed once**, then the parsed document is dispatched to every recipe, amortizing the read/parse work from ~N× to 1× across N recipes.

```bash
sumpter recipes run extract-multi \
  ./recipes/summary ./recipes/line-items ./recipes/financial \
  --file-list ./batch/inputs.list \
  --output-path ./out
```

Each recipe writes to its **own** subdirectory under `--output-path`, named by the recipe `id` (`<output-path>/<recipe-id>/`): its records, provenance manifest, and — when applicable — `dispositions.json` / `failures.json`. Per-recipe state is fully isolated: output, formats, `defaults.parameters`, reference tables, and credential handles all come from each recipe's own manifest, and one recipe can never read or clobber another's output. The input set, the output root, and run-level controls (`--continue-on-error`, credentials, the shared run id) are shared across recipes; the run id resolves once (flag → `SUMPTER_RUN_ID` → generated) so every recipe's provenance ties to one invocation.

**Shared run-level parameters.** Each recipe's `defaults.parameters` stay authoritative for its per-recipe config, but a repeatable `--parameter key=value` on `extract-multi` is a **run-level override layer applied to every recipe** in the pass — the same operator-supplied override that single-recipe `recipes run extract --parameter` already provides (see [Recipe Parameters](#recipe-parameters)). It is layered **over** each recipe's `defaults.parameters` (CLI wins uniformly across recipes), satisfies each recipe's `parameters_required` independently, and supports the same scalar and JSON-list typed values. By default the value is injected into **every** recipe's records — use it for the genuinely per-run keys every recipe shares (for example a per-run provenance/version stamp or an operator runtime value) that have no manifest or input-path home.

Use repeatable `--parameter-internal key=value` when a shared run-level value must stay available to expressions in every recipe but must not appear in any recipe's records or provenance argv value. It uses the same scalar/JSON-list typing and required-parameter checks as `--parameter`, but suppresses the key from every recipe's record grain and records only `key=<internal>` in each per-recipe manifest. This is the preferred way to pass a shared classifier list or other derive-only run input through `extract-multi`; it avoids listing the key under `defaults.parameters_internal` in every bystander recipe. Supplying the same key through both `--parameter` and `--parameter-internal` in one run is rejected because separate repeatable flags do not preserve a reliable cross-flag ordering. A shared key from either flag that collides with any recipe's `field_mappings[].output_field` fails the whole run at plan-load preflight, before any output is written.

```bash
sumpter recipes run extract-multi \
  ./recipes/summary ./recipes/line-items ./recipes/financial \
  --file-list ./batch/inputs.list \
  --output-path ./out \
  --parameter harness_version=2024.11.3
```

```bash
sumpter recipes run extract-multi \
  ./recipes/summary ./recipes/line-items ./recipes/financial \
  --file-list ./batch/inputs.list \
  --output-path ./out \
  --parameter-internal 'curated_prefixes=["NM_","NR_"]'
```

`--parameter` is **not** a credential transport — credential material stays behind named credential handles. Secret-shaped parameter keys (`token`, `secret`, `password`, `credential`, …) are redacted by key in the recorded provenance argv.

Failure handling follows the input-vs-recipe boundary: a read/parse/acquire failure is **input-level** (it affects every recipe's view of that file), while an applicability, signature, extraction, `min_occurrences`, or output failure is **recipe-level** — isolated to that recipe and recorded in its own `failures.json` (under `--continue-on-error`) without aborting the others.

**Scope (v0).** `extract-multi` writes **JSON/NDJSON** output only; a recipe declaring another format (e.g. Parquet) is rejected — run it with single-recipe `recipes run extract` instead. The large-file **streaming** path is not supported: each file is parsed once into memory, so a file large enough to route to streaming is rejected (`--allow-large-files` does not relax this). Cross-recipe joins, ordering, and combined-record assembly are out of scope — the pass amortizes the read + parse, nothing more.

#### Parallel input processing with `--input-workers`

Once the per-recipe re-parse is amortized, the remaining per-invocation cost at high file counts is **per-input processing** — read+parse **plus** each recipe's application to that input (signature/applicability matching, extraction, `min_occurrences` checks). By default that runs single-threaded: one `extract-multi` invocation pins roughly one core regardless of how many inputs it has. `--input-workers N` runs that work across **N workers within the single invocation** — each worker parses an input and runs its full per-recipe application into a worker-local bundle — and a single ordered committer applies the bundles in input order. A large batch can then scale toward the available cores / the workload's ceiling instead of running single-file.

```bash
sumpter recipes run extract-multi \
  ./recipes/summary ./recipes/line-items \
  --file-list ./inputs.txt \
  --output-path ./out \
  --input-workers 8
```

The workers do parse **and** per-input recipe application; only the **durable commit** — writing records, the per-input spool barrier, manifest accounting, and shard rolling — stays on a single ordered committer. Output is therefore **identical** to a single-worker run: records emit in resolved input order (`--file-list`/`--files` order, or sorted `--input-path` discovery order) regardless of which worker processed which input, with the same per-invocation provenance manifest and the same per-input/per-recipe failure attribution. The default is `1` (serial, byte-identical to earlier releases); a value below `1` is rejected.

`--input-workers > 1` works with **local and cloud (`s3://`)** aggregate and per-input output. Cloud shard publishes stay on the single ordered committer (deterministic shard order, one object per shard), and cloud inputs are staged before any worker starts, so concurrency never changes the cloud publish, proactive byte-cap (`--aggregate-max-bytes`), partial-publish, or publish-fatal semantics.

**Choosing a worker count.** Because both parse and per-input application run on the workers, the knob helps two common high-file-count shapes:

- **Parse-bound inputs** — large or deeply-structured documents, especially with sparse extraction (a few records from a big document), where DOM construction dominates. These scale toward your core count.
- **Tiny-file, high-count batches** — tens of thousands of small inputs, often with several recipes applied per file, where parse is cheap but the per-input application (repeated across recipes) is the long pole. Parallelizing that application is what lets these batches scale instead of plateauing near one core.

The throughput gain still depends on the workload: it flattens once the single ordered committer (or an external ceiling — storage bytes/s, IOPS, an object-store/API quota) becomes the limit, and very dense recipes that emit large per-input record sets are bounded by an in-memory per-input limit (split such inputs or reduce per-input output; streaming/spill for very large per-input outputs is planned). Because the output is identical at every worker count, you can tune `--input-workers` freely without changing results.

**Measuring with `--stats`.** Add `--stats` to print an end-of-run diagnostic block to **stderr** (stdout and the record/manifest output are unchanged). It reports observed counters only — it does not recommend a worker count:

```text
extract-multi --stats (diagnostic; observed counters, not a recommendation)
  wall:          12.4s
  inputs:        50000 (4032.3/s)
  input bytes:   1430.2 MiB (115.3 MiB/s)
  input-workers: 4
  GOMAXPROCS:    8 (logical CPUs: 8)
  effective CPU: 3.10 cores (~78% of 4 input-workers)
```

`effective CPU` is process user+sys CPU divided by wall time, shown as cores and as a fraction of `--input-workers`. Read it together with throughput across runs rather than as a target:

- **Low effective CPU + throughput still rising as N grows** — workers are waiting and more may help; raise N and re-measure.
- **Low effective CPU + throughput flat as N grows** — the ceiling is elsewhere (storage bytes/s, IOPS or object-store/API quota, decompression/acquisition throttling, or the ordered committer), not CPU; more workers won't help.
- **High effective CPU near `GOMAXPROCS` + throughput flat** — you are CPU-bound; you are near the useful limit for this workload.

The practical loop: run `--input-workers 1`, then `N`, then `2N`, and stop when inputs/s (or MiB/s) plateaus. `input bytes`/MiB/s is best-effort from already-resolved source sizes and shows `unavailable` when sizes are not cheaply available; CPU shows `unavailable` on platforms where process CPU cannot be read. Stats are nondeterministic, so they are never written to records or the manifest.

#### Worked example: three projections in one pass

Suppose each input is a synthetic order document, and three recipes each extract a
**different projection** of it — an order-level summary, a flattened line-item
view, and a per-order tax rollup:

```xml
<!-- ./orders/order-1001.xml -->
<Order id="1001" placedAt="2026-06-20">
  <Customer segment="wholesale"/>
  <Lines>
    <Line sku="NM-100" qty="3" unitPrice="4.00"/>
    <Line sku="NR-205" qty="1" unitPrice="9.50"/>
  </Lines>
  <Tax rate="0.07" amount="1.51"/>
</Order>
```

Each recipe is an ordinary extract workspace with its **own** `recipe.yaml`,
signature, and `extract.yaml`; they differ only in what they project:

| Recipe workspace    | `id`            | Record boundary | Projects                           |
| ------------------- | --------------- | --------------- | ---------------------------------- |
| `./recipes/summary` | `order-summary` | `//Order`       | one row per order (id, segment)    |
| `./recipes/lines`   | `line-items`    | `//Line`        | one row per line (sku, qty, price) |
| `./recipes/tax`     | `tax-rollup`    | `//Order`       | one row per order (id, tax amount) |

Run all three over the one input set in a single parse-once pass:

```bash
sumpter recipes run extract-multi \
  ./recipes/summary ./recipes/lines ./recipes/tax \
  --input-path ./orders \
  --output-path ./out
```

Each input file under `./orders` is read and parsed **once**, then the parsed
document is dispatched to all three recipes. Each recipe writes to its own
subdirectory under `--output-path`, keyed by its `id`:

```text
out/
├── order-summary/
│   ├── extract-order-1001.xml.json   # one summary row per order
│   └── manifest.json                 # per-recipe provenance (shared run id)
├── line-items/
│   ├── extract-order-1001.xml.json   # one row per <Line>
│   └── manifest.json
└── tax-rollup/
    ├── extract-order-1001.xml.json   # one tax row per order
    └── manifest.json
```

Running these as three separate `recipes run extract` invocations would read and
parse `order-1001.xml` (and every other input) **three times** — once per recipe.
`extract-multi` parses it once and fans the in-memory document to all three, so
the read/parse cost is **~N×→1×** in the number of recipes. Outputs are identical
to running each recipe separately; only the redundant parse is removed. Add a
shared run-level stamp to every recipe's records with `--parameter` (e.g.
`--parameter harness_version=2026.06.3`), and keep a derive-only intermediate out
of the records with a `source_extraction` pattern marked `internal: true` (see
[Source Extraction](#source-extraction)).

### Input selection (batch lists, directories, large trees)

Processing **many files in one invocation** is a supported, first-class workflow — it is much faster than a separate run per file, especially for many small files. Pick the input mode that scopes the run precisely; exactly one of these applies per run:

- **`--file-list <path>` / manifest `defaults.input.files_from`** — a newline-delimited file listing input references (local paths or `s3://` URIs), one per line; blank lines and `#` comments are ignored. **This is the batch input for large or precisely-scoped sets:** it does **no directory walk** and is not subject to the shell argv limit, so an upstream selection step (a metadata/index query, a crawl listing) can hand sumpter exactly the file set — including an arbitrary subset that no single path glob can express. Relative local entries resolve against the **list file's directory**; entries are processed in listed order.

  ```bash
  sumpter recipes run extract ./workspace --file-list ./batch/inputs.list
  ```

- **`--files a.xml,b.xml`** — a short, ad hoc comma-separated set. Convenient for a handful of files, but a comma argument hits the shell's `ARGV_MAX` ceiling at thousands of entries — use `--file-list` for large batches.

- **`--input-path <dir>`** — walk a directory tree and filter by `--include-pattern` / `--exclude-pattern`. Note the walk enumerates the **entire** tree before the include pattern filters files (a filename-only pattern like `*.xml` can match in any subtree and so cannot prune directories), which can be a multi-minute stall on a large mixed-grain corpus. Sumpter now announces the enumeration phase and warns when the walk is slow. To scope precisely on a large tree, prefer `--file-list`, a narrower `--input-path`, or `--exclude-pattern` to skip known-large subtrees (exclude patterns **do** prune directories).

`--file-list` is designed to accept `s3://` references under the same credential-handle posture as cloud sources, so the same mechanism carries over for cloud inputs.

### Recipe Parameters

Recipes can declare literal parameters that are injected into every extracted record's `extract.data` block. Use them for operator-known identifiers or tags the source XML does not carry.

```yaml
defaults:
  parameters:
    region_id: "westcoast"
    tenant_id: "1234"
    curated_prefixes: ["NM_", "NR_"]
  parameters_required:
    - tenant_id
  parameters_internal:
    - curated_prefixes
```

At runtime, legacy `client_id` and `site_id` defaults or flags write through to the same external-field map, then `defaults.parameters` overlays those values, then repeatable CLI parameters have final precedence:

```bash
sumpter recipes run extract ./recipes/customer/retail-daily-sales \
  --parameter tenant_id=5678 \
  --parameter run_period=2024Q3
```

The lower-level `sumpter extract files` command accepts the same repeatable `--parameter key=value` flag. Missing `parameters_required` entries fail before extraction. After all parameter sources are merged, Sumpter rejects any parameter key that collides with a `field_mappings[].output_field`; injected values must not silently replace values extracted or derived from content.

Declare `defaults.parameters_internal` for derive-only parameters: the resolved
value is available as a bare DSL variable, but it is never written to
`extract.data` in JSON/NDJSON or Parquet output. CLI overrides still apply and
still satisfy `parameters_required`; when an internal parameter is supplied with
`--parameter`, the provenance manifest records the key but redacts the value in
`argv_sanitized` as `<internal>`. This declaration is recipe-backed; direct
`sumpter extract files` invocations have no standalone flag for marking a
parameter internal.

#### List-typed parameters and set classification

A parameter value can be a **list of strings** as well as a scalar. A list is read by the DSL membership/prefix helpers — `starts_with_any(field, list)` (leading-prefix) and `value_in(field, list)` (exact membership) — so a recipe can classify a record against a set that an operator supplies as run config instead of inlining the set as an XPath disjunction. The recommended pattern: `xpath:` extracts the source value, an `expression:` mapping derives the classification.

```yaml
defaults:
  parameters:
    curated_prefixes: ["NM_", "NR_", "NC_"] # list-typed; operator-overridable
  parameters_internal:
    - curated_prefixes                  # expression-visible, not emitted

# extract.yaml — one helper, no inlined member list:
field_mappings:
  - output_field: accession
    xpath: Accession
    type: string
  - output_field: is_curated_molecule
    expression: "(string_length(accession) >= 5) && starts_with_any(accession, curated_prefixes)"
    type: boolean
```

This references `accession` directly, which is safe when the element is present —
including **present-but-empty** (it binds `""`, so `string_length` is `0`). If
`Accession` can be **absent** in your inputs, guard it with
`has_accession ? (...) : false` (where `has_accession: boolean(Accession)`), since a
direct reference to an absent field fails with `undefined variable` — see
[Empty elements vs. absent elements](dsl-reference.md) in the DSL reference.

Override the set at run time with a JSON array — no recipe edit, no revalidation:

```bash
sumpter recipes run extract ./recipes/genomics/refseq-classify \
  --parameter curated_prefixes='["NM_","NR_","NC_","XM_"]'
```

A CLI `--parameter` value becomes a list **only** when it is a valid JSON array of strings (quote it for your shell); otherwise it stays a literal string, including a value that merely contains commas. List members must be non-empty strings — numbers, booleans, objects, nested/mixed arrays, and empty members are rejected, not coerced. An empty list (`[]`) matches nothing and counts as provided for `parameters_required`. List parameters are emitted into `extract.data` (as a JSON array) like scalar parameters unless declared in `parameters_internal` or withheld by output configuration. See the [DSL Reference](dsl-reference.md#recipe-parameters) for the full semantics and function reference.

### Source Extraction

Recipes can derive file-level fields from the source `filename`, `relative_path`, or `absolute_path` using Go regular expressions with named captures. These fields are evaluated once per source file, before record extraction. If multiple source patterns produce the same capture name, later patterns overwrite earlier source values:

```yaml
defaults:
  input:
    mode: path
    path: testdata
  source_extraction:
    - id: filename-date-token
      source: filename
      pattern: '^(?P<business_date>\d{4}-\d{2}-\d{2})-.*\.xml$'
    - id: path-site-identifier
      source: relative_path
      pattern: "^sites/(?P<source_site_id>[a-z0-9-]+)/"
  source_extraction_required:
    - business_date
```

Merge precedence is legacy `client_id` / `site_id`, then source-extracted captures, then `defaults.parameters`, then CLI `--parameter` overrides. Required source captures are checked before `defaults.parameters` and CLI values are merged, so a missing filename/path capture cannot be masked by a literal parameter.

`relative_path` always requires an explicit root from `--input-path` or `defaults.input.path`; `--files` and `--file-list` runs without that root must use `filename` or `absolute_path` (neither input mode defines a traversal root on its own). Recipe `files`/`files_from` mode may still set `defaults.input.path` as metadata so relative extraction has a stable root. Source capture names must not collide with `field_mappings[].output_field` or `defaults.parameters` keys.

**Tagging records by source grain or provenance.** The example above captures a _partition key_ (`business_date`), but the same `source: filename` capture also tags each record by **which file produced it** — a provenance or classification field emitted once per file at extract time, instead of every downstream consumer re-deriving it from `_runtime.source_file`. This is the right tool when one logical record arrives under two filename conventions at different grains — e.g. a fine-grained `unit-*.xml` (one record per file) and a coarse-grained `batch-*.xml` (many records per file) sharing one schema — and reconciliation must keep one across both. Capture the discriminator from the filename prefix:

```yaml
defaults:
  source_extraction:
    - id: grain-tag
      source: filename
      pattern: '^(?P<grain>unit|batch)-.*\.xml$'
```

The capture is emitted directly as a `grain` field on every record — when a single grain/provenance column is all you need, **name the capture as the final field** and add no `field_mappings` for it. To derive a _separate_ field from it, capture under a name like `source_grain` and add a `field_mappings` expression with a **different** `output_field` (e.g. `output_field: grain_class`, `expression: 'source_grain == "unit" ? "fine" : "coarse"'`). By default the raw capture is still emitted alongside the derived field — an expression _adds_ a column, it does not rename or suppress the capture — and a capture name must not collide with any `field_mappings[].output_field` (the collision rule above). Because `source: filename` needs no input root, this composes with `--files` / `--file-list` batches (no directory walk), and the captured field sits alongside the `_runtime.source_file` provenance already on each record.

**Derive-only captures (`internal: true`).** When a capture is wanted **only** as an intermediate — visible to a `field_mappings[].expression` but never emitted as its own column — mark the pattern `internal: true`:

```yaml
defaults:
  source_extraction:
    - id: grain-tag
      source: filename
      pattern: '^(?P<source_grain>unit|batch)-.*\.xml$'
      internal: true # source_grain drives expressions but is not written to the record
```

An internal pattern's captures are available in expression scope (so `output_field: grain_class`, `expression: 'source_grain == "unit" ? "fine" : "coarse"'` resolves) but are stripped from the record body across **every** sink (JSON, NDJSON, Parquet) — no stray intermediate column. The flag defaults to `false`, so existing patterns keep emitting their captures unchanged. Internal captures still participate fully in `source_extraction_required` (a required internal capture still fails per file when absent) and in the capture↔`output_field` / capture↔`defaults.parameters` collision checks. This works identically under single-recipe `recipes run extract` and `recipes run extract-multi`. Use it for a true intermediate; when you actually want the raw value as a column, leave `internal` off (or omit it).

> **`internal: true` is an output-shaping control, not a redaction or secrecy mechanism.** It removes the stray intermediate column, but the captured value still lives in expression scope, so a recipe author can deliberately re-emit it (or any value derived from it) to an `output_field`. Do not rely on it to keep a sensitive value out of output; for that, do not capture the value at all.

### Reference Tables

A recipe can declare external **reference tables** loaded once per run to back the
[`in_reference` and `lookup_reference`](dsl-reference.md#reference-table-functions)
DSL functions — membership tests and key→value enrichment against an authority list
that is too large or too volatile to inline as a list parameter.

```yaml
defaults:
  reference_tables:
    - name: curated # referenced as in_reference('curated', ...)
      source: refdata/curated_accessions.csv # workspace-relative; contained
      format: csv # csv | tsv | ndjson
      header: true # csv/tsv: columns referenced by name
      column: accession # membership (Pattern A): the single set column
      max_rows: 500000 # fail-loud cap on physical source rows
    - name: molecule # referenced as lookup_reference('molecule', ...)
      source: refdata/molecule_types.csv
      format: csv
      header: true
      key_column: accession # key→value (Pattern B): match the key column…
      value_column: molecule_type # …and return this column on a hit
      max_rows: 500000
      max_bytes: 52428800 # optional pre-read byte cap (default 100 MiB)
```

Each table declares **exactly one** shape: a membership `column` (Pattern A) _or_ a
`key_column` + `value_column` pair (Pattern B). Tables are loaded into immutable,
in-memory maps before extraction begins and projected down to only the declared
column(s) — unused columns and raw rows are never retained. Duplicate keys in a
key→value table fail loud; an oversized source (past `max_rows` or `max_bytes`)
fails loud rather than truncating.

**Source containment.** A local `source` is a **workspace-relative** path. Absolute
paths, `..` escapes, and symlinks (final file or any parent component) are refused: a
reference-table source is read, and a Pattern-B lookup can emit its values into
output, so an unconstrained path would be a local-file read/exfil surface. Keep
reference data inside the recipe workspace.

**Cloud sources (S3-compatible).** A `source` may instead be an `s3://` URI with a
`credentials_handle` naming the cloud credential handle — a handle reference, never a
secret (see [Cloud Sources and Outputs](#cloud-sources-and-outputs-s3-compatible)).
The object is acquired once to the run staging directory under a **pre-read size cap**
(`max_bytes`), so an oversized object is rejected using its size metadata before it can
fill staging disk. Provenance records the logical `s3://` source and the handle
**name** — never the signed URL or credentials.

```yaml
defaults:
  reference_tables:
    - name: curated
      source: s3://refdata-bucket/curated/accessions.csv
      credentials_handle: refdata-reader
      format: csv
      header: true
      column: accession
      max_rows: 500000
      max_bytes: 52428800
```

A `--reference-table name=s3://…/other.csv` override re-points a declared table at a
different object for one run, reusing the table's declared `credentials_handle`. On
`--dry-run` a cloud table's handle is validated for resolvability but the object is
**not** acquired.

**Refresh without a recipe edit.** `--reference-table name=source` overrides a
declared table's source path for a single run (repeatable). Only the source
changes; the format, columns, and caps stay recipe-declared, so a refreshed data
file cannot silently change the table's shape. The override path is held to the
same containment rule, and provenance records the **effective** (overridden) source.

```bash
# Re-point the 'curated' table at this week's authority file, recipe unchanged:
sumpter recipes run extract ./workspace \
  --reference-table curated=refdata/curated_accessions_2026w24.csv
```

**Pre-flight and dry-run.** Unknown or non-literal table names fail at config
validation, before any record is processed. `--dry-run` validates the declarations,
source containment, and resolvability but does **not** load table contents, so a dry
run stays cheap even with large tables.

**Provenance.** Each loaded table is recorded once in the sidecar manifest under
`reference_tables` (name, effective source, format, mode, row count, a `sha256:`
content hash of the source bytes, and caps) — never row values, and never repeated
per output record.

### Field Mappings

`field_mappings` can extract values from XML with `xpath` or derive scalar fields with `expression`.

```yaml
field_mappings:
  - output_field: a_count
    xpath: "Counts/A"
    type: integer
  - output_field: b_count
    xpath: "Counts/B"
    type: integer
  - output_field: ab_combined
    expression: "a_count + b_count"
    type: integer
```

Expression mappings use the same Sumpter DSL as validation metadata,
reconciliations, and summaries. See the
[Sumpter DSL Reference](dsl-reference.md) for operator precedence, functions,
type rules, filter behavior, and parser contracts. Extraction runs in two
passes: every top-level XPath mapping is populated first, then top-level
expression mappings are evaluated in declaration order against the populated
record. That means expression fields can reference any XPath field in the same
record, and can reference earlier expression fields, but cannot reference
expression fields declared later. Undefined names fail the extraction with the
output field and expression in the error message.

Each scalar mapping must declare exactly one of `xpath` or `expression`. Expression mappings are only supported for top-level scalar `field_mappings`; nested `item_mapping` and `polymorphic_mapping` fields remain XPath-only.

### XML Namespace Binding

The same logical vocabulary is often serialized in more than one namespace
shape: fully prefixed (`<n:Record>` with `xmlns:n="URI"`), a default namespace
(`xmlns="URI"`), or a default plus a secondary extension namespace. An optional
`namespaces:` map binds XPath prefixes to namespace **URIs** so a selector
resolves the same way regardless of the document's literal prefixes. It is
accepted at the root of the extract config and the file signature:

```yaml
namespaces:
  n: "urn:example:ledger"
  ext: "urn:example:ledger-ext"
match_selectors:
  - xpath: "//n:Record"
field_mappings:
  - output_field: posted_date
    xpath: "ext:PostedDate"
    type: string
```

The alias (`n`, `ext`) is a **local name you choose**; it is bound to the URI and
need not match any prefix that appears in the document. `//n:Record` then matches
an element in `urn:example:ledger` whether the document wrote it as `<v:Record>`,
`<Record xmlns=…>`, or `<n:Record>`, and two elements that share a local name in
different URIs (`//n:Record` vs `//ext:Record`) are disambiguated. Attribute
reads bind the same way (`@ext:origin`). Binding by URI is the stable,
serialization-independent form and also resists namespace-confusion spoofing
(a look-alike element placed in an unexpected namespace).

Semantics:

- **Map absent → behavior unchanged.** Existing recipes keep the current lenient
  (literal-prefix) matching byte-for-byte; the map is opt-in.
- **Map present → prefixes resolve by URI.** A prefix used in any XPath that is
  **not bound** in the map fails at **config load** (fail-closed) with the
  offending config, XPath, and prefix named — no silent 0-match from a typo'd
  or unbound prefix.
- **Bare name tests stay lenient.** A `namespaces:` map governs *prefixed* tests;
  an unprefixed test like `//Record` keeps lenient matching. Because a bare test
  does **not** match fully-prefixed documents, one used alongside a non-empty map
  emits a load-time warning (it will silently under-match prefixed input) — bind
  it to an alias if you meant the URI.
- Aliases must be ordinary non-colon names; `xml` and `xmlns` are reserved.
  XPath 1.0 has no default element namespace, so bind a default-source namespace
  to an explicit alias (e.g. `n`) rather than an empty alias.

Namespace URIs are inert match keys — they are never fetched, resolved, or
networked.

Scope of binding in this release:

- **Whole-document mode.** Binding applies to match selectors and field-mapping
  XPaths, including the record-boundary selector (`//n:Record`) — the full
  document is in scope, so URI resolution and same-local-name disambiguation
  work at the boundary too. This is the acceptance target; see the worked
  example `examples/cases/12-namespace-binding`.
- **Streaming and indexed modes.** The record-boundary selector is matched by
  **local name only**, so a bound prefix on the *boundary* is not URI-resolved
  in these modes and cannot disambiguate two records that share a local name in
  different URIs — binding still applies to field/relative-path XPaths within a
  matched record. Full boundary parity across modes is tracked separately.
- **Applicability predicates** are evaluated leniently and are **not**
  URI-bound, even when the extract config declares a `namespaces:` map. A
  prefixed test in an applicability predicate keeps literal-prefix semantics in
  this release.

#### Portability across parse paths (whole-document / streaming / indexed)

Sumpter selects a parse path by input size and flags: whole-document (below the
~100 MB threshold), streaming (at or above it), and indexed (`--record-index`).
Namespace handling is **not identical** across these paths, so an unbound
prefixed XPath can behave differently depending on the path — with no error.

A **literal-prefix** XPath (`//v:Record`, `ext:Foo`) that relies on the
document's textual prefix and does **not** declare a `namespaces:` map is
mode-dependent:

- **Streaming** re-encodes buffered tokens, rewriting `<v:Record>` to
  `<Record xmlns="URI">`. A literal-prefix test can silently match **zero**
  records once an input crosses the streaming threshold, and a bare `//Record`
  can conversely *start* matching — so results can change with input size, with
  no diagnostic.
- **Indexed** parses each record fragment without the ancestor `xmlns`
  declarations that were in scope on the full document, so a literal-prefix test
  matches by string and `namespace-uri()` is empty.

Two mode-stable forms:

- **Bind by URI** — declare a `namespaces:` map and use bound prefixes
  (`//n:Record`). This is the recommended, serialization-stable form. Binding is
  fully resolved in **whole-document** mode; **streaming** preserves namespace
  URIs, so bound field XPaths resolve there too. Full URI resolution of bound
  XPaths in **indexed** mode is being completed — until it lands, prefer
  whole-document/streaming for a bound recipe, or use `local-name()`.
- **`local-name()` predicates** (`//*[local-name()='Record']`) match by local
  name in every mode and are the mode-proof option when you cannot bind (for
  example on an older release). They cannot disambiguate elements that share a
  local name in different URIs — bind by URI when you need that.

### Conditional Expressions

Expression mappings can use the DSL ternary conditional:

```yaml
field_mappings:
  - output_field: widget_status
    xpath: Status
    type: string
  - output_field: widget_status_friendly
    expression: 'widget_status == "online" ? "ready" : widget_status'
    type: string
```

The condition must evaluate to a boolean. Only the selected branch is
evaluated, so untaken-branch undefined variables or type errors do not fire.
When a ternary is used in `field_mappings[*].expression`, both branches should
produce values compatible with the declared `type:`. JSONL can represent
variant per-record values, but Parquet derives a fixed column schema from the
declared field type and can fail at write time if a branch returns an
incompatible value. Authors who need branch heterogeneity should coerce both
branches into a compatible type, usually `string`.

### Output Options

```
output_options:
  show_summaries: true        # default; set false to omit extract.summary
  show_validation_metadata: true   # default; set false to suppress _validation
```

Recipe manifests control file serialization under `defaults.output`.

```yaml
defaults:
  output:
    format: json # legacy single-format form
    path: outputs
    pattern: extract-{}.jsonl
```

`json` is the legacy/canonical recipe token for this writer family, and
`ndjson` is a first-class accepted alias for it: both emit the same
newline-delimited JSON records — one JSON object per line — and are
behavior-identical, so a recipe may use either token interchangeably. Neither
token produces a single-file JSON array or object; the output is always
NDJSON/JSONL. JSONL remains the canonical extract output because it contains the
full record envelope, including `_runtime`, `_validation`, and
`extract.summary`.

### RecordSink Streaming Contract

ADR-0009 defines the v0.1.7 `RecordSink` contract for replacing full-result
buffering with writer callbacks. The contract requires sinks to receive
already-enriched emitted envelopes in final output order, preserve
`_runtime.record_num` gaps, provide bounded backpressure, and treat sink write
or finalize failures as fatal output failures.

Sequential JSON/NDJSON file output and record-index parallel JSON/NDJSON file
output now stream emitted records through `RecordSink` instead of materializing
the full source-file result before writing. The record-index path uses a
bounded reorder window so later worker results cannot grow without limit while
an earlier record is still pending. Record-index parallel runs with
unambiguous `min_occurrences` floors preflight those floors from the index
count before opening output and still use the streaming path. Parquet, mixed
JSON+Parquet output, sequential `min_occurrences`, and ambiguous indexed-floor
recipes still use the buffered path. Parquet remains a secondary projection of
`extract.data` and is not part of any bounded-memory claim unless a future
implementation adds true incremental Parquet writing.
The in-tree memory-regression fixture exercises both sequential and
record-index parallel JSON/NDJSON sink routes with synthetic many-record input
and verifies that the streaming APIs do not populate `ExtractResult.Records`.

Record-index streaming JSON/NDJSON output is transactional for the indexed
source: if any indexed record fails to read or extract, Sumpter emits a failed
file boundary, aborts the temporary JSONL target, and returns a terminal error.
`--continue-on-error` remains a multi-source buffered-run recovery control; it
does not publish partial record-index streaming output after a per-record
failure.

### Empty-output contract and `min_occurrences`

A successful extract run writes the requested output artifact even when a
source file yields zero records. JSONL output is a zero-byte file. Parquet
output is a schema-only file with zero rows, using the fields declared by the
recipe. This is intentional: absence of an output file means the run failed or
was not asked to write that format.

Each `match_selectors[]` entry may declare `min_occurrences`. The default is
`0`, so omitted selectors accept zero matches. A selector with
`min_occurrences: 0` also accepts zero matches and writes empty outputs when no
records are extracted. A selector with `min_occurrences: N` where `N > 0`
opts into fail-loud enforcement: if that selector yields fewer than `N`
matches for a source file, the command exits non-zero and names the recipe,
selector index, selector XPath, declared floor, actual count, and source file.
For record-index parallel JSON/NDJSON runs where the index selector maps
unambiguously to the declared floor selector, Sumpter checks the floor against
the index count before opening output so the run can keep the streaming
transactional output path. Sequential runs and ambiguous indexed floors remain
buffered so the floor can be enforced before publishing output.
No payload output or `manifest.json` is written for that failing source.

The check is per selector, not aggregate across a polymorphic recipe. If a
recipe has multiple selectors violating their floors in the same file, Sumpter
reports the first violation and stops. Fix that selector or recipe input, then
rerun to surface any later violations.

For multi-file runs, files completed before the first violation may already
have payload outputs on disk. The run still exits non-zero and no manifest is
written, so downstream drivers should treat manifest absence as failure. On
successful zero-record runs, `manifest.json` includes an `outputs[]` entry with
`RecordCount: 0`, and `counts_by_record_type` includes the processed record
type with value `0`.

Streaming mode currently tracks selector counts for the streaming record
selector. The streaming/index record boundary grammar is a single local
element name with exact case-sensitive matching: `Record` and `//Record` are
supported. Predicate selectors, multi-segment paths, and namespace-prefixed
forms are not yet supported for streaming/index mode and fail before scanning
so they cannot silently over-match. Multi-selector
streaming recipes may return sparse per-selector counts, so per-selector floor
enforcement can be relaxed for selectors the streaming scanner did not count.

For namespace-bound extraction, record-indexed mode requires
`record-index/v0.1.2` or newer. That format stores a compact
`namespace_contexts` table plus each record's `namespace_context_ref`, allowing
Sumpter to reconstruct ancestor `xmlns` declarations before parsing a sliced
record. Namespace-bound recipes fail with rebuild guidance when pointed at an
older index that lacks namespace context. Namespace-free recipes keep legacy
index compatibility.

The CLI applies this enforcement at the command layer. Library consumers that
call `extract.ProcessFileWithProvenance` directly should iterate
`cfg.MatchSelectors` against `result.PerSelectorCounts` to get the same
fail-loud semantics.

### Parquet Secondary Output

Parquet can be enabled as an additional analytics projection. It does not
replace JSONL and does not carry the full extract envelope. Parquet files
contain `extract.data` columns only; runtime, validation, summaries, and audit
context stay in JSONL plus the provenance manifest.

```yaml
defaults:
  output:
    formats: [json, parquet]
    path: outputs
    patterns:
      json: extract-{}.jsonl
      parquet: extract-{}.parquet
    parquet:
      compression: zstd # zstd, snappy, gzip, or none
```

Use either `format` or `formats`, not both. Use either `pattern` or
`patterns`, not both. If a singular `pattern` is used with Parquet, Sumpter
swaps the generated extension to `.parquet`.

### Parquet withhold for hive-partitioned targets

When output files are written under hive-style partition paths such as
`year=2026/month=05/site=store_17/records.parquet`, analytics engines often
project those path segments as virtual columns. To avoid duplicate physical
and virtual columns, recipe authors can withhold declared partition columns
from the Parquet body while keeping them in JSONL.

Every declared withhold name must exist in `output_schema.properties`. The
withhold list only applies to Parquet output; JSON and NDJSON still include
the full `extract.data` payload.

```yaml
defaults:
  output:
    formats: [json, parquet]
    parquet:
      compression: zstd
      withhold_columns: [year, month, day, site, org, program, datasubject]
```

Operator note: Parquet outputs are only useful to Glue crawlers, Trino,
Athena, DuckDB, Spark, and similar analytics consumers when identifying
dimensions are present as columns. Source-data identifiers extracted by XPath
are system-internal. Operational identifiers that downstream teams join on are
often external to the source XML and must be injected into every record.

Recipe-author checklist for analytics handoff:

- Add stable operational dimensions such as `site_id`, `program_id`,
  `tenant_id`, `business_date`, or other join keys to `extract.data`.
- Use declared parameters for run-level dimensions supplied by the operator or
  orchestrator.
- Use source extraction for dimensions derivable from filename, relative path,
  or absolute path.
- Keep source-native identifiers as separate fields when they are useful for
  quality checks; do not treat them as replacements for operational dimensions.
- Verify the generated Parquet columns include the dimensions consumers need
  before handing files to Glue, Athena, Trino, DuckDB, Spark, or Iceberg flows.

### Summaries

```
summaries:
  - name: "transaction_mix"
    label: "Transaction Mix"
    format: "count"
    total:
      expression: "total_events_count"
    components:
      - name: "inside"
        label: "Inside"
        expression: "inside_active_transaction_count"
      - name: "outside"
        label: "Outside"
        expression: "outside_active_transaction_count"
      - name: "suspended"
        label: "Suspended"
        expression: "active_count"
      - name: "others"
        label: "All Others"
        remainder: true
```

- `total.expression` and each `component.expression` use the Sumpter DSL variable space (fields, accumulations, aggregations, etc.). See the [Sumpter DSL Reference](dsl-reference.md) for expression behavior.
- A component with `remainder: true` absorbs the remaining share of the total once other components are subtracted.

### Validation Metadata

```
validation_metadata:
  enable: true
  array_path: "sale_events"
  accumulations: [...]
  aggregations: [...]
  validations: [...]
  reconciliations: [...]
```

- Validation runs before output is assembled. If failure policies trip, the extractor stops with an error and writes nothing to the payload.
- When validation passes, `_validation` is included only if `output_options.show_validation_metadata` is true.

### Declarative Reconciliation Grouping

Reconciliations can auto-generate components from a source array with
`group_by`. The recipe declares the grouping once, and the runtime partitions
the records, evaluates a per-record contribution, and emits one reconciliation
component per observed group.

```yaml
validation_metadata:
  enable: true
  array_path: "order_lines"
  reconciliations:
    - name: amount_by_category
      base_expression: "total_order_amount"
      tolerance: 0.01
      group_by:
        source: "order_lines[]"
        field: "category_code"
        label_field: "category_label"
        missing_label: "uncategorized"
        filter: "line_amount != 0"
        value_expression: "line_amount"
        aggregation: "sum"
        name_template: "category_{{group}}"
        description_template: "Amount for {{label}}"
        overflow_strategy: "none"
```

`group_by` fields:

| Field                  | Required | Description                                                                                                                              |
| ---------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `source`               | Yes      | Dot-delimited path to the array used for grouping. Array segments use `[]`, such as `order_lines[]` or `sale_events[].tender_details[]`. |
| `field`                | Yes      | Field within each grouped object used as the grouping key.                                                                               |
| `label_field`          | No       | Field used for human-friendly labels in templates. Falls back to the group key.                                                          |
| `missing_label`        | No       | Label used when the grouping field is missing or blank. Default is `unknown`.                                                            |
| `filter`               | No       | Boolean Sumpter DSL expression deciding whether a record participates in grouping.                                                       |
| `value_expression`     | Yes      | Numeric Sumpter DSL expression evaluated for each grouped record.                                                                        |
| `aggregation`          | No       | Aggregation across values in each group. Current supported value is `sum`.                                                               |
| `name_template`        | No       | Component name template. Supports `{{group}}` and `{{label}}`.                                                                           |
| `description_template` | No       | Component description template. Supports `{{group}}` and `{{label}}`.                                                                    |
| `overflow_strategy`    | No       | Strategy for grouped totals that exceed the base value. Supported values are `none` and `cap_to_base`.                                   |

For the example above, `_validation.reconciliations[]` keeps the existing
output contract:

```json
{
  "name": "amount_by_category",
  "base_value": 1000,
  "components": [
    {
      "name": "category_A",
      "description": "Amount for Category A",
      "value": 400
    },
    {
      "name": "category_B",
      "description": "Amount for Category B",
      "value": 350
    },
    {
      "name": "category_C",
      "description": "Amount for Category C",
      "value": 250
    }
  ],
  "components_total": 1000,
  "residual": 0,
  "tolerance": 0.01,
  "status": "balanced",
  "allow_unexplained": false,
  "severity": "warning"
}
```

Residuals within `tolerance` produce `status: "balanced"`. Residuals outside
tolerance produce `status: "unbalanced"` unless `allow_unexplained: true`, in
which case the status is `unexplained`. The default tolerance remains `0.01`.

Use `group_by` when the source data can introduce new categories over time and
the recipe should not need a new explicit component for each category. Use
hand-written `components` when each contribution is a fixed business rule or a
derived scalar that does not come from grouped source records.

Mixed mode is supported: a reconciliation may declare both `group_by` and
`components`. The runtime emits grouped components first in lexicographic group
key order, then appends hand-written components. This is useful when an audit
block needs an automatic per-category breakdown plus fixed adjustments such as
freight, rounding, or manual corrections.

Parquet output intentionally contains only `extract.data`. Validation
metadata, including grouped reconciliations, remains in the JSONL envelope. Use
`defaults.output.formats: [json, parquet]` when analytics consumers need
Parquet columns and auditors need the `_validation` block from the same run.

## Cloud Sources and Outputs (S3-compatible)

`extract` can read its source from, and publish its results to, an S3-compatible
object store. Either boundary is optional and independent: a run can be
`local→local` (the default, unchanged), `cloud→local`, `local→cloud`, or
`cloud→cloud`. Bare paths and `file://` URIs always resolve locally and behave
byte-for-byte as before — a run that references no `s3://` URI does no credential
or network work at all.

- **Source input** (`--input-path`/`--files`/`--file-list`): a single object
  `s3://bucket/key.xml`, or a prefix `s3://bucket/prefix/` with the usual
  `--include-pattern`/`--exclude-pattern` globs (matched against keys relative to
  the prefix). A `--file-list` may instead enumerate explicit `s3://` objects
  directly — no prefix listing — under the same credential-handle posture, which
  is the precise batch input when an upstream step already knows the exact object
  set. Each matched object is fetched and **materialized to local working
  storage** under `${SUMPTER_HOME}/work/`, processed with the normal local
  pipeline, and removed when the run finishes. Operators should be aware that a
  cloud source is fully copied to local disk for the duration of the run (see
  [ADR-0008](architecture/adr/0008-sensitive-data-outside-repository-trees.md)).
  An abnormally terminated run (kill/OOM/crash) may leave a staged copy under
  `${SUMPTER_HOME}/work/` (owner-only `0700`); it is reaped automatically on a
  later run and is safe to delete.
- **Result output** (`--output-path`): JSON/NDJSON records, the Parquet secondary
  output, and the provenance sidecar manifest are published to the destination
  prefix. The logical `s3://` identity is what appears in record `_runtime`,
  provenance manifests, failure/disposition sidecars, and Parquet metadata — the
  local staging path never leaks into a published artifact.

The core stays local: temporary files, record indexes, and all intermediate
state are local-disk only even when both ends are cloud.

**Other commands that read cloud sources.** `inspect` and `index build`/`index
verify` also accept a single `s3://` object as their source (prefixes/globs are
rejected — these commands operate on one file). The object is staged to local
working storage, read byte-for-byte, and the **logical `s3://` URI** is what
appears in the inspect report, the index header, and any generated config — never
the staging path. They take the same `--credentials`/`--credential` flags
described below. Their own outputs (the report, the index file) remain local.

**Durability and limits.** Each artifact is written completely to local disk and
then published as a single object, so a failed or interrupted publish never
leaves a truncated object that a consumer could read as complete. A publish
failure is fatal and is **not** suppressed by `--continue-on-error` (the durable-
output posture of [ADR-0009](architecture/adr/0009-record-sink-output-streaming-contract.md)).
Single-object publication is capped at 5 GiB; a larger output fails with a clear
message rather than a cryptic store error. Larger/multipart cloud output, cloud
range reads, and non-S3 providers (`gs://`, `azblob://`) are on the roadmap and
return an actionable error today.

### Credentials

Credentials are referenced **by handle name** — never inlined as secrets. A
handle names an account/endpoint/region; the bucket comes from the `s3://` URI at
I/O time. Cloud credentials are configured on the `sumpter extract files` CLI
(the `index build`/`index verify` and `inspect` commands accept the same flags
for their cloud sources). Handles are declared in a credentials config passed with
`--credentials <path>`:

```yaml
# credentials.yaml — handle names map to account/endpoint/region, no buckets
handles:
  default: # used when an s3:// URI declares no explicit handle
    profile: my-aws-profile # AWS shared-config profile (preferred — no secret here)
    region: us-east-1
  custom: # an S3-compatible store reached through a custom endpoint
    region: us-east-1
    endpoint: https://objstore.internal.example
    force_path_style: true
```

- **Profiles are preferred.** A `profile` resolves credentials from the AWS
  shared-config chain (`~/.aws/...`), so no secret material lives in the config
  file. Inline `access_key_id`/`secret_access_key` are permitted but discouraged;
  a config that carries literal keys must be owner-only (`chmod 0600`) or loading
  fails. **No secrets ever belong in recipe YAML.**
- **CLI selection and override.** `--credential <handle>=<profile>` overrides (or
  defines) a handle's profile from the command line — a reference, never a raw
  key, so secrets stay out of `argv`, `ps`, and shell history. `--input-credentials-handle <name>`
  and `--output-credentials-handle <name>` select the handles used for the
  **input** and **output** sides independently, so a `cloud→cloud` run can read
  from one account and write to another. Each side resolves as: its CLI selector,
  otherwise the recipe's declared handle (below), otherwise the `default` handle.
- **From a recipe.** `sumpter recipes run` accepts the same credential flags
  (`--credentials`, `--credential`, `--input-credentials-handle`,
  `--output-credentials-handle`), and a recipe can name its handles inline:

  ```yaml
  defaults:
    input:
      path: s3://my-bucket/incoming/
      credentials_handle: reader # resolved from --credentials at run time
    output:
      path: s3://archive/extracted/
      credentials_handle: writer
  ```

  The `reader`/`writer` handle names are defined in the credentials config supplied
  at run time:

  ```bash
  sumpter recipes run extract ./my-recipe --credentials credentials.yaml
  ```

  A recipe carries a handle **name** only — never key material; the
  no-secrets-in-recipe-YAML rule holds. A `--input-credentials-handle` /
  `--output-credentials-handle` CLI value overrides the recipe's declared handle.

- **Default-chain vs hermetic posture.** A handle with no `endpoint` and no
  explicit keys uses the **ambient AWS default credential chain** — convenient,
  but environment/shared-config settings can influence where requests go. A
  **hermetic** handle pins an explicit profile (or keys), `region`, and
  `endpoint`. Prefer the hermetic posture for unattended and CI runs so nothing is
  inherited from the environment.
- **Transport.** A custom `endpoint` must be `https://`. A plaintext `http://`
  endpoint is refused unless the handle sets `insecure: true` — a loud, explicit
  opt-in, since a plaintext endpoint puts credentials and data on the wire.
- **Anonymous/unsigned reads (public buckets).** A handle with `anonymous: true`
  reads a public bucket with unsigned requests — no credentials. It is **read-only
  by construction**: the provider rejects every write, and Sumpter additionally
  refuses an anonymous handle on any output target (result, sidecar, parquet)
  before any staging, so a misdirected write fails fast rather than at upload time.
  `anonymous: true` is **mutually exclusive** with `profile`, `access_key_id`/
  `secret_access_key`, and a `--credential` override — combining them is a config
  error, not a silent precedence. TLS posture still applies: an anonymous request
  is still on the wire, so a custom `endpoint` must be `https://` (or `insecure:
true`). Example:

  ```yaml
  handles:
    public-data: # e.g. an open-data bucket
      region: us-east-1
      anonymous: true
  ```

- **Handle names are shareable logical labels — choose neutral slugs.** The
  _resolved_ input and output handle **names** are recorded in the run's
  provenance sidecar (`inputs[].credentials_handle` / `outputs[].credentials_handle`)
  for cloud I/O, as logical identity alongside the `s3://` URI — never the
  profile, endpoint, region, or any key material behind them. Because that sidecar
  can itself be published to a cloud destination, a handle **name** can travel
  into a published artifact, exactly like the bucket/prefix in the `s3://` URI
  already does. Name handles with neutral, non-sensitive slugs (`prod-readonly`,
  `archive`, `reader`, `writer`) — never client, engagement, vendor, or
  vertical-trade identifiers. Local/`file://` runs record no handle and stay
  byte-identical.

The credentials config is parsed fail-closed: an unknown or misspelled field
(e.g. `insecur: true`) is an error, never a silent fall-through to an insecure
default. A missing or undefined handle fails at load time, before any object is
read or written.

## Streaming / NDJSON (Future Work)

The upcoming NDJSON mode will emit:

1. **Header**: `_runtime` and optional `_validation` in the first record (for consumers that want run metadata separate from data rows).
2. **Records**: Each data row emitted as a JSON line (format defined by the recipe, similar to `extract.data`).
3. **Footer**: Optional summary or audit record.

Recipes will reuse the same `output_options` block. Additional NDJSON-specific flags (e.g., `ndjson.include_header`) will be introduced alongside the implementation.

## Consumer Guidance

- If you need only the business data, read `extract.data` (structured mode) or the NDJSON record stream (future) and drop `_runtime`/`_validation`.
- If you need audit trails or completeness checks, inspect `_validation` and the summary contents in addition to the primary payload.
- Recipes should keep metadata blocks lightweight so downstream consumers can safely load the entire JSON into memory, even if they discard the ancillary sections.

## Example (Structured Output)

```
{
  "_runtime": {
    "generated_at": "2025-10-02T11:39:30Z",
    "source_file": ".scratchpad/data/retail/transactions.xml",
    "record_type": "retail_daily_sales",
    "summaries_included": true,
    "validation_included": true
  },
  "_validation": { ... },
  "extract": {
    "summary": { ... },
    "data": {
      "journal_date": "2024-08-01",
      "sale_events": [ ... ]
    }
  }
}
```

This layout keeps the schema explicit, separates audit/metadata from the payload, and gives recipe authors clear control over what the extractor publishes.
