# AI Agents - Sumpter Technical Standards

**Project**: sumpter
**Governance**: Fulmen Ecosystem Standards
**Last Updated**: May 10, 2026

## 🚨 Critical Safety Protocols

**BEFORE ANY WORK**: Read this document completely for mandatory safety rules including:

- Go-specific security protocols for XML processing development
- Lifecycle phase-aware quality gates and coverage requirements
- Documentation and SOP compliance for enterprise development
- Security-first development patterns for XML data processing

These protocols ensure professional development standards and enterprise readiness.

## 🔒 **Repository Operations Requirements**

### **CRITICAL: Never Commit Gitignored Content**

- **DO NOT commit** any files or directories that are covered by `.gitignore`.
  - Common examples: `.plans/`, `.scratchpad/`, `dist/`, `tmp/`, `coverage*/`, `test-results/`, and other local artifacts.
- Before any commit, **always** run `git status` and ensure only intended, non-ignored files are staged.
- **NEVER remove or weaken** ignore rules in `.gitignore` without explicit maintainer approval.
  - If a file is ignored but you think it should be tracked, stop and ask.

### **Commit and Push Protocols**

Sumpter operates on a **feature-branch + PR review model**. The microtool-era
"per-occurrence approval for every commit" rule has been relaxed in favor of
human review at PR time and CI-enforced quality gates. Some controls remain
strict because they cross the trust boundary or touch protected branches.
This section is the canonical source of truth;
[REPOSITORY-SAFETY-PROTOCOLS.md](REPOSITORY-SAFETY-PROTOCOLS.md) cross-references it.

**On feature branches (the normal case):**

- ✅ Agents commit freely after running `make check-all` (or `make precommit`
  for the heavier coverage gate). The goneat pre-commit hook is authoritative
  for what blocks a commit.
- ✅ Agents may stage, draft commit messages, and create commits without
  per-occurrence supervisor approval. Review happens at PR time.
- ✅ Follow the Repository Operations SOP for commit hygiene:
  "check-all, stage, pre-commit, commit".

**Pushes still require per-occurrence approval:**

- 🚨 `git push` to any remote branch requires explicit, real-time authorization
  from a supervisor (typically @3leapsdave). This is unchanged.
- 🚨 Pre-push review must include CI expectations, branch protection state, and
  any race or security concerns surfaced by `make prepush`.

**Strict controls (unchanged from microtool-era policy):**

- ❌ **Direct commits or pushes to `main` or release branches** require
  per-occurrence approval, regardless of feature-branch policy.
- ❌ **Bypassing git hooks** (`--no-verify`, `GIT_NO_HOOKS`, etc.) requires
  per-occurrence supervisor approval with written justification.
- ❌ **Force-push** (`--force`, `--force-with-lease`) requires per-occurrence
  supervisor approval, including on feature branches.
- ❌ **Weakening `.gitignore`** or quality gate thresholds requires maintainer
  approval (see Confidentiality Posture and Quality Gates sections).

**Violation of the strict controls results in immediate work stoppage.**

## 🤐 Confidentiality Posture (OSS Surface)

Sumpter is an open-source extraction engine that supports many input formats
and verticals. The OSS surface (this repo: code, docs, schemas, examples,
fixtures, commit messages, PR descriptions, issue tracker) **must not name or
telegraph specific clients, customer-facing product brands, proprietary trade
formats, or vertical-trade identifiers**. Generic industry framing is fine
(for example "retail POS journals", "genomics variants") and public open-data
examples (ClinVar, SEC EDGAR XBRL) are encouraged. Client-specific recipes,
fixtures, and workspaces live outside this repo and stay there.

The forthcoming `limensafe` scanner will enforce this in CI/DX; until then,
follow the posture manually and call out questionable wording in review.

## 🗂️ Local-Only Scratchpads

A `.plans/active/` directory at the repo root is the conventional location
for **machine-local, non-evergreen** planning notes (in-flight design,
release checkpoints, strategy drafts). It is gitignored — you will not find
it in a fresh clone, and it is not synchronized across machines. (This
replaces the older single-file `AGENTS.local.md` convention with a directory
of dated documents.) Anything that needs to be shared or versioned belongs
in `docs/` and goes through the normal PR flow.

