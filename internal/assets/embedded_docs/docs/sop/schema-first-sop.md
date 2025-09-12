# SOP: Schema-First Programmatic Outputs

Status: Active

Purpose: Establish a single, unambiguous principle for all programmatic inputs and outputs across Sumpter.

---

## Principle

- All programmatic outputs MUST be JSON and MUST conform to a versioned JSON Schema.
- All programmatic inputs (configs, profiles, registries, etc.) MUST be validated against versioned JSON Schemas before use (fail fast).
- Human-readable renderings (Markdown, console/pretty, CSV, HTML, PDF) are derived views of the JSON SSOT. They must never drift from schema-backed JSON.

## Scope

- Outputs: reports, result sets, diagnostics, metrics snapshots, environment info, registry data.
- Inputs: tool configuration (main/logger/pii), profiles, registry manifests, embed manifests.

## Versioning and Layout

- Schemas live under `schemas/` with product- and version-specific folders. See `schemas/README.md`.
- Each schema has a semantic identifier (example: `inspect-report/v0.1.0`).
- JSON outputs include a `version` field equal to their schema identifier for disambiguation and forward/backward mapping.

## Enforcement

- Build/CI: `make schema-validate` runs schema validation via `goneat validate` for repository schemas.
- Runtime (inputs): Configs validated with the `goneat` library (`pkg/schema`) before unmarshaling.
- Runtime (outputs): Where applicable, add `--validate-output` to verify JSON outputs against their schema (optional in v0.1.0; required in later phases).

## Rendering Policy

- Rendering to Markdown/HTML/CSV/pretty console is a pure transformation step from the JSON SSOT and must not introduce new semantics.
- Renderers must tolerate missing optional fields per the schema and must not depend on undocumented structure.

## Security & PII

- Schema and renderers must avoid exposing sensitive environment variables or secrets. Where PII is possible, redaction policies apply prior to output.

## Tooling

- Validation: `github.com/fulmenhq/goneat/pkg/schema` (Draft-07 and 2020-12; JSON and YAML).
- Embedding: curated schemas/docs/examples embedded into the binary; see `docs/sop/embedding-assets-sop.md` (referenced from goneat).

## Responsibilities

- Architecture maintains schema contracts and evolution policy (ADRs).
- Feature owners MUST update or add schemas when introducing new programmatic IO.

---

## References

- ADR: `docs/architecture/adr/0001-schema-first-outputs.md`
- Schemas overview: `schemas/README.md`
- Output rendering guidance: `docs/output_rendering.md`
- Validation library appnote (goneat): `../goneat/docs/appnotes/library-schema-validation.md` (external repo)


