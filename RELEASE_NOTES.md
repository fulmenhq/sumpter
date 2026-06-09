# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.1.10 (2026-06-09)

**Homebrew + Scoop distribution; Intel-Mac (`darwin-amd64`) prebuilt retired.**

v0.1.10 makes Sumpter installable through Homebrew and Scoop, and trims the release matrix to the platforms worth shipping prebuilt. It is a distribution-ergonomics release — no product-code changes.

### What's new (summary)

- **Homebrew + Scoop install paths** - `brew install fulmenhq/tap/sumpter` and `scoop install fulmenhq/sumpter` install from the FulmenHQ tap/bucket using the raw-binary convention (no archives), building on v0.1.9's arch-complete matrix. Two thin delegation targets (`make update-homebrew-formula` / `make update-scoop-manifest`) refresh the sibling tap/bucket repos; RELEASE_CHECKLIST.md § Distribution documents the post-release housekeeping.
- **README install sections** - dedicated Homebrew + Scoop quick-start sections above the direct-download block.

### Behavior changes (please review before upgrading)

- **`darwin-amd64` (Intel-Mac) prebuilt retired.** The release matrix is now five raw binaries: linux amd64/arm64, darwin **arm64**, windows amd64/arm64. Intel-Mac users build from source (a source build on an Intel Mac produces a native `darwin-amd64`). Ecosystem-wide move off Intel-Mac prebuilts; not a CI-cost change (single-runner pure-Go cross-compile).
- **`brew`/`scoop` are now first-class install paths** alongside direct download and `go install`.
- **No product-code, output-format, or CLI changes.**

### Deferred

- **Cloud URI read/write (S3 and S3-compatible) I/O** remains the next major capability thread, tracked separately.
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.1.10`. Binaries from this tag emit `v0.1.10` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.10` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication. Homebrew/Scoop PRs land as post-release housekeeping.

See [`docs/releases/v0.1.10.md`](docs/releases/v0.1.10.md) for the full release narrative.

---

## v0.1.9 (2026-06-09)

**Complete the release artifact matrix — `windows-arm64` now shipped.**

v0.1.9 is a small, front-loaded distribution patch. It closes the one gap in Sumpter's published binary matrix: the release now ships `sumpter-windows-arm64.exe`, so all six raw binaries (linux/darwin/windows × amd64/arm64) are present and arch-complete. There are no product-code changes.

This release deliberately precedes the Homebrew + Scoop wiring (next cycle): the package-manager formula and manifest reference these published assets by URL, so the full matrix must exist and be published first.

### What's new (summary)

- **`windows-arm64` release binary** - `make release-build` / `make build-all` now emit `sumpter-windows-arm64.exe` alongside the existing five targets, completing arch parity with the dimlox/refbolt convention. The binary cross-compiles on the pure-Go path (`CGO_ENABLED=0`); checksums, signatures, and the release upload all glob over the release directory, so the new binary is covered with no other tooling change.
- **GitHub Actions runtimes on Node 24** - `actions/checkout` (v4→v6), `actions/setup-go` (v5→v6), and `softprops/action-gh-release` (v2→v3) moved to their current Node-24 majors across all four workflows, clearing the GitHub Actions Node-20 runtime deprecation.

### Behavior changes (please review before upgrading)

- **Windows-on-ARM is now a published target.** Windows/ARM64 users get a native binary directly from the release instead of relying on emulation of the amd64 build.
- **No product-code or output-format changes.** Extraction, indexing, and CLI behavior are identical to v0.1.8.

### Deferred

- **Homebrew + Scoop wiring** is the next cycle (v0.1.10) and depends on these published assets.
- **Cloud URI read/write (S3 and S3-compatible) I/O** remains the next major capability thread, tracked separately.

### Release notes

- `VERSION` is `0.1.9`. Binaries from this tag emit `v0.1.9` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.1.9` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.1.9.md`](docs/releases/v0.1.9.md) for the full release narrative.

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