## 📋 Quick Reference SOPs

**CRITICAL**: Use these Standard Operating Procedures for common development tasks:

- **[Repository Operations SOP](docs/sop/repository-operations-sop.md)** - "Check-all, stage, pre-commit, commit"
- **[Lifecycle Phase Acceptance](docs/sop/lifecycle-phase-acceptance-criteria.md)** - "Phase validation and transition procedures"
- **[Dynamic Coverage Gating](config/coverage-thresholds.yaml)** - "Alpha: 50%, Beta: 70%, Production: 80%"

**Usage**: Reference SOPs for consistent development practices across all Sumpter development tasks.

## Agent Identity and Coordination

**For maintainer and governance details, see [MAINTAINERS.md](MAINTAINERS.md).**

Sumpter now follows the FulmenHQ/Lanyte team model. Do not assume a persistent
named agent persona from this repository. Derive identity from the environment
for each session:

| Variable             | Purpose                                                                      |
| -------------------- | ---------------------------------------------------------------------------- |
| `LANYTE_AGENT_ROLE`  | Role slug, usually team-scoped (for example `india-devlead`, `india-devrev`) |
| `LANYTE_AGENT_SCOPE` | Org scope (`fulmenhq`)                                                       |
| `LANYTE_AGENT_TEAM`  | NATO team name (`india` for Sumpter)                                         |

The org-root warmup in [`../AGENTS.md`](../AGENTS.md) is authoritative for
identity, Mattermost channel usage, and checkpointing. Repo guidance here is
authoritative for Sumpter build, safety, lifecycle, and XML-processing rules.

Role functions are coordinated by slug rather than persona name:

| Role family | Typical Sumpter focus                                             |
| ----------- | ----------------------------------------------------------------- |
| `devlead`   | Implementation, CLI behavior, XML/extract/index features          |
| `devrev`    | Code review, regression risk, quality-gate assessment             |
| `qa`        | Test strategy, coverage, fixtures, lifecycle validation           |
| `secrev`    | XML/data security, redaction, dependency and scanner review       |
| `releng`    | Release sequencing, CI/CD, tags, packaging                        |
| `entarch`   | Cross-repo architecture and Fulmen ecosystem alignment            |
| `dataeng`   | Data pipeline ergonomics, extraction workflows, analytics outputs |

## 🔥 Agent Warmup Guidelines

### Known Interface Adapters

| Agentic Interface | Definitive Prompt File | Attribution interface value |
| ----------------- | ---------------------- | --------------------------- |
| Claude Code       | `CLAUDE.md`            | `Claude Code`               |
| Cursor            | `AGENTS.md`            | `Cursor`                    |
| Cline             | `AGENTS.md`            | `Cline`                     |
| KiloCode          | `AGENTS.md`            | `KiloCode`                  |
| OpenCode          | `AGENTS.md`            | `OpenCode`                  |
| Codex CLI         | `CODEX.md`             | `Codex CLI`                 |

Attribution identifies the model and interface, while coordination identifies
the active role slug from `LANYTE_AGENT_ROLE`.

## 📜 Critical Documents to Review and Follow

The following documents are critical to the safety and security of this repository. All agents must read and follow them at all times:

- **[REPOSITORY-SAFETY-PROTOCOLS.md](REPOSITORY-SAFETY-PROTOCOLS.md)**: This document contains the mandatory safety protocols for this repository. It includes information about operational danger classifications, explicit authorization protocols, and mandatory safety protocols.

- **[docs/sop/repository-operations-sop.md](docs/sop/repository-operations-sop.md)**: This document describes the standard operating procedures for repository operations, including commit and push workflows. It contains a specific alert for AI agents regarding push operations.

### 🚨 CRITICAL: No Project Work Until Authorized

**MANDATORY PROTOCOL**: All AI agents must obtain explicit authorization before beginning any project work, especially for XML processing CLI development.

#### ⚡ **Fast Start Checklist**

