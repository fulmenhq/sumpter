# Changelog

All notable changes to this project will be documented in this file.

The format is based on Keep a Changelog and this project adheres to Semantic Versioning.

## [Unreleased]

### Changed

- `match_selectors[].min_occurrences` now defaults to `0`; non-zero floors are
  explicit opt-in enforcement.
- Successful zero-record extract runs now write empty output artifacts and
  manifest entries instead of omitting payload files.

### Fixed

- Enforced per-selector `min_occurrences` floors in extract runs so aggregate
  record counts cannot mask a selector-specific violation.

## [0.1.2] - TBD

### Added

- **Seekable-Zstd Compressed Index Store** - 10-20x smaller indexes with parallel random access
  - Two-file format: `*.recordindex.header.json` (metadata) + `*.recordindex.records.szst` (compressed records)
  - `--emit-szst` flag for `index build` command
  - Automatic format detection in all index commands
  - CGO bindings with pre-built libraries for Linux glibc and musl
- **Format-Agnostic Index CLI** - All index commands support both JSON and seekable-zstd formats
  - `index stream`: Stream-walk indexes without loading into memory
  - `index verify`: Verify index integrity against source XML
  - `index build`: Generate indexes in JSON, seekable-zstd, or both formats
- **CI/CD for Cross-Platform Builds** - GitHub Actions workflow validates CGO linking
  - Linux glibc (Debian Bookworm) validation
  - Linux musl (Alpine) validation

### Changed

- Parallel extraction orchestrator now streams records on-demand (constant memory)
- Index commands auto-detect format by file extension
- Build tooling improvements: renamed `pre-commit`/`pre-push` to `precommit`/`prepush`

### Fixed

- Yamllint issues in CI workflow files
- Routine security improvements from audit findings

### Docs

- Comprehensive index workflow guide with seekable-zstd documentation
- Container deployment patterns for CGO builds
- v0.1.2 release notes with migration guide

## [0.1.1] - 2025-10-13

### Added

- **Hybrid Streaming XML Architecture** (ADR-0005): Constant-memory XML processing for 50GB+ files
  - RecordScanner with token-by-token streaming and XPath-based record selection
  - Automatic streaming mode for files >100MB with `--allow-large-files` flag
  - 99.95% memory reduction: 111GB → 50MB RSS for 50GB XML files
  - Transparent .gz decompression support in streaming mode
- Recipe system with manifest-based workflows (`recipes` command)
  - `recipes init`: Scaffold new recipe workspaces with templates
  - `recipes run extract`: Execute extract recipes with manifest defaults
  - `recipes retrieve`: Acquire data from APIs and file systems (SEC EDGAR support)
  - Manifest validation with JSON Schema 2020-12
- Comprehensive test suite achieving 50% coverage (alpha phase gate)
  - Streaming package: 83.9% coverage (exceeds production 80% threshold)
  - Transforms: 91.4% coverage
  - Recipes: 86.7% coverage
  - DSL validation: 68.0% coverage
- New test files: transforms, recipes manifest, regulatory scraper, validate/retrieve commands, doctor/envinfo
- Doctor command for environment setup and diagnostics
  - Interactive SEC EDGAR configuration wizard with compliance validation
  - Environment health checks and setup script generation
- Retrieve command with validation helpers and path security

### Changed

- Extract command automatically uses streaming for large files when `--allow-large-files` is set
- Enhanced extract record-match schema with streaming mode documentation
- Recipe manifest schema with defaults, input/output configuration, and asset references

### Fixed

- Recipe subcommand test expectations (init/retrieve/run vs list/show/run)
- Validate command test compilation errors

### Docs

- ADR-0005: Hybrid Streaming XML Architecture with performance benchmarks
- Recipe system documentation and workflow examples
- Release notes for v0.1.1 with streaming architecture details

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
