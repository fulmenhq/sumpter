# ADR-0006: Extraction Provenance — Per-Run Manifest + Inline Run Identity

**Status:** Accepted
**Date:** 2026-05-11
**Deciders:** @3leapsdave, agent-india-devlead (Claude Opus via Claude Code)
**Context:** Alpha phase / v0.1.3 — making every extracted record traceable to its source

## Context

Sumpter's extract command produces JSONL records today with a per-record
`_runtime` envelope:

```json
{
  "_runtime": {
    "generated_at": "2026-05-09T17:13:46Z",
    "record_type": "saleevent_summary",
    "signature_id": "retail-saleevent-summary",
    "signature_name": "Retail SaleEvent or VoidEvent",
    "source_file": "/abs/path/to/input.xml",
    "summaries_included": false,
    "validation_included": false
  },
  "extract": { "data": { /* record fields */ } }
}
```

This is enough to answer "which file did this come from?" and "which signature
matched?" — but not enough to answer the questions an analyst, auditor, or
downstream pipeline actually needs:

- Which XPath in the source produced each field?
- Which version of the recipe ran, exactly — including comments and field order?
- Which sumpter binary produced this output?
- Which run is this record part of (so I can correlate across shards / workers)?
- What was the source byte range for the record (so I can show it in context)?
- Who authored the recipe (when that matters for trust/audit)?
- Can the manifest be verified, or sealed for a specific recipient?

The v0.1.3 release scope and the engagement validation work both require a
trustworthy provenance story before the next round of analytics handoffs and
the chain-month corpus sweep. This ADR records the v0.1.3 increment and
reserves the schema surface for v0.1.4+ attestation work.

### What we deliberately do **not** want

- A 3–10× line-size blowup on every JSONL output for users who only need
  record-level traceability.
- A "single canonical record" that bakes hostnames, PIDs, or local paths
  into shipped artifacts.
- A recipe-versioning scheme that relies solely on author discipline
  (semver alone) or solely on opaque hashes (hash alone).
- A canonicalization scheme that future verifiers in other languages can't
  reproduce off-the-shelf.

## Decision

### Summary

Provenance ships as a **hybrid model**: per-record inline carries the *run-level
identity* a consumer needs to correlate and verify, and a **sidecar manifest
co-located with the extract output** carries the per-field XPath/description
detail, the verbatim recipe content, and the input-file ledger. The manifest
reserves an `attestations[]` field — empty in v0.1.3, populated by v0.1.4+
signing and encryption workflows.

### Seven concrete decisions

| # | Decision | Notes |
|---|----------|-------|
| 1 | **Sidecar manifest is the canonical provenance artifact**, co-located with extract output (same folder or object-store prefix). Per-record `_runtime` carries run-level identity only. An opt-in `--inline-provenance` flag enables per-record field-level annotation for users who need self-contained records. | See "Sidecar layout" below. |
| 2 | **Recipe versioning uses both semver and content-hash**, both emitted. Semver is author-managed in the recipe YAML; content-hash is auto-computed by sumpter from canonical recipe bytes (JCS). The hash is authoritative when they disagree. | See "Recipe versioning" below. |
| 3 | **Field-level byte/line offsets deferred to v0.1.4** (evaluation target, snooze-able). v0.1.3 emits *record-level* `record_byte_range` opportunistically when IndexStore has tracked the source. | Tracked in backlog; not lost. |
| 4 | **Run ID is a UUIDv7**, generated once per `sumpter extract` invocation and shared across parallel workers. No hostname, PID, or argv leakage. Sorts chronologically as lexicographic strings. `SUMPTER_RUN_ID` env var and `--run-id` flag override for deterministic replay. | See "Run identity" below. |
| 5 | **Provenance in Parquet is a projection of the sidecar**, not a divergent source of truth. Parquet column-level KV metadata carries `source_xpath`, `description`, `recipe_field_id`; file-level KV metadata carries `run_id`, `recipe_version`, `recipe_content_hash`, `sumpter_version`. JSONL paths stay sidecar-only. | Stacks on top of the Parquet writer (separate PR slice). |
| 6 | **Recipe author attribution extends the existing `owners` block** on the recipe manifest with an optional `role` field (`name` required, `contact` and `role` optional). Safe default = name only. Workspace maintainers decide what propagates. Future signing/encryption attestation gates on `contact` and `role` being filled in. Surfaces in `manifest.recipe.owners` in the provenance sidecar; never in per-record `_runtime`. | See "Recipe authorship" below. |
| 7 | **Manifest reserves an `attestations[]` field for v0.1.4+ signing and encryption** (Ed25519 via seclusor or SSH, age encryption via seclusor). v0.1.3 outputs omit the field; the canonical-for-attestation byte sequence is locked **now** as JCS (RFC 8785). | See "Forward compatibility for attestation" below. |

