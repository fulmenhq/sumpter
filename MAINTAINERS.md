# Sumpter Project Maintainers

**Project**: sumpter
**Repository**: github.com/fulmenhq/sumpter
**Governance**: Fulmen Ecosystem Standards
**Last Updated**: May 10, 2026

## Project Identity

**Sumpter** ⚡ (XML Streaming Engine) is a high-performance, Go-based streaming XML engine that transforms massive, malformed, and variant-heavy XML into clean, analytics-ready tables. With sub-second inspection, auto-generated extraction configs, and resilient outputs to Parquet, DuckDB, or NDJSON, Sumpter helps teams **start fast and thrive on scale**. Built for enterprises where XML still runs the world, Sumpter makes the messy manageable — with speed, safety, and clarity.

## Human Maintainer

### @3leapsdave - Technical Supervisor

- **Name**: David Thompson
- **Role**: Technical Lead & Project Supervisor
- **Email**: dave.thompson@3leaps.net
- **Responsibilities**: Strategic direction, architecture decisions, lifecycle progression oversight
- **Authority**: Final approval on design decisions, quality standards, and lifecycle transitions

## Role-Based Agent Model

Sumpter uses the FulmenHQ/Lanyte role and team model rather than persistent
named agent personas. Agents identify themselves per session from:

| Variable             | Purpose                                                                      |
| -------------------- | ---------------------------------------------------------------------------- |
| `LANYTE_AGENT_ROLE`  | Active role slug, such as `india-devlead`, `india-devrev`, or `india-releng` |
| `LANYTE_AGENT_SCOPE` | Org scope, normally `fulmenhq`                                               |
| `LANYTE_AGENT_TEAM`  | NATO team assignment, normally `india` for Sumpter                           |

Coordination happens in the FulmenHQ Mattermost workspace. Use the org-root
[`../AGENTS.md`](../AGENTS.md) warmup for channel selection, checkpointing, and
team context.

### Active Role Families

| Role family | Capabilities                                                           | Constraints                                                 |
| ----------- | ---------------------------------------------------------------------- | ----------------------------------------------------------- |
| `devlead`   | Implementation, CLI behavior, XML/extract/index features               | Human approval before commits and pushes                    |
| `devrev`    | Code review, bug finding, regression analysis, quality-gate assessment | Review stance; do not rewrite author work unless asked      |
| `qa`        | Test design, coverage, fixtures, lifecycle validation                  | Coordinate with devlead for feature changes                 |
| `secrev`    | XML/data security, redaction, dependencies, vulnerability findings     | Escalate sensitive data or credential concerns immediately  |
| `releng`    | Release sequencing, CI/CD, tags, packaging                             | Tags, releases, pushes require explicit approval            |
| `entarch`   | Cross-repo architecture, schemas, ecosystem alignment                  | Major architecture/config schema changes require review     |
| `dataeng`   | Extraction workflows, analytics outputs, data-pipeline ergonomics      | New integrations and data pipelines require scoped approval |

### Agent Operational Guidelines

**Authorization Protocol**: Agents must seek explicit permission before:

- Lifecycle phase transitions (alpha → beta → production)
- Major architectural modifications
- Security protocol changes
- Quality gate threshold modifications
- External dependency additions
- Data pipeline architecture changes

**Quality Requirements**:

- Follow Fulmen ecosystem development standards
- Implement lifecycle-aware coverage requirements
- Maintain professional XML processing patterns
- Zero-tolerance for security violations
- Enterprise-grade documentation and SOPs

## Technical Leadership Structure

### Architecture Council

- **Lead**: @3leapsdave
- **Primary agent roles**: `entarch`, `devlead`
- **Analytics/data roles**: `dataeng`, `devlead`
- **Focus**: Enterprise architecture, XML streaming algorithms, data pipelines, analytics integration

### Quality Assurance Board

- **Lead**: @3leapsdave
- **Primary agent roles**: `qa`, `devrev`, `releng`
- **Focus**: Lifecycle-aware quality gates, dynamic coverage validation, professional standards compliance
- **Standards**: Alpha: 50%+ coverage, Beta: 70%+ coverage, Production: 80%+ coverage

### Security Review

- **Lead**: @3leapsdave
- **Primary agent roles**: `secrev`, `devrev`
- **Integration roles**: `devlead`, `dataeng`
- **Focus**: Security-first XML processing, vulnerability management, data pipeline security, environment variable redaction
- **Standards**: Zero-tolerance security violations, gosec + govulncheck compliance

### Data Engineering Council

- **Lead roles**: `dataeng`, `devlead`
- **Processing**: XML streaming algorithms, data pipeline design, output optimization
- **Integration roles**: `entarch`, `releng`
- **Focus**: Data engineering excellence, pipeline optimization, security-first data handling

## Project Scope & Responsibilities

### Core Domains

**XML Processing Engine** 🔧

- Streaming token decoder with constant memory profiles (<50MB RSS)
- UTF-8 normalization with BOM detection and legacy encoding support
- Path tracking with dot notation (Envelope.Header.Message)
- Multi-platform builds (Linux, macOS, Windows) with consistent behavior

