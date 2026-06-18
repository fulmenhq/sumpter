# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.2.0 (2026-06-18)

**S3-compatible cloud I/O, external reference-table lookup, list-typed recipe parameters, and a parallel-extract correctness fix.**

v0.2.0 is the first minor release after the 0.1.x line. It makes S3-compatible cloud I/O first-class across the engine, adds external reference-table lookups and list-typed recipe parameters for richer recipe-driven classification and enrichment, and fixes a parallel-extraction data race — backed by a new race-detector gate in CI. The cloud URI read/write thread deferred since v0.1.8 lands here.

### What's new (summary)

- **Cloud URI I/O (S3-compatible)** - `s3://` sources and outputs (and the provenance sidecar) across `extract`, `index`, recipe runs, and `inspect`, using named credential handles (references, never secrets). Includes parallel/record-index cloud sources, anonymous public-bucket reads (read-only), a truly-dry `--dry-run` (no acquire/stage), and classify+redact of cloud-I/O errors at a single seam. Provenance records the logical source/destination and handle name only.
- **External reference-table lookup** - recipes load reference tables once per run and query them with the `in_reference` (membership) and `lookup_reference` (key→value) DSL functions, from a contained local path or an `s3://` object; CSV/TSV/NDJSON, `max_rows`/`max_bytes` caps, with local-path containment, a cloud pre-read size cap, config-validation pre-flight, and sidecar-only provenance (no row values).
- **List-typed recipe parameters** - parameters may be lists of strings with the `starts_with_any`, `value_in`, and `string_length` predicates; a `--parameter key='["a","b"]'` override supplies a list without a recipe edit.
- **Scoped race-detector CI gate** - `make test-race-parallel` is wired into CI over the parallel-extract and index packages plus the canonical repro.

### Behavior changes (please review before upgrading)

- **Cloud I/O is opt-in and back-compatible.** A run that references no `s3://` URI does no credential or network work; local/bare paths behave byte-for-byte as before.
- **Parallel-extract data races fixed** (detached index-header snapshot + per-worker XPath plans); no output/provenance behavior change, no hot-path locks.
- **Provenance sidecar schema (`sumpter.provenance/v1`)** gained an additive `reference_tables` block; existing manifests remain valid.
- **No output-format changes** to existing local extraction/index paths.

### Deferred

- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, and >5 GiB multipart cloud output** remain follow-on work (an unsupported URI returns an actionable error today).
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.2.0`. Binaries from this tag emit `v0.2.0` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.0` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.0.md`](docs/releases/v0.2.0.md) for the full release narrative.

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
