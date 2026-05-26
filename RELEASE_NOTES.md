# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

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

---

## v0.1.4 (2026-05-23)

**Recipe ergonomics, DSL evolution, extract integrity, and the first signed release pipeline.**

v0.1.4 brings sumpter's accumulated improvements since the v0.1.2 release under a single tagged, signed release. The v0.1.3 and v0.1.4 development cycles shipped continuously to main; v0.1.4 is the first formal tag to incorporate that work, and it lands alongside a new hand-rolled release pipeline (minisign + optional PGP signing, checksum manifests, CI-built artifacts, manual signing ceremony).

Three threads dominate the user-visible changes:

1. **Recipe ergonomics.** Authors can now declare per-recipe parameters with CLI overrides, derive parameters from source paths, attach cadence metadata, and control how columns flow into Parquet outputs. The same parameter values are visible both as output columns AND as DSL expression variables.
2. **DSL evolution.** Ternary conditionals, string-handling functions (`lower`, `upper`, `normalize_space`), quoted-string-literal hardening, and recipe-parameter access in expression scope. The consolidated DSL reference now lives in a single canonical document.
3. **Extract integrity and provenance.** Zero-record extract runs now emit empty output artifacts (instead of silently omitting them), per-selector `min_occurrences` floors are enforced, runtime provenance is captured in sidecar manifests, and recipes carry an explicit `content_version` with a migration path.

### What's new (summary)

- **Recipe parameters and ergonomics** - `defaults.parameters.*` declarations, `--parameter <key>=<value>` CLI overrides, source-path-derived parameters, cadence metadata, Parquet withhold-columns, and parameter access in DSL expression scope.
- **DSL evolution** - ternary conditionals (`cond ? a : b`), string functions (`lower`, `upper`, `normalize_space`), strict quoted-string-literal parsing, consolidated DSL reference at `docs/dsl-reference.md` v1.1.
- **Extract integrity** - zero-record runs emit artifacts (no more silent omission), explicit `min_occurrences` floors per selector, Parquet secondary output, grouped reconciliation hardening, upstream `xpath.Compile`/`xpath.Evaluate`.
- **Provenance** - runtime provenance core, per-output sidecar manifests, recipe `content_version` + `sumpter recipes migrate`.
- **Operations + CLI polish** - root-level `cobra.SilenceUsage` on runtime errors, `sumpter inspect --generate-extract`, examples harness with OSS-clean replacements.
- **Dependencies** - direct-dep minor-version refresh, SBOM regenerated out-of-tree, license + cooling policy enforced via `.goneat/dependencies.yaml`.
- **Release infrastructure** - `.github/workflows/release.yml`, `RELEASE_CHECKLIST.md` operator runbook, minisign + optional PGP signing ceremony, multi-platform CGO-free release artifacts.

### Behavior changes (please review before upgrading)

- **DSL quoted-string parsing now strict.** Bare unquoted values containing quote characters must now be written as quoted values (`name == Bob's` -> `name == "Bob's"`). Unterminated string literals fail loudly during parse.
- **`match_selectors[].min_occurrences` defaults to 0.** Recipes that relied on an implicit positive floor must declare it explicitly.
- **Zero-record extract runs emit artifacts.** Pipelines that detected zero-record runs by file-absence must now check the manifest's record count.
- **Recipe-parameter / record-field name collisions fail loud.** Disambiguate via renaming; a configurable per-recipe collision policy may be introduced in a future brief if fallback semantics earn a use case.

See [`docs/releases/v0.1.4.md`](docs/releases/v0.1.4.md) for the full release narrative.

---

## v0.1.3 (no separate release)

The v0.1.3 development cycle shipped continuously to main without a tagged release. SUM-001..009 (process-debt cleanup, recipe declared/derived parameters, provenance core + sidecar manifests, recipe `content_version` + `migrate`, extract derived field mappings, examples harness + replacement, Parquet secondary output, validator grouped-reconciliation hardening, extract output integrity, DSL ternary conditionals, DSL quoted-string hardening, DSL reference consolidation) all landed during the v0.1.3 cycle and are included in the v0.1.4 tag.

The earlier `docs/releases/v0.1.3.md` skeleton was never finalized into a release narrative; v0.1.4 supersedes it. See [`CHANGELOG.md`](CHANGELOG.md) for the chronological change list.