```bash
# ✅ Session Authorization Checklist
□ Confirm role/team and supervisor (@3leapsdave)
□ Confirm scope and allowed ops (read/edit code, run tests, create commits/PRs on feature branches)
□ Confirm push authority (CRITICAL: pushes still require per-occurrence approval; commits on feature branches do not)
□ Confirm no main-branch ops, no git config changes, no production secrets
□ Confirm current lifecycle phase (alpha/beta/production) from LIFECYCLE_PHASE
□ Verify quality gates required (make pre-commit, coverage-check-dynamic)
□ Verify Go tooling availability (go, gofmt, golangci-lint)
□ Verify security scanning tools (gosec, govulncheck)
□ Understand lifecycle-specific coverage requirements
```

#### Required Warmup Process

**🚨 MANDATORY READINGS (ALL AGENTS):**

1. **Read AGENTS.md** - Review current role and responsibilities
2. **Read MAINTAINERS.md** - Maintainer governance, role families, and coordination protocols
3. **Read REPOSITORY-SAFETY-PROTOCOLS.md** - Understand the repository's safety protocols
4. **Read LIFECYCLE_PHASE** - Current repository lifecycle phase
5. **Read config/coverage-thresholds.yaml** - Phase-specific coverage requirements

**📋 STANDARD OPERATING PROCEDURES (ALL AGENTS):**

5. **Read docs/sop/repository-operations-sop.md** - "Check-all, stage, pre-commit, commit"
6. **Read docs/sop/lifecycle-phase-acceptance-criteria.md** - Phase validation procedures
7. **Read docs/standards/lifecycle-maturity.md** - Lifecycle framework
8. **Read docs/standards/agentic-attribution.md** - AI agent attribution standards
9. **Read all docs/architecture/adr/\*.md** - Architecture decision records (mandatory, with any specified exclusions)
10. **Use make targets for all development operations** - Prefer `make check-all`, `make test`, `make build` over direct `go` commands or shell scripts whenever possible

**🔧 ENVIRONMENT VERIFICATION:** 8. **Check project status** - Understand current state and active work 9. **Request authorization** - Wait for explicit go-ahead from supervisor 10. **Confirm scope** - Verify specific tasks and boundaries before proceeding 11. **Verify Go environment** - Ensure all required tools are available and working 12. **Understand lifecycle requirements** - Phase-appropriate quality standards

**🔄 SSOT (Single Source of Truth) Context:** 13. **Understand environment standard** - Read `docs/standards/application-environment.md` for directory structure

**SSOT Architecture Overview:**

- **Environment Standard**: `docs/standards/application-environment.md` defines directory structure
- **Home Directory**: `${SUMPTER_HOME:-${HOME}/.sumpter}` with functional organization
- **Shared Resources**: Assets, cache, and configs shared across ecosystem

#### Why This Matters for Sumpter

- **Prevents quality regressions** - XML processing requires lifecycle-aware quality gates
- **Maintains professional standards** - Streaming XML tools require enterprise patterns
- **Respects development boundaries** - Alpha/Beta/Production phases have different requirements
- **Quality assurance** - Coverage and security standards must be maintained per phase

#### 🛑 **Warning Signs to STOP**

```bash
# ❌ STOP if any of these are true:
□ Unclear about current Sumpter development state or lifecycle phase
□ No recent context about coverage requirements for current phase
□ Missing supervisor direction on quality gate requirements
□ Uncertainty about what XML processing functionality is already implemented
□ Unfamiliar with Sumpter architecture and streaming algorithms
```

**🚨 When in doubt, ASK for authorization rather than assume.**

## 🔧 Sumpter Specific Guidelines

### 🎯 **CRITICAL**: Lifecycle Phase Awareness

**ALWAYS follow phase-appropriate development patterns:**

```bash
# ✅ DO: Check current lifecycle phase
cat LIFECYCLE_PHASE    # Current: alpha

# ✅ DO: Use phase-appropriate coverage requirements
./scripts/validate-coverage-threshold.sh    # Dynamic phase-based validation

# ✅ DO: Follow quality gates for current phase
make check-all         # Basic quality checks
make pre-commit        # Alpha: 50% coverage required
make pre-push          # Full validation with dynamic thresholds
```

