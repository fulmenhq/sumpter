# Data-Artifact Producer Profile (Sumpter)

**How Sumpter adopts the portable `data-artifact/v0` contract.**

This document is the in-repo **reference producer-adoption** guide for Sumpter.
It is additive documentation only: it does not change runtime behavior.

> **Headline for existing users.** Almost nothing changes if you adopt nothing.
> The portable surfaces are **opt-in** and **byte-compatible** on the default
> path. What changes is that opted-in output becomes legible to consumers that
> understand `contract: data-artifact/v0` without Sumpter-specific knowledge.

> **Security posture at a glance.** (1) **Opt-in / byte-compatible default path**
> — adopting nothing leaves pre-profile extract behavior. (2) **Default-deny** —
> top-level `block_export` / `internal`, catalog fields default `unknown` +
> `block_export`, and `value_profile` enumerates only under an affirmative gate.
> (3) **Declare vs enforce** — Sumpter declares portable protection metadata and
> applies producer-side physical controls (Parquet page-metadata suppression when
> the descriptor is on); consumers and data planes enforce export and read policy.

Operational command detail (flags, recipes, cloud I/O) lives in
[Extract Workflow](extract-workflow.md). Descriptor validation lives in
[Validate Command](user-guide/commands/validate.md).

This release documents **baseline-bound structural producer adoption**: extract
emits descriptors and catalogs that validate against the pinned contract bundle
and follow the protection floors below. It is not a claim of full semantic L3
export-gate conformance for every grain shape (see
[Current validation scope](#current-validation-scope)).

---

## What stays the same

Nothing a current user depends on is renamed or removed by the producer profile:

| Surface | Posture |
| --- | --- |
| Record envelopes | `extract.data` / `_runtime` layout unchanged |
| `format: json` / `ndjson` | Same NDJSON/JSONL writer family (`json` remains the canonical token) |
| Provenance | Still `sumpter.provenance/v1`; new fields are omit-empty only |
| Parquet | Still a projection of `extract.data`; existing `sumpter.column.*` metadata remains valid when written |
| Recipes / DSL / reconciliation | Unchanged unless you opt into new recipe keys (for example `defaults.value_profile`) |

A run that never sets `--artifact-descriptor`, never enables
`defaults.value_profile`, and leaves `--validate-output` at `off` is intended to
remain compatible with pre-profile extract output (including ordinary Parquet
page statistics).

---

## Capability identity (host-less)

Two identities appear on the wire and must not be conflated:

| Identity | Role |
| --- | --- |
| `contract: data-artifact/v0` | Portable **contract** capability (host-less). Selects the galaxy contract and its entry schema. |
| `sumpter.extract-artifact/v0` | Sumpter **producer profile** string (`producer.profile` / opaque `profile_ref`). Identifies this concrete adoption of the contract. |

Resolution uses an explicit local **`--contract-base`**: a directory containing
`contract.json` and the relative entry schema it names. Production publish is
**baseline-gated**: the resolved bundle must match Sumpter's pinned Crucible
release and resolved-bundle SHA-256, not merely any directory that advertises
`data-artifact/v0`. The current pin is Crucible **`v0.1.19`** with:

```text
sha256:37eca167cfa9a86357c14239eb9c3274c40c5cfee48f48ebb81480d737104b82
```

How that digest is computed (file order and path-delimited hash input) is
documented under
[Data Artifact Contract Baseline Hash](user-guide/commands/validate.md#data-artifact-contract-baseline-hash).

The fixture tree `tests/fixtures/data-artifact-contract/v0` is a **CI / offline
conformance input** that matches the pin. It is not an independently evolving
runtime identity: changing it without updating the pin fails closed.

Before publishing an artifact descriptor, extract validates the generated JSON
against that resolved baseline (fail-closed).

---

## Publication integrity

Local record outputs, Parquet files, and portable sidecars are finalized with
same-directory **temp + rename** so an interrupted run does not leave a torn
canonical file. Parquet is renamed into place only after the writer closes
successfully (footer present). Generated field-catalog and descriptor payloads
are validated against the pinned baseline **before** publish; the catalog is
published **before** the descriptor that references it. Cloud destinations
validate the complete staging file **before** Publish (single PutObject), then
remove staging.

In aggregate mode, a failed run that already published one or more cloud shards
may emit `incomplete: true` on the provenance manifest solely to inventory those
shards for cleanup or rerun. Treat `incomplete: true` as a **failed** run, not
as successful output.

---

## Opt-in surfaces

Adoption is a dial. Emitting none of these remains valid. One fixed point: if
`value_profile` is emitted at all, its default-deny guard is mandatory.

### 1. Artifact descriptor

```bash
sumpter extract files \
  --files ./input.xml \
  --signature-config-path ./signature.yaml \
  --extract-config-path ./extract.yaml \
  --output-path ./out \
  --artifact-descriptor \
  --contract-base ./contracts/data-artifact/v0
```

Writes `artifact-descriptor.json` beside the provenance manifest. Requires
`--output-path`, a normal manifest, and `--contract-base`.

**Grains.** The **primary records grain** is always present. Its `kind` depends
on output mode; other grains may be added:

| Condition | Grain behavior |
| --- | --- |
| Default / per-input extract | Primary grain `kind: record_stream` |
| `--output-mode aggregate` | Primary grain becomes `kind: aggregation` (same protection floors as the record stream — no lineage laundering). Multi-shard aggregate runs mark representations `sharded` and attach per-shard digests when present. |
| `--record-index` | **Additional** `object_index` grain describing the **record-index file consumed** by indexed extraction. Sumpter path-sanitizes the reference (relative under known roots, otherwise basename) so host-local absolute paths never appear. It does **not** copy the index into the output bundle and does **not** promise the basename resolves beside the descriptor. |

Identity is non-deterministic: `artifact_id` is a fresh UUID URN per run.
Integrity is carried by digests where present; a rerun is a new artifact even if
byte content matches.

### 2. Field catalog sidecar

With `--artifact-descriptor`, extract also writes
`fields/records.fields.json` (refs from the descriptor; not an embedded catalog
on the production path).

- Source-structure-derived keys (xpath / description) are **withheld by count**
  (`withheld_field_count`), not listed by name.
- Disclosed fields default to `sensitivity: unknown` and
  `export_action: block_export` (default-deny).
- A fully withheld catalog (`fields: []` + positive withheld count) is valid
  under the pinned baseline.

### 3. Portable lifecycle

Descriptor `lifecycle` is mapped from existing provenance completeness signals
— no second accounting system:

| Provenance signal | `lifecycle` |
| --- | --- |
| `incomplete: true` | `incomplete` |
| Any failed inputs | `partial` |
| Otherwise | `complete` |

`draft`, `building`, and `retired` are reserved by the contract and are not
emitted for finished extract runs.

### 4. Protection declarations and writer-side metadata suppression

Two layers:

1. **Portable declarations** — Sumpter emits protection metadata on the
   descriptor and field catalog. Consumers / data planes **enforce** export and
   read policy from those declarations.
2. **Producer-side physical controls** — when `--artifact-descriptor` is on,
   the Parquet writer suppresses page bounds and page statistics on every leaf
   and never configures Bloom filters. That is a Sumpter integrity/privacy
   control on the file bytes, not a substitute for consumer export gates.

| Surface | Default posture |
| --- | --- |
| Top-level `protection` | `default_action: block_export`, `default_export_class: internal`, opaque `profile_ref` |
| NDJSON / aggregate NDJSON | `protection_enforceable_granularity: row` |
| Parquet **with** `--artifact-descriptor` | `column` floor; page bounds + page statistics suppressed on every leaf; Bloom filters never wired |
| Parquet **without** descriptor | Pre-profile writer configuration (page stats retained); no portable column claim |
| Scan claims | `columnar_scan` only — no `predicate_pushdown` without a matching `pushdown_withheld` set |

Recipe `defaults.output.parquet.withhold_columns` is a stronger, separate
control: named columns are omitted from the Parquet projection entirely
(JSON/NDJSON still include them). It composes with descriptor-side catalog
withholding and page-metadata suppression.

### 5. Guarded `value_profile`

Optional diagnostic on the **provenance** manifest (not the artifact
descriptor). Recipe:

```yaml
defaults:
  value_profile:
    enabled: true
    max_distinct: 100
    small_cell_threshold: 5
    fields:
      - field: status
        safe_to_profile: true
        sensitivity: public
      - field: account_id
        protection_tags: [linkage_key]
```

Field classification (`sensitivity`, `protection_tags`, `safe_to_profile`) is
**operator-declared**. Enabling `value_profile` is not a blanket “safe on any
field.” Never-enumerate dominance backstops the worst mis-tags; it does not
replace careful field configuration.

- **Tier A (concrete values)** only when `safe_to_profile` **and** sensitivity
  is `public` or `internal` **and** distinct count is **at or below**
  `max_distinct` (`≤ max_distinct`).
- **Never-enumerate tags** (`direct_identifier`, `source_structure`,
  `opaque_payload`, `access_control_metadata`) force aggregates-only even if
  public + safe_to_profile is set.
- **Tier B** emits counts, capped distinct form, length stats, and coarse shape
  — never sample values, top-K, or string min/max. Capped fields use
  `status: high_cardinality_capped` with `distinct_count: ">=N"` only.
- Quasi / linkage small cells are suppressed under `small_cell_threshold`.
- Hard ceiling on `max_distinct` is 10000 (recipe + runtime + output schema).
- Disabled / omitted → no `value_profile` field (byte-identical manifests).

Observation is exactly-once per committed logical record (not per output
format). Failed or floor-rejected inputs discard staged observations.

### 6. `--validate-output` ladder

Opt-in extract validation (`off` by default). Modes are **cumulative** where
noted; higher rungs include lower portable checks:

| Mode | Checks |
| --- | --- |
| `off` (default) | No extra output validation |
| `sidecars` | Provenance `manifest.json`; `failures.json` / `dispositions.json` when present |
| `artifact` | `sidecars` plus generated `artifact-descriptor.json` and `fields/records.fields.json` (requires `--artifact-descriptor` and `--contract-base`) |
| `envelope-sample` | `artifact` plus sampled NDJSON envelopes (first, every 100th, last) against the extract-record-envelope schema |
| `strict` | `artifact` plus **every** NDJSON record envelope |

Validation runs on the complete local (or cloud staging) file **before**
Publish so cloud destinations cannot drop staging before the check.

Standalone descriptor check:

```bash
sumpter validate artifact-descriptor ./out/artifact-descriptor.json \
  --contract-base ./contracts/data-artifact/v0
```

#### Current validation scope

`--validate-output artifact` and `sumpter validate artifact-descriptor` provide
**baseline-bound structural / schema validation** (and field-catalog shape
checks). They are **not** a complete L3 semantic or export-gate validator:
they do not enforce consumer policy, full predicate-pushdown rules, or every
contract prose requirement for queryable grains.

Known honesty notes for this adoption:

- A catalog-less `object_index` is structurally valid and shipped as a sanitized
  reference to a **consumed** record index; contract prose for fully queryable
  object indexes may still require catalog/lineage refinements.
- Sharded aggregate outputs still need the opaque shard-id / count-expectation
  semantic story closed for full L3 conformance claims.

Treat this guide as documenting **what Sumpter produces and pins today**, not as
unqualified full semantic conformance for every grain.

---

## Scope guardrails (this profile)

**Does:**

- Make extract bundles discoverable and protection-legible under
  `data-artifact/v0` at the structural baseline.
- Keep existing extract paths safe by default (opt-in, fail-closed validation).

**Does not:**

- Implement consumer-side export gates or any reader binding.
- Activate reserved contract slots (for example richer lineage or
  disclosure-control objects) reserved for later contract advances.
- Conform to the sibling `process-run/v0` process contract (tracked separately).
- Force recipe rewrites or change default high-volume extract performance
  characteristics.

---

## Where to go next

| Need | Document |
| --- | --- |
| Extract flags, grains, Parquet, value_profile ops | [Extract Workflow](extract-workflow.md) |
| Validate descriptor / configs | [Validate Command](user-guide/commands/validate.md) |
| Product overview | [Sumpter Overview](sumpter_overview.md) |
| Contract fixtures for offline CI | `tests/fixtures/data-artifact-contract/v0/` |

---

## Confidentiality note

Public docs, examples, and fixtures stay generic. Prefer public open-data
shapes (for example ClinVar, SEC EDGAR XBRL) over client-specific corpora.
Do not put proprietary trade formats or customer identifiers into in-repo
examples.
