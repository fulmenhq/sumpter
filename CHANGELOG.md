# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Retention policy: the latest 10 versions live inline; older versions are archived in `docs/releases/v<semver>.md`.

## [Unreleased]

## [0.3.4] - 2026-09-04

**Bounded cloud extract with distinct logical reader and writer handles.**

See [`docs/releases/v0.3.4.md`](docs/releases/v0.3.4.md) for the full release narrative. Operator notes: [`docs/extract-workflow.md`](docs/extract-workflow.md).

### Added

- **Bounded cloud extract** — `extract-multi --cloud-input-mode bounded` acquires `s3://` objects from a URI `--file-list` just-in-time under run-global staging byte/count caps and a per-object max. Eager staging remains the default. Hermetic `FixtureDocument` signature/extract examples are included. Bounded mode hashes each staged object before reap so the aggregate provenance ledger does not `stat` a released path. Objects above the per-object cap are refused before staging.
- **extract-multi cloud examples** — public workflow docs show CLI reader handle vs recipe-owned writer handle, a positive `--aggregate-max-bytes` cap, and the 5 GiB single-PUT ceiling. `extract-multi` does not take `--output-credentials-handle`.
- **VERSION bumped to `0.3.4`.**

### Deferred

- Nested item/polymorphic `internal: true` remains deferred.
- Process-run control socket / run-steering surface remains a later release track (unchanged from v0.3.1 / v0.3.2 / v0.3.3).
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3 claims remain follow-on (unchanged).
- Million-object scale characterization of bounded cloud extract is not part of this cut.

## [0.3.3] - 2026-07-27

**Retire the interim local `antchfx/xpath` pin — stock module v1.3.8 carries the numeric operand-context isolation that v0.3.2 shipped via `third_party`.**

See [`docs/releases/v0.3.3.md`](docs/releases/v0.3.3.md) for the full release narrative. Operator notes: [`docs/extract-workflow.md`](docs/extract-workflow.md).

### Changed

