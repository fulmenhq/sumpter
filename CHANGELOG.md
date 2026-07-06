# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Retention policy: the latest 10 versions live inline; older versions are archived in `docs/releases/v<semver>.md`.

## [Unreleased]

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

## [0.1.10] - 2026-06-09

**Homebrew + Scoop distribution; Intel-Mac (`darwin-amd64`) prebuilt retired.**

See [`docs/releases/v0.1.10.md`](docs/releases/v0.1.10.md) for the full release narrative.

### Added

- **Homebrew + Scoop install paths** - `brew install fulmenhq/tap/sumpter` and `scoop install fulmenhq/sumpter` distribute sumpter from the fulmenhq tap/bucket using the raw-binary convention. `make update-homebrew-formula` / `make update-scoop-manifest` refresh the sibling formula/manifest from a published tag; RELEASE_CHECKLIST.md § Distribution documents the post-release housekeeping (SUM-039).
- **README install sections** - dedicated Homebrew and Scoop quick-start sections above the direct-download block.

### Changed

- **Supported-platform matrix: `darwin-amd64` (Intel-Mac) prebuilt retired.** The release matrix is now five raw binaries: linux amd64/arm64, darwin **arm64**, windows amd64/arm64. Intel-Mac users build from source (a source build on an Intel Mac produces a native `darwin-amd64`). Rationale: ecosystem-wide move off Intel-Mac prebuilts (most acute for CGO=1 projects).
- **VERSION bumped to `0.1.10`** for this release.

## [0.1.9] - 2026-06-09

**Complete the release artifact matrix — `windows-arm64` now shipped.**

See [`docs/releases/v0.1.9.md`](docs/releases/v0.1.9.md) for the full release narrative.

### Added

- **`sumpter-windows-arm64.exe` release binary** - the release artifact matrix now ships all six raw binaries (linux/darwin/windows × amd64/arm64), completing arch parity with the dimlox/refbolt convention. `release-checksums`, signing, and the `release.yml` upload all glob over `dist/release/*`, so the new binary flows through with no other tooling change (SUM-038).

### Changed

