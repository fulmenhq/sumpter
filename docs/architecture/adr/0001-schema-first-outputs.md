# ADR 0001: Schema-First Programmatic Outputs

Status: Accepted
Date: 2025-09-12

## Context

Sumpter must serve both machine and human consumers at scale. Consistency and safety
require a single source of truth for programmatic IO.

## Decision

- All programmatic outputs are JSON and validated against versioned JSON Schemas.
- All programmatic inputs are validated against versioned JSON Schemas prior to use.
- Human-readable formats (Markdown, HTML, CSV, pretty console) are renderings of the JSON SSOT.

## Consequences

- Schema governance and evolution are first-class; changes require schema updates and tests.
- Renderers can evolve independently but must not add semantics beyond the JSON model.
- CI enforces schema validity of repository schemas; runtime validates inputs; outputs gain an optional validation flag.

## Alternatives Considered

- Ad-hoc JSON without schemas (rejected: ambiguity, drift, harder automation)
- YAML-first (rejected for outputs: less standard for downstream tooling)

## References

- SOP: `docs/sop/schema-first-sop.md`
- Schemas: `schemas/README.md`
- Output rendering: `docs/output_rendering.md`


