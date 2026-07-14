# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.3.1 (2026-07-14)

**Opt-in `process-run/v0` flight recorder for `extract-multi` — pin, event stream, process card, and terminal → data-artifact bridge.**

**Released:** 2026-07-14 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.1 adds an opt-in **process-run/v0** flight recorder on
`recipes run extract-multi`: a host-less contract pin, an append-only NDJSON
event stream, an owner-only process card under a runtime directory, and a
reference-only bridge from the sole terminal event to successfully published
`data-artifact/v0` descriptors.

The release is additive and opt-in. Runs that never enable process-run telemetry
keep extract outputs, exit codes, and provenance manifests byte-compatible with
v0.3.0. Process-run flags are omitted from the sanitized provenance argv. This
ships **observe-only** process-run surfaces — the card is telemetry-only; there
is no control socket in this release.

Operator notes: [`docs/process-run.md`](docs/process-run.md). Full narrative:
[`docs/releases/v0.3.1.md`](docs/releases/v0.3.1.md).

### Features

#### Host-less process-run contract baseline

`process-run/v0` reuses the same host-less contract resolution discipline as
`data-artifact/v0` (shared primitive). Pin checks cover the process-run
entry-bundle and sibling event-schema digests (Crucible **`v0.1.19`**:
entry-bundle
`sha256:4589befc1d0d3485744c7eea3dfb569ff79457f99996f2ee8313595489a7091b`,
event-schema
`sha256:7138fba72fea862d7964d6c235b1b93da0047e9eb76862be4d111701f887b12d`).
`make process-run-contract-check` is wired into `make check-all`.

#### Event stream

`extract-multi --process-run-events <path>` (or the process-run enable path)
emits a single-writer NDJSON stream: `started`, settled `progress`, heartbeat,
and exactly one terminal (`completed` / `failed` / `canceled`). Exclusive
create, owner-only `0600`, fail-open setup/write. Placement under home/workdir
roots is rejected. CLI cancel uses SIGINT/SIGTERM via context cancellation.
The terminal event is authoritative for run outcome — `done == total` alone is
not success.

#### Process card and reclaim

With process-run enabled (not stream-only), an owner-only discovery root is
published under `<runtime>/proc/<run_id>/` (`card.json`, `claim.json`,
`events.ndjson`, kernel `reclaim.lock`). Cards are pin-validated before publish
and appear only after atomic temp+hard-link publish. Clean exit withdraws the
card and retains the stream; crash leaves the discovery root. Stale reclaim is
fail-closed on live `(pid, started_at)` and serialized by `reclaim.lock`.

#### Terminal → data-artifact bridge

When process-run telemetry and `--artifact-descriptor` are both enabled,
successfully published descriptors appear on the sole terminal as
`data.artifacts[]` with exact `artifact_id` and `lifecycle` plus portable
non-locator `descriptor` (`<artifact_id>#descriptor`). Refs register only after
output Publish succeeds; multi-recipe runs list only successful publications in
plan order. Reference-only — no paths, cloud URIs, or recipe identity in the
event stream.

### Compatibility & notes

- **All-additive.** Default paths stay byte-compatible when process-run is off.
- **Telemetry vs durable output.** Event/card failures fail open; descriptor
  publish failures remain extract-fatal.
- **Two contracts.** `process-run/v0` (run telemetry) composes optionally with
  `data-artifact/v0` (portable extract output).
- **Platforms:** unchanged — linux amd64/arm64, darwin **arm64**, windows
  amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Process-run control socket / run-steering surface (later release track).
- Contract graduation and broader process-run control vocabulary.
- Event rotation, OTLP/forwarders, and WAN/TLS profiles.
- Richer data-artifact validation, cross-artifact lineage, and full semantic L3
  claims (unchanged from v0.3.0 posture).
- Cloud range-reads, cloud-side indexing, GCS/Azure, DuckDB/Arrow, service health
  endpoints, and repair modes remain roadmap items.

### Release notes

- **Version bump.** `VERSION` is `0.3.1`. Binaries from this tag emit `v0.3.1`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.1`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.1.md`](docs/releases/v0.3.1.md) for the full release narrative.

---

## v0.3.0 (2026-07-10)

**Portable `data-artifact/v0` producer profile — opt-in extract output legible to catalogs, query engines, and data planes.**

