# Public-Data Examples

Sumpter is a generic streaming-XML extraction engine. It does not bake in
any vertical's record types, schemas, or vendor dialects — those live in
recipes (signature + extract YAML pairs) that are authored separately
from the engine.

This page is the canonical pointer to the **public-data exemplars** that
ship with the repo. Use these when you want to:

- See sumpter end-to-end against real, openly available data
- Understand the recipe pattern (signature → match → field-mapping →
  validation) on data you can download and run yourself
- Author a new recipe against your own data, starting from a working
  reference

These examples are deliberately drawn from different verticals and
record shapes so the engine's genericity is demonstrable by example
rather than only described in prose. They use public-domain sources so
every example here is runnable by anyone — the same engine is equally at
home on private or proprietary formats.

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

## USGS QuakeML seismic event catalogs — scientific / research

**Vertical**: scientific / geophysics. **Format**: QuakeML 1.2 (an XML
representation of seismological data) with the ANSS catalog extension
namespace. **Record types**: `event` records, each nesting a preferred
`origin` (location, time, depth), a preferred `magnitude`, and a solution
`quality` block.

QuakeML is a useful genericity test because the record is a _scientific
measurement_: leaf values arrive as value/uncertainty pairs and the document
carries an extension namespace on its attributes. The recipe pair lives in
[`examples/config/extract/`](../../examples/config/extract/):

- [`usgs-quakeml-signature.yaml`](../../examples/config/extract/usgs-quakeml-signature.yaml) —
  matches the `q:quakeml` document root and the `eventParameters` wrapper
- [`usgs-quakeml-event-extract.yaml`](../../examples/config/extract/usgs-quakeml-event-extract.yaml) —
  extracts one record per `event`, flattening the preferred origin's
  latitude/longitude/depth and time, the preferred magnitude, and origin
  quality metrics (azimuthal gap, used phase/station counts)

A sliced public-domain sample ships at
[`examples/data/public-data/usgs-quakeml-sample.xml`](../../examples/data/public-data/usgs-quakeml-sample.xml)
(see [PROVENANCE.md](../../examples/data/public-data/PROVENANCE.md)). Run it
end-to-end:

```bash
sumpter extract files \
  --files examples/data/public-data/usgs-quakeml-sample.xml \
  --signature-config-path examples/config/extract/usgs-quakeml-signature.yaml \
  --extract-config-path examples/config/extract/usgs-quakeml-event-extract.yaml \
  --output-path out/
```

USGS earthquake data are served by the FDSN `event` web service; QuakeML is
the default response format. USGS information products are in the U.S. public
domain, so a small sample ships with the repo for local testing.

## NWS CAP public alerts — geospatial

**Vertical**: geospatial / public-safety. **Format**: Atom 1.0 feed wrapping
OASIS Common Alerting Protocol (CAP) v1.2 payloads. **Record types**: Atom
`entry` records, each carrying a CAP alert with a geospatial `polygon`, an
affected-area description, and the severity/urgency/certainty triad.

This exemplar exercises **cross-namespace field mapping**: the record boundary
is an Atom element while the alert payload lives under the `cap:` namespace in
the same record. It also treats a packed geospatial coordinate string as a
first-class extracted field. The recipe pair lives in
[`examples/config/extract/`](../../examples/config/extract/):

- [`nws-cap-alerts-signature.yaml`](../../examples/config/extract/nws-cap-alerts-signature.yaml) —
  matches the Atom `feed` root and the CAP alert payload
- [`nws-cap-alert-extract.yaml`](../../examples/config/extract/nws-cap-alert-extract.yaml) —
  extracts one record per `entry`, mapping Atom identifiers and titles
  alongside `cap:event`, `cap:severity`, `cap:areaDesc`, and the geospatial
  `cap:polygon` geometry

A sliced public-domain sample ships at
[`examples/data/public-data/nws-cap-alerts-sample.xml`](../../examples/data/public-data/nws-cap-alerts-sample.xml)
(see [PROVENANCE.md](../../examples/data/public-data/PROVENANCE.md)). Run it
end-to-end:

```bash
sumpter extract files \
  --files examples/data/public-data/nws-cap-alerts-sample.xml \
  --signature-config-path examples/config/extract/nws-cap-alerts-signature.yaml \
  --extract-config-path examples/config/extract/nws-cap-alert-extract.yaml \
  --output-path out/
```