## Detailed design

### Per-record `_runtime` envelope (v0.1.3)

```json
{
  "_runtime": {
    "generated_at": "2026-05-11T14:30:00Z",
    "run_id": "0190a3f4-1c2d-7abc-9def-0123456789ab",
    "sumpter_version": "0.1.3",
    "record_type": "saleevent_summary",
    "signature_id": "retail-saleevent-summary",
    "signature_name": "Retail SaleEvent or VoidEvent",
    "recipe_version": "1.0.0",
    "recipe_content_hash": "sha256:7b3f...c2e1",
    "source_file": "input.xml",
    "source_file_sha256": "sha256:9a1d...4f0e",
    "record_byte_range": [102400, 104832],
    "summaries_included": false,
    "validation_included": false
  },
  "extract": { "data": { /* unchanged */ } }
}
```

Field notes:

- `run_id` (UUIDv7) — generated once per CLI invocation. Stable across all
  workers and all output records of that run.
- `sumpter_version` — value of `VERSION` baked at build time.
- `recipe_version` — value of `content_version:` on the recipe manifest
  (`recipe.yaml`). v0.1.3 emits a deprecation warning if missing; v0.1.4
  treats missing as a hard error.
- `recipe_content_hash` — `sha256:` over the JCS canonicalization of the
  loaded signature, extract, and any declared applicability YAML (see
  "Recipe content hash" below).
- `source_file` — relative path under the extract input root, not absolute.
  Reduces accidental hostname/path leakage on shipped output.
- `source_file_sha256` — computed lazily on first record from each file.
- `record_byte_range` — present when the streaming scanner tracked
  per-record offsets (IndexStore-backed path); absent otherwise.

### Sidecar manifest layout

For an extract command writing JSONL to `outputs/<run_id>/records.jsonl`,
the sidecar lives at `outputs/<run_id>/manifest.json` (same prefix, whether
local or object-store). For Parquet, `outputs/<run_id>/<recipe>.parquet`
pairs with `outputs/<run_id>/manifest.json`.

```json
{
  "schema_version": "sumpter.provenance/v1",
  "run_id": "0190a3f4-1c2d-7abc-9def-0123456789ab",
  "sumpter_version": "0.1.3",
  "started_at": "2026-05-11T14:30:00Z",
  "completed_at": "2026-05-11T14:31:17Z",
  "cli": {
    "command": "sumpter extract files",
    "argv_sanitized": ["--recipe", "recipes/retail", "--out", "outputs/<run_id>"]
  },
  "recipe": {
    "id": "retail-saleevent-summary",
    "manifest_schema_version": "recipe/v0.1.0",
    "content_version": "1.0.0",
    "content_hash": "sha256:7b3f...c2e1",
    "owners": [
      { "name": "Fulmen Sumpter contributors" }
    ],
    "manifest_yaml": "...verbatim...",
    "signature_yaml": "...verbatim...",
    "extract_yaml": "...verbatim...",
    "field_provenance": [
      {
        "output_field": "business_date",
        "xpath": "BusinessDate",
        "type": "string",
        "description": "POS-reported business date for the event"
      },
      {
        "output_field": "net_amount",
        "expression": "gross_amount - discount_amount",
        "type": "number",
        "description": "Recipe-derived net amount"
      }
    ]
  },
  "inputs": [
    {
      "path": "input.xml",
      "sha256": "sha256:9a1d...4f0e",
      "size_bytes": 4729184
    }
  ],
  "outputs": [
    {
      "path": "records.jsonl",
      "format": "jsonl",
      "record_count": 1247
    }
  ],
  "counts_by_record_type": {
    "saleevent_summary": 1247
  }
  /* "attestations": [...]  — reserved, see Forward compatibility section */
}
```

Notes:

- `schema_version` is the manifest schema, not the recipe — pins the
  sidecar contract so downstream tooling can parse confidently.
