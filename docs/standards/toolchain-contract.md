# Toolchain Contract

Sumpter's local quality gate must predict CI. The pinned toolchain contract is
stored in [`config/toolchain.env`](../../config/toolchain.env) and is consumed
by `make lint`, `make check-all`, CI, and release builds.
The verifier also checks `.goneat/tools.yaml` so the goneat installer metadata
cannot drift silently from the contract.

Current contract:

- Go: `1.26.6`
- golangci-lint: `v2.11.2`
- `GOFLAGS`: empty

## Local Checks

Run the same contract check that `make lint` runs before static analysis:

```bash
make toolchain-check
```

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

SUM-036 was filed after a false-green local review where CI caught staticcheck
`SA5011` and local `make check-all` did not. The regression probe verifies that
the pinned local linter catches a synthetic `SA5011` case:

```bash
make lint-staticcheck-probe
```

Run it after changing the linter version, Go version, `.golangci.yml`, or CI
lint setup.

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
