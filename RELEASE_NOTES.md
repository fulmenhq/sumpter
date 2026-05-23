# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.1.4 (2026-05-23)

**Recipe ergonomics, DSL evolution, extract integrity, and the first signed release pipeline.**

v0.1.4 brings sumpter's accumulated improvements since the v0.1.2 release under a single tagged, signed release. The v0.1.3 and v0.1.4 development cycles shipped continuously to main; v0.1.4 is the first formal tag to incorporate that work, and it lands alongside a new hand-rolled release pipeline (minisign + optional PGP signing, checksum manifests, CI-built artifacts, manual signing ceremony).

Three threads dominate the user-visible changes:

1. **Recipe ergonomics.** Authors can now declare per-recipe parameters with CLI overrides, derive parameters from source paths, attach cadence metadata, and control how columns flow into Parquet outputs. The same parameter values are visible both as output columns AND as DSL expression variables.
2. **DSL evolution.** Ternary conditionals, string-handling functions (`lower`, `upper`, `normalize_space`), quoted-string-literal hardening, and recipe-parameter access in expression scope. The consolidated DSL reference now lives in a single canonical document.
3. **Extract integrity and provenance.** Zero-record extract runs now emit empty output artifacts (instead of silently omitting them), per-selector `min_occurrences` floors are enforced, runtime provenance is captured in sidecar manifests, and recipes carry an explicit `content_version` with a migration path.

### What's new (summary)

- **Recipe parameters and ergonomics** — `defaults.parameters.*` declarations, `--parameter <key>=<value>` CLI overrides, source-path-derived parameters, cadence metadata, Parquet withhold-columns, and parameter access in DSL expression scope.
- **DSL evolution** — ternary conditionals (`cond ? a : b`), string functions (`lower`, `upper`, `normalize_space`), strict quoted-string-literal parsing, consolidated DSL reference at `docs/dsl-reference.md` v1.1.
- **Extract integrity** — zero-record runs emit artifacts (no more silent omission), explicit `min_occurrences` floors per selector, Parquet secondary output, grouped reconciliation hardening, upstream `xpath.Compile`/`xpath.Evaluate`.
- **Provenance** — runtime provenance core, per-output sidecar manifests, recipe `content_version` + `sumpter recipes migrate`.
- **Operations + CLI polish** — root-level `cobra.SilenceUsage` on runtime errors, `sumpter inspect --generate-extract`, examples harness with OSS-clean replacements.
- **Dependencies** — direct-dep minor-version refresh, SBOM regenerated out-of-tree, license + cooling policy enforced via `.goneat/dependencies.yaml`.
- **Release infrastructure** — `.github/workflows/release.yml`, `RELEASE_CHECKLIST.md` operator runbook, minisign + optional PGP signing ceremony, multi-platform CGO-free release artifacts.

### Behavior changes (please review before upgrading)

- **DSL quoted-string parsing now strict.** Bare unquoted values containing quote characters must now be written as quoted values (`name == Bob's` → `name == "Bob's"`). Unterminated string literals fail loudly during parse.
- **`match_selectors[].min_occurrences` defaults to 0.** Recipes that relied on an implicit positive floor must declare it explicitly.
- **Zero-record extract runs emit artifacts.** Pipelines that detected zero-record runs by file-absence must now check the manifest's record count.
- **Recipe-parameter / record-field name collisions fail loud.** Disambiguate via renaming; a configurable per-recipe collision policy may be introduced in a future brief if fallback semantics earn a use case.

### Out of scope (deferred)

- **SUM-005 — Cloud URI I/O.** Cloud URI support for input/output paths (S3/GCS/Azure) is deferred to v0.1.5; gonimbus-tag-blocked through most of the v0.1.4 cycle and unblocked just as v0.1.4 closed.

### Upgrade notes

- **Version bump.** `VERSION` is `0.1.4`. Binaries from this tag emit `v0.1.4` via `sumpter version`.
- **Go installation.** `go install github.com/fulmenhq/sumpter/cmd/sumpter@v0.1.4`.
- **Recipe migration.** Run `sumpter recipes migrate` to update older recipes to current schema.
- **Signed release verification** — see `docs/releases/v0.1.4.md §Upgrade notes` for the full `sha256sum -c` / `minisign -V` / `gpg --verify` sequence.

See [`docs/releases/v0.1.4.md`](docs/releases/v0.1.4.md) for the full release narrative.

---

## v0.1.3 (no separate release)

The v0.1.3 development cycle shipped continuously to main without a tagged release. SUM-001..009 (process-debt cleanup, recipe declared/derived parameters, provenance core + sidecar manifests, recipe `content_version` + `migrate`, extract derived field mappings, examples harness + replacement, Parquet secondary output, validator grouped-reconciliation hardening, extract output integrity, DSL ternary conditionals, DSL quoted-string hardening, DSL reference consolidation) all landed during the v0.1.3 cycle and are included in the v0.1.4 tag.

The earlier `docs/releases/v0.1.3.md` skeleton was never finalized into a release narrative; v0.1.4 supersedes it. See [`CHANGELOG.md`](CHANGELOG.md) for the chronological change list.

---

## v0.1.2 (2026-05-10)

**Seekable-Zstd Compressed Index Store + Bugfix Sweep.**

v0.1.2 introduced a high-performance seekable-zstd compressed index store that dramatically reduces disk usage and memory requirements for large-scale XML indexing. The release enables parallel random access to compressed record indexes without loading multi-GB files into memory.

### Hero feature: Seekable-Zstd record index store

A two-file compressed index format:

- **Header file** (`*.recordindex.header.json`): metadata, source info, and summary statistics
- **Records file** (`*.recordindex.records.szst`): fixed-width binary records with parallel random access

Compared to the JSON format, the seekable-zstd path delivers 10–20× smaller indexes (≈1 GB → 50–100 MB on ClinVar-scale corpora) with O(1) seek and parallel-ready random access. Compression ratio ≈10:1 via zstd.

### Surface additions

- `index build --emit-szst` — emit compressed index format
- `index stream` / `index verify` / `extract files --record-index` all auto-detect both JSON and seekable-zstd formats
- IndexStore abstraction with format-agnostic interface
- CI validation for Linux glibc + musl environments

See [`docs/releases/v0.1.2.md`](docs/releases/v0.1.2.md) for the full release narrative (technical architecture, binary record format, performance benchmarks, etc.).
