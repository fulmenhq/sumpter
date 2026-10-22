# Toolchain Contract

Sumpter's local quality gate must predict CI. The pinned toolchain contract is
stored in [`config/toolchain.env`](../../config/toolchain.env) and is consumed
by `make lint`, `make check-all`, CI, and release builds.
The verifier also checks `.goneat/tools.yaml` so the goneat installer metadata
cannot drift silently from the contract.

Current contract:

- Go: `1.26.10`
- golangci-lint: `v2.14.0`
- `GOFLAGS`: empty

## Local Checks

Run the same contract check that `make lint` runs before static analysis:

```bash
make toolchain-check
```

When another Go release is the host default, select the pinned SDK explicitly:

```bash
PATH="$HOME/sdk/go1.26.10/bin:$PATH" GOTOOLCHAIN=go1.26.10 make toolchain-check
```

The module requires Go 1.26.10. When `go.mod` omits a `toolchain` directive,
the verifier uses its `go` directive as the implicit toolchain, matching Go's
toolchain selection rules. The executing Go version must still match the
selected contract exactly.

If golangci-lint is missing, install the CI-pinned version:

```bash
make install-golangci-lint
```

`make lint` installs the pinned golangci-lint when it is missing. If a different
golangci-lint version is already on `PATH`, the command fails before linting and
prints the remediation command. It does not run a drifted linter.

If `go version` or `GOFLAGS` differs from the contract, `make lint` also fails
before static analysis. Clear local `GOFLAGS` or switch to the pinned Go
toolchain before trusting a local green result.

## Staticcheck Probe

The probe verifies that the pinned local linter executes staticcheck and fails
on a synthetic `SA4000` identical-expression finding:

```bash
make lint-staticcheck-probe
```

Run it after changing the linter version, Go version, `.golangci.yml`, or CI
lint setup.

The previous probe used `SA5011` (possible nil-pointer dereference). The
staticcheck version bundled with golangci-lint 2.14.0 disables that analyzer.
The current probe proves staticcheck execution; it does not establish equivalent
nil-pointer analysis or a replacement for the removed `SA5011` coverage.

## Updating The Contract

Update `config/toolchain.env` first, then run:

```bash
make toolchain-check
make lint-staticcheck-probe
make check-all
```

If the Go or golangci-lint version changes, update `.goneat/tools.yaml` in the
same commit. `make toolchain-check` fails if the goneat tool metadata no longer
matches `config/toolchain.env`.

If documentation changes touch embedded docs, run:

```bash
make embed-assets
make verify-embeds
```
