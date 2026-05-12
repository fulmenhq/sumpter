# 🔧 SUMPTER REPOSITORY SAFETY PROTOCOLS - MANDATORY COMPLIANCE

## 🚨 CRITICAL WARNING: ENTERPRISE XML PROCESSING SAFETY

**Sumpter** is an enterprise-grade XML streaming engine designed for processing massive, malformed, and variant-heavy XML files with the capability to handle sensitive business data including financial transactions, compliance logs, and proprietary information. These safety protocols are **MANDATORY**, **NON-NEGOTIABLE**, and must be followed by all team members, AI agents, and contributors.

**⚠️ DATA SENSITIVITY WARNING**: As an XML processing system, Sumpter may handle sensitive content including personal information, business documents, proprietary data, and confidential materials. Security and privacy protection are fundamental requirements.

## 📋 Framework Compliance

**Compliance with:**

- [Fulmen Repository Safety Framework](https://codex.fulmenhq.dev/policies/repository-safety-framework/)
- [Fulmen Agentic Attribution Standard](https://codex.fulmenhq.dev/standards/agentic-attribution-standard/)
- [Fulmen Frontmatter Standard](https://codex.fulmenhq.dev/standards/frontmatter-standard/)

**Repository-specific modifications:**

- Lifecycle-aware quality gates with dynamic coverage thresholds
- XML processing security patterns and data privacy protection
- Professional streaming XML development standards and enterprise patterns

---

## 🔥 OPERATIONAL DANGER CLASSIFICATION

### **Level 1: CATASTROPHIC (Never Execute Without User Confirmation)**

- **Lifecycle Phase Transitions**: Moving between alpha/beta/production phases without validation
- **Production XML Processing**: Processing live business XML files or sensitive data streams
- **Quality Gate Bypasses**: Using `--no-verify` or skipping mandatory quality validation
- **Security Configuration Changes**: Modifying coverage thresholds, security scanning, or authentication
- **External System Integration**: Adding XML processing APIs or third-party data connectors
- **Batch XML Operations**: Processing multiple XML files or complex data pipelines

### **Level 2: HIGH RISK (Validate Before Execution)**

- **Coverage Threshold Changes**: Modifying phase-specific coverage requirements
- **XML Processing Architecture Changes**: Significant changes to streaming algorithms or parsing logic
- **Data Processing Logic**: Changes to extraction, normalization, or transformation algorithms
- **Security Pattern Changes**: Modifications to encoding detection or data sanitization
- **Build System Changes**: Makefile modifications or quality gate adjustments
- **XML Schema Processing**: Automated generation or modification of XML processing schemas

### **Level 3: MEDIUM RISK (Proceed with Caution)**

- **Basic XML Processing Development**: Adding new inspection commands or extending existing functionality
- **Test Coverage Improvements**: Adding tests to meet lifecycle phase requirements
- **Documentation Updates**: Manual updates to user guides and technical documentation
- **Configuration Inspection**: Read-only analysis of current repository state
- **Quality Gate Execution**: Running standard validation and testing procedures

---

## 📋 EXPLICIT AUTHORIZATION PROTOCOL

For any Level 1 or Level 2 operation:

1. **STOP** - Pause and assess the operation completely, considering lifecycle impact
2. **DESCRIBE** - Explain exactly what will be performed, including XML processing implications
3. **CLASSIFY** - Identify risk level and potential impacts on data security and system integrity
4. **ASK** - Request explicit user confirmation with full context of lifecycle and quality implications
5. **WAIT** - Do not proceed until explicit authorization received with full understanding
6. **CONFIRM** - Repeat back what user authorized, including specific scope and limitations
7. **EXECUTE** - Perform only the specific authorized operation within defined boundaries
8. **AUDIT** - Document authorization and execution results with lifecycle compliance notes

## 🛡️ MANDATORY SAFETY PROTOCOLS

### **Protocol 1: LIFECYCLE-AWARE QUALITY GATES**

**All commits must pass lifecycle-appropriate quality standards:**

```bash
# MANDATORY - All must pass without errors for current phase
make check-all                    # Code quality (fmt, vet, lint)
make coverage-check-dynamic       # Phase-specific coverage validation
make security-scan                # Vulnerability scanning (gosec + govulncheck)
make pre-commit                   # Full pre-commit validation for current phase
make build                        # Multi-platform compilation verification

# NEVER bypass without maintainer approval
git commit --no-verify            # ❌ REQUIRES MAINTAINER APPROVAL AND JUSTIFICATION
```

**Lifecycle-Specific Quality Requirements:**

- ✅ **Alpha Phase (Current)**: 50%+ coverage, comprehensive security scanning, professional tooling
- ✅ **Beta Phase**: 70%+ coverage, enhanced testing, community readiness validation
- ✅ **Production Phase**: 80%+ coverage, enterprise deployment validation, monitoring integration
- ✅ **Mature Phase**: 90%+ coverage, optimization focus, operational excellence validation

### **Protocol 2: XML PROCESSING SECURITY**

**Every XML processing operation must follow strict security protocols:**

```bash
# ✅ SAFE - Controlled, validated XML processing
sumpter version                    # Safe command with no data access
sumpter envinfo                    # Environment info with automatic redaction
sumpter completion bash            # Shell completion generation

# ❌ DANGEROUS - Unvalidated XML processing (Future functionality)
# sumpter process --recursive /sensitive/xml/
# sumpter extract --export-data /business/xml/
# sumpter transform --batch /personal/xml/
```

**XML Processing Requirements:**

1. **Content Classification**: Identify sensitive data patterns before processing
2. **Access Validation**: Verify user authorization for XML file access
3. **Privacy Protection**: Implement redaction and anonymization for sensitive XML content
4. **Audit Logging**: Log all XML processing operations with privacy protection
5. **Secure Cleanup**: Ensure temporary files and processed XML content are securely cleaned

### **Protocol 3: DYNAMIC COVERAGE VALIDATION**

**Lifecycle phase-aware coverage enforcement:**

```bash
# Current Phase Validation
cat LIFECYCLE_PHASE                              # Check: alpha
./scripts/validate-coverage-threshold.sh         # Validates 50% for alpha

# Phase Transition Validation
./scripts/validate-coverage-threshold.sh beta    # Validates 70% for beta
./scripts/validate-coverage-threshold.sh production  # Validates 80% for production
```

**Coverage Requirements by Phase:**

- **Alpha**: 50% minimum (current: bootstrap phase)
- **Beta**: 70% minimum (feature-complete validation)
- **Production**: 80% minimum (enterprise deployment ready)
- **Mature**: 90% minimum (operational excellence)

### **Protocol 4: PROFESSIONAL XML PROCESSING BOUNDARIES**

**Strict boundaries for enterprise XML processing development:**

```go
// MANDATORY: Professional XML processing patterns
type SecurityLevel int

const (
    SecurityBasic SecurityLevel = iota      // Basic environment validation
    SecurityEnterprise                      // Enterprise redaction and validation
    SecurityXML                             // XML processing security
    SecurityProduction                      // Production-grade compliance
)

// MANDATORY: Lifecycle-aware development
func ValidateLifecycleCompliance(phase string, coverage float64) error {
    thresholds := map[string]float64{
        "alpha": 50.0,
        "beta": 70.0,
        "production": 80.0,
        "mature": 90.0,
    }

    required, exists := thresholds[phase]
    if !exists {
        return fmt.Errorf("unknown lifecycle phase: %s", phase)
    }

    if coverage < required {
        return fmt.Errorf("coverage %.1f%% below %s threshold %.1f%%",
            coverage, phase, required)
    }

    return nil
}
```

### **Protocol 5: AI AGENT BEHAVIORAL REQUIREMENTS**

**For ANY significant development operation:**

1. **PAUSE** - Never proceed with major changes automatically
2. **CLASSIFY** - Determine lifecycle impact and quality gate requirements
3. **VALIDATE** - Confirm current phase requirements and coverage thresholds
4. **DESCRIBE** - Explain exactly what operation will be performed and why
5. **ASK** - Request explicit user authorization with full context
6. **CONFIRM** - Repeat back what user authorized with specific scope
7. **EXECUTE** - Only the specific authorized operation within lifecycle boundaries
8. **AUDIT** - Ensure all operations maintain lifecycle compliance and quality standards

**Prohibited Behaviors:**

> **Note:** Commit-time approval on feature branches was relaxed in May 2026
> as Sumpter moved from microtool mode to a feature-branch + PR review model.
> See [AGENTS.md § Commit and Push Protocols](AGENTS.md#commit-and-push-protocols)
> for the canonical current policy. The bullets below describe the controls
> that **remain strict** under the new model.

- ❌ **`git push` without explicit, real-time authorization**: Never push to any remote branch without direct, explicit, and immediate permission from a supervisor for that specific push.
- ❌ **Direct commits or pushes to `main` or release branches** without per-occurrence approval, regardless of feature-branch policy.
- ❌ **Bypassing git hooks** (`--no-verify`, `GIT_NO_HOOKS`, force-push) without per-occurrence supervisor approval.
- ❌ **Automatic lifecycle transitions** without explicit validation and approval
- ❌ **Quality gate bypasses** without supervisor approval and justification
- ❌ **Coverage threshold modifications** without architecture review
- ❌ **Security scanning disables** or vulnerability acceptance without review
- ❌ **Bulk XML changes** without individual file validation
- ❌ **Chaining operations** with `&&` or `;` without independent validation
- ❌ **XML processing** without explicit security and privacy validation

**Required Behaviors:**

- ✅ **Explicit authorization** for each significant development operation
- ✅ **Lifecycle compliance** with phase-appropriate quality requirements
- ✅ **Professional standards** following Fulmen ecosystem patterns
- ✅ **Security validation** with comprehensive scanning and protection
- ✅ **Quality assurance** with comprehensive testing and validation
- ✅ **Documentation compliance** with SOPs and enterprise standards

---

## 🎯 SUMPTER-SPECIFIC COMPLIANCE FRAMEWORK

### **Quality Gate Integration**

Dynamic quality enforcement based on lifecycle phase:

```yaml
# Current Configuration (config/coverage-thresholds.yaml)
lifecycle_thresholds:
  alpha: 50 # Current phase - bootstrap phase
  beta: 70 # Next milestone
  production: 80 # Enterprise deployment
  mature: 90 # Operational excellence

# Safety default for unknown phases
default_threshold: 80 # Production-level safety
```

### **Professional Development Standards**

**XML Processing Architecture Requirements:**

- Streaming-first token decoder with constant memory profile (<50MB RSS)
- UTF-8 normalization with BOM detection and legacy encoding support
- Path tracking with dot notation (Envelope.Header.Message)
- Multi-platform builds (Linux, macOS, Windows) with consistent behavior

**Security Requirements:**

- Environment variable redaction for sensitive data protection
- Secure error handling with controlled information disclosure
- Professional logging with structured output and privacy protection
- Vulnerability scanning integration with zero-tolerance policy

**Testing Requirements:**

- Lifecycle-aware coverage with dynamic threshold validation
- Comprehensive XML processing testing with encoding variations
- Professional test organization with table-driven patterns
- Security testing for redaction patterns and malformed XML handling

### **Enterprise Readiness Validation**

**Alpha Phase (Current) Requirements:**

- ✅ Professional XML processing foundation with enterprise patterns
- ✅ 50%+ test coverage with comprehensive validation
- ✅ Zero security vulnerabilities with continuous scanning
- ✅ Professional documentation with SOPs and enterprise standards
- ✅ Multi-platform build capability with consistent behavior

**Beta Phase (Next) Requirements:**

- 70%+ test coverage with enhanced validation
- Feature-complete XML inspection capabilities
- Community readiness with comprehensive user documentation
- Enhanced security patterns for XML content handling
- Performance benchmarks and optimization validation

**Production Phase Requirements:**

- 80%+ test coverage with enterprise validation standards
- Full XML processing security with privacy protection
- Enterprise deployment validation with monitoring integration
- Professional support documentation with troubleshooting guides
- Operational excellence with comprehensive incident response

---

## 🚨 EMERGENCY PROCEDURES

### **Security Incident Response**

**Level 1: CRITICAL Security Incident**

```bash
# Immediate Actions (within 5 minutes)
git stash                         # Secure work in progress
make security-scan --full         # Complete security assessment
./scripts/validate-coverage-threshold.sh  # Validate quality compliance

# Notification (within 15 minutes)
# 1. Notify @3leapsdave with incident details and scope
# 2. Document potential impact on XML processing security
# 3. Preserve evidence for security analysis and remediation
# 4. Begin containment and remediation procedures
```

**Level 2: HIGH Risk Development Event**

```bash
# Assessment Actions
make check-all                    # Full quality validation
make pre-push                     # Comprehensive testing
cat LIFECYCLE_PHASE               # Validate phase compliance

# Remediation with supervisor approval and documentation
```

### **Quality Gate Bypass Protocol**

Emergency bypass procedures with mandatory documentation:

```bash
# EMERGENCY ONLY - Requires supervisor approval
export EMERGENCY_JUSTIFICATION="Critical XML processing security patch for encoding vulnerability"
export SUPERVISOR_APPROVAL="@3leapsdave"
export FOLLOW_UP_COMMITMENT="Complete quality gate remediation within 24 hours"

git commit --no-verify -m "emergency: critical XML security patch

EMERGENCY BYPASS: Quality gates skipped
Supervisor: ${SUPERVISOR_APPROVAL}
Justification: ${EMERGENCY_JUSTIFICATION}
Follow-up: ${FOLLOW_UP_COMMITMENT}
Formatting: Applied (make fmt + make fmt-docs)

Emergency patch for XML processing security vulnerability"
```

---

## 📊 SUCCESS METRICS & MONITORING

### **Lifecycle Metrics (Continuously Monitored)**

- **Coverage Compliance**: Current phase requirements met (Alpha: 50%+ achieved)
- **Security Compliance**: Zero vulnerabilities in all scans
- **Quality Gates**: 100% compliance rate for all commits
- **Professional Standards**: Full Fulmen ecosystem compliance
- **Documentation**: Complete SOPs and enterprise-grade user documentation

### **Development Metrics (Daily Tracking)**

- **Test Coverage**: Phase-appropriate with automated validation
- **Security Scanning**: Zero HIGH/CRITICAL vulnerabilities
- **Code Quality**: Zero linting issues with comprehensive rule enforcement
- **Build Success**: Multi-platform builds with consistent behavior
- **Professional Tooling**: Complete automation with quality gate enforcement

### **Enterprise Readiness (Monthly Reporting)**

- **Lifecycle Compliance**: Phase requirements and transition readiness
- **Security Posture**: Vulnerability management and threat mitigation
- **Quality Assurance**: Testing effectiveness and coverage validation
- **Documentation**: User experience and support documentation completeness
- **Professional Standards**: Fulmen ecosystem compliance and enterprise patterns

---

## 📋 MANDATORY COMPLIANCE ACKNOWLEDGMENT

**All team members and AI agents must explicitly confirm:**

✅ I have read and understand the Sumpter Repository Safety Protocols
✅ I understand the lifecycle phase requirements and dynamic coverage validation
✅ I will follow professional XML processing standards and enterprise patterns
✅ I will request explicit user authorization for all significant operations
✅ I will never bypass quality gates without supervisor approval and justification
✅ I will follow commit quality gates and comprehensive security scanning
✅ I understand that XML processing requires strict security and privacy protection
✅ I will follow incident response procedures for any security or quality events
✅ I will maintain confidentiality and security of all processed XML content
✅ I understand that safety protocols are mandatory and non-negotiable for enterprise development

**Digital Signature**: **\*\*\*\***\_**\*\*\*\*** **Date**: **\*\*\*\***\_**\*\*\*\***

---

**⚡ SUMPTER POWER = SUMPTER RESPONSIBILITY**

**Remember**: Every Sumpter operation can either:

- 🚀 **Enable professional, secure XML processing with enterprise-grade streaming performance**
- 💥 **Compromise XML data security, violate quality standards, and damage professional reputation**

**The difference is FOLLOWING THESE MANDATORY SAFETY PROTOCOLS.**

---

**Maintained by**: `secrev` (primary), `releng`, `devlead` — see [MAINTAINERS.md](MAINTAINERS.md)
**Document Version**: 1.0
**Last Updated**: 2026-05-11
**Next Review**: 2026-08-11
**Approval**: @3leapsdave (David Thompson)
**Based On**: [Fulmen Repository Safety Framework](https://codex.fulmenhq.dev/policies/repository-safety-framework/)

_"Stream XML with Professional Excellence ⚡ - Secure Everything. Process Everything. Protect Everything."_
