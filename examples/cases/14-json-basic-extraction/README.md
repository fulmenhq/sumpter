# 14 JSON Basic Extraction

The JSON twin of `01-basic-extraction`: the same WidgetCo order as JSON input,
extracted to the same record.

The only recipe differences from case 01 are `defaults.input.format: json` in
`recipe.yaml`, `format_type: json` in the signature, and `@k` → `k` in XPaths
(JSON has no attributes, so XML attributes become ordinary members).

Run:

```bash
examples/scripts/run-case.sh examples/cases/14-json-basic-extraction
```
