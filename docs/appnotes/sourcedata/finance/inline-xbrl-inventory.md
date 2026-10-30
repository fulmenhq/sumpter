# Inline XBRL Structure Inventory via XML

Sumpter can extract inline-XBRL **structures** from well-formed XHTML using the
existing `xml` input format. The reference recipes are an inventory, not an
inline-XBRL processor: they do not produce normalized facts, validate a filing,
or reconstruct rendered content.

## Supported profile

The [synthetic worked example](../../../../examples/cases/16-inline-xbrl-inventory/README.md)
contains three copyable recipe manifests:

| Manifest in `examples/cases/16-inline-xbrl-inventory/recipe/` | Inventory |
| --- | --- |
| `recipe.yaml` | Every URI-matched `nonFraction`, `nonNumeric` and `fraction`, including hidden/nested facts; identity, lexical attributes, text and fraction component text |
| `resources.yaml` | Contexts and units with identifier/period/dimension/measure projections; continuation and footnote resources |
| `references.yaml` | Inline reference containers and relationships, plus schema/role/arcrole references |

The recipes bind the 2008/2013 inline-XBRL, XBRL instance, dimensions and
linkbase namespaces by their exact URIs. Source prefixes may differ or be
rebound. Same-local-name elements in foreign namespaces are not selected.
This selection covers only `nonFraction`, `nonNumeric` and `fraction`, with
the mapped lexical attributes. It excludes `ix:tuple`, `tupleRef` and
fact-level `order`; relationship `order` remains part of the reference
inventory. This is not a claim of complete conformance to either inline-XBRL
specification or a lossless resource serialization.

**Inventory of the supported fact kinds is DOM-only.** Use these invocations with uncompressed
XML at or below **100 MiB (104,857,600 bytes)**, without `--record-index`:

```bash
make build
./dist/sumpter recipes run extract examples/cases/16-inline-xbrl-inventory/recipe \
  --files examples/cases/16-inline-xbrl-inventory/input.xml \
  --output-path ./outputs --output-pattern facts.jsonl --no-manifest
```

Repeat with `--manifest resources.yaml`/`--manifest references.yaml` and distinct output patterns for
the companion inventories. The examples/tests use synthetic inputs and a fresh
binary. Their successful small DOM runs establish neither a retained-filing
streaming/index result nor an RSS bound.

Above 100 MiB, XML dispatch can choose streaming depending on flags and output
eligibility. **`--allow-large-files` is not an XML force-DOM switch.** Do not
reuse these DOM selectors as local-name scanner boundaries. Streaming and
indexed routes accept `Name`/`//Name` boundaries; those do not identify a
namespace URI and can omit nested same-name facts. Supported-kind namespace/nesting
parity is not advertised for these routes. See
[route dispatch](../../../extract-workflow.md#xml-route-dispatch).

Each inventory uses one `descendant::*[...]` predicate with URI-bound `self::`
tests. It preserves preorder in the tested profile without an XPath union.
**Union selectors are excluded:** their ordering divergence remains a separate
contract issue, not resolved by this recipe. Plain local-name boundaries are
also excluded from the supported-kind fact profile.

## What the fields mean

- Fact `text`, `numerator_text`, `denominator_text` and numeric attributes are
  strings. `9007199254740993`, `123.4500`, `INF`, sign and scale remain lexical
  data; no XPath numeric conversion or decimal transform is performed.
- Attribute mappings retain decoded values with Sumpter's current outer
  whitespace trimming. A missing top-level attribute is omitted; a
  present-empty attribute is `""`. Optional nested item fields may be `null`.
  This is not source-byte/markup preservation.
- `name`, `format`, dimension names/member values and measure strings are
  lexical QName spellings. A recipe namespace map resolves XPath tests, **not
  QName-valued source strings**. Identical spellings under different source
  bindings need not mean the same concept.
- `context_ref`, `unit_ref`, `continued_at`, `footnote_refs`, relationship
  endpoints and `href` are references only. Duplicate IDs, missing targets and
  cycles remain rows/data, not a validated or joined graph. No reference is
  fetched.
- `nil` is the lexical `xsi:nil` attribute, not computed nil semantics.
  `fraction` rows include numerator/denominator text, not an evaluated ratio.
- Text uses the [XML node model](../../../standards/document-node-model.md#xml-text-including-xhtml).
  It is neither serialized markup nor exclusion-aware rendered text. `escape`
  and `format` are attributes, not instructions executed by the inventory.

There is no continuation joining, `ix:exclude` removal, escaped-markup
reconstruction, transformation-registry execution, context/unit resolution or
semantic value normalization. Consumers needing these operations must use a
separately defined processor; do not treat inventory text as normalized values.

## Fidelity and failure boundaries

The acceptance test uses an independent `encoding/xml` token parser to compare
IDs **with multiplicity**, fact kinds, lexical attributes and the documented
text/resource projection. Missing and present-empty IDs are separate buckets;
duplicate IDs cannot overwrite a row. Source preorder and 1-based
`_runtime.record_num` are asserted separately. Count equality alone is not the
fidelity test.

Input must be well-formed XML. Predefined XML entities and numeric references
are decoded; unsupported HTML entities such as `&nbsp;` fail with the entity
name. There is no HTML repair, `xhtml` format token, external DTD/entity fetch or
new tokenizer. Inventory references do not authorize fetching external data.

Pre-commit input/extraction failures with file-backed record sinks withhold
staged rows. Stdout/library records are provisional until successful completion
and cannot be recalled; later publish errors and prior completed inputs have
different boundaries. See the [publication contract](../../../extract-workflow.md#recordsink-streaming-contract).