- `argv_sanitized` strips secrets and absolute paths; the sanitization
  rules live next to the env-var redaction helpers.
- `signature_yaml` / `extract_yaml` / optional `applicability_yaml` are the
  *verbatim* loaded recipe bytes (not the canonicalized hash input).
  Audit-friendly.
- Derived fields use `expression` instead of `xpath` in
  `field_provenance` so consumers can distinguish recipe-computed values
  from XML-sourced fields.

The JSON Schema for this manifest ships as `schemas/provenance/v1.json`
alongside the implementation (committed in PR-C).

### Recipe authorship — extending the existing `owners` block

The recipe manifest (`recipe.yaml`) already carries an `owners` array
(see `internal/recipes/manifest.go` and `schemas/recipes/v0.1.0/recipe.schema.yaml`):

```yaml
# Today
owners:
  - name: "Fulmen Sumpter contributors"            # required
    contact: "noreply@fulmenhq.dev"                # optional
```

v0.1.3 extends `Owner` with an optional `role` slug:

```yaml
# v0.1.3
owners:
  - name: "Fulmen Sumpter contributors"            # required
    contact: "noreply@fulmenhq.dev"                # optional
    role: "india-devlead"                          # optional (new)
```

Sumpter copies the block verbatim into `manifest.recipe.owners` in the
provenance sidecar. The **safe default is name only** — `contact` and
`role` only appear when the recipe author explicitly sets them.
Per-workspace maintainers choose what propagates.

**Future attestation gating**: v0.1.4+ signing/encryption workflows require
`contact` (signing identity, expected in email form) and `role`
(chain-of-trust slug) to be present on every owner entry. v0.1.3 does not
enforce this; owners that omit those fields are valid but cannot
participate in attestation until they're added.

**OSS-recipe convention** (any recipes shipped inside this repo): name-only
entries using the project handle (e.g. `"Fulmen Sumpter contributors"`).
No personal contact info in OSS-shipped recipes. A future `limensafe` rule
will enforce this (out of scope here).

**Independence from git commit attribution**: `manifest.recipe.owners`
identifies who maintains the *recipe workspace*. Git commit attribution
(per `docs/standards/agentic-attribution.md`) identifies who authored the
*code change*. These are different surfaces and the same person/agent may
appear in one, both, or neither. Tooling must not conflate them.

**Why `owners` (not a new `authors` block)**: the existing manifest
already models recipe stewardship via `owners`. Introducing a parallel
`authors` block would duplicate the concept and force authors to maintain
two near-identical lists. Extending `owners` with one optional `role`
field reuses the existing surface and keeps the manifest single-sourced.

### Recipe schema additions (v0.1.3)

Recipes are workspace directories with a `recipe.yaml` manifest pointing at
signature/extract/validation assets (see `internal/recipes/manifest.go`).
Provenance changes land on the manifest and on the extract config it
references.

**Recipe manifest** (`recipe.yaml`) gains an optional `content_version` and
an optional `role` field on each `owner`. The existing schema-level
`version: "recipe/v0.1.0"` is **unchanged** in v0.1.3.

```yaml
# Existing (unchanged in v0.1.3)
version: "recipe/v0.1.0"           # manifest schema version
id: "retail-saleevent-summary"
kind: "extract"

# New in v0.1.3 (optional; required in v0.1.4)
content_version: "1.0.0"           # semver, author-managed

# Existing block, now with optional `role`
owners:
  - name: "Fulmen Sumpter contributors"
    contact: "noreply@fulmenhq.dev"   # optional
    role: "india-devlead"             # optional (new)
```

A recipe manifest missing `content_version:` emits a deprecation warning
on v0.1.3 and runs anyway:

```
warning: recipe.yaml is missing `content_version` (semver). Provenance
         will record `recipe_version` as "unversioned". v0.1.4 will treat
         this as a hard error. Run `sumpter recipes migrate` to stamp a
         starter version, or edit recipe.yaml manually.
```

In v0.1.4 the same condition becomes a hard error and the manifest schema
bumps to `recipe/v0.2.0` to mark `content_version` as required.

**Extract config** (the YAML pointed to by `assets.extract` — an
`ExtractRecordMatch` in Go) gains an optional `description` per
`field_mappings` entry:

