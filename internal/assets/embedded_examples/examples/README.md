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

| Case | Feature |
| ---- | ------- |
| `01-basic-extraction` | XPath scalar extraction |
| `02-multi-record-line-items` | Array `item_mapping` |
| `03-summaries-with-remainder` | Summary components and remainder |
| `04-validation-metadata-clean` | Validation metadata accumulations and validations |
| `05-validation-metadata-reconciliation` | Validation metadata reconciliation |
| `06-derived-field-convenience-sums` | SUM-002 expression fields |
| `07-declared-parameters-injection` | SUM-003 declared parameters |
| `08-polymorphic-line-items` | Polymorphic array mapping |
| `09-predicate-match-selector` | Predicate match selectors |
| `10-optional-fields` | Optional fields and boolean coercion |
| `90-negative-malformed-xml` | XML parser failure |
| `91-negative-missing-required` | Output schema required failure |
| `92-negative-validation-fails` | Validation failure |
| `93-negative-parameter-required-missing` | Missing required declared parameter |
| `94-negative-schema-collision` | Parameter/field collision |
