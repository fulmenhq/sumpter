# Public-Data Examples

Sumpter is a generic streaming-XML extraction engine. It does not bake in
any vertical's record types, schemas, or vendor dialects — those live in
recipes (signature + extract YAML pairs) that are authored separately
from the engine.

This page is the canonical pointer to the **public-data exemplars** that
ship with the repo. Use these when you want to:

- See sumpter end-to-end against real, openly available data
- Understand the recipe pattern (signature → match → field-mapping →
  validation) without needing access to private corpora
- Author a new recipe against your own data, starting from a working
  reference

These examples are deliberately drawn from different verticals and
record shapes so the engine's genericity is demonstrable by example
rather than only described in prose.

## SEC EDGAR XBRL — financial filings

**Vertical**: regulatory finance. **Format**: XBRL (XML-based business
reporting). **Record types**: schema files, linkbases, role definitions.

The recipe pair lives in [`examples/config/extract/`](../../examples/config/extract/):

- [`sec-edgar-xbrl-signature.yaml`](../../examples/config/extract/sec-edgar-xbrl-signature.yaml) —
  pattern-weighted detection for `xs:schema`, `link:linkbase`, and
  filing-type variants (10-K, 10-Q, 8-K)
- [`sec-edgar-role-extract.yaml`](../../examples/config/extract/sec-edgar-role-extract.yaml) —
  extracts `link:roleType` definitions from XBRL schema files, including
  per-link-type usage counts (`count(link:usedOn[...])`)

The `sumpter recipes retrieve finance sec-edgar` flow can download
sample filings directly from EDGAR for local testing — see the
retrieve command help for details.

## ClinVar variant archives — genomics

**Vertical**: biomedical / clinical. **Format**: ClinVar VCV release XML
from NCBI. **Record types**: `VariationArchive` records (~2-50KB each;
the full release is ~50GB uncompressed).

ClinVar is the dataset that drove sumpter's streaming-architecture
decisions (see [ADR-0005 Hybrid Streaming XML Architecture](../architecture/adr/0005-hybrid-streaming-xml-architecture.md))
and the seekable-zstd index work — it's the canonical scale test for the
engine.

End-to-end staging and parallel extraction is documented in the
[ClinVar Parallel Extraction Runbook](../runbooks/clinvar-parallel.md),
covering decompress → hash → record-index → parallel extract.

## What's deliberately not in this repo

Recipes for proprietary or vendor-specific formats (POS journal
dialects, vertical-specific trade-XML, billing-system exports, etc.)
live outside this repo, in private workspaces. Sumpter is
**designed** to be format-agnostic; if you want to use it against a
non-public format, author your own recipe in your own workspace using
these examples as the starting template.

The [recipe schema](../../schemas/recipes/v0.1.0/recipe.schema.yaml)
and the [recipes init template](../../templates/commands/recipe/) are
the supported authoring surfaces.

## See also

- [Extract Workflow](../extract-workflow.md) — running an extract end-to-end
- [Index Workflow](./index-workflow.md) — building a record index for parallel extraction
- [Inspect Workflow](./inspect-workflow.md) — schema-free structural discovery as a recipe-authoring aid
- [ADR-0005 — Hybrid Streaming XML Architecture](../architecture/adr/0005-hybrid-streaming-xml-architecture.md) — why the engine works at ClinVar scale
- [ADR-0006 — Extraction Provenance](../architecture/adr/0006-extraction-provenance.md) — how extracted records carry their source story