```yaml
field_mappings:
  - output_field: "business_date"
    xpath: "BusinessDate"
    type: "string"
    description: "POS-reported business date for the event"   # new (optional)
```

**Signature config** is unchanged in v0.1.3. The existing per-dialect
`version` field stays free-form (semver tightening deferred to v0.1.4
alongside the schema-version bump).

**Manifest schema file** (`schemas/recipes/v0.1.0/recipe.schema.yaml`)
gains the new optional fields under `additionalProperties: false`. No
version bump in v0.1.3; v0.1.4 will introduce `v0.2.0`.

**Applicability config** (the YAML pointed to by
`assets.applicability`) is a standalone behavior-bearing asset with a
top-level `applicability:` wrapper. Predicate fields are nested under that
wrapper; top-level `type:` or `expression:` fields are invalid.

```yaml
applicability:
  type: xpath
  expression: "boolean(/*[local-name()='Document'])"
  description: "Only run this recipe for document-style XML inputs"
```

### Recipe content hash — narrow scope

Goal: hash is stable across whitespace, comment, and key-order churn, but
changes when any **extraction-defining** content changes.

**Scope**: the hash covers `signature.yaml`, `extract.yaml`, and any declared
`applicability.yaml` asset (the files pointed to by `manifest.assets.signature`,
`manifest.assets.extract`, and optional `manifest.assets.applicability`). The
recipe manifest itself — display name, description, owners, documentation,
defaults — is **not** part of the hash. Rationale: manifest fields describe
the recipe workspace; they don't change the extracted output bytes. Conflating
them would churn the hash on cosmetic edits (updating an owner's contact,
fixing a typo in the description) without any change to extraction behavior.

Algorithm:

1. Load signature YAML, extract YAML, and any declared applicability YAML (the
   behavior-bearing asset files).
2. Parse each to an in-memory Go object (comments and whitespace dropped).
3. Serialize each to JSON using **JCS (RFC 8785)** — JSON Canonicalization
   Scheme: sorted keys, no insignificant whitespace, UTF-8, RFC 8785 number
   normalization.
4. Concatenate declared assets in order with `"\n---\n"` separators:
   `signature_jcs`, `extract_jcs`, then `applicability_jcs` when present.
5. SHA-256 the result. Emit as `sha256:<lowercase-hex>`.

Using JCS (not a homegrown canonicalizer) means future verifiers in any
language with a JCS library can recompute the hash without bespoke Go
compatibility work.

The manifest itself is still recorded verbatim in the sidecar
(`manifest.recipe.manifest_yaml`) for audit; it is just outside the
content-hash window.

### Run identity (UUIDv7)

Generated once at `extract` command startup, before any worker spawns:

```go
runID := uuid.Must(uuid.NewV7())
```

(Using `github.com/google/uuid` v1.6+ which has `NewV7`.)

Stored on the command context; threaded through worker goroutines via the
existing context-with-values plumbing.

**UUIDv7 chronological sort**: the leading 48 bits encode Unix
milliseconds big-endian, so `outputs/<run_id>/` listings sort by creation
time under any standard lexicographic sort (`ls`, `aws s3 ls`, `gsutil ls`,
`find … | sort`). No prefix or date-segment needed for chronological
browsing.

**Overrides for deterministic replay**:

- Env var: `SUMPTER_RUN_ID=0190a3f4-...` (also useful in CI)
- Flag: `sumpter extract files --run-id 0190a3f4-...`

Both are documented as testing/replay escape hatches, not production
features. The flag takes precedence over the env var.

### Output paths and co-location

`sumpter extract files --out PATH` interprets PATH as a run-scoped
directory or prefix:

- **Local FS**: `--out outputs/item-master/` writes
  `outputs/item-master/<run_id>/records.jsonl` and
  `outputs/item-master/<run_id>/manifest.json`. The `<run_id>` segment is
  added automatically; tooling can list runs by directory.
- **Object store** (future): same shape with the bucket prefix
  (`s3://bucket/item-master/<run_id>/...`).
- **Single-file mode** (current behavior, kept for backwards compat):
  `--out output.jsonl` writes the records and emits `output.manifest.json`
  next to it.

A `--no-manifest` flag exists for users who explicitly opt out (e.g.,
piping records through another tool that re-wraps them). Default: manifest
on.

### Forward compatibility for attestation (v0.1.4+)

