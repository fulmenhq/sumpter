Schemas

Versioned JSON Schemas for Sumpter configuration, recipes, and outputs. Every
schema carries a `contract://` resource id; see
[Schema identity](../docs/standards/schema-identity.md) for the id rules,
manifests, catalog, and offline resolution.

---

## Layout

Each family keeps one directory per version. Every version directory has a
`contract.json` manifest that owns its schema files.

| Family | Versions | Entry schema | Kind |
| --- | --- | --- | --- |
| `config/` | `v0.1.0` | `sumpter-config.schema.json` (+ logger, PII) | input |
| `dialects/` | `v0.1.0` | `dialect-registry.schema.yaml` | input |
| `envinfo/` | `v0.1.0` | `complete.schema.json` (+ network, paths, system, vars, xml) | output |
| `extract/` | `v0.1.0` | `extract-record-envelope.schema.json` (+ dispositions, failures: output; file signature, record match: input) | mixed |
| `index/` | `v0.1.0`, `v0.1.1`, `v0.1.2` | `record-index.schema.json` | output |
| `inspect/` | `v0.1.0`, `v0.1.1` | `inspect-report.schema.yaml` | output |
| `provenance/` | `v1` (`v1.json`) | `v1.json` | output |
| `recipes/` | `v0.1.0` | `recipe.schema.yaml` (+ applicability) | input |
| `retrieve/` | `v0.1.0` | `retrieve-config.schema.yaml` | input |

`index.json` at this root is the generated catalog of every resource.

---

## Versioning

- Semantic versioning per family; a breaking change bumps the major version.
- Earlier versions stay in the tree so their ids keep resolving.
- New versions are made by copying a version directory; relative `$ref`s move
  with it.

---

## Maintenance

- `make schema-contract-check` checks ids, manifests, references, and that
  `index.json` is current (part of `make check-all`).
- `make schema-catalog` regenerates `index.json` after a schema changes.
- `make embed-assets` refreshes the embedded copy shipped in the binary.