- **GitHub Actions runtimes bumped to Node 24** - `actions/checkout` (v4→v6), `actions/setup-go` (v5→v6), and `softprops/action-gh-release` (v2→v3) moved to their current Node-24 majors across all four workflows, clearing the GitHub Actions Node-20 runtime deprecation (PR #73).
- **VERSION bumped to `0.1.9`** for this release.

## [0.1.8] - 2026-06-08

**Bounded-memory JSON/NDJSON output streaming, CI/local toolchain parity, and public-data corpus expansion.**

See [`docs/releases/v0.1.8.md`](docs/releases/v0.1.8.md) for the full release narrative.

### Added

- **Bounded sequential JSON/NDJSON output streaming** - sequential extraction streams emitted records through the record-sink path instead of buffering full-result slices, bounding memory by parser state, active record work, and writer buffers (PR #65).
- **Bounded parallel JSON/NDJSON output streaming** - record-index parallel extraction streams output through a bounded reorder window that preserves source-document order without retaining all records (PR #67).
- **Streaming memory-regression proof** - a fixture and test assert heap stays flat at scale on the streaming output paths, guarding against future buffering regressions (PR #68).
- **Streaming eligibility gating** - `min_occurrences` handling and a large-file gate determine when the bounded streaming route applies versus the buffered fallback, with a warning when a sequential run falls back to the buffered floor (PR #69).
- **Toolchain contract** - `config/toolchain.env` pins the Go and golangci-lint versions for local and CI use; `make toolchain-check` verifies local tools, the `go.mod` toolchain, `GOFLAGS`, and `.goneat/tools.yaml` against the contract before linting (PR #70).
- **Public-data exemplars** - USGS QuakeML (geophysics), NWS CAP (public-safety geospatial), and GovInfo USLM (government/legal) recipe pairs and sliced public-domain samples, each runnable end-to-end (PR #71).

### Changed

- **Lint toolchain install path** - golangci-lint installs via a pinned `go install ...@v2.11.2` path (no brew/latest path); CI and release workflows load `config/toolchain.env` (PR #70).
- **Public-data positioning** - the public-data examples guide and README reframe the exemplars as a domain-neutrality demonstration (public-domain sources so every example ships runnable by anyone); public and proprietary formats are both first-class (PR #71).
- **VERSION bumped to `0.1.8`** for this release.

### Fixed

- **Staticcheck false-green class** - a pinned-staticcheck probe (`make lint-staticcheck-probe`) asserts the `SA5011` diagnostic is reported, closing the local/CI lint divergence surfaced during the v0.1.8 streaming work (PR #70).
- **Docs table-padding drift** - normalized pre-existing prettier table-padding drift in two docs that `make check-all` does not gate.

### Deferred

- Incremental Parquet writing, bounded mixed-output runs, bounded sequential `min_occurrences`, and ambiguous indexed-floor paths remain buffered in v0.1.8 and are future work. Cloud URI read/write is tracked as the next major capability thread.

## [0.1.7] - 2026-06-02

**Public-flip release: document-order semantics, streaming groundwork, index-scale hardening, and DX cleanup.**

See [`docs/releases/v0.1.7.md`](docs/releases/v0.1.7.md) for the full release narrative.

### Added

- **Document-order emission contract** - single-selector regular DOM, streaming, and indexed parallel extraction now preserve source-document output order and emit stable `_runtime.record_num` values assigned before filters run (PR #58).
- **Record-sink streaming contract** - ADR-0009 defines the emitted-envelope sink contract, ordering rules, backpressure requirements, fatal output-error behavior, and Parquet buffering exception for future bounded-output work (PR #59).
- **Sequential record-sink primitives** - sequential extraction gained the internal sink interfaces and groundwork needed to move JSONL/NDJSON output away from full-result slices in later implementation work (PR #60).
- **Streaming index writers** - `index build` can stream JSON record-index output and seekable-zstd index stores during the build path instead of retaining every record metadata row before writing (PR #61).

### Changed

- **Public-surface genericization** - internal coordination identifiers and non-public reference wording were scrubbed from the public docs and examples surface (PR #57).
- **Formatter scope** - `make fmt-docs` now formats tracked and non-ignored files according to Git's exclude rules, keeping ignored local scratchpads and planning notes out of formatter scope (PR #62).
- **VERSION bumped to `0.1.7`** for this release.

### Fixed

- **Record-index artifact integrity** - streaming index writers publish through transactional temp/prepare/commit/complete steps so parse, start, or commit failures do not leave corrupt final artifacts or clobber existing outputs (PR #61).
- **Raw-byte record hashing** - index record hashes are computed from source byte ranges so integrity checks remain stable across writer implementations (PR #61).
- **Formatter noise** - ignored `.scratchpad/` content no longer produces YAML-format warnings during local docs formatting (PR #62).

### Security

- **Public-flip confidentiality posture** - the release keeps repository-facing examples, docs, release notes, and PR messaging generic and leaves private data, local settings, and specialized recipes outside the repository tree (PR #57/#62).

### Deferred

- **Bounded end-to-end JSONL/NDJSON output streaming** remains roadmap work. v0.1.7 ships the contract and sequential primitives, but command paths still buffer extracted records per source file before output.
- **Parallel sink integration and incremental Parquet writing** remain future work. Parquet is still a projection path and may buffer rows.

## [0.1.6] - 2026-06-01

**Public-readiness, capability honesty, and streaming/index correctness.**

See [`docs/releases/v0.1.6.md`](docs/releases/v0.1.6.md) for the full release narrative.

### Added

- **Uniform per-record schema** - recipes can emit declared-but-absent properties as explicit `null` values for stable downstream table shapes (PR #52).
- **Final PR drift gate** - `make pr-final` now checks generated/embedded drift before release-prep completion (PR #45/#46).
- **ADR-0008 confidentiality posture** - sensitive data belongs outside repository working trees, with operator-specific scans handled out of band (PR #48).

### Changed

- **Go 1.26 floor** - `go.mod` now declares `go 1.26.0` with `toolchain go1.26.3`; stale seekable-zstd integration containers now use Go 1.26 images.
- **Public capability copy** - README, overview, envinfo schemas, and release docs now distinguish shipped outputs from roadmap sinks and integrations (PR #54).
- **VERSION bumped to `0.1.6`** for this release.

### Fixed

- **Compressed-source index safety** - record index metadata and extraction paths no longer imply unsafe byte seeking for compressed sources (PR #53).
- **Streaming/index selector over-match** - streaming extraction and `index build --selector` now reject unsupported selectors instead of silently reducing XPath forms to local names (PR #55).
- **Final readiness correctness sweep** - retrieval, DSL defaults, and memory-contract wording were corrected before the public repository transition (PR #47).

### Security

- **Public-readiness and dependency posture** - release metadata, dependency checks, and security gate documentation were aligned for the final release-prep step (PR #45/#46).

### Deferred

- **Record-sink output streaming and index streaming** are deferred to v0.1.7 or later; v0.1.6 keeps the honest memory contract that extracted records are buffered per file before output.
