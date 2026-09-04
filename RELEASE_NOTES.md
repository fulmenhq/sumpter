# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.3.4 (2026-09-04)

**Bounded cloud extract with distinct logical reader and writer handles.**

**Released:** 2026-09-04 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.4 adds opt-in `--cloud-input-mode bounded` on `extract-multi` so `s3://`
URI-list inputs are acquired just-in-time under run-global staging caps and a
per-object max. Eager staging remains the default. Objects above the
per-object cap are refused before staging.

The shared CLI selects the reader handle; each recipe owns its writer handle.
`extract-multi` does not take `--output-credentials-handle`. Cloud aggregate
still requires a positive `--aggregate-max-bytes` at or below the 5 GiB
single-PUT ceiling.

This cut was verified with distinct **logical** reader and writer handles on
bounded cloud-to-local (100,000 objects, exact bytes/records) and
cloud-to-cloud (1,000 objects, independent size and digest read-back). A live
different-account writer was not proven and is not a claim of this release.

**Start here:** [`docs/extract-workflow.md`](docs/extract-workflow.md). Full
narrative: [`docs/releases/v0.3.4.md`](docs/releases/v0.3.4.md).

### Added

#### Bounded cloud extract

- `--cloud-input-mode bounded` with `--cloud-staging-max-bytes`,
  `--cloud-staging-max-files`, and `--cloud-object-max-bytes`.
- Provenance hashes each staged object before reap.
- Hermetic `FixtureDocument` examples; 7 MiB admit under an 8 MiB cap;
  oversize pre-stage refusal with staging cleanup.

#### extract-multi cloud handles

- CLI `--input-credentials-handle` is the reader; recipe YAML names the writer.
- Workflow docs show a positive `--aggregate-max-bytes` and the 5 GiB ceiling.

### Compatibility & notes

- **Eager default unchanged.** Bounded mode is opt-in.
- **Not cross-account validated.** Logical handles are distinct; the writer
  account was not proven different from the reader.
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Nested item/polymorphic internals.
- Process-run control socket / run-steering (unchanged from v0.3.1–v0.3.3).
- Richer data-artifact validation and full semantic L3 claims (unchanged).
- Million-object bounded-cloud characterization is not part of this cut.

### Release notes

- **Version bump.** `VERSION` is `0.3.4`. Binaries from this tag emit `v0.3.4`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.4`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.4.md`](docs/releases/v0.3.4.md) for the full release narrative.

---

## v0.3.3 (2026-07-27)

**Stock `github.com/antchfx/xpath` v1.3.8 — interim local pin retired.**

**Released:** 2026-07-27 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.3 retires the interim `./third_party/antchfx-xpath` tree that v0.3.2 used
for numeric operand-context isolation. Upstream merged and tagged the fix
(antchfx/xpath#124 → **v1.3.7** on the merge commit; **v1.3.8** is current).
Sumpter now depends on the stock module at **v1.3.8** with no `replace`.
`xmlquery` remains **v1.5.1**.

Behavior for predicated `sum(...) *` context-sensitive factors matches the
v0.3.2 pin. No new recipe surfaces; no process-run or data-artifact contract
pin changes.

**Start here:** [`docs/extract-workflow.md`](docs/extract-workflow.md). Full
narrative: [`docs/releases/v0.3.3.md`](docs/releases/v0.3.3.md).

### Changed

#### XPath pin retirement (`xpath-sum-multiply`)

- `go.mod` requires `github.com/antchfx/xpath v1.3.8`.
- Deleted `third_party/antchfx-xpath` and the `go.mod` `replace`.
- Hermetic extract regressions for the silent-wrong class stay green against
  the module cache.
- Operator docs: trailing factors are correct on this binary; factor-first
  remains a valid style (and the safer form on older Sumpter builds).

### Compatibility & notes

- **Corrective path unchanged** vs v0.3.2 for the operand-context isolation class.
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Nested item/polymorphic internals.
- Process-run control socket / run-steering (unchanged from v0.3.1 / v0.3.2).
- Richer data-artifact validation and full semantic L3 claims (unchanged).
- Cloud range-reads, GCS/Azure, DuckDB/Arrow, service health endpoints, and
  repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.3`. Binaries from this tag emit `v0.3.3`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.3`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.3.md`](docs/releases/v0.3.3.md) for the full release narrative.

---

## v0.3.2 (2026-07-15)

**Same-record helpers without stray columns, and no more silent-wrong XPath sign arithmetic.**

**Released:** 2026-07-15 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.2 is for recipe authors who need same-record intermediates without
polluting output schemas, and for anyone who multiplies a predicated `sum(...)`
by a context-sensitive factor in XPath:

- Mark a top-level `field_mappings` entry `internal: true` — compute a helper
  once, reuse it from later expressions, never emit a column or portable
  catalog field.
- Trailing context-sensitive factors now evaluate against the correct node —
  **no more silent-wrong sign totals** that still report a green extract.

The internal-mapping surface is additive and byte-compatible when unused. The
XPath fix changes results only for expressions that previously hit the wrong
operand context. No process-run or data-artifact contract pin changes.

**Start here:** [`docs/extract-workflow.md`](docs/extract-workflow.md). Full
narrative: [`docs/releases/v0.3.2.md`](docs/releases/v0.3.2.md).

### Features

#### Derive-only field mappings (`internal-field-mappings`)

**Minimal enable:**

```yaml
field_mappings:
  - output_field: sign_factor
    xpath: "1 - 2*count(self::RefundEvent)" # synthetic
    type: number
    internal: true
  - output_field: amount
    expression: "sign_factor * raw_amount"
    type: number
```

- Two-phase evaluation: all top-level XPath mappings, then expressions in order.
- Projected out before filters, schema fill, validation, enrichment,
  value_profile, and sinks.
- Omitted from record bodies, Parquet columns, field_provenance **entries**, and
  the portable field catalog.
- Plan-load rejects internal names in `value_profile.fields`, `output_schema`,
  and filters.
- Expression-only internals allowed; nested item/polymorphic internals deferred.
- **Names are not confidential** (may appear in recipe provenance and expression
  lineage) — do not put secrets in field names.

#### XPath numeric operand isolation (`xpath-sum-multiply`)

Predicated `sum(...) * factor` no longer silently mis-evaluates the factor.
v0.3.2 used an interim pin of `github.com/antchfx/xpath` under
`./third_party/antchfx-xpath` (`xmlquery` stayed v1.5.1). That pin is **retired
in v0.3.3** in favor of stock **v1.3.8**.

### Compatibility & notes

- **All-additive** for `internal: true` when unused; no-opt recipes unchanged.
- **Corrective** for XPath expressions that previously evaluated trailing
  context-sensitive factors against the wrong node.
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Nested item/polymorphic internals.
- ~~Upstream-tagged xpath release to retire the local pin~~ → done in **v0.3.3**.
- Process-run control socket / run-steering (unchanged from v0.3.1).
- Richer data-artifact validation and full semantic L3 claims (unchanged).
- Cloud range-reads, GCS/Azure, DuckDB/Arrow, service health endpoints, and
  repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.2`. Binaries from this tag emit `v0.3.2`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.2`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.2.md`](docs/releases/v0.3.2.md) for the full release narrative.