**Released:** 2026-07-10 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.3.0 makes Sumpter extract output portable under the host-less
`contract: data-artifact/v0` capability. Catalogs, query engines, and data planes
that understand that contract can read opted-in bundles without Sumpter-specific
knowledge. The producer profile string on the wire is `sumpter.extract-artifact/v0`.

The release is additive and opt-in. A run that never sets `--artifact-descriptor`,
never enables `defaults.value_profile`, and leaves `--validate-output` at `off` is
intended to stay compatible with pre-0.3.0 extract output (including ordinary
Parquet page statistics). This documents **baseline-bound structural producer
adoption**, not full semantic L3 export-gate conformance for every grain shape.

### Features

#### Host-less contract baseline

Extract resolves `data-artifact/v0` from an explicit local `--contract-base`
bundle. Production publish is baseline-gated against the pinned Crucible release
and resolved-bundle SHA-256 (current pin: Crucible **`v0.1.19`**,
`sha256:37eca167cfa9a86357c14239eb9c3274c40c5cfee48f48ebb81480d737104b82`).
Hash derivation is documented under
[Validate Command](docs/user-guide/commands/validate.md#data-artifact-contract-baseline-hash).
CI/offline fixtures match the pin; they are not an independently evolving
runtime identity.

#### Artifact descriptor, catalog, and grains

Opt-in `--artifact-descriptor` writes baseline-validated `artifact-descriptor.json`
and `fields/records.fields.json`:

- Primary grain `record_stream` by default; `--output-mode aggregate` switches the
  primary grain to `aggregation`; `--record-index` adds an **additional**
  `object_index` grain for the **consumed** record-index file (path-sanitized;
  not co-located by promise).
- Field catalog: source-structure keys withheld by count; disclosed fields default
  `unknown` + `block_export`; fully withheld catalogs are valid under the pin.
- Descriptor `lifecycle` maps from existing provenance completeness
  (`incomplete` / `partial` / `complete`).

#### Publication integrity

Local record, Parquet, and portable sidecar paths finalize via same-directory
temp+rename; Parquet renames only after a successful close; catalog publishes
before the descriptor; cloud validates staging before Publish.
`incomplete: true` inventories already-published aggregate cloud shards on failed
runs and must not be treated as successful output.

#### Protection declarations and Parquet suppression

Two layers: (1) portable protection metadata for consumers to enforce; (2) when
the descriptor is on, the Parquet writer suppresses page bounds/statistics on
every leaf and never wires Bloom filters. Pre-profile Parquet retains page stats.
Recipe `withhold_columns` remains a stronger projection control.

#### `--validate-output` ladder and `value_profile`

Cumulative opt-in modes: `off` → `sidecars` → `artifact` → `envelope-sample` →
`strict`, plus `sumpter validate artifact-descriptor`. Structural / baseline-bound
— not a complete L3 semantic or export-gate validator.

Optional provenance `value_profile` (recipe `defaults.value_profile`): Tier A
only under operator-declared `safe_to_profile` + `public|internal` +
`≤ max_distinct`; never-enumerate tags dominate; Tier B aggregates only;
disabled/omitted leaves manifests byte-identical.

### Compatibility & notes

- **All-additive.** Default paths stay byte-compatible when unused.
- **Two identities.** `contract: data-artifact/v0` vs `sumpter.extract-artifact/v0`.
- **Validation altitude.** Structural baseline, not full consumer-policy enforcement.
- **Platforms:** unchanged from 0.2.x — linux amd64/arm64, darwin **arm64**,
  windows amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull
  requests are welcome.

### Deferred / follow-ups

- Richer validation strictness, cross-artifact lineage, and reserved contract-slot
  activations.
- Sibling `process-run/v0` portable run observability / control (separate track).
- Full semantic L3 conformance for every grain shape.
- Cloud range-reads, cloud-side indexing, GCS/Azure, DuckDB/Arrow, service health
  endpoints, and repair modes remain roadmap items, as in 0.2.x.

### Release notes

- **Version bump.** `VERSION` is `0.3.0`. Binaries from this tag emit `v0.3.0`
  via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.3.0`
  is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in
  [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md).

See [`docs/releases/v0.3.0.md`](docs/releases/v0.3.0.md) for the full release narrative.

---

## v0.2.6 (2026-07-07)

**Namespace-correct XML extraction across whole-document, streaming, and indexed modes.**

**Released:** 2026-07-07 · **Lifecycle:** alpha (interface-stability track; external contributions welcome)

v0.2.6 adds opt-in namespace-aware XPath binding for recipes that need to be portable across XML documents with different literal prefixes. Recipe authors can declare a `namespaces:` map on extract record-match configs and file signatures, bind XPath prefixes to namespace URIs, and get the same extracted records from whole-document, streaming, and indexed execution paths.

The release is additive. Existing recipes without a `namespaces:` map keep their current behavior, and an absent or empty map preserves the byte-compatible floor. When a map is present, Sumpter fails closed on undeclared prefixes and treats namespace URIs as inert match keys, never as resources to fetch.

### Features

#### Namespace binding — `namespaces:`

XPath-bearing config assets can now declare aliases once and use them in selectors:

```yaml
namespaces:
  rec: "urn:example:sumpter-records"
  ext: "urn:example:sumpter-records-ext"

match_selectors:
  - xpath: "//rec:Record"

field_mappings:
  - output_field: "record_id"
    xpath: "@ext:id"
    type: "string"
```

The map is available on extract record-match configs and file signatures. The recipe manifest schema is unchanged.

- **URI binding, not prefix matching.** A recipe prefix such as `rec` is bound to a namespace URI. Input documents may use a different literal prefix, or a default namespace, and still match by URI.
- **Fail-closed explicit maps.** If a selector uses a prefix that is not declared in the present map, config loading fails before extraction.
- **Byte-compatible default.** Recipes with no map, or an empty map, use the legacy XPath behavior.
- **No namespace URI dereference.** Namespace URIs are compared as strings only; Sumpter does not fetch or resolve them.

#### Namespace-mode parity

Namespace-bound extraction now converges across whole-document, streaming, and indexed execution for field selection inside records. The shared synthetic conformance corpus covers prefixed, default-namespace, and dual-namespace documents plus adversarial namespace URI and prefix-shadowing cases.

Streaming and indexed record boundaries remain local-name-only in v0.2.6. That means URI binding applies to field selection after a record has been identified, while boundary-level URI disambiguation remains future scope.

#### Record-index `v0.1.2`

Record indexes now carry namespace context so indexed extraction can re-evaluate namespace-bound fields consistently.

- Namespace-free recipes continue to read legacy indexes unchanged.
- Namespace-bound recipes against pre-`v0.1.2` indexes fail loud with rebuild guidance instead of silently matching empty namespace context.
- Indexed slice re-injection escapes captured namespace values structurally, with regression coverage for adversarial URI values.

### Compatibility & notes

- **All-additive.** `namespaces:` is opt-in, and the map-absent path stays compatible with earlier releases.
- **Legacy prefixed XPath without a map.** Existing recipes keep the prior mode-dependent behavior. To get URI binding and fail-closed prefix validation, add an explicit `namespaces:` map.
- **Applicability predicates.** Applicability predicates are not namespace-bound by the new map in this release.
- **Record boundaries.** Streaming and indexed record-boundary selectors remain local-name-only in v0.2.6.
- **Platforms:** unchanged from 0.2.0-0.2.5 — linux amd64/arm64, darwin **arm64**, windows amd64/arm64. Intel-Mac users build from source.
- **Alpha:** interfaces may still change between minor releases; external pull requests are welcome.

### Deferred / follow-ups

- **Boundary-level URI binding** for streaming/indexed record detection remains future scope.
- **Public-domain namespace showcase corpora** are deferred to a later release; v0.2.6 uses synthetic hermetic fixtures for correctness.
- **Portable data-artifact contract support** remains on its own track.
- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, DuckDB/Arrow, service health endpoints, and repair modes** remain roadmap items, as in 0.2.5.

### Release notes

- **Version bump.** `VERSION` is `0.2.6`. Binaries from this tag emit `v0.2.6` via `sumpter version`.
- **Tag/version guard.** `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.6` is the intended tag/version sanity check.
- **Release ceremony.** Use the standard draft-release and signing flow in [`RELEASE_CHECKLIST.md`](RELEASE_CHECKLIST.md): CI builds the tag, then the operator signing ceremony uploads checksums, signatures, public keys, and release notes before publishing.

See [`docs/releases/v0.2.6.md`](docs/releases/v0.2.6.md) for the full release narrative.
