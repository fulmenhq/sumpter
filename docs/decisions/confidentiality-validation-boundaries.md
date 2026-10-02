# DR: Confidentiality Validation Boundaries

**Status:** Accepted
**Date:** 2026-10-02

## Contract

This record refines the enforcement subsection of
[ADR-0008](../architecture/adr/0008-sensitive-data-outside-repository-trees.md).
Its unconfigured no-op describes optional mode; required mode fails instead.
ADR-0008's sensitive-data residency rule remains authoritative.

`SUMPTER_CONFIDENTIALITY_CHECK` supplies one executable pathname, not a shell
command string. The hook invokes the resolved executable with no arguments,
preserves the caller's current working directory, and propagates the checker
exit status unchanged.

`SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK` is validated before skip or execution:

| Value                                                                      | Mode                |
| -------------------------------------------------------------------------- | ------------------- |
| Absent or exact `0`                                                        | Optional            |
| Exact `1`                                                                  | Required            |
| Any other set value, including empty, whitespace, `true`, `false`, or `01` | Configuration error |

In optional mode, an absent or empty checker produces **SKIPPED** and exit 0.
That is not confidentiality clearance. In required mode, absent or empty
checker configuration fails. Configured checks must pass admission and execute
successfully in either mode; optional mode does not turn invalid configuration
or a failing check into a skip.

Hook configuration errors exit 1. Checker failures retain their nonzero status.
The hook does not label an unexecuted check as PASS.

## Path admission

For configured execution in either mode, fully resolve the existing hook,
checkout root, and checker, including leaf symlinks. The checker must be a
readable executable regular file outside the hook's checkout and must not
resolve to the hook itself. Compare path components, not a shared string prefix.
Relative checker paths resolve from the caller's current directory; the checker
is not located by searching `PATH`.

Admission requires a working `realpath` or Python 3 resolver. Missing resolvers,
resolution failures, loops, and empty, relative or ambiguous results fail.
There is no parent-only fallback. Multiline pathnames are not admitted.
Resolving the hook first preserves the checkout boundary when it is invoked
through a symlink alias.

This hook checks its own checkout boundary. It does not certify every other
repository or the checker's transitive inputs. The configured checker owns its
input residency, policy, scan completeness, and metadata coverage. Failed Git
discovery is not proof that a path is outside a working tree.

Canonical admission is startup validation for trusted operator-controlled
files, not a race-proof sandbox for attacker-mutated paths.

## Required validation

With an approved checker already configured, use the existing targets:

```sh
SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK=1 make confidentiality-tree-check
SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK=1 make pr-final
```

The ordinary contributor default remains optional, including `make pr-final`.
Required invocations apply to trusted reviewed checkouts. They do not make it
safe to execute untrusted pull-request hooks or builds with private inputs.
This record introduces no CI orchestration or contributor-wide private-input
requirement.

Hook diagnostics are generic and do not echo configured paths. Checker output
is inherited; the configured checker must keep sensitive diagnostics and
reports private. Required mode enforces execution, not catalog approval, scan
completeness, or publication clearance. Those claims require evidence bound to
the exact revision and scope checked.
