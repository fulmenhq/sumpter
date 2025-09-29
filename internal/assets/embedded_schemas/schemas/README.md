Schemas

Versioned JSON Schemas for Sumpter configurations and outputs.

---

## Layout

- `inspect-report/`
  - `v0.1.0/inspect-report.schema.yaml` — SSOT for inspect JSON output
- `extract-config/` (future)
  - `v1.0.0/extract-config.schema.json`
- `env/` (future)
  - `v0.1.0/sumpter-home.schema.json`

---

## Versioning

- Semantic versioning per family (`inspect-report/v0.1.0`, `extract-config/v1.0.0`, etc.).
- Breaking changes bump the major version in the directory.
- `$id` uses canonical public URLs under `https://sumpterhq.github.io/schemas/...`.

---

## Validation

- Outputs: validate inspect JSON in CI; optional runtime `--validate-output`.
- Inputs (future): validate configs/env on load with helpful error messages.

---

## Publishing

- Schemas are kept in-repo under `schemas/` and can be published to GitHub Pages for resolvable `$id` URLs.
