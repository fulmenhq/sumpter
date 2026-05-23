# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Retention policy: the latest 10 versions live inline; older versions are archived in `docs/releases/v<semver>.md`.

## [Unreleased]

## [0.1.4] - 2026-05-23

**First tagged release since v0.1.1 OSS-clean; introduces the signed-release pipeline and brings the v0.1.4 cycle (SUM-010..020) under a tag.**

See [`docs/releases/v0.1.4.md`](docs/releases/v0.1.4.md) for the full release narrative.

### Added

- **Recipe parameters in DSL expression scope** — `defaults.parameters.<key>` values are now readable as variables in `field_mappings[].expression` evaluation, in addition to their existing record-map column emission. Name collisions with XPath-extracted (or previously-evaluated) fields fail loud with an explicit error (PR #34, SUM-020).
- **Recipe cadence metadata** — `defaults.cadence` accepts a free-form string (`daily-rolling`, `weekly`, `weekly-2x`, `on-demand`, or operator-chosen vocabulary) that flows into the per-extract provenance record (PR #31, SUM-016).
- **Parquet withhold columns** — `defaults.output.parquet.withhold_columns` declares columns that flow through the substrate/partition path but are omitted from the Parquet output body (PR #30, SUM-015).
- **DSL string functions** — `lower(s)`, `upper(s)`, `normalize_space(s)` (null-propagating) (PR #32, SUM-017).
- **Signed release pipeline** — `.github/workflows/release.yml`, `RELEASE_CHECKLIST.md`, `RELEASE_NOTES.md`, hand-rolled minisign + optional PGP signing ceremony, `make release-*` targets, 10 new release scripts (generate-checksums, sign-release-manifests, verify-checksums, verify-public-key, verify-minisign-public-key, release-download, release-upload-provenance, release-upload, export-release-keys, sign-release-artifacts). Multi-platform CGO-free release artifacts (linux/{amd64,arm64}, darwin/{amd64,arm64}, windows/amd64).

### Changed

- **`SilenceUsage` on root command** — runtime errors (extract-time contract violations, schema failures, missing parameters) no longer dump full cobra usage help. Genuine flag-parse errors still emit usage (PR #29, SUM-014).
- **Direct dependency refresh** — minor-version bumps; pre-1.0 minor-bump risk evaluated per-bump; SBOM regenerated out-of-tree under gitignored `sbom/` (PR #33, SUM-019).
- **VERSION bumped to `0.1.4`** for this release.

### Security

- **`--fail-on high`** is the new default for `goneat dependencies` vulnerability gating (stricter than the goneat default of `critical`) — established by the SUM-019 deps-hygiene brief and applied in CI.
- **`sbom/` is gitignored and never committed.** SBOMs are operator-local artifacts; the OSS-surface artifact is the package-change summary in PR descriptions and release notes.

### Deferred

- **SUM-005 — Cloud URI I/O** (S3/GCS/Azure input/output paths) deferred to v0.1.5; gonimbus-tag-blocked through most of the v0.1.4 cycle and unblocked just as v0.1.4 closed.

## [0.1.3] - 2026-05-18

**v0.1.3 cycle shipped continuously to main without a tagged release. SUM-001..009 work landed during this cycle and is included in the v0.1.4 tag.**

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
- **Dataeng role override** for `agent-dataeng-blue` (PR #12).
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
