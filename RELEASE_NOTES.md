# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.2.1 (2026-06-20)

**Two dogfood-driven fixes: present-but-empty string elements bind `""` instead of erroring, and a batch file-list input that skips directory enumeration.**

v0.2.1 is a focused patch off the v0.2.0 dogfood feedback (a 1.2M-row run). It carries two independent fixes — a present-but-empty XML string element now binds a defined `""` instead of an undefined value, and a new batch file-list input hands the engine an exact file set with no directory walk. The v0.2.0 surface (cloud I/O, reference-table lookup, list-typed parameters) is unchanged.

### What's new (summary)

- **Present-but-empty string elements bind `""` (`empty-element-bind`)** - an XPath string field over a present-but-empty element now binds the empty string (a defined value) instead of undefined, agreeing with `boolean()` presence, so the `has_x ? f(string_x) : default` guard pattern no longer crashes with `undefined variable`. The v0.2.0 doc-note workaround already shipped; this is the real fix.
- **Batch file-list input (`input-prune`)** - `extract --file-list <path>` and recipe `defaults.input.files_from` read a newline-delimited list of input references (local paths or `s3://`/`file://` URIs; `#` comments and blanks ignored), feeding the existing batch path with **no directory walk** and without the `--files` argv ceiling. Relative local entries resolve against the list file's directory; order preserved; unsupported scheme / empty list fail loud with line context. Mutually exclusive with `--files` / `--input-path`. Cloud entries are verified end to end (moto).
- **`--input-path` discovery visibility (`input-prune`)** - directory enumeration now announces its start, reports matched count + elapsed, and warns loudly past a slow threshold; no change to discovery results.

### Behavior changes (please review before upgrading)

- **Present-but-empty binding changed.** A recipe that previously errored `undefined variable` on a present-but-empty element now produces `""` for that field. Absent vs present-but-empty remains distinguishable (`boolean()` is `false` vs `true`); only the present-but-empty *string binding* changed from undefined to `""`. To reject empty elements, add an explicit guard (`string_length(field) > 0 ? … : …`).
- **No other output-format changes.** The v0.2.0 cloud-I/O, reference-table, list-parameter, and provenance behavior is unchanged; the platform matrix is identical to 0.2.0.

### Deferred

- **File-list single-read refactor**, **`files_from`/`mode` precedence docs-or-validation**, and **directory-prune + streaming discovery** (plus a secondary `--input-glob` shortcut) are tracked follow-ups.
- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, DuckDB/Arrow, service health endpoints, Prometheus metrics, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.2.1`. Binaries from this tag emit `v0.2.1` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.1` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.1.md`](docs/releases/v0.2.1.md) for the full release narrative.

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
