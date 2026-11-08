# Interim pin: antchfx/xpath union document order

**Status:** temporary reviewed local tree for Sumpter correctness floor
(`xpath-union-document-order`). **Remove** this tree and the `go.mod` `replace`
once a tagged upstream release that includes the fix is adopted.

## Base (full SHAs)

| Ref | Full SHA | Notes |
| --- | --- | --- |
| Upstream module | `github.com/antchfx/xpath` | MIT |
| Tag still required by consumers | `v1.3.8` | `go.mod` `require` line |
| Base tag of this tree | `d666d4b6f3b570811b144144414971401472b83c` | `v1.3.8` |
| Upstream merge of the fix | `286597fe456d52f4a1b389e47852c3dff7a96ed4` | `Sort union node-set results in document order (#178)` |

`xmlquery` remains **`v1.5.1`** (unchanged). Go MVS accepts the replaced `xpath`
module without an `xmlquery` bump; this replace applies the merged upstream
union-ordering fix onto `v1.3.8` without pulling in unreviewed post-`v1.3.8`
coercion changes.

## Local divergence from `v1.3.8` (`d666d4b6f3b570811b144144414971401472b83c`)

Compared to `v1.3.8`, this directory differs as follows:

### Production / test divergence

1. **`query.go` — `unionQuery.Select` + `sortDocumentOrder` / `getNodeOrderKey`**
   (exact match to upstream merge `286597fe456d52f4a1b389e47852c3dff7a96ed4`,
   PR #178): when both operands of a union (`|`) select nodes, stable-sort the
   deduplicated node list into document order (`sortDocumentOrder` using
   ancestor sibling indices from `getNodeOrderKey`, with `-1` for attributes so
   attributes sort after their owner element and before its children).
2. **`xpath_expression_test.go` — `TestUnionDocumentOrder`**: upstream PR #178
   test suite, with `(//email | //name)[position() = 2]` expressed as
   `(//email | //name)[2]` because `[position() = 2]` on a grouped union also
   depends on upstream PR #162 (`9e409235ae5db18eb1de5e8e2a7fc04f9318a395`,
   post-`v1.3.9`).
3. **`//go:build` lines** on `func_go110.go` / `func_pre_go110.go` — mechanical
   dual build-tag form (`gofmt`); no behavior change.
4. **This README** (`SUMPTER-PIN-README.md`) — pin / removal notes only.

### Packaging-only divergence (not runtime)

5. **EOF single-newline normalization** on `.gitignore` and `LICENSE` (tree
   format gate; no content change).
6. **Omitted upstream CI tree** — `.github/workflows/{coverage,testing}.yml` are
   not vendored here.

Because a local `replace` has **no `go.sum` checksum** for this tree, the
Sumpter commit that adds `third_party/antchfx-xpath` is the integrity boundary.
Any edit under this directory must be re-diffed against upstream before merge.