**WHY**: Different lifecycle phases have different quality requirements and coverage expectations.

### 🔒 **Professional Development Requirements**

**MANDATORY for all XML processing development:**

```bash
# ✅ DO: Always validate lifecycle-appropriate coverage
make coverage-check-dynamic    # Uses LIFECYCLE_PHASE + config/coverage-thresholds.yaml

# ✅ DO: Follow enterprise XML processing patterns
- Use encoding/xml with streaming token decoder
- Implement UTF-8 normalization and encoding detection
- Follow security-first patterns for XML data processing
- Apply environment variable redaction for sensitive data

# ✅ DO: Use professional tooling
make check-all         # Formatting, vetting, linting
make security-scan     # gosec + govulncheck
make build            # Multi-platform builds
```

**MANDATORY Steps for XML processing development:**

- **Before writing any code**: Understand current lifecycle phase and requirements
- **Before committing changes**: Run appropriate quality gates (make pre-commit)
- **Before architecture changes**: Ensure compatibility with lifecycle progression
- **After significant changes**: Validate coverage meets phase requirements

### 📋 **Quality Standards for Sumpter**

**Before writing ANY XML processing code, ensure:**

```bash
# ✅ DO: Follow XML processing best practices
- Use streaming parsers for large files (<50MB RSS target)
- Implement encoding detection and UTF-8 normalization
- Follow security-first patterns for XML data handling
- Use lifecycle-aware coverage validation

# ✅ DO: Use Sumpter patterns
- Follow cmd/sumpter/ command organization
- Use internal/inspect/ for XML processing logic
- Follow docs/sop/ procedures for development
- Use config/coverage-thresholds.yaml for validation
- Follow Fulmen ecosystem standards
```

### 🧪 **Testing Requirements**

**MANDATORY for all XML processing functionality:**

```bash
# ✅ DO: Test lifecycle compliance
func TestAlphaCoverageRequirements(t *testing.T) {
    // Test that current coverage meets alpha requirements (50%+)
    // Test dynamic coverage validation script
    // Test quality gate enforcement
}

# ✅ DO: Test XML processing functionality
func TestInspectCommand(t *testing.T) {
    // Test streaming XML parsing with encoding detection
    // Test path tracking and attribute enumeration
    // Test report generation (Markdown/JSON)
}

# ✅ DO: Test enterprise patterns
func TestSecurityFirst(t *testing.T) {
    // Test environment variable redaction
    // Test secure error handling for malformed XML
    // Test logging without sensitive data exposure
}
```

## 📝 **Commit Attribution Format**

