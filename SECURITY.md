# Security Policy

## Overview

3 Leaps, LLC is committed to ensuring the security of our open-source projects and supported ecosystems (e.g., fulmenhq, mdmeld, docemist). We appreciate the community's help in responsibly disclosing vulnerabilities to protect users. This policy outlines how to report issues and our process for handling them.

All reports and handling must align with our [Code of Conduct](CODE_OF_CONDUCT.md).

## Supported Versions

Security updates are provided for:

- **Latest stable release**: Current production-ready version
- **Alpha releases**: Best-effort support during active development

**Current Status**: Sumpter v0.1.x is in **alpha**. We provide security patches for the latest v0.1.x release. There is no commitment to backport fixes to earlier 0.1.x patches once a newer 0.1.x ships. For an overview of what alpha means across the whole project, see the **Project status: alpha** section of the [README](README.md); this document is the canonical home for supported versions and reporting.

| Version | Supported          |
| ------- | ------------------ |
| 0.1.x   | :white_check_mark: |
| < 0.1   | :x:                |

## Reporting a Vulnerability

If you discover a potential security vulnerability, please report it privately — do not disclose it publicly (e.g., via issues or forums) until we've had a chance to address it.

### How to Report

- **Preferred Method**: Email **security@3leaps.net** with details, including:
  - Description of the vulnerability
  - Steps to reproduce (e.g., affected version, configuration, sample XML payload if relevant)
  - Potential impact (e.g., data exposure, denial of service, privilege escalation, path traversal, XXE/billion-laughs-style amplification)
  - Any proposed fixes or patches
- **Alternative**: Use GitHub Security Advisories in this repository (if enabled) for private reporting
- **Encryption**: If sensitive, encrypt your report using our public PGP key (available upon request from security@3leaps.net)

We prioritize confidentiality and will acknowledge your report within 3 business days.

## Vulnerability Handling Process

1. **Acknowledgment**: We'll confirm receipt and provide an initial assessment within 3 business days.
2. **Triage and Validation**: Our team will investigate and validate the issue, typically within 7 days.
3. **Fix Development**: If confirmed, we'll develop a fix. Timeline depends on severity but aims for resolution within 30 days for critical issues.
4. **Coordinated Disclosure**: We'll work with you on a disclosure plan. Vulnerabilities are publicly disclosed after a fix is released, or no later than 90 days from report (whichever comes first), unless mutually agreed otherwise.
5. **Credit**: Reporters are credited in advisories (with your permission) for responsible disclosures.

## Scope

This policy applies to:

- The `sumpter` CLI and its libraries (`github.com/fulmenhq/sumpter/...`)
- The streaming XML inspection, extraction, indexing, and retrieve subsystems
- Recipe execution and DSL evaluation paths
- Output adapters (NDJSON, Parquet)
- Bundled example recipes and the public-data example corpus (SEC EDGAR XBRL, ClinVar) when those examples could lead to insecure implementations if copied verbatim

Out of scope:

- Theoretical vulnerabilities without a practical exploit path against the current alpha surface
- Vulnerabilities in dependencies (report to upstream; notify us as well if they affect sumpter at runtime — see "Scanner Output Interpretation" below for guidance)
- Issues requiring physical access to user systems
- Issues that require the operator to deliberately disable a documented safeguard (e.g., turning off output-path validation)

## Safe Harbor

