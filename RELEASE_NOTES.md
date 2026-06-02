# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.1.7 (2026-06-02)

**Public flip, document-order semantics, streaming groundwork, and index-scale hardening.**

v0.1.7 closes the final public-surface and developer-experience cleanup after v0.1.6 while landing the next layer of streaming architecture. It gives consumers a stable document-order record contract, defines the record-sink output-streaming contract, adds sequential sink primitives, and makes index builds stream index metadata instead of retaining every record row before writing.

The release is intentionally precise about memory behavior: XML input tokenization is incremental, but extracted records are still buffered per source file before JSONL/NDJSON and optional Parquet output. v0.1.7 ships the contract and groundwork for bounded output streaming, not the final bounded end-to-end output implementation.

### What's new (summary)

- **Document-order record semantics** - single-selector regular DOM, streaming, and indexed parallel extraction preserve source-document order and emit `_runtime.record_num` before filters run.
- **Record-sink streaming contract** - ADR-0009 defines emitted-envelope sinks, ordering, backpressure, close/finalize behavior, and fatal output-error semantics.
- **Sequential sink primitives** - internal groundwork is in place for moving JSONL/NDJSON paths away from full-result slices in later implementation work.
- **Streaming index writers** - `index build` streams JSON and seekable-zstd index artifacts, with transactional publish behavior that preserves existing outputs on failure.
- **Public-surface cleanup** - repository-facing docs and examples keep generic language and avoid internal coordination identifiers.
- **Formatter-scope cleanup** - `make fmt-docs` now respects Git ignore rules, keeping ignored scratchpads and local planning notes out of formatter scans.

### Behavior changes (please review before upgrading)

- **Single-selector output order is contracted.** Consumers can rely on source-document order and `_runtime.record_num` for single-selector extraction paths.
- **Filtered records leave `record_num` gaps.** Surviving records are not renumbered after filters or skips.
- **Multi-selector recipes remain selector-major.** Do not treat their `record_num` values as a global document-order contract across selectors.
- **Index build writes incrementally.** JSON and seekable-zstd index outputs are streamed and published transactionally.
- **Docs formatting excludes ignored local-only files.** Ignored scratchpads should no longer produce local formatter warnings.

### Deferred

- **Bounded end-to-end JSONL/NDJSON output streaming** remains future work.
- **Parallel sink integration and incremental Parquet writing** remain future work.
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.1.7`. Binaries from this tag emit `v0.1.7` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.7` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-flip gate before tagging and publication.

See [`docs/releases/v0.1.7.md`](docs/releases/v0.1.7.md) for the full release narrative.

---

## v0.1.6 (2026-06-01)

**Public-readiness, capability honesty, and streaming/index correctness.**

v0.1.6 is the final release-prep step before the public repository transition. It tightens Sumpter's public capability surface, hardens selector and index behavior, adds uniform per-record schema output, and documents the confidentiality posture expected for the open-source repository.

The release keeps the memory contract explicit: XML input is tokenized incrementally, while extracted records are still buffered per file before output.

### What's new (summary)

- **Honest public capability surface** - README, overview, schemas, and command outputs distinguish shipped outputs from planned sinks and integrations.
- **Uniform per-record schema** - recipes can emit declared-but-absent properties as explicit `null` values for stable downstream table shapes.
- **Safer record indexes** - compressed-source indexes no longer imply unsafe byte seeking for parallel extraction.
- **Restricted streaming/index selector grammar** - streaming extraction and `index build --selector` reject unsupported XPath forms instead of silently over-matching.
- **Release and confidentiality gates** - `make pr-final`, refreshed OSS metadata, and ADR-0008 align the public-readiness path.

### Behavior changes (please review before upgrading)

- **Go 1.26 is the minimum supported Go version.**
- **Streaming/index selectors are intentionally narrower.** Use `Name` or `//Name`; predicates, multi-segment paths, prefixes, and other XPath forms fail loud in streaming/index mode.
- **XML local-name matching in streaming/index mode is case-sensitive.**
- **Compressed-source indexes are not advertised as seekable extraction inputs.**
- **Planned sinks and service integrations are described as roadmap items.**