**MANDATORY**: All agent commits must use proper attribution. See [Agentic Attribution Standard](../docs/standards/agentic-attribution.md#standard-pattern) for the exact format.

**Quick Reference**:

```
Generated by [Model Name] via [Interface] under supervision of [@3leapsdave](https://github.com/3leapsdave)

Co-Authored-By: [Model Name] <noreply@fulmenhq.dev>
Role: [LANYTE_AGENT_ROLE]
Committer-of-Record: Dave Thompson <dave.thompson@3leaps.net> [@3leapsdave](https://github.com/3leapsdave)
```

## 📂 **Sumpter File Operation Rules**

### XML Processing Development Protocols

**ALWAYS understand XML processing structure before operating:**

```bash
# ✅ DO: Understand Sumpter architecture before changes
ls cmd/sumpter/commands/     # See existing CLI commands
ls internal/inspect/         # Check XML processing implementation
ls docs/sop/                # Review development procedures
```

**WHY**: XML processing applications have specific patterns that must be preserved for professional development.

### 🔒 **Lifecycle-Critical File Handling**

**ALWAYS read lifecycle-related files completely before editing:**

```bash
# ✅ DO: Read lifecycle implementations fully
Read: LIFECYCLE_PHASE                                    # Current phase
Read: config/coverage-thresholds.yaml                   # Phase requirements
Read: docs/sop/lifecycle-phase-acceptance-criteria.md   # Transition procedures

# ❌ DON'T: Modify lifecycle code without full context understanding
# ❌ DON'T: Modify internal/assets/embedded* files directly - these are auto-generated
#          from SSOT sources (docs/, schemas/, examples/) via scripts/embed-assets.sh
```

**MANDATORY Steps for lifecycle code:**

- **Before editing coverage config**: Understand phase-specific requirements
- **Before editing SOPs**: Understand enterprise development workflow
- **Before editing standards**: Understand Fulmen ecosystem compliance
- **After lifecycle changes**: Run comprehensive validation and documentation updates

## 🚀 **Development Workflow**

### **Sumpter Specific Commands**

```bash
# ✅ DO: Use our lifecycle-aware build and test commands
make check-all                    # Quality checks (fmt, vet, lint)
make coverage-check-dynamic       # Phase-appropriate coverage validation
make pre-commit                   # Full pre-commit validation
make pre-push                     # Comprehensive validation
make build                        # Multi-platform binary builds

# ✅ DO: Test XML processing functionality
./dist/sumpter --help            # Test CLI help system
./dist/sumpter version            # Test version with build metadata
./dist/sumpter inspect --file test.xml  # Test XML inspection

# ✅ DO: Validate lifecycle compliance
./scripts/validate-coverage-threshold.sh              # Dynamic coverage validation
./scripts/validate-coverage-threshold.sh production   # Test production requirements
```

### **XML Processing Development Standards**

```bash
# ✅ DO: Follow XML processing patterns
- Implement streaming token decoder for large files
- Use golang.org/x/net/html/charset for encoding detection
- Implement path tracking with dot notation (Envelope.Header.Message)
- Follow lifecycle-aware quality gates for professional development

# ✅ DO: Test XML security and quality
- Validate encoding normalization accuracy
- Test quality gates under different lifecycle phases
- Verify multi-platform build compatibility
- Test professional help and usage patterns
```

## 🌟 **Enterprise Readiness Standards**

### **Quality Requirements**

- **Test Coverage**: Phase-appropriate (Alpha: 50%+, Beta: 70%+, Production: 80%+)
- **Security Scanning**: Zero vulnerabilities (gosec + govulncheck)
- **Code Quality**: Zero linting issues (golangci-lint)
- **Documentation**: Complete SOPs and lifecycle documentation

### **Professional Standards**

- **XML Processing**: Streaming-first with <50MB RSS target
- **Encoding Support**: UTF-8 normalization with charset detection
- **CLI Framework**: Cobra-based with structured commands
- **Versioning**: Single source of truth with build-time injection

### **Lifecycle Compliance**

- **Phase Awareness**: Respect current lifecycle phase requirements
- **Quality Gates**: Phase-appropriate pre-commit and pre-push validation
- **Documentation**: Comprehensive SOPs and acceptance criteria
- **Professional Tooling**: Enterprise-grade build system and automation

---

## 📝 **Continuation Notes - Extract Command Development**

**Next Phase**: Extract Command Implementation

- **Status**: Ready to begin after retrieve command completion
- **Scope**: Implement XML extraction engine with streaming token decoder
- **Requirements**:
  - Follow existing patterns from inspect command
  - Implement schema-driven extraction
  - Add configuration-based output formatting
  - Ensure lifecycle-aware quality gates
- **Dependencies**: Retrieve command data for testing extraction workflows

**Previous Work Summary**:

- ✅ Retrieve command with SEC EDGAR support implemented
- ✅ Path validation and fail-fast error handling added
- ✅ Command-specific directory structure ($SUMPTER_HOME/work/retrieve/)
- ✅ XBRL XML file detection and download working
- ✅ Commit created with proper attribution

---

**Team Model**: FulmenHQ India team, role-driven via `LANYTE_AGENT_ROLE`
**Technical Supervision**: @3leapsdave (David Thompson) - Technical Lead & Project Supervisor
**Quality Standards**: Fulmen ecosystem compliance with lifecycle-aware development
**Current Phase**: Alpha (50% coverage requirement, enterprise XML processing foundation)