**Data Pipeline Architecture** 📊

- XML inspection and structure discovery
- Auto-generated extraction configurations
- Analytics-ready outputs (NDJSON, Parquet, DuckDB)
- Security-first data processing and privacy protection

**Lifecycle Management** 📈

- Phase-aware development (Alpha → Beta → Production → Mature)
- Dynamic coverage gating based on lifecycle phase
- Professional transition procedures and acceptance criteria
- Enterprise readiness validation and compliance

**Professional Tooling** ⚙️

- Comprehensive Makefile with enterprise-grade automation
- Security scanning integration (gosec + govulncheck)
- Quality gates with golangci-lint and comprehensive testing
- Professional documentation with SOPs and standards

### Repository Management

**Code Quality Standards**:

- Go 1.25+ with enterprise patterns
- Lifecycle-aware coverage requirements
- Security-first XML processing with redaction patterns
- Comprehensive error handling and logging

**Testing Requirements**:

- Phase-appropriate coverage thresholds (Alpha: 50%+, Beta: 70%+, Production: 80%+)
- Dynamic coverage validation with automated scripts
- Security vulnerability scanning with zero-tolerance policy
- Professional XML processing testing with comprehensive validation

**Documentation Standards**:

- Comprehensive SOPs and lifecycle documentation
- Enterprise-grade README with professional presentation
- Complete user guides and troubleshooting documentation
- Fulmen ecosystem compliance documentation

## Lifecycle Phase Management

### Current Phase: Alpha

- **Coverage Requirement**: 50% minimum (bootstrap phase)
- **Quality Focus**: Core XML processing engine and professional tooling
- **Development Status**: Enterprise-grade foundation with security-first XML processing
- **Next Milestone**: Feature completion for Beta transition (70%+ coverage)

### Phase Transition Authority

- **Alpha → Beta**: Technical Lead approval with feature completion validation
- **Beta → Production**: Architecture Review Board with enterprise deployment validation
- **Production → Mature**: Stability metrics and operational excellence validation
- **Emergency Procedures**: Documented rollback and incident response protocols

## Communication Protocols

### Issue Management

- **Lifecycle Issues**: GitHub issues with lifecycle phase impact assessment
- **Feature Requests**: Professional RFC template with enterprise consideration
- **Quality Issues**: Detailed reproduction with coverage and security impact analysis
- **Security Issues**: Private disclosure to @3leapsdave with immediate attention

### Development Process

- **All Changes**: Must pass lifecycle-appropriate quality gates
- **Coverage Changes**: Require validation against phase-specific thresholds
- **Architecture Changes**: Require architecture council review
- **Security Changes**: Require security review board approval
- **AI Contributions**: Role-based contributions under `LANYTE_AGENT_ROLE` with @3leapsdave oversight

### Release Management

- **Version Strategy**: Semantic versioning with lifecycle phase alignment
- **Release Notes**: Comprehensive changelog with security and lifecycle notes
- **Quality Gates**: Automated CI/CD with phase-appropriate validation
- **Professional Standards**: Enterprise-grade release procedures and documentation

## Standards Compliance

### Fulmen Ecosystem Standards

- **Agentic Attribution**: Proper AI agent contribution attribution
- **Repository Safety**: Comprehensive safety protocols and procedures
- **Frontmatter Standards**: Consistent documentation metadata
- **Professional Patterns**: Enterprise-grade development and deployment

### Sumpter-Specific Standards

- **Lifecycle Management**: Phase-aware development with dynamic quality gates
- **Coverage Validation**: Automated threshold enforcement based on lifecycle phase
- **XML Processing**: Enterprise-grade streaming with security-first patterns
- **Data Pipeline**: Professional data engineering with analytics-ready outputs

### Go-Specific Standards

- **Formatting**: gofmt + goimports compliance with professional presentation
- **Linting**: golangci-lint with 20+ enabled linters and security focus
- **Testing**: Comprehensive test coverage with race detection and security validation
- **Documentation**: Complete GoDoc with security considerations and usage examples

## Contributing Guidelines

### New Contributors

1. **Read**: Repository safety protocols and lifecycle documentation
2. **Study**: Sumpter XML processing architecture and data pipeline patterns
3. **Follow**: Fulmen ecosystem development standards
4. **Verify**: All contributions pass lifecycle-appropriate quality gates
5. **Document**: Include lifecycle impact assessment and security considerations

### AI Agent Contributions

1. **Authorization**: Explicit approval required for lifecycle-sensitive work
2. **Standards**: Follow Fulmen ecosystem patterns with lifecycle awareness
3. **Testing**: Comprehensive coverage meeting phase-specific requirements
4. **Documentation**: Professional technical documentation with enterprise patterns
5. **Review**: devrev/qa review with @3leapsdave oversight and approval

---

**Contact**: For maintainer inquiries, reach out to @3leapsdave or open a GitHub issue with the "maintainer-question" label.

**Governance**: This project operates under Fulmen Ecosystem Standards with lifecycle-aware development and enterprise-grade quality assurance.