### Deferred

- **Record-sink output streaming and index streaming follow-up work** were deferred to v0.1.7 or later.
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release and operator notes

- `VERSION` is `0.1.6`. Binaries from this tag emit `v0.1.6` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.6` is the intended tag/version sanity check.

See [`docs/releases/v0.1.6.md`](docs/releases/v0.1.6.md) for the full release narrative.

---

## v0.1.5 (2026-05-26)

**Release readiness, recipe applicability gates, and resilient multi-file extraction.**

v0.1.5 is a focused hardening release after v0.1.4. It proves the signed-release path introduced in v0.1.4, adds PR-time release validation so tag-time surprises are caught earlier, and delivers two extraction workflow improvements for mixed input cohorts: applicability gates and continue-on-error failure manifests.

The release is intentionally narrow. It does not broaden cloud URI support; instead it makes the current recipe and release surfaces more predictable before the next feature cycle.

### What's new (summary)

- **Recipe applicability gates** - recipes can point `assets.applicability` at a YAML predicate file. The predicate runs before signature matching; false inputs are reported as `not_applicable`, produce no records, and do not count as extraction failures.
- **Disposition summaries** - applicability-aware runs add per-input provenance dispositions and write `dispositions.json` at the output root, validated by `schemas/extract/v0.1.0/dispositions.schema.json`.
- **Continue-on-error extraction** - multi-file `extract files` and `recipes run extract` runs may opt into `--continue-on-error`. Successful inputs still write normal outputs; recoverable per-input failures are captured in `failures.json`.
- **Failure manifest schema** - `schemas/extract/v0.1.0/failures.schema.json` defines the closed failure-reason set used by `failures.json`.
- **Release hardening** - tag-triggered GitHub Releases are draft-by-default until the operator signing ceremony attaches checksum manifests, signatures, public keys, and notes.
- **PR-time release dry run** - CI now validates `make release-build` in the release container before merge.
- **Toolchain alignment** - CI and release workflows use Go `1.26.3`; CI installs `golangci-lint` `v2.11.2` to analyze Go 1.26 source.

### Behavior changes (please review before upgrading)

- **`--continue-on-error` is opt-in and still exits non-zero when any input fails.** The non-zero exit tells schedulers the cohort was not fully clean; `failures.json` carries the per-input details.
- **`--continue-on-error` requires `--output-path` in v0.** Sumpter needs a durable output root for the failure manifest and successful sibling outputs.
- **Output-write and manifest-write failures remain terminal.** The flag isolates recoverable per-input parse/signature/validation failures; it does not mask durability failures.
- **Applicability gates run before signature matching.** Inputs that evaluate false are not treated as signature failures. Inputs that evaluate true still proceed through normal signature and extraction checks.
- **Applicability is currently limited to supported file-run modes.** Unsupported combinations fail loud rather than silently skipping the predicate.
- **Signature mismatch diagnostics are clearer.** Signature misses that previously surfaced through downstream `min_occurrences` paths now report `signature_mismatch` with confidence and threshold details.

### Deferred

- **Cloud URI I/O** remains deferred to a future cycle; v0.1.5 keeps scope on release hardening and local/multi-file extraction resilience.
- **Shell shfmt baseline cleanup** remains a separate housekeeping item.

### Release and operator notes

- `VERSION` is `0.1.5`. Binaries from this tag emit `v0.1.5` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.5` is the intended tag/version sanity check.
- The release ceremony remains manifest-only for provenance: CI builds binaries, the operator verifies checksums, signs checksum manifests with minisign and optional PGP, exports public keys, uploads provenance assets, then promotes the draft release.
- The known release-script shfmt baseline remains deferred to a separate housekeeping PR. It is visible as nine medium goneat lint findings and is non-blocking under the current hook policy.

See [`docs/releases/v0.1.5.md`](docs/releases/v0.1.5.md) for the full release narrative.