v0.1.3 reserves an `attestations[]` field at the top level of the manifest
to accommodate future verification (signing) and confidentiality
(encryption) features. The field is **omitted** in v0.1.3 outputs; it
appears as soon as a v0.1.4+ workflow attaches an attestation.

Shape:

```json
"attestations": [
  {
    "algorithm": "seclusor-ed25519",
    "purpose": "sign",
    "public_key_id": "RWQ...",
    "signature": "...",
    "covers_hash": "sha256:<JCS hash of manifest with attestations excluded>",
    "signed_at": "2026-05-11T14:31:18Z",
    "signed_by": "agent-india-devlead"
  },
  {
    "algorithm": "seclusor-age",
    "purpose": "encrypt-recipient",
    "recipient": "age1...",
    "encrypted_payload_ref": "manifest.json.age"
  }
]
```

Algorithms recognized at design time:

- `seclusor-ed25519` — Ed25519 signing via 3leaps/seclusor (planned)
- `seclusor-age` — age encryption via 3leaps/seclusor (manifest sealed for
  a recipient identity)
- `ssh` — `ssh-keygen -Y sign` Ed25519 signing (alternative; integrates
  with hardware keys and `allowed_signers` model)
- `minisign` — minisign Ed25519 signing (alternative when seclusor is
  unavailable)

The `algorithm` field is a discriminator, not a closed enum — additional
algorithms (cosign bundles, in-toto attestations, etc.) land via additive
schema updates without bumping `schema_version`.

**Canonical-for-attestation serialization** is locked in v0.1.3 even
though no v0.1.3 manifest carries an attestation. Algorithm:

1. Take the manifest as an in-memory object.
2. Remove the `attestations` field if present.
3. Serialize using **JCS (RFC 8785)**.
4. The resulting bytes are what gets hashed (`covers_hash`) and signed.

Locking JCS now means v0.1.3 manifests stay verifiable when v0.1.4+
attaches signatures post-hoc — no canonicalization drift.

**Schema-version additive-only policy**: `sumpter.provenance/v1` accepts
new optional fields without a version bump. `attestations[]` is the
canonical example. A version bump (`v2`) is reserved for shape-breaking
changes only.

## Consequences

### Positive

- Every extracted record can be traced to a verified recipe version and a
  specific run, without bloating per-record bytes.
- Sidecar is self-contained — a downstream pipeline can rebuild the
  extraction context (recipe content, input ledger, run metadata) from
  manifest alone.
- Parquet column metadata makes analytics-engine handoff (DuckDB / Arrow /
  Spark) "free" when sumpter writes Parquet directly.
- Content-hash catches the "I forgot to bump the version" mistake without
  forcing semver discipline through review alone.
- UUIDv7 sorts by time, correlates across workers, and reveals nothing
  about the dev environment.
- JCS + reserved `attestations[]` field means v0.1.4+ signing and
  encryption can attach to v0.1.3-produced manifests without breaking
  verifiability.

### Negative / costs

- Every extract run now writes two artifacts. Single-file pipelines need
  to know about the sidecar (or opt out with `--no-manifest`).
- Recipe authors are nudged to add `content_version:` to their manifests
  (warning in v0.1.3, hard error in v0.1.4). The one-time `sumpter recipes
  migrate` helper handles bulk stamping and the v0.1.4 schema-version bump.
- JCS canonicalization adds a Go dependency (a JCS library — small).
- Canonical-hash computation adds O(recipe-size) work on extract startup —
  negligible (recipes are <100KB) but a measurable line item.

### Migration

- v0.1.3: recipes without `content_version:` on the manifest emit a
  **deprecation warning** and run anyway. The provenance `recipe_version`
  is recorded as `"unversioned"` for these records. Manifest schema stays
  at `recipe/v0.1.0`.
- v0.1.4: same condition becomes a **hard error**. Manifest schema bumps
  to `recipe/v0.2.0` with `content_version` required. Sumpter detects
  `recipe/v0.1.0` manifests and instructs the user to migrate.
- Workspace-local recipes (the ones living in private engagement
  workspaces outside this repo) need `content_version` added before the
  v0.1.4 cutover. A `sumpter recipes migrate` helper lands alongside the
  v0.1.3 implementation to bulk-stamp `content_version: "0.0.1"` on
  opt-in, and the same helper handles the v0.1.4 schema-version bump.

