<!-- Sumpter Pull Request Template -->
<!-- Adapted from: https://github.com/3leaps/oss-policies (PULL_REQUEST_TEMPLATE.md, 2025-11-03) -->
<!-- Sumpter-specific customizations: make pr-final + drift gate, agentic attribution, MAINTAINERS.md, AGENTS.md confidentiality posture -->

## Description

<!-- Provide a clear and concise description of your changes. If this PR implements a SUM-NNN brief, link it here. -->

## Type of Change

<!-- Check the relevant option(s) -->

- [ ] Bug fix (non-breaking change that fixes an issue)
- [ ] New feature (non-breaking change that adds functionality)
- [ ] Breaking change (fix or feature that would cause existing functionality to not work as expected)
- [ ] Documentation update
- [ ] Performance improvement
- [ ] Code refactoring
- [ ] Test additions or improvements
- [ ] Build / CI / release-pipeline configuration

## Related Issues / Briefs

<!-- Link to related issues using #issue_number; link briefs as SUM-NNN -->

Closes #
Implements brief: SUM-

## Changes Made

<!-- List the specific changes made in this PR -->

-
-
-

## Testing

### Test Coverage

- [ ] All existing tests pass (`make test`)
- [ ] New tests added for new functionality
- [ ] `make coverage-check-dynamic` passes (alpha threshold: 50% overall)

### Manual Testing

<!-- Describe manual testing performed -->

**Test Environment:**

- OS:
- Go version:
- Sumpter version (`sumpter version`):

**Steps taken:**

1.
2.
3.

## Documentation

- [ ] Code is self-documenting with clear function/variable names
- [ ] Comments added for public APIs
- [ ] README.md updated (if needed)
- [ ] `docs/user-guide/` updated (if user-visible CLI surface changed)
- [ ] Migration guide provided (for breaking changes)
- [ ] Examples updated (if applicable)

## Quality Gates

<!-- The pre-commit and pre-push hooks run these automatically; confirm clean before requesting review -->

- [ ] `make check-all` passes (fmt + vet + lint)
- [ ] `make precommit` passes (the pre-commit hook gate)
- [ ] `make prepush` passes (the pre-push hook gate; matches CI)
- [ ] `make pr-final` passes — **including the `pr-final-drift-check` drift gate**
- [ ] Build succeeds on all targeted platforms
- [ ] No new warnings introduced

> **Drift gate note**: `make pr-final` will fail if it mutates any tracked file (most commonly via `go mod tidy` writing back to `go.mod` / `go.sum`). If drift is detected, land the mutation as a **separate PR** and rebase this one — do not amend silently.

## Security Considerations

- [ ] No security implications
- [ ] Security review requested (if needed)
- [ ] New dependencies security-audited (`govulncheck ./...` clean; `goneat dependencies --licenses --vuln` passes policy)
- [ ] Input validation added for new user-facing surfaces (CLI flags, recipe schema, DSL functions)
- [ ] No secrets, credentials, or operator-machine paths committed

## Confidentiality Posture

<!-- Sumpter ships as OSS. See AGENTS.md § "Confidentiality Posture (OSS Surface)" for the rule. -->

- [ ] No client / customer / vendor names introduced
- [ ] No vertical-trade vocabulary, persona names, or engagement codenames introduced
- [ ] No operator-machine paths (`~/devsecops/`, `~/data/`, etc.) introduced into committed surface
- [ ] Examples use synthetic data or public open data (ClinVar, SEC EDGAR XBRL)

## Pre-submission Checklist

- [ ] I have read [CONTRIBUTING.md](../CONTRIBUTING.md)
- [ ] My code follows the project's coding standards (see [docs/standards/](../docs/standards/))
- [ ] I have added tests that prove my fix is effective or that my feature works
- [ ] All CI checks pass
- [ ] I have updated documentation as needed
- [ ] My changes do not introduce new dependencies without discussion
- [ ] Commit attribution follows the [Agentic Attribution Standard](../docs/standards/agentic-attribution.md) (where AI agents contributed)
- [ ] I have reviewed [MAINTAINERS.md](../MAINTAINERS.md) for governance context

## Additional Notes

<!-- Any additional information that reviewers should know -->