- **XPath dependency — stock `github.com/antchfx/xpath` v1.3.8 (`xpath-sum-multiply` pin retirement)** - drop the interim `./third_party/antchfx-xpath` tree and `go.mod` `replace`. Upstream tagged the numeric operand-context isolation fix in **v1.3.7** (merge of antchfx/xpath#124) and advanced to **v1.3.8**; Sumpter now requires **v1.3.8**. `xmlquery` remains **v1.5.1**. Hermetic extract regressions for predicated `sum(...) *` context-sensitive factors stay green against the module cache. Operator docs note that trailing factors are correct on this binary; factor-first remains a valid style and the safer form on older Sumpter releases (#157).
- **VERSION bumped to `0.3.3`.**

### Deferred

- Nested item/polymorphic `internal: true` remains deferred.
- Process-run control socket / run-steering surface remains a later release track (unchanged from v0.3.1 / v0.3.2).
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3 claims remain follow-on (unchanged).

## [0.3.2] - 2026-07-15

**Same-record helpers without stray columns (`internal: true`), and correct XPath field arithmetic when a predicated sum is multiplied by a context-sensitive factor — additive helpers, corrective numbers (no more silent-wrong sign totals).**

See [`docs/releases/v0.3.2.md`](docs/releases/v0.3.2.md) for the full release narrative. Operator notes: [`docs/extract-workflow.md`](docs/extract-workflow.md).

### Added

- **Derive-only field mappings — `field_mappings[].internal: true` (`internal-field-mappings`)** - same-record helpers without stray columns: a top-level scalar mapping may be marked derive-only, computed into expression scope (all XPath bindings first, then expressions in declaration order) but projected out before filters, uniform-schema fill, output-schema validation, enrichment, value_profile, and all sinks. Absent from NDJSON/JSON bodies, Parquet columns, field_provenance **entries**, and the portable field catalog. Expression-only internals are allowed; nested item/polymorphic internals are rejected. Internal names are invalid in `output_schema` / `value_profile.fields` / filter keys (fail loud). Feature-scoped prepare rules cover shape (XOR xpath/expression), external name reservation (including zero-match paths), and duplicates when any internal mapping is present. Internal field **names** are not confidential (they may appear in recipe provenance and expression lineage) — do not put secrets in field names. Defaults stay byte-compatible when unused (#159).
- **VERSION bumped to `0.3.2`.**

### Fixed

- **XPath numeric operand context for field arithmetic (`xpath-sum-multiply`)** - no more silent-wrong sign arithmetic: field-mapping XPath that multiplies a predicated `sum(...)` (or similar left operand) by a context-sensitive trailing factor could evaluate the factor against the wrong node while extract still succeeded. An interim reviewed pin of `github.com/antchfx/xpath` under `./third_party/antchfx-xpath` isolates operand context (`xmlquery` remains v1.5.1). Hermetic regressions cover the prepared field path and signature/applicability selectors; factor-first authoring guidance is documented. Pin retirement path: `third_party/antchfx-xpath/SUMPTER-PIN-README.md` (#156).

### Changed

- **Pre-push whole-tree format gate** - local pre-push validation checks the full tree for format drift so subset-touched branches cannot leave drift outside the change set (#158).
- **README / overview capabilities** - derive-only field mappings (helpers without stray columns) and corrected XPath field arithmetic called out as available today.

### Deferred

- Nested item/polymorphic `internal: true` remains deferred.
- Upstream-tagged xpath release to retire the local third_party pin.
- Process-run control socket / run-steering surface remains a later release track (unchanged from v0.3.1).
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3 claims remain follow-on (unchanged from v0.3.0/v0.3.1 posture).

## [0.3.1] - 2026-07-14

**Opt-in `process-run/v0` flight recorder for long-running `extract-multi` — portable local discovery, settled progress, and an authoritative terminal (with an optional reference-only bridge to published data-artifact descriptors); default paths stay byte-compatible.**

See [`docs/releases/v0.3.1.md`](docs/releases/v0.3.1.md) for the full release narrative. Operator notes: [`docs/process-run.md`](docs/process-run.md).

### Added

- **Host-less process-run contract baseline** — `process-run/v0` reuses the same host-less contract resolution discipline as `data-artifact/v0` (shared primitive). Pin checks cover the process-run entry-bundle and sibling event-schema digests (Crucible `v0.1.19`; entry-bundle `sha256:4589befc1d0d3485744c7eea3dfb569ff79457f99996f2ee8313595489a7091b`, event-schema `sha256:7138fba72fea862d7964d6c235b1b93da0047e9eb76862be4d111701f887b12d`), with `make process-run-contract-check` wired into `make check-all` (#151).
- **Opt-in process-run event stream** — `extract-multi --process-run-events <path>` (or the process-run enable path) emits a single-writer NDJSON stream: `started`, settled `progress`, heartbeat, and exactly one terminal (`completed` / `failed` / `canceled`). Exclusive create, owner-only `0600`, fail-open setup/write; placement under home/workdir roots rejected; CLI cancel via SIGINT/SIGTERM context (no control socket). Flags omitted from provenance argv; no-opt runs stay byte-identical (#152).
- **Process card under a runtime directory** — owner-only `card.json` + exclusive `claim.json` under `<runtime>/proc/<run_id>/`, pin-validated before publish, atomic card publish (temp+fsync+hard-link, no rename fallback). Stale reclaim via claim-token quarantine; live `(pid, started_at)` is fail-closed. Kernel `reclaim.lock` (flock / LockFileEx) is the sole mutual-exclusion authority for recovery and stale takeover; `claim.taking` is non-authoritative diagnostics only. Clean exit withdraws the card and retains the stream; crash leaves the discovery root for operators (#153).
- **Terminal → data-artifact bridge** — when process-run telemetry and `--artifact-descriptor` are both enabled, successfully published descriptors appear on the sole terminal as `data.artifacts[]` with exact `artifact_id` and `lifecycle` plus portable non-locator `descriptor` (`<artifact_id>#descriptor`). Refs register only after output Publish succeeds; omitted when the descriptor flag is off or publication fails; multi-recipe runs list only successful publications in plan order. Reference-only — no paths, cloud URIs, or recipe identity in the event stream (#154).
- **Process-run producer notes** — in-repo operator guide at [`docs/process-run.md`](docs/process-run.md) (embedded with the docs bundle).
- **VERSION bumped to `0.3.1`.**

### Changed

- **README / overview capabilities** — opt-in process-run flight recorder called out as available today for long-running `extract-multi`.
- **Adoption discoverability** — process-run producer notes gain a minimal-enable path; extract-workflow links the flight recorder from `extract-multi`; data-artifact producer profile points operators at the sibling process-run surface.

### Security

- Process-run stream and card files are owner-only; placement under home/workdir is rejected; live-identity reclaim is fail-closed. The terminal bridge is allow-listed (`artifact_id`, `lifecycle`, `descriptor` only) and uses an ID-derived non-locator descriptor form so cloud/path locators cannot ride the event payload. Descriptor publish failures remain extract-fatal; telemetry write failures fail open without rolling back published descriptors.

### Deferred

- Process-run control socket / run-steering surface remains a later release track.
- Contract graduation and broader process-run control vocabulary remain later work.
- Event rotation, OTLP/forwarders, and WAN/TLS profiles remain follow-on.
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3 conformance for every grain shape remain follow-on (unchanged from v0.3.0 posture).

## [0.3.0] - 2026-07-10

**Portable `data-artifact/v0` producer profile — opt-in extract output that is legible to catalogs, query engines, and data planes without Sumpter-specific knowledge, while default paths stay byte-compatible.**

See [`docs/releases/v0.3.0.md`](docs/releases/v0.3.0.md) for the full release narrative.

### Added

- **Host-less data-artifact contract baseline** — extract resolves `contract: data-artifact/v0` from an explicit local `--contract-base` bundle. Production publish is baseline-gated against the pinned Crucible release and resolved-bundle SHA-256 (current pin: Crucible `v0.1.19`). CI/offline fixtures match the pin; a mismatched bundle fails closed before publish (#139).
- **Artifact descriptor sidecar** — opt-in `--artifact-descriptor` writes baseline-validated `artifact-descriptor.json` with producer profile `sumpter.extract-artifact/v0`. Primary grain is `record_stream` by default; `--output-mode aggregate` switches the primary grain to `aggregation`; `--record-index` adds an `object_index` grain for the consumed index (path-sanitized reference; not copied into the output bundle) (#140, #146).
- **Field catalog sidecar** — with the descriptor enabled, extract also writes `fields/records.fields.json` (refs from the descriptor). Source-structure keys are withheld by count; disclosed fields default to `sensitivity: unknown` and `export_action: block_export`. Fully withheld catalogs (`fields: []` + positive withheld count) are valid under the pin (#141).
- **Portable lifecycle mapping** — descriptor `lifecycle` is derived from existing provenance completeness signals (`incomplete` / `partial` / `complete`); no second accounting system (#144).
- **Publication integrity (atomic writers)** — local record, Parquet, and portable sidecar paths finalize via same-directory temp+rename; Parquet renames only after a successful close; catalog publishes before the descriptor that references it; cloud destinations validate staging before Publish (#142, #143).
- **Protection declarations and Parquet page-metadata suppression** — descriptor/catalog emit portable protection floors (`block_export` / `internal` defaults, row vs column enforceable granularity, `columnar_scan` without `predicate_pushdown`). When the descriptor is on, the Parquet writer suppresses page bounds and page statistics on every leaf and never wires Bloom filters. Pre-profile Parquet (no descriptor) retains page statistics. Recipe `withhold_columns` remains a stronger, separate projection control (#147).
- **Opt-in `--validate-output` ladder** — cumulative modes `off` → `sidecars` → `artifact` → `envelope-sample` → `strict`, plus standalone `sumpter validate artifact-descriptor`. Modes provide baseline-bound structural/schema validation (and envelope checks on higher rungs); they are not a complete L3 semantic or consumer export-gate validator (#145).
- **Guarded provenance `value_profile`** — optional recipe `defaults.value_profile` diagnostic on the provenance manifest (not the artifact descriptor). Tier A concrete values only under operator-declared `safe_to_profile` + `public|internal` + `≤ max_distinct`; never-enumerate tags dominate; Tier B emits aggregates only; small-cell suppression; hard ceiling `max_distinct` ≤ 10000; disabled/omitted leaves manifests byte-identical (#148).
- **Producer-profile adoption guide** — in-repo reference at [`docs/data-artifact-producer-profile.md`](docs/data-artifact-producer-profile.md), linked from overview, extract workflow, and validate docs (#149).
- **VERSION bumped to `0.3.0`.**

### Changed

- **Supported-version surface** — security patches target the latest `0.3.x` release; see [SECURITY.md](SECURITY.md).
- **README capabilities** — portable data-artifact producer surfaces called out as available today (opt-in).

### Security

- **Default-deny portable protection floors** when descriptors are emitted; producer-side Parquet page-metadata suppression on the descriptor path; `value_profile` default-deny with never-enumerate tag dominance. Consumers and data planes still enforce export and read policy.

### Deferred

- Richer validation strictness, cross-artifact lineage, and reserved contract-slot activations remain later work.
- Sibling `process-run/v0` (portable run observability / control) remains a separate track.
- Full semantic L3 conformance for every grain shape (for example queryable catalog requirements on `object_index`, opaque shard-id carriers for multi-shard aggregates) remains follow-on; this release documents baseline-bound structural producer adoption.

## [0.2.6] - 2026-07-07

**Namespace-correct XML extraction across whole-document, streaming, and indexed modes: opt-in URI-bound XPath maps, namespace-aware record indexes, and synthetic mode-parity coverage with byte-compatible defaults.**

See [`docs/releases/v0.2.6.md`](docs/releases/v0.2.6.md) for the full release narrative.

### Added

- **Namespace binding - `namespaces:` maps for XPath-bearing assets (`namespace-binding`)** - extract record-match configs and file signatures may now declare a `namespaces:` alias-to-URI map, and Sumpter compiles XPath selectors with URI bindings when the map is present. The recipe manifest schema is unchanged. Absent or empty maps keep legacy behavior, preserving byte-compatible defaults; explicit maps fail closed on undeclared prefixes and reject reserved aliases. Namespace URIs are inert match keys only and are never dereferenced (#135).
- **Namespace-mode parity across whole-document, streaming, and indexed extraction (`namespace-mode-parity`)** - namespace-bound field selection now converges across the three extraction paths, backed by shared synthetic conformance fixtures. Streaming and indexed record boundaries remain local-name-only in this release, while field selection inside each record uses the declared URI bindings (#135, #136, #137).
- **Record-index schema `v0.1.2` namespace context** - record indexes now capture the namespace context needed for indexed extraction to re-evaluate namespace-bound fields consistently. Namespace-free recipes continue to read legacy indexes unchanged; namespace-bound recipes fail loud against stale pre-`v0.1.2` indexes with rebuild guidance instead of silently matching empty namespaces (#137).
- **Synthetic namespace conformance fixtures** - the test corpus covers prefixed, default-namespace, and dual-namespace documents plus adversarial namespace URI and prefix-shadowing cases, with mode coverage over whole-document, streaming, and indexed extraction (#135, #137).
- **VERSION bumped to `0.2.6`.**

### Changed

- **Namespace portability guidance** - extraction docs now describe when to use `namespaces:`, the compatibility posture for legacy prefixed XPath without a map, the local-name-only boundary limitation in streaming/indexed modes, and the non-URI-bound applicability predicate scope (#136).

## [0.2.5] - 2026-07-06

**Non-emitted recipe parameters — declare parameters that drive extraction logic but never land in output records, the Parquet projection, or the provenance sidecar; per-recipe and run-level, all additive with byte-identical defaults.**

See [`docs/releases/v0.2.5.md`](docs/releases/v0.2.5.md) for the full release narrative.

### Added

- **Per-recipe internal parameters — `parameters_internal` (`internal-parameters`)** - a recipe may declare `parameters_internal: [keys]`; the named parameters stay in expression/DSL scope (list typing preserved, so membership predicates such as `starts_with_any` still fire) but are suppressed at emit time from NDJSON/JSON records, the Parquet projection, and the provenance manifest's `argv_sanitized` sidecar. Values remain run-overridable via `--parameter`, and a required internal parameter with no default still fails loud when omitted. Suppression happens at write time rather than by dropping the value from scope, so a JSON-array override still resolves to a list and feeds extraction logic while staying out of both the record body and the manifest (#132).
- **Run-level internal parameters — `--parameter-internal` on `extract-multi` (`run-internal-parameters`)** - `recipes run extract-multi --parameter-internal key=value` (repeatable) layers a run-level parameter over every recipe in the pass like `--parameter`, but applies the same suppress-at-emit behavior to every recipe. The value is in each recipe's expression scope, never written to any sink by any recipe (bystanders included), and redacted in every recipe's `argv_sanitized`. It satisfies `parameters_required`, composes with a recipe's own `parameters_internal`, and is `extract-multi`-only (#133).
- **VERSION bumped to `0.2.5`.**

## [0.2.4] - 2026-06-27

**Configurable parallel input processing for high-volume `extract-multi` — thousands of input files and beyond — with the `--stats` instrumentation to tune it by measuring rather than by core count, plus contract-independent output hardening; all additive, with byte-for-byte unchanged defaults.**

See [`docs/releases/v0.2.4.md`](docs/releases/v0.2.4.md) for the full release narrative.

### Added

- **Parallel input processing — `--input-workers N` (`input-parallelism`)** - `recipes run extract-multi --input-workers N` runs each input's read+parse **plus** its full per-recipe application (signature/applicability matching, extraction, `min_occurrences` checks) across N workers into worker-local bundles, and a single ordered committer applies the bundles in input order, so the per-input long pole at high file counts scales toward available cores instead of running single-file. Output is **byte-identical at every worker count** (the load-bearing guarantee): records emit in resolved input order, the per-invocation provenance manifest is unchanged, and every aggregate determinism/isolation/cloud invariant holds — only the durable commit (writing, the per-input spool barrier, ledgers, manifest accounting, shard rolling, and cloud publish) stays single-owner on the ordered committer. Memory is bounded by a per-input bundle limit and a cross-input in-flight ceiling: an over-limit input fails deterministically with guidance to split it or reduce its per-input output, and records are never dropped (streaming/spill for very large per-input outputs is planned). A worker panic is contained as a per-input failure, never a process crash. Works with local and cloud (`s3://`) aggregate and per-input output. Default `1` is serial and byte-identical to earlier releases; a value below 1 is rejected. Scales both parse-bound runs (large or deeply-structured inputs with sparse extraction) and the tiny-file regime (tens of thousands of small inputs across several recipes); flattens once the committer or an external ceiling becomes the limit (#122, #123, #124, #125, #131).
- **Run statistics — `--stats` (`run-stats`)** - opt-in `recipes run extract-multi --stats` prints an end-of-run diagnostic summary to **stderr** so `--input-workers` can be tuned from Sumpter's own output: wall clock, inputs and inputs/s, best-effort input bytes + MiB/s, the input-workers value, `GOMAXPROCS` + logical CPU count, and **effective CPU** (process user+sys CPU ÷ wall) shown as cores and as a percentage of input-workers. Observed counters only — never a recommendation. It is entirely off the deterministic artifact path: no record fields, no provenance manifest fields, no schema change, and `--stats` is not recorded in `cli.argv_sanitized`, so records and the manifest are byte-identical with stats on or off. Process CPU is read via `getrusage` / `GetProcessTimes` and degrades to `unavailable` rather than failing the run (#129).
- **Manifest input-accounting integers (`manifest-completeness`)** - aggregate provenance manifests gain four optional top-level integers — `inputs_total`, `inputs_applied`, `inputs_not_applicable`, `inputs_failed` — so a consumer can reconcile completeness from a single place rather than walking the `inputs[]` inventory. They mirror the closed input-disposition enum exactly and satisfy `inputs_applied + inputs_failed + inputs_not_applicable == inputs_total == len(inputs)`. Emitted only on aggregate manifests with an authoritative gap-free inventory; omitted on per-input/default and `incomplete:true` manifests, so existing manifests stay byte-identical. The required schema set is unchanged; the semantic `status` enum is deferred to the v0.3.0 data-artifact contract (#128).

### Changed

- **`json` / `ndjson` format-name clarity (`format-naming`)** - the recipe output `format` token is now documented unambiguously: `json` is the legacy/canonical token and `ndjson` is a first-class accepted alias for the same writer family — both emit newline-delimited JSON (NDJSON/JSONL) record envelopes and are behavior-identical, and neither produces a single-file JSON array. Docs and recipe-schema descriptions only; zero breakage, no code-path change, no deprecation warning (#127).
- **Logging hardening** - `logging.GetLogger()` now returns a no-op logger when none is configured, closing a latent nil-logger panic class in tests and non-CLI entrypoints, and a `forbidigo` lint rule bans the `GetLogger().Method()` call pattern so the package-level `logging.Info/Warn/Error` house style is enforced as a gate (#126).
- **VERSION bumped to `0.2.4`** for this release.

## [0.2.3] - 2026-06-25

**Aggregate output: stream one NDJSON file per recipe across many inputs (local and cloud) with deterministic ordering, rolling shards, and per-shard provenance; plus a schema-backed record envelope and a whole-tree format gate.**

See [`docs/releases/v0.2.3.md`](docs/releases/v0.2.3.md) for the full release narrative.

### Added

- **Aggregate output mode (`aggregate-output`)** - `--output-mode aggregate` streams all of a recipe's records into a **single** NDJSON file per invocation instead of one output file per input, so a run over many small inputs is bounded by one streamed write rather than per-input file fan-out. Records emit in **input order × intra-input `record_num`** (deterministic for a fixed input subset); `--aggregate-max-records <n>` / `--aggregate-max-bytes <bytes>` roll the stream into sequential shards proactively on the running count. The provenance manifest is authoritative (not filename parsing): a gap-free `inputs[]` inventory (per-input `sha256`, `record_count`, disposition) plus a per-shard `aggregate_outputs[]` entry (shard `sha256`, `record_count`, contributing input-ordinal span; a record-straddling input appears in both adjacent spans). `--continue-on-error` and recipe `min_occurrences` floors are supported for **both local and cloud** via a per-input spool barrier — an input's records commit to the shard only on success, so a failed/floor-missing input never contributes partial records and is recorded in `failures.json`. Cloud aggregate writes each shard via the existing stage-and-publish single-PUT boundary, **requires `--aggregate-max-bytes` ≤ 5 GiB** at plan time, and on partial publish marks the manifest `incomplete:true` enumerating exactly the committed shards. Applies to `extract-multi` per-recipe (own writer and shard sequence under `<output-path>/<recipe-id>/`) (#114, #115, #116, #117, #118, #119).
- **Batch input selection on `extract-multi` (`--file-list` / `--files` / `--input-path`)** - `extract-multi` accepts the same mutually-exclusive input-selection set as single-recipe `extract`. `--file-list` is the batch input for large or precisely-scoped sets (newline-delimited references, no directory walk, no argv ceiling, listed order preserved). For aggregate runs the order is load-bearing: **aggregate ordinals are assigned in `--file-list` / `--files` order** (and `--input-path` sorts before assigning), so output ordering is operator-controlled and reproducible (#114, #115).
- **Schema-backed record envelope (`record-envelope`)** - a Draft 2020-12 JSON Schema (`schemas/extract/v0.1.0/extract-record-envelope.schema.json`) validates one emitted NDJSON row: a closed top-level envelope (`_runtime` + `extract`, `extract.data` required) over an open, additively-extensible `_runtime`/payload. Each row self-identifies via the additive `_runtime.envelope_schema = "extract-record-envelope/v0"` (major-only, so additive schema revisions do not churn rows). A build-time meta-validation target (`make extract-output-contract-check`, wired into `make check-all`) validates fixture rows and a round-trip test validates a live-emitted record. Contract-independent: schematizes existing output, no behavior change (#120).

### Changed

- **Whole-tree format normalization + full-tree format gate (`format-normalize`)** - a one-time repository-wide format sweep (Markdown / JSON / YAML via `goneat format` plus an EOF-newline pass) normalizes accumulated whitespace drift, and a full-tree format gate plus a VERSION-drift guard now run in CI. Previously the format check compared only changed-vs-`main`, so unrelated drift could ride into an unrelated PR; the full-tree gate means every branch forks from a clean tree (#113).
- **VERSION bumped to `0.2.3`** for this release.

### Docs

- **Reviewing a Pull Request locally (`review-worktree-hygiene`)** - the Repository Operations SOP gains a "Reviewing a Pull Request Locally" section (inspect-without-checkout first; otherwise a throwaway sibling `git worktree` removed after review), with short pointers from `CONTRIBUTING.md` and `AGENTS.md`. Documents the reviewer-side local workflow that was previously a gap.

## [0.2.2] - 2026-06-23

**Multi-recipe throughput: apply many extract recipes to one input set in a single parse-once pass, plus the ergonomics — shared run-level parameters and derive-only capture fields — that make it adoptable.**

See [`docs/releases/v0.2.2.md`](docs/releases/v0.2.2.md) for the full release narrative.

### Added

- **Multi-recipe single-pass extract (`extract-multi`)** - `recipes run extract-multi <workspace>...` reads and parses each input file **once**, then dispatches the parsed document to every signature-matched recipe, amortizing the per-recipe re-parse (the dominant cost at high file counts) from ~N× to 1× across N recipes. Each recipe writes to its own isolated `<output-path>/<recipe-id>/` subdirectory (records, provenance manifest, and `dispositions.json` / `failures.json` when applicable); per-recipe output, formats, `defaults.parameters`, reference tables, and credential handles come from each recipe's own manifest, while the input set, output root, and run-level controls are shared, with a single shared run id. Failure handling follows the input-vs-recipe boundary (a read/parse failure is input-level; applicability/signature/extraction/`min_occurrences`/output failures are recipe-level and isolated under `--continue-on-error`). JSON/NDJSON output only in v0; the streaming/large-file path is not supported (#104, #105, #106, #107, #108, #109).
- **Shared run-level `--parameter` for `extract-multi` (`multi-parameter`)** - a repeatable `--parameter key=value` on `extract-multi` is a run-level override layered over **every** recipe's `defaults.parameters` with the same override / collision / typed-value (scalar or JSON-list) semantics as single-recipe `recipes run extract --parameter`. It satisfies each recipe's `parameters_required` independently and is injected into every recipe's records — for the genuinely per-run keys every recipe shares (e.g. a per-run provenance/runtime stamp). A shared key colliding with any recipe's `field_mappings[].output_field` fails the run at plan-load preflight; secret-shaped parameter values are redacted by key in the provenance argv (`--parameter` is not a credential transport) (#110).
- **Derive-only `source_extraction` captures (`internal-fields`)** - a `source_extraction` pattern may declare `internal: true`, making its named captures visible in `field_mappings[].expression` scope but **never emitted** into the record body on any sink (JSON/NDJSON/Parquet) — a true intermediate with no stray column. The flag defaults to `false` (existing captures emit unchanged); internal captures still participate in `source_extraction_required` and in the capture↔`output_field` / capture↔`defaults.parameters` collision checks. A capture name declared on both an `internal: true` and a non-internal pattern is rejected at plan validation so emit visibility is unambiguous (#111).

### Changed

- **VERSION bumped to `0.2.2`** for this release.

### Docs

- **`source_extraction { source: filename }` grain/provenance tagging (`filename-derive`)** - documented the filename named-capture pattern for tagging every record by which file or grain produced it, and its relationship to derived (`expression`) fields (#103).

## [0.2.1] - 2026-06-20

**Two dogfood-driven fixes: present-but-empty string elements bind `""` instead of erroring, and a batch file-list input that skips directory enumeration.**

See [`docs/releases/v0.2.1.md`](docs/releases/v0.2.1.md) for the full release narrative.

### Added

- **Batch file-list input (`input-prune`)** - `extract --file-list <path>` and recipe `defaults.input.files_from` read a newline-delimited list of input references (local paths or `s3://`/`file://` URIs, one per line; `#` comments and blank lines ignored), feeding the existing batch path with **no directory walk** and without the `--files` argv ceiling. Relative local entries resolve against the list file's directory; listed order is preserved; an unsupported scheme or an empty list fails loud with line context. Mutually exclusive with `--files` / `--input-path`; the recipe schema gains `input.files_from` (#101).
- **`--input-path` discovery visibility (`input-prune`)** - directory enumeration now announces its start, reports the matched count and elapsed time, and warns loudly past a slow threshold, so an accidental over-scope or a slow walk is visible in the first seconds instead of a silent multi-minute stall. No change to discovery results (#101).

### Changed

- **Present-but-empty string elements bind `""` (`empty-element-bind`)** - an XPath _string_ field over an element that is present but empty now binds the empty string (a defined value) in DSL scope, agreeing with what `boolean()` reports for the same node, so the `has_x ? f(string_x) : default` guard pattern no longer crashes with `undefined variable`. This is a behavior change for upgraders — see [`docs/releases/v0.2.1.md`](docs/releases/v0.2.1.md) (#100).
- **VERSION bumped to `0.2.1`** for this release.

## [0.2.0] - 2026-06-18

**S3-compatible cloud I/O, external reference-table lookup, list-typed recipe parameters, and a parallel-extract correctness fix.**

See [`docs/releases/v0.2.0.md`](docs/releases/v0.2.0.md) for the full release narrative.

### Added

- **Cloud URI I/O (S3-compatible)** - extract sources and outputs (and the provenance sidecar) may be `s3://` URIs across `extract`, `index`, and recipe runs, using named credential handles (references, never secrets); provenance records the logical source/destination and handle name only. Includes parallel/record-index cloud sources, anonymous public-bucket reads, a truly-dry `--dry-run` (no acquire/stage), `inspect` cloud reads, and classify+redact of cloud-I/O errors at a single seam (#78, #79, #80, #82, #83, #85, #86, #88, #89, #90, #91, #97).
- **External reference-table lookup** - recipes declare reference tables loaded once per run, queried by the `in_reference` (membership) and `lookup_reference` (key→value) DSL functions, from a contained local path or an `s3://` object with a `credentials_handle`; CSV/TSV/NDJSON, `max_rows`/`max_bytes` caps, a `--reference-table name=source` override, local-path containment, a cloud pre-read size cap, config-validation pre-flight, and sidecar-only provenance with no row values (#94, #95, #96).
- **List-typed recipe parameters** - parameters may be lists of strings with the `starts_with_any`, `value_in`, and `string_length` predicates for set classification; a `--parameter key='["a","b"]'` override supplies a list without a recipe edit (#92).
- **Scoped race-detector CI gate** - `make test-race-parallel` (`-race -count=1` on the parallel-extract + index packages and the canonical repro) is wired into CI (#98).

### Changed

- **Provenance sidecar schema (`sumpter.provenance/v1`)** - additive `reference_tables` block (name, effective source, format, mode, row count, content hash, caps, optional handle name); existing manifests remain valid (#95, #96).
- **VERSION bumped to `0.2.0`** for this release.

### Fixed

- **Parallel-extract data races** - the streaming index store returns a detached header snapshot (no caller-held header mutated mid-iteration), and each worker compiles its own XPath plans while sharing the immutable reference-table registry and resolved parameters read-only; ownership/snapshot fixes, no hot-path locks (#98).
- **Cloud provider-construction error redaction** - the S3 provider-construction error now routes through the shared classify+redact seam, completing the "no raw cloud-op error surfaces" invariant (#97).
- **`--version` out of tree** - `--version` and `index build` metadata report the build-injected version regardless of the working directory (#93).
- **`make install` codesign inode-cache kill** - `install` removes any existing binary before copying so the new binary lands on a fresh inode, avoiding the macOS/arm64 `zsh: killed` (SIGKILL) on reinstall; removal failure is fail-loud, a missing target is not.

Older versions are archived under [`docs/releases/`](docs/releases/).
