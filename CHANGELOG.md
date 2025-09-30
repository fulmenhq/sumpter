# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog and this project adheres to Semantic Versioning.

## [Unreleased]

### Added

- Guardian browser-based approval system for commits and pushes
- Extract command with XPath-based XML processing
- String transform registry for field transformations (trim, upper, lower, title, replace, blind_string)
- Validation DSL for extract recipes with accumulations, aggregations, and quality checks
- Retrieve command with SEC EDGAR support
- Dialect support for file signatures
- License compliance checking
- Enhanced configuration system and core infrastructure
- Infrastructure tooling and quality gates improvements

### Changed

- Refactored dialect registry to YAML schema format
- Enhanced inspect functionality with logging and progress
- Updated golangci-lint configuration
- Improved extract schema with validation metadata support
- Migrated to YAML-first schema approach (removed redundant JSON schemas)
- Refactored extract config preparation with once-initialization pattern

### Fixed

- Resolved errcheck linting violations in tests and output functions
- Corrected golangci-lint v2 configuration
- Fixed deprecated strings.Title usage in transform registry
- Removed debug print statements from extractor

### Docs

- Added schema-first and logging SOPs + ADRs
- Embedded architecture documentation
- Clarified commit and push approval requirements
- Updated embed-manifest and synced documentation

## [0.1.0] - 2025-09-18

### Added

- Initial release: Sumpter v0.1.0 bootstrap
- XML inspection foundations with dialect registry and SEC EDGAR dialect
- Logging component with console/JSON output and PII redaction
- Makefile quality gates; schema validation via goneat; coverage scripts
- Embedded docs and schemas; CLI commands: version, envinfo, inspect, docs

### Fixed

- Errcheck issues in envinfo

### Docs

- SOPs, ADRs, and user guides embedded
