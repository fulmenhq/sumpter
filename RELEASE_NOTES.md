# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.1.8 (2026-06-08)

**Bounded-memory JSON/NDJSON output streaming, CI/local toolchain parity, and public-data corpus expansion.**

v0.1.8 delivers the bounded end-to-end output streaming that v0.1.7 deferred. JSON and NDJSON file output now streams through the record-sink path for both sequential and record-index parallel runs, so extraction memory stays bounded instead of buffering every record per source file before writing.

The bound is precise: it covers JSON/NDJSON sequential and record-index parallel output. Parquet, mixed-output runs (JSON/NDJSON and Parquet together), sequential `min_occurrences` paths, and ambiguous indexed-floor paths remain buffered in v0.1.8 by design.

### What's new (summary)

- **Bounded JSON/NDJSON output streaming** - sequential and record-index parallel extraction emit records through the record-sink path; parallel runs preserve document order through a bounded reorder window. Memory is bounded by parser state, active record work, writer buffers, and (parallel) the reorder window.
- **Streaming memory-regression proof** - a fixture and test assert heap stays flat at scale so future changes cannot silently reintroduce full-result buffering.
- **Streaming eligibility gating** - `min_occurrences` handling and a large-file gate decide when the bounded route applies; a sequential run that falls back to the buffered floor now warns.
- **CI/local toolchain parity** - `config/toolchain.env` pins the Go and golangci-lint versions; `make toolchain-check` fails before linting on drift, and a pinned-staticcheck probe asserts `SA5011` is reported.
- **Public-data corpus expansion** - USGS QuakeML, NWS CAP, and GovInfo USLM exemplars (recipes + runnable public-domain samples) widen the worked-example corpus across three new verticals.

### Behavior changes (please review before upgrading)

- **JSON/NDJSON output is bounded for sequential and record-index parallel runs.** Capacity planning for these paths can assume bounded output memory.
- **Parquet, mixed-output, sequential `min_occurrences`, and ambiguous indexed-floor paths remain buffered.**
- **Sequential buffered fallback now warns** so the behavior is observable.
- **Local lint can fail before linting on toolchain drift** via `make toolchain-check` (run by `make lint` / `make check-all`).

### Deferred

- **Incremental Parquet writing, bounded mixed-output runs, and bounded sequential `min_occurrences` / ambiguous indexed-floor paths** remain future work.
- **Cloud URI read/write (S3 and S3-compatible) I/O** is the next major capability thread, tracked separately.
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.1.8`. Binaries from this tag emit `v0.1.8` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.8` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.1.8.md`](docs/releases/v0.1.8.md) for the full release narrative.

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
