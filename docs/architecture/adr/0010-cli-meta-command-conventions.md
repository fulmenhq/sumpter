# ADR 0010: CLI Meta-Command Invocation Conventions

Status: Proposed
Date: 2026-06-10

## Context

[ADR-0003](0003-unified-command-syntax.md) standardized flag categories, naming, and
per-command syntax across Sumpter's data-processing commands. It did not govern
_meta-commands_ — the invocation surfaces that report on or describe the tool itself
(version, help) rather than process data.

A concrete need surfaced during the v0.1.10 distribution work (SUM-039). Package-manager
surfaces require a canonical "is it installed, and what version?" smoke command: the
Homebrew formula `test` block, the Scoop manifest, install docs, and CI smoke checks all
need one agreed form. Across the FulmenHQ Homebrew tap the sibling products diverge —
`goneat` uses `--version`, while `dimlox` and `refbolt` use `version`.

Sumpter today supports **both** forms:

- a `version` subcommand (`cmd/sumpter/commands/version.go`) with `--extended` (build + git
  detail) and `--json` (machine-readable) flags;
- a Cobra-provided `--version` root flag, present because the root command sets its
  `Version` field (`cmd/sumpter/commands/root.go`).

Without a documented canonical form, downstream surfaces pick arbitrarily and future
commands risk re-litigating the same choice.

## Decision

Establish meta-command invocation conventions, extending ADR-0003.

### 1. Version reporting — the `version` subcommand is canonical

- `sumpter version` is the canonical, documented form:
  - `sumpter version` — concise human-readable version output;
  - `sumpter version --extended` — build and git detail;
  - `sumpter version --json` — machine-readable output.
- `sumpter --version` (the Cobra root flag) **remains supported** as a terse alias and is
  **not** removed — it satisfies habit and any tooling that probes `--version`. It is not
  the documented form and is not guaranteed to expose the extended/JSON surface.
- All Sumpter-authored surfaces — docs, install instructions, the Homebrew formula `test`
  block, Scoop smoke checks, and CI smoke tests — **must** use the `version` subcommand.

Rationale: bare `version` is the Go/Cobra ecosystem idiom (`go version`, `kubectl version`,
`gh version`, `hugo version`); it is the majority within the FulmenHQ tap (`dimlox`,
`refbolt`); and the subcommand is the only form that exposes the extended/JSON detail that
is useful in diagnostics and bug reports.

### 2. Help — Cobra-standard

`sumpter help [command]` and `--help`/`-h` follow Cobra defaults; no bespoke help command.
This is consistent with ADR-0003's `--help` formatting note.

### 3. Naming rule for future meta-commands

New meta/introspection commands (for example a future `completion` or `man`) **should** be
bare subcommands — not flags — when they produce structured or multi-line output. Reserve
root flags for terse boolean or alias behavior.

## Consequences

### Positive

- A single canonical answer; downstream surfaces stop diverging.
- Idiomatic for Go-tool users.
- `--version` keeps working, so there is no breakage and no penalty for the common habit.

### Negative / costs

- Two forms coexist (`version` and `--version`); the relationship must be explained once
  (here).
- This ADR governs Sumpter only. Sibling-repo divergence (e.g. `goneat`'s `--version`
  formula `test`) is out of scope.

## Alternatives Considered

- **`--version` flag as canonical** (goneat's choice): rejected — the flag form cannot carry
  the extended/JSON surface, and it is the minority within the tap.
- **Drop one form entirely**: rejected — removing `--version` breaks habit and tooling for no
  benefit; removing the subcommand loses the rich output.

## Implementation

No code change is required — both forms already exist. This ADR ratifies the convention and
points downstream surfaces at it.

Validation checklist:

- [ ] Homebrew `Formula/sumpter.rb` `test` uses `system bin/"sumpter", "version"`.
- [ ] Scoop manifest / install docs reference `sumpter version`.
- [ ] README / quickstart show `sumpter version`.
- [ ] Entarch pre-read before merge (cxotech-authored ADR; entarch consulted, per the
      established Sumpter ADR workflow).

## References

- [ADR-0003](0003-unified-command-syntax.md) — Unified Command Syntax Patterns; this ADR
  extends it to meta-commands.
- SUM-039 (Homebrew + Scoop distribution) — surfaced the canonical-version need; the formula
  `test` block consumes this decision.
- `cmd/sumpter/commands/version.go` — the `version` subcommand and its `--extended`/`--json`
  flags.
- `cmd/sumpter/commands/root.go` — the root `Version` field that yields the Cobra `--version`
  flag.
