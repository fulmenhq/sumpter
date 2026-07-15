# Interim pin: antchfx/xpath numeric operand context isolation

**Status:** temporary reviewed local fork for Sumpter correctness floor
(`xpath-sum-multiply`). **Remove** this tree and the `go.mod` `replace`
once a tagged upstream release includes the fix.

## Base (full SHAs)

| Ref | Full SHA | Notes |
|---|---|---|
| Upstream module | `github.com/antchfx/xpath` | MIT |
| Tag still required by consumers | `v1.3.6` | go.mod require line |
| Base tip of this tree | `d645baed50e0ccf0201cb696779bc7badfdc5bbd` | Merge PR #123 on top of v1.3.6 |
| Intermediate upstream | `eca28236872d897b97b17a7e6d7d18ae8d779124` | `query: reset mergeQuery iterator in Evaluate` (#121) |

`xmlquery` remains **`v1.5.1`** (unchanged). Go MVS accepts a newer direct
`xpath` selection without an xmlquery bump; this replace supplies the fixed
tree without changing xmlquery.

## Local divergence from base tip

Compared to `d645baed50e0ccf0201cb696779bc7badfdc5bbd` (and the v1.3.6 module
cache where noted), this directory differs as follows.

### Production / test divergence (authored here)

1. **`query.go` — `numericQuery.Evaluate`** (the fix): snapshot operator
   context, evaluate + eager `asNumber` left, restore, evaluate + eager
   `asNumber` right, restore, then `Do`. Prevents predicate-moved navigators
   from poisoning a context-sensitive RHS.
2. **`numeric_context_test.go`** — hermetic library regression for the
   isolation class (new file), including same-context and cross-context
   (A→B→A) evaluation of one compiled expression.
3. **`//go:build` lines** on `func_go110.go` / `func_pre_go110.go` — mechanical
   dual build-tag form (modern `//go:build` plus legacy `// +build`); no
   behavior change.
4. **This README** (`SUMPTER-PIN-README.md`) — pin / removal notes only.

### Packaging-only divergence (not runtime)

5. **EOF single-newline normalization** on `.gitignore` and `LICENSE` (tree
   format gate; no content change).
6. **Omitted upstream CI tree** — upstream module cache / repo may carry
   `.github/workflows/{coverage,testing}.yml`; those workflows are **not**
   vendored here (Sumpter does not run upstream’s Actions from this tree).

Upstream content already present at base tip (not authored here):

- `query.go` — `mergeQuery.Evaluate` sets `m.iterator = nil` (#121 /
  `eca28236872d…`)
- related upstream test coverage for mergeQuery stale state

Because a local `replace` has **no go.sum checksum** for this tree, the
Sumpter commit that adds/updates `third_party/antchfx-xpath` is the integrity
boundary. Any later edit under this directory must be re-diffed against
upstream before merge.

## Removal task

Tracked follow-up: **https://github.com/fulmenhq/sumpter/issues/157**
(supervisor / human with GitHub write to upstream):

1. Open or land an upstream `antchfx/xpath` issue/PR with the `numericQuery`
   isolation fix and tests.
2. When a **tagged** fixed release is available: bump
   `github.com/antchfx/xpath` to that tag, delete this directory, delete the
   `go.mod` `replace`, re-run extract/index suites and `make check-all`.
3. Do not leave this replace past the tagged fix without an explicit
   supervisor hold.

In-tree reminder only — not a substitute for the tracked removal issue once
upstream write access is available.
