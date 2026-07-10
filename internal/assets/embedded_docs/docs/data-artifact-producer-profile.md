# Data-Artifact Producer Profile (Sumpter)

**How Sumpter adopts the portable `data-artifact/v0` contract.**

This document is the in-repo **reference producer-adoption** guide for Sumpter.
It is additive documentation only: it does not change runtime behavior.

> **Headline for existing users.** Almost nothing changes if you adopt nothing.
> The portable surfaces are **opt-in** and **byte-compatible** on the default
> path. What changes is that opted-in output becomes legible to consumers that
> understand `contract: data-artifact/v0` without Sumpter-specific knowledge.

Operational command detail (flags, recipes, cloud I/O) lives in
[Extract Workflow](extract-workflow.md). Descriptor validation lives in
[Validate Command](user-guide/commands/validate.md).

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

Sumpter declares conformance with the host-less capability string:

```text
contract: data-artifact/v0
```

Resolution uses an explicit local **`--contract-base`**: a directory containing
`contract.json` and the relative entry schema it names. Sumpter does **not**
vendor the live contract as its identity; a pinned fixture under
`tests/fixtures/data-artifact-contract/v0` supports CI and offline validation.

Before publishing an artifact descriptor, extract validates the generated JSON
against that resolved baseline (fail-closed).

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

**Grains Sumpter emits:**

| Condition | Grain `kind` |
| --- | --- |
| Default / per-input extract | `record_stream` |
| `--output-mode aggregate` | `aggregation` (same protection floors as the record stream — no lineage laundering) |
| `--record-index` | additional `object_index` grain; URI path-sanitized (no host-local absolute paths) |

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

### 4. Protection declarations (declare, do not enforce)

Sumpter **declares** protection metadata; consumers / data planes **enforce**.

| Surface | Default posture |
| --- | --- |
| Top-level `protection` | `default_action: block_export`, `default_export_class: internal`, opaque `profile_ref` |
| NDJSON / aggregate NDJSON | `protection_enforceable_granularity: row` |
| Parquet **with** `--artifact-descriptor` | `column` floor; page bounds + page statistics suppressed on every leaf; Bloom filters never wired |
| Parquet **without** descriptor | Pre-profile writer configuration (page stats retained); no portable column claim |
| Scan claims | `columnar_scan` only — no `predicate_pushdown` without a matching `pushdown_withheld` set |

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

- **Tier A (concrete values)** only when `safe_to_profile` **and** sensitivity
  is `public` or `internal` **and** distinct count is under `max_distinct`.
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

Opt-in extract validation (`off` by default):

| Mode | Checks |
| --- | --- |
| `off` | No extra ladder (default) |
| `sidecars` | Provenance / failure / disposition sidecars |
| `artifact` | Sidecars + descriptor + field catalog (requires descriptor flags) |
| `envelope-sample` | Sample NDJSON envelopes |
| `strict` | Full applicable ladder |

Validation runs on the complete local (or cloud staging) file **before**
Publish so cloud destinations cannot drop staging before the check.

Standalone descriptor check:

```bash
sumpter validate artifact-descriptor ./out/artifact-descriptor.json \
  --contract-base ./contracts/data-artifact/v0
```

---

## Scope guardrails (this profile)

**Does:**

- Make extract bundles discoverable and protection-legible under
  `data-artifact/v0`.
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
