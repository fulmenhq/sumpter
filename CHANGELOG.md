# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Retention policy: the latest 10 versions live inline; older versions are archived in `docs/releases/v<semver>.md`.

## [Unreleased]

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

## [0.1.5] - 2026-05-26

**Release-readiness hardening, recipe applicability gates, and resilient multi-file extraction.**

See [`docs/releases/v0.1.5.md`](docs/releases/v0.1.5.md) for the full release narrative.

### Added

- **Recipe applicability gates** - recipes may reference an `assets.applicability` YAML asset with an XPath predicate. Applicability runs before signature matching; false predicates mark inputs `not_applicable` without counting them as extraction failures (PR #39).
- **Schema-backed extraction dispositions** - applicability-aware runs add per-input provenance dispositions and write `dispositions.json` summaries backed by `schemas/extract/v0.1.0/dispositions.schema.json` (PR #39).
- **Per-file failure manifests** - `sumpter extract files` and `sumpter recipes run extract` support `--continue-on-error` for multi-file cohorts. Recoverable per-input failures are written to `failures.json`, backed by `schemas/extract/v0.1.0/failures.schema.json` (PR #40).
- **PR-time release-build dry run** - CI now runs normal quality gates plus a `make release-build` dry run in the same container shape used by the tag-triggered release workflow, catching release-container drift before tag time (PR #38).

### Changed

- **Release workflow hardening** - tag-triggered releases publish as drafts first, so signing manifests and provenance assets can be attached before the release is made public (PR #37).
- **Release tag guardrails** - release ceremony targets prefer `SUMPTER_RELEASE_TAG` and fail loud if the requested tag does not match `VERSION`; `RELEASE_TAG` remains available for one-off invocations (PR #37).
- **Go toolchain pin** - CI and release workflows now use Go `1.26.3`, and CI installs `golangci-lint` `v2.11.2` so linting can analyze Go 1.26 source (PR #41).
- **VERSION bumped to `0.1.5`** for this release.

### Fixed

- **PGP verification ceremony** - release signature verification now checks for both minisign and PGP output when both signatures are present, preventing a silent skipped-verification path (PR #37).
- **Signature mismatch routing** - inputs that miss a recipe signature and then encounter declared `min_occurrences` floors now report `signature_mismatch` with confidence details instead of a misleading `min_occurrences` violation (PR #40).

### Security

- **No unsigned public-release window** - GitHub Releases remain drafts until checksum manifests, signatures, public keys, and release notes are attached and verified by the operator ceremony (PR #37).
- **Go standard-library vulnerability fix path** - the local and CI toolchains are aligned on Go `1.26.3`, resolving the Go 1.26.1 standard-library vulnerability findings that blocked local `govulncheck` during validation (PR #41).

### Deferred

- **Cloud URI I/O** remains deferred to a future cycle; v0.1.5 focuses on release hardening and local/multi-file extraction resilience.
- **Shell shfmt baseline cleanup** remains visible as nine medium goneat lint findings in release scripts. It is non-blocking under the current hook policy and is intentionally deferred to a separate housekeeping PR.

## [0.1.4] - 2026-05-23

**First tagged release since v0.1.1 OSS-clean; introduces the signed-release pipeline and brings the prior development cycle under a tag.**

See [`docs/releases/v0.1.4.md`](docs/releases/v0.1.4.md) for the full release narrative.

### Added

- **Recipe parameters in DSL expression scope** — `defaults.parameters.<key>` values are now readable as variables in `field_mappings[].expression` evaluation, in addition to their existing record-map column emission. Name collisions with XPath-extracted (or previously-evaluated) fields fail loud with an explicit error (PR #34).
- **Recipe cadence metadata** — `defaults.cadence` accepts a free-form string (`daily-rolling`, `weekly`, `weekly-2x`, `on-demand`, or operator-chosen vocabulary) that flows into the per-extract provenance record (PR #31).
- **Parquet withhold columns** — `defaults.output.parquet.withhold_columns` declares columns that flow through the substrate/partition path but are omitted from the Parquet output body (PR #30).
- **DSL string functions** — `lower(s)`, `upper(s)`, `normalize_space(s)` (null-propagating) (PR #32).
- **Signed release pipeline** — `.github/workflows/release.yml`, `RELEASE_CHECKLIST.md`, `RELEASE_NOTES.md`, hand-rolled minisign + optional PGP signing ceremony, `make release-*` targets, 10 new release scripts (generate-checksums, sign-release-manifests, verify-checksums, verify-public-key, verify-minisign-public-key, release-download, release-upload-provenance, release-upload, export-release-keys, sign-release-artifacts). Multi-platform CGO-free release artifacts (linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64).

### Changed

- **`SilenceUsage` on root command** — runtime errors (extract-time contract violations, schema failures, missing parameters) no longer dump full cobra usage help. Genuine flag-parse errors still emit usage (PR #29).
- **Direct dependency refresh** — minor-version bumps; pre-1.0 minor-bump risk evaluated per-bump; SBOM regenerated out-of-tree under gitignored `sbom/` (PR #33).
- **VERSION bumped to `0.1.4`** for this release.

### Security

- **`--fail-on high`** is the new default for `goneat dependencies` vulnerability gating (stricter than the goneat default of `critical`) and is applied in CI.
- **`sbom/` is gitignored and never committed.** SBOMs are operator-local artifacts; the OSS-surface artifact is the package-change summary in PR descriptions and release notes.

### Deferred

- **Cloud URI I/O** (S3/GCS/Azure input/output paths) deferred beyond v0.1.4; still out of scope for v0.1.5.

## [0.1.3] - 2026-05-18

**v0.1.3 cycle shipped continuously to main without a tagged release. Its work is included in the v0.1.4 tag.**

### Added

- **Runtime provenance core** — every extract run produces a provenance record capturing recipe `content_version`, recipe parameters (resolved values, including CLI overrides), and per-output sidecar manifests (PR #11).
- **Extract sidecar manifests** — `*.manifest.json` files ship alongside NDJSON/Parquet outputs, capturing per-output statistics, source attribution, and recipe lineage (PR #13).
- **Recipe `content_version` + `migrate` subcommand** — recipes carry an explicit content-version field; `sumpter recipes migrate` migrates older recipes to current schema (ADR-0006 PR-A, PR #9).
- **Recipe `defaults.parameters.*` declarations** — recipe authors declare per-recipe string constants that become output columns on every extracted record; CLI `--parameter <key>=<value>` overrides supported (PR #18).
- **Source-path-derived parameters** — `defaults.source_extraction[]` extracts recipe parameters from regex matches against the source file path; `parameters_required[]` marks which keys must resolve to a non-empty value at extract time (PR #19).
- **Derived field mappings** — `field_mappings[].expression` supports DSL expressions for derived column values (PR #17).
- **Parquet secondary output** — `output.parquet` declares a Parquet sink alongside the default NDJSON output (PR #23).
- **`sumpter inspect --generate-extract`** produces a starter `extract.yaml` from observed XML structure (PR #22).
- **Examples harness** with OSS-clean replacement examples (PRs #20, #21).
- **DSL ternary conditionals** — `cond ? a : b` syntax in expressions and filters (PR #26).

### Changed

- **`match_selectors[].min_occurrences` defaults to `0`** — non-zero floors are explicit opt-in enforcement (was: implicit positive floor). Per-selector floors are now enforced so aggregate record counts cannot mask a selector-specific violation.
- **Successful zero-record extract runs emit empty output artifacts and manifest entries** instead of silently omitting payload files. Downstream consumers can no longer mistake a successful zero-record run for a failed run with missing files.
- **XPath compile/evaluate** — extract now uses upstream `xpath.Compile`/`xpath.Evaluate` rather than an ad-hoc selector parser (PR #4).
- **DSL reference consolidation** — canonical DSL reference at `docs/dsl-reference.md` v1.1 (PR #28).

### Fixed

- **DSL quoted-string-literal hardening** — DSL parsing now treats operator characters inside quoted string literals as literal content, not split points. Unterminated string literals fail loudly during parse. Applies across binary expressions, ternary expressions, simple filters, function arguments, and accumulation filter routing (PR #27).
- **Grouped reconciliation hardening** — validator handling is safer against malformed input and clearer in error reporting (PR #24).
- **YAML schema 3-space indent break repaired** (PR #3).

### Security

- **gosec scan scope tightened** — module cache, vendored deps, and generated code excluded from scans (PR #10).
- **License compliance enforced** via `.goneat/dependencies.yaml`: no GPL-3.0/AGPL-3.0/MPL-2.0; permissive licenses (MIT/Apache-2.0/BSD/ISC/0BSD/Unlicense) allowed.

### Docs

- **OSS-clean sanitization** — genericized ADR examples, swept legacy persona names, added public-data examples index (PRs #7, #14).
- **Dataeng role override** for downstream review workflows (PR #12).
- **Agents-md alignment** with the role model (PR #6).

### Removed

- **Vestigial `BlindingConfig` types** removed from extract surface (PR #15).

## [0.1.2] - 2026-05-10

### Added

- **Seekable-Zstd Compressed Index Store** - 10-20x smaller indexes with parallel random access
  - Two-file format: `*.recordindex.header.json` (metadata) + `*.recordindex.records.szst` (compressed records)
  - `--emit-szst` flag for `index build` command
  - Automatic format detection in all index commands
  - CGO bindings with pre-built libraries for Linux glibc and musl
- **Format-Agnostic Index CLI** - All index commands support both JSON and seekable-zstd formats
  - `index stream`: Stream-walk indexes without loading into memory
  - `index verify`: Verify index integrity against source XML
  - `index build`: Generate indexes in JSON, seekable-zstd, or both formats
- **CI/CD for Cross-Platform Builds** - GitHub Actions workflow validates CGO linking
  - Linux glibc (Debian Bookworm) validation
  - Linux musl (Alpine) validation

### Changed

- Parallel extraction orchestrator now streams records on-demand (constant memory)
- Index commands auto-detect format by file extension
- Build tooling improvements: renamed `pre-commit`/`pre-push` to `precommit`/`prepush`

### Fixed

- Yamllint issues in CI workflow files
- Routine security improvements from audit findings

### Docs

- Comprehensive index workflow guide with seekable-zstd documentation
- Container deployment patterns for CGO builds
- v0.1.2 release notes with migration guide

## [0.1.1] - 2025-10-13

### Added

- **Hybrid Streaming XML Architecture** (ADR-0005): Constant-memory XML processing for 50GB+ files
  - RecordScanner with token-by-token streaming and XPath-based record selection
  - Automatic streaming mode for files >100MB with `--allow-large-files` flag
  - 99.95% memory reduction: 111GB → 50MB RSS for 50GB XML files
  - Transparent .gz decompression support in streaming mode
- Recipe system with manifest-based workflows (`recipes` command)
  - `recipes init`: Scaffold new recipe workspaces with templates
  - `recipes run extract`: Execute extract recipes with manifest defaults
  - `recipes retrieve`: Acquire data from APIs and file systems (SEC EDGAR support)
  - Manifest validation with JSON Schema 2020-12
- Comprehensive test suite achieving 50% coverage (alpha phase gate)
  - Streaming package: 83.9% coverage (exceeds production 80% threshold)
  - Transforms: 91.4% coverage
  - Recipes: 86.7% coverage
  - DSL validation: 68.0% coverage
- New test files: transforms, recipes manifest, regulatory scraper, validate/retrieve commands, doctor/envinfo
- Doctor command for environment setup and diagnostics
  - Interactive SEC EDGAR configuration wizard with compliance validation
  - Environment health checks and setup script generation
- Retrieve command with validation helpers and path security

### Changed

- Extract command automatically uses streaming for large files when `--allow-large-files` is set
- Enhanced extract record-match schema with streaming mode documentation
- Recipe manifest schema with defaults, input/output configuration, and asset references

### Fixed

- Recipe subcommand test expectations (init/retrieve/run vs list/show/run)
- Validate command test compilation errors

### Docs

- ADR-0005: Hybrid Streaming XML Architecture with performance benchmarks
- Recipe system documentation and workflow examples
- Release notes for v0.1.1 with streaming architecture details

## [0.1.0] - 2025-09-18

### Added

- Initial release: Sumpter v0.1.0 bootstrap
- XML inspection foundations with dialect registry and SEC EDGAR dialect
- Logging component with console/JSON output and PII redaction
- Makefile quality gates; schema validation via goneat; coverage scripts
- Embedded docs and schemas; CLI commands: version, envinfo, inspect, docs

### Fixed

- Errcheck issues in envinfo

### Docs

- SOPs, ADRs, and user guides embedded
