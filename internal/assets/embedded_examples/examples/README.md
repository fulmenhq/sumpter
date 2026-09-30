# Sumpter Worked Examples

These examples are self-contained recipes using a fictional WidgetCo / GearCo
parts-and-orders domain. They double as copyable recipe authoring examples and
as smoke tests for extraction semantics.

Run all cases:

```bash
make examples
```

Run directly through Go tests:

```bash
go test ./examples/...
```

Positive cases live in `01`-`89`; negative cases live in `90`-`99`.

Run one case:

```bash
examples/scripts/run-case.sh examples/cases/02-multi-record-line-items
examples/scripts/run-case.sh examples/cases/02-multi-record-line-items --variant json
```

Without `--variant`, a case runs its root `input.xml` (or `input.json` when no
XML input exists) against `recipe/` and `expected/`. A case may also carry
format variants under `variants/<xml|json|ndjson>/`, each with its own
`input.<fmt>`, `recipe/` and `expected/`. An explicit variant that does not
exist fails; there is no fallback.

JSON variants exist for cases 02 through 11, 13 and 91 (case 14 is the JSON
twin of case 01). Each JSON variant reuses its case's golden: the recipe adds
`format_type: json` to the signature and uses `k` in place of `@k`, since JSON
has no attributes. Case 08 uses the ordered wrapper idiom for polymorphic
items. Cases 12 (namespaces) and 90 (malformed XML) are XML only.

`examples/scripts/list-cases.sh` prints every runnable `case[:variant]` entry
and is the single inventory used by `make examples` and `go test`. It fails on
an empty or malformed inventory.

Negative cases run with the manifest enabled and must exit non-zero with the
expected error, publishing no records, manifest or failure files.

| Case                                             | Feature                                           |
| ------------------------------------------------ | ------------------------------------------------- |
| `01-basic-extraction`                            | XPath scalar extraction                           |
| `02-multi-record-line-items`                     | Array `item_mapping`                              |
| `03-summaries-with-remainder`                    | Summary components and remainder                  |
| `04-validation-metadata-clean`                   | Validation metadata accumulations and validations |
| `05-validation-metadata-reconciliation`          | Validation metadata reconciliation                |
| `05b-validation-metadata-grouped-reconciliation` | Declarative reconciliation grouping               |
| `06-derived-field-convenience-sums`              | Expression fields                                 |
| `06b-derived-field-ternary`                      | Conditional expression fields                     |
| `07-declared-parameters-injection`               | Declared parameters                               |
| `08-polymorphic-line-items`                      | Polymorphic array mapping                         |
| `09-predicate-match-selector`                    | Predicate match selectors                         |
| `10-optional-fields`                             | Optional fields and boolean coercion              |
| `14-json-basic-extraction`                       | JSON input (twin of case 01)                      |
| `90-negative-malformed-xml`                      | XML parser failure                                |
| `91-negative-missing-required`                   | Output schema required failure                    |
| `92-negative-validation-fails`                   | Validation failure                                |
| `93-negative-parameter-required-missing`         | Missing required declared parameter               |
| `94-negative-schema-collision`                   | Parameter/field collision                         |
