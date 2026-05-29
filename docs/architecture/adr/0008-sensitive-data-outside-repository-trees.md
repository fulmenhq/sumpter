# ADR-0008: Sensitive Data Lives Outside Repository Working Trees

**Status:** Accepted
**Date:** 2026-05-29
**Decision Makers:** @3leapsdave
**Conforms to:** 3 Leaps OSS — Sensitive Local Data Policy ([3leaps/oss-policies](https://github.com/3leaps/oss-policies/blob/main/SENSITIVE-LOCAL-DATA.md))

## Context

Sumpter is a data **extraction** tool, so it is run against real, often
proprietary inputs. Configuration and tooling sometimes need machine-local data
that is necessary to operate but not appropriate for public repository history.
The project also maintains a generic open-source surface.

A common instinct is to keep such data inside the repository and rely on
`.gitignore` to keep it out of commits. That is not sufficient: ignore rules can
be edited or bypassed, copied worktrees and clones may not carry the same local
setup, and reviewers cannot reliably distinguish an intentional local-only file
from one exposed by accident. `.gitignore` is a convenience filter, not a
security boundary.

Sumpter already follows this instinct in places (for example the sibling
`.state/` location for local checkpoints, chosen to be structurally
uncommittable rather than merely ignored). This ADR generalizes it into a rule
and aligns sumpter with the canonical org policy referenced above.

## Decision

**Sensitive or proprietary local data lives outside the repository working
tree.**

- Do not place such data under the checkout or any worktree, even when the path
  is covered by `.gitignore`. Keep it in a location that is structurally
  uncommittable (an automation/developer-local home, or a sibling of the repo
  rather than a child).
- Tooling that accepts a file-backed sensitive input reads it from a path
  supplied via an environment variable or maintainer-provided process notes,
  rejects a configured path that resolves inside the repository root, and does
  not echo sensitive content in its own output.
- Concrete machine-local locations and any operational specifics belong in
  local operator notes (`AGENTS.local.md`), not in tracked files.

### Permitted in-tree exception: abstract placeholders only

Gitignored operator-guidance files intended to be local (`AGENTS.local.md`,
local planning notes) may remain in the tree only if they contain abstract
placeholders, never concrete sensitive values. Concrete values always go to the
out-of-tree location.

### `.env` files: secrets by reference

Where `.env` files are used, they hold **secrets by reference** — a pointer to
where a secret or proprietary value lives — rather than the value itself. This
keeps the residency rule intact even for the configuration files that
conventionally sit inside the tree.

## Enforcement

- `make pr-final` runs a confidentiality hook. The concrete check — and
  anything it needs to know — is supplied by the operator or CI through an
  environment variable and lives outside this repository; when none is
  configured the hook is a no-op. This keeps enforcement detail off the public
  surface while giving CI a place to run a real check.
- `AGENTS.md` states the rule declaratively, and the repo's agent role prompts
  reference this ADR so the expectation is carried where work actually happens
  (not as a generic "comply with all policies" line).
- `RELEASE_CHECKLIST.md` includes a confirmation step before release.

## Consequences

### Positive

- `.gitignore` becomes a convenience layer, not the primary protection.
- Worktrees and clones can be deleted, copied, or published with lower risk.
- The rule is mechanically testable and enforceable in configured CI/release
  gates (the `make pr-final` hook is a no-op until a concrete check is wired in).
- Public docs describe the rule generically, without naming private systems,
  customers, or coordination identifiers.

### Negative / mitigations

- One extra local-setup step per machine or worktree to point tooling at the
  out-of-tree location → keep machine-specific *locations* (not values) in
  `AGENTS.local.md`; prefer environment variables for paths.

## References

- [3leaps/oss-policies — SENSITIVE-LOCAL-DATA.md](https://github.com/3leaps/oss-policies/blob/main/SENSITIVE-LOCAL-DATA.md) — canonical org policy (this ADR declares conformance and adds sumpter-specific detail).
- `AGENTS.md` § Confidentiality Posture (OSS Surface) — the behavioral half; this ADR is the structural half.
- `AGENTS.local.md` — local operator guidance, including the concrete out-of-tree locations and any tooling specifics.