National Weather Service web content is in the public domain, so a small
point-in-time capture of active alerts ships with the repo. Alert content is
time-sensitive; the sample reflects alerts active at capture time, while the
record structure is stable.

## GovInfo USLM legislative bills — government / regulatory

**Vertical**: government / regulatory open data. **Format**: United States
Legislative Markup (USLM), an XML schema derived from the Akoma Ntoso /
LegalDocML standard, with Dublin Core metadata. **Record types**: `section`
divisions within a bill, above further `subsection` / `paragraph` /
`subparagraph` / `clause` nesting.

USLM is a recursive legal-document grammar: the natural record is a structural
division that carries both attributes (a stable `identifier` path) and
mixed-content prose. Handling it with the same signature → match →
field-mapping → validation pattern as a flat measurement record is the point.
The recipe pair lives in
[`examples/config/extract/`](../../examples/config/extract/):

- [`govinfo-uslm-bill-signature.yaml`](../../examples/config/extract/govinfo-uslm-bill-signature.yaml) —
  matches the USLM `bill` root and its `main` body
- [`govinfo-uslm-section-extract.yaml`](../../examples/config/extract/govinfo-uslm-section-extract.yaml) —
  extracts one record per top-level `section`, capturing its number, heading,
  stable USLM `identifier` path, and immediate content

A public-domain sample ships at
[`examples/data/public-data/govinfo-uslm-bill-sample.xml`](../../examples/data/public-data/govinfo-uslm-bill-sample.xml)
(see [PROVENANCE.md](../../examples/data/public-data/PROVENANCE.md)). Run it
end-to-end:

```bash
sumpter extract files \
  --files examples/data/public-data/govinfo-uslm-bill-sample.xml \
  --signature-config-path examples/config/extract/govinfo-uslm-bill-signature.yaml \
  --extract-config-path examples/config/extract/govinfo-uslm-section-extract.yaml \
  --output-path out/
```

US federal bills and laws are not subject to copyright (17 U.S.C. 105); the
GovInfo USLM files state this in-band in their Dublin Core `rights` metadata.
A small enrolled-bill sample ships with the repo.

## Namespace-portable recipes

Several formats above are multi-namespace: XBRL mixes the `xs:` and `link:`
vocabularies, QuakeML carries the ANSS catalog extension namespace, and CAP
alerts nest under an Atom feed. XPath selectors that lean on a document's
*literal* prefixes are tied to one serialization — a document that uses
different prefixes, or a default namespace, can silently under-match.

Sumpter's opt-in `namespaces:` map binds XPath prefixes to namespace **URIs**
instead, so a recipe selects the same fields regardless of the document's
literal prefixes, and bound field selection resolves consistently across
whole-document, streaming, and indexed execution. Recipes without a map are
unchanged (byte-compatible default), and an undeclared prefix fails closed at
config load rather than matching zero records silently. Record boundaries in
streaming and indexed modes remain local-name-only in this release.

See [XML Namespace Binding](../extract-workflow.md#xml-namespace-binding) for
the full semantics and the synthetic worked example at
[`examples/cases/12-namespace-binding`](../../examples/cases/12-namespace-binding).

## Using sumpter on your own formats

Sumpter is format-agnostic by design: it bakes in no vertical's schema,
so the same signature → match → field-mapping → validation pattern shown
above works on any XML — public or proprietary. Recipes for formats that
aren't openly redistributable (vendor dialects, internal exports, and the
like) naturally live wherever that data lives, rather than in this repo;
author your own using these exemplars as a working starting point.

The [recipe schema](../../schemas/recipes/v0.1.0/recipe.schema.yaml)
and the [recipes init template](../../templates/commands/recipe/) are
the supported authoring surfaces.

## See also

- [Extract Workflow](../extract-workflow.md) — running an extract end-to-end
- [Index Workflow](./index-workflow.md) — building a record index for parallel extraction
- [Inspect Workflow](./inspect-workflow.md) — schema-free structural discovery as a recipe-authoring aid
- [ADR-0005 — Hybrid Streaming XML Architecture](../architecture/adr/0005-hybrid-streaming-xml-architecture.md) — why the engine works at ClinVar scale
- [ADR-0006 — Extraction Provenance](../architecture/adr/0006-extraction-provenance.md) — how extracted records carry their source story