## Out of scope (tracked for later)

- **Field-level byte/line offsets** — defer to v0.1.4 evaluation slot.
  Requires streaming-scanner state threading; coordinates with the
  operational layer / Hatchet integration. Snooze-able if v0.1.4 fills
  with higher-priority items.
- **Inline per-record field provenance** — `--inline-provenance` flag is
  reserved but unimplemented in v0.1.3. Will land when a concrete consumer
  needs it.
- **Attestation UX (`sumpter session bless`, key management, seclusor
  integration)** — v0.1.4+. v0.1.3 only reserves the schema + locks
  canonicalization.
- **Recipe registry & composition** — separate ADR. Provenance assumes
  recipes are versioned, which the registry will formalize.
- **Datalake-format provenance** (Iceberg snapshot metadata, Delta commit
  info) — separate ADR when those sinks land.
- **OSS-recipe author-block enforcement** — future `limensafe` rule that
  flags emails/roles in `recipes/**` under this repo. Out of scope here.

## Implementation sequencing

Tracked in the release coordination channel for the v0.1.3 rollout. That
channel carried the PR sequencing, scope splits, and coordination history.
This ADR records the durable decisions; the release channel recorded how
they landed.

Standing constraints regardless of sequencing:

- Each PR gated by `make pre-commit` (alpha 50% coverage threshold).
- Each PR requires explicit @3leapsdave approval per the repo safety
  protocol; no commits or pushes without per-occurrence authorization.
- PRs scoped to <500 lines of diff where the work allows.

## References

- v0.1.3 strategic themes: `.plans/active/v0.1.3/00-strategic-themes.md`
- Provenance design starter (this ADR's source): `.plans/active/v0.1.3/01-provenance-design-starter.md`
- Backlog: `.plans/active/v0.1.3/02-backlog-prioritization.md`
- Agentic attribution standard: `docs/standards/agentic-attribution.md`
- ADR-0001: Schema-first outputs (sets the precedent for JSON Schema as
  the recipe-output contract that provenance projects into Parquet).
- ADR-0005: Hybrid streaming XML architecture (provides IndexStore offsets
  for `record_byte_range`).
- RFC 8785: JSON Canonicalization Scheme (JCS).
- RFC 9562 §5.7: UUIDv7 specification.
- 3leaps/seclusor: planned signing (Ed25519) + encryption (age) tool;
  v0.1.4+ attestation integration target.

## Decision log

- **2026-05-10** — Strategic themes seeded after engagement-scale validation
  run (`00-strategic-themes.md`).
- **2026-05-11 AM** — Provenance design starter authored with six open
  questions (`01-provenance-design-starter.md`).
- **2026-05-11 PM** — @3leapsdave confirmed all six original recommendations
  with refinements on Q1 (sidecar co-location) and Q3 (defer-but-track).
- **2026-05-11 PM (continued)** — Refinements landed: `authors` field
  becomes structured object with name-only safe default (Option C);
  reserved field renamed `signatures[]` → `attestations[]` to cover both
  signing and encryption tracks; canonical-for-attestation locked as JCS
  (RFC 8785); `schemas/provenance/v1.json` explicitly committed in PR-C;
  `--run-id` flag added; recipe-author vs git-commit-author independence
  documented.
- **2026-05-11** — ADR-0006 v2 drafted with first-pass design.
- **2026-05-11 PM (continued)** — Code-surface reconciliation: existing
  `internal/recipes/manifest.go` already models a recipe workspace with a
  manifest carrying `version` (schema-level, locked to `recipe/v0.1.0`)
  and `owners` (`name` + `contact`). ADR-0006 v3 amends to: (a) content
  version becomes `content_version` on the manifest (not a new field on
  extract.yaml); (b) recipe authorship extends the existing `owners`
  block with optional `role` (no new `authors` block); (c)
  `recipe_content_hash` scope narrows to behavior-bearing recipe assets
  (manifest changes don't churn the hash); (d) manifest schema stays
  at `recipe/v0.1.0` in v0.1.3 with `content_version` as
  optional-with-warning; v0.1.4 bumps to `recipe/v0.2.0` with it required.
- **2026-05-11** — ADR-0006 v3 accepted (this document). Branch
  `feat/provenance-recipe-schema` opened; PR-A work begins.
