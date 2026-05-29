# Contributing to Sumpter (ALPHA)

Thanks for your interest in Sumpter! We're currently in the **ALPHA** phase. We value your feedback and early testing while we stabilize the streaming-XML core, the recipe DSL, and the output adapters.

## Current posture

- **External pull requests**: Temporarily paused except by invitation
- **Issues and discussions**: Welcome — bug reports, feature requests, recipe-DSL questions, UX feedback
- **Security reports**: Please report privately per [SECURITY.md](SECURITY.md); coordinated disclosure only

Rationale: during ALPHA we iterate quickly, may make breaking changes to the CLI, the recipe schema, and the DSL, and are prioritizing velocity and internal dogfooding. This posture prevents churn for contributors while we converge on stable APIs. We expect to open PRs to external contributors at BETA.

## How to help today

- Try the latest release (see [releases](https://github.com/fulmenhq/sumpter/releases)) and file issues with clear repro steps
- Share use cases and environment details (OS, Go version, sumpter version, representative XML input size and shape)
- Propose design ideas in issues *before* coding — we can provide guidance and pointers to in-flight work
- For public-data examples (SEC EDGAR XBRL, ClinVar, similar), recipe contributions via issue discussion are welcome

## Road to BETA

We expect to accept public PRs at BETA when:

- The recipe DSL surface stabilizes (no more breaking grammar changes)
- The output-adapter contracts (NDJSON, Parquet) freeze on minor versions
- Coverage gates raise from the alpha 50% baseline toward the beta 70% target

Until then, maintainers may tag issues as **"help wanted (invited)"** for targeted contributions.

## Development basics

If you're working on an invited contribution or a maintainer-internal fork:

```bash
# Build
make build                    # standard sumpter binary
make build-seekable-zstd      # variant with seekable-zstd output support

# Test
make test                     # full suite
make coverage-check-dynamic   # alpha threshold: 50% overall

# Quality gates
make check-all                # fmt + vet + lint
make precommit                # what the pre-commit hook runs
make prepush                  # what pre-push runs (matches CI)
make pr-final                 # full PR-finalization gate (incl. drift check)
```

The `make pr-final` gate includes a **drift check**: if the gate mutates any tracked file (most commonly via `go mod tidy` writing back to `go.mod` / `go.sum`), it fails and asks you to land those mutations as a separate PR before requesting review on the current one. Running `pr-final` on a ready PR should always be a no-op.

## Code quality

- **Tests**: Include unit and/or integration tests where practical. New CLI surface needs a corresponding test under the same package.
- **Style**: Follow existing patterns and the Go community style. `gofmt` and `goimports` are enforced.
- **Documentation**: Update README, `docs/user-guide/`, and the relevant SOPs for user-visible changes. New commands need a page under `docs/user-guide/commands/`.
- **Confidentiality posture**: Sumpter is an open-source extraction engine that supports many input formats and verticals. The OSS surface (this repo) **must not** name specific clients, customer-facing product brands, proprietary trade formats, or vertical-trade identifiers. Generic industry framing is fine; public open-data examples (ClinVar, SEC EDGAR XBRL) are encouraged. See [AGENTS.md § Confidentiality Posture](AGENTS.md) for the full rule.

## Attribution

- Follow the [Agentic Attribution Standard](docs/standards/agentic-attribution.md) for commits when applicable.
- All AI-agent contributions require human supervision and clear attribution.
- See [MAINTAINERS.md](MAINTAINERS.md) for the project's governance and role-based coordination.

## Reporting bugs and requesting features

- Bug: open an issue using the **Bug report** template. Include OS, Go version, sumpter version, and a minimal repro (XML input + recipe if relevant).
- Feature: open an issue using the **Feature request** template. Describe the use case before the implementation; we'll discuss design tradeoffs before any code is written.

Thanks for helping make Sumpter better!
