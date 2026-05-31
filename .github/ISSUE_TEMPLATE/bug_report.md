---
name: Bug report
about: Report something in sumpter that isn't working as expected
title: "[bug] "
labels: bug
assignees: ""
---

<!--
Thanks for taking the time to file a bug. Sumpter is in alpha — clear repro
steps with a minimal XML input + recipe (if relevant) help us reproduce
quickly and triage.

Please do NOT file security vulnerabilities here. See SECURITY.md for the
private reporting process (email security@3leaps.net).
-->

## Summary

<!-- One or two sentences describing what's wrong. -->

## Environment

- **OS / architecture**: (e.g., macOS 14.5 arm64, Ubuntu 22.04 x86_64)
- **Go toolchain**: (output of `go version`)
- **Sumpter version**: (output of `sumpter version`)
- **Install method**: (built from source / downloaded release binary / Docker / other)
- **Seekable-zstd build?**: yes / no (if applicable)

## Steps to reproduce

1.
2.
3.

## Expected behavior

<!-- What did you expect to happen? -->

## Actual behavior

<!-- What actually happened? Include CLI output, error messages, or stack traces. -->

## Minimal repro inputs

<!--
If the bug involves XML processing, please attach or inline the smallest
possible XML input that reproduces the issue (sanitize any sensitive data
first; ClinVar / SEC EDGAR XBRL fragments and the bundled examples/ corpus
are good starting points).

If the bug involves a recipe or DSL expression, include the recipe (or the
relevant excerpt).
-->

```xml
<!-- paste minimal XML repro here -->
```

```yaml
# paste minimal recipe / DSL excerpt here
```

## Additional context

<!--
- Any workaround you've found?
- Related issues or briefs?
- If you've already run `govulncheck`, `make pr-final`, or other diagnostics,
  please paste relevant output.
-->