If you follow this policy in good faith (e.g., no exploitation beyond proof-of-concept, no exfiltration of data beyond what's strictly necessary to demonstrate the issue), we will not pursue legal action against you. We consider this ethical security research.

## Security Best Practices for Sumpter Users

When using sumpter in your applications and pipelines:

- **XML Input Validation**: Treat untrusted XML as untrusted. Sumpter tokenizes large XML inputs incrementally, but extracted records are buffered per file before output and operator-supplied recipes or DSL expressions can amplify problematic inputs.
- **Recipe Trust Boundary**: Recipes are executable configuration. Run recipes you didn't write the same way you'd run a script you didn't write — review the DSL, the field bindings, and any external references before invoking.
- **Output Path Discipline**: Use the documented output-path validation and never disable it for production runs. Avoid writing into shared directories that other processes are reading from.
- **Resource Limits**: For unattended pipelines, run sumpter with explicit memory and time bounds (ulimit, systemd resource controls, container limits). Current releases do not promise bounded end-to-end extraction memory across all paths.
- **Dependency Hygiene**: Keep your Go toolchain and `go.sum` current. Sumpter ships with a clean `govulncheck` baseline; preserve that posture in downstream forks by running `govulncheck ./...` after dependency changes.
- **Logging**: Sumpter's logging package applies environment-variable redaction. Don't disable that redaction in production. If your recipes embed credentials (they shouldn't), audit log output before forwarding to centralized logging.

## Scanner Output Interpretation

Sumpter's authoritative vulnerability posture is measured by **[govulncheck](https://pkg.go.dev/golang.org/x/vuln/cmd/govulncheck)**, which uses Go-call-graph reachability to determine whether a known CVE in a dependency is actually exercised by sumpter's code. A clean `govulncheck ./...` report is the canonical signal of "no actionable Go vulnerabilities reachable from the sumpter binary."

We also generate an SBOM and run a Common Platform Enumeration (CPE) match against it using [grype](https://github.com/anchore/grype) (via `goneat dependencies --vuln`). **CPE-based scanners produce a noisier report than govulncheck** because they flag every package in the SBOM that has a CVE recorded, regardless of whether sumpter actually calls into the vulnerable code path. If you scan sumpter (or a sumpter-derived artifact) with grype, trivy, snyk, or a similar CPE-match scanner, expect to see findings that fall into one of the following categories:

1. **Go stdlib findings against an older minor version** — The SBOM generator sometimes records an older Go stdlib version than the toolchain actually used to build the binary. Sumpter is built against the Go toolchain pinned in `go.mod`; check the actual binary's stdlib version with `go version <binary>` to determine whether the flagged CVE applies. Sumpter releases pin a toolchain that includes upstream Go security patches at release time.
2. **Non-runtime artifacts in the source tree** — The SBOM may include packages from documentation tooling, embedded asset bundles, GitHub Actions, or build-only dependencies that are not part of the shipped binary. These do not affect sumpter at runtime.
3. **Transitive Go modules not reachable from sumpter code** — Some Go modules are pulled in by your dependency graph but never called by sumpter. `govulncheck ./...` is authoritative on reachability.
4. **Genuine runtime CVEs** — Anything that survives the above three categories _is_ a real concern. Report it via the process above.

If you're triaging a scan report against sumpter and want to confirm whether a finding is actionable, the fastest path is:

```bash
# install govulncheck if needed
go install golang.org/x/vuln/cmd/govulncheck@latest

# run against the same checkout / dependency graph
govulncheck ./...
```

If govulncheck says the vulnerability is not reachable from your call graph, sumpter is not affected. If it _is_ reachable, please report it (see "Reporting a Vulnerability" above).

## Dependency Audit

To audit sumpter's dependencies yourself:

```bash
# View dependency graph
go mod graph

# Check for known vulnerabilities (reachability-based — authoritative)
govulncheck ./...

# Generate SBOM + CPE-based scan (broader, noisier — see "Scanner Output Interpretation")
goneat dependencies --licenses --vuln    # writes SBOM and vuln report under sbom/
```

The `sbom/` output is gitignored; treat it as scratch.

## Questions

For questions about this policy, contact security@3leaps.net or open a non-security issue in this repository.

For additional governance details and contributor obligations, see the [3 Leaps Open Source Policies](https://github.com/3leaps/oss-policies) (when public) and [MAINTAINERS.md](MAINTAINERS.md).

---

_This policy is subject to change. Last updated: 2026-05-28._
