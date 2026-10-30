# 16 Inline XBRL Inventory

Inventory inline-XBRL structures in **strict XML, DOM-only** mode. The fixture
is synthetic; it is not a filing or an XBRL conformance suite. All three recipes
bind namespaces by URI, including the 2008 and 2013 inline-XBRL URIs.

Run the worked fact example:

```bash
make build
examples/scripts/run-case.sh examples/cases/16-inline-xbrl-inventory
```

The companion resource and reference manifests use the same input:

```bash
./dist/sumpter recipes run extract examples/cases/16-inline-xbrl-inventory/recipe \
  --manifest resources.yaml \
  --files examples/cases/16-inline-xbrl-inventory/input.xml \
  --output-path ./outputs --output-pattern resources.jsonl --no-manifest
./dist/sumpter recipes run extract examples/cases/16-inline-xbrl-inventory/recipe \
  --manifest references.yaml \
  --files examples/cases/16-inline-xbrl-inventory/input.xml \
  --output-path ./outputs --output-pattern references.jsonl --no-manifest
```

- **Facts:** `nonFraction`, `nonNumeric` and `fraction`, including hidden and
  nested facts. Values and attributes are strings, not normalized numbers.
  Missing attributes are omitted; present-empty attributes remain `""`.
- **Resources:** contexts, units, continuations and footnotes. Context
  identifier/period/dimension and unit measure arrays are structural projections,
  not validated definitions or lossless XML serialization.
- **References:** inline reference containers, relationships and linkbase
  schema/role/arcrole references. Fact-to-context/unit/continuation/footnote
  references remain on fact rows. Nothing follows or validates these links.

Each recipe uses one explicit `descendant::*[...]` traversal with URI-bound
`self::` tests. The tested recipe avoids XPath union ordering; it is **not a
general union-order fix**. Tests compare complete projected rows by ID with
multiplicity, distinguishing missing from empty IDs, and separately check
source preorder and `_runtime.record_num`. Companion recipes are independently
checked by `TestInlineXBRLInventory` against `encoding/xml`.

The fixture includes duplicate/missing IDs, prefix aliases and rebinding,
same-name foreign elements, nil, positive/negative scale, a number above 2^53,
continuation cycles/missing references and mixed markup. Cycles are only
inventoried. Text includes `ix:exclude` content and does not join continuations.
QName-valued attributes/measures retain their spelling; the recipe does not
resolve their source namespace bindings.

Use uncompressed XML no larger than **104,857,600 bytes (100 MiB)** with these
invocations. Do not select a record index or assume `--allow-large-files` forces
XML DOM. Streaming/index local-name boundaries do not provide this supported-kind fact
profile. See the [application note](../../../docs/appnotes/sourcedata/finance/inline-xbrl-inventory.md)
for support boundaries and the [XML text model](../../../docs/standards/document-node-model.md#xml-text-including-xhtml).
