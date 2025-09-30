# Docs Command

Access embedded documentation for Sumpter.

## Usage

```bash
sumpter docs [command]
```

## Description

The `docs` command provides access to Sumpter's comprehensive embedded documentation system. All documentation is bundled with the binary, eliminating the need for external documentation access.

## Commands

### List Command

```bash
sumpter docs list
```

Lists all available documentation files organized by category.

### Show Command

```bash
sumpter docs show <path>
```

Displays the content of a specific documentation file.

## Examples

### List All Documentation

```bash
sumpter docs list
```

Output:

```
Sumpter Embedded Documentation
==============================

📚 root:
  ├── sumpter_overview.md
  ├── output_rendering.md

📚 architecture:
  ├── adr/0001-schema-first-outputs.md
  ├── adr/0002-logging-stderr-json-pretty.md

📚 standards:
  ├── agentic-attribution.md
  ├── application-environment.md
  ├── lifecycle-maturity.md

📚 sop:
  ├── lifecycle-phase-acceptance-criteria.md
  ├── logging-sop.md
  ├── repository-operations-sop.md
  ├── schema-first-sop.md

📚 user-guide:
  ├── commands/envinfo.md
  ├── commands/inspect.md
  ├── commands/version.md
  ├── inspect-workflow.md

💡 Tip: Use 'sumpter docs show <path>' to view specific documentation
   Example: sumpter docs show standards/application-environment
```

### Show Specific Documentation

```bash
sumpter docs show standards/application-environment
```

### Show Command Documentation

```bash
sumpter docs show user-guide/commands/inspect
```

### Show SOP Documentation

```bash
sumpter docs show sop/repository-operations-sop
```

## Documentation Categories

### Root Level

- **sumpter_overview.md**: High-level overview of Sumpter's capabilities
- **output_rendering.md**: Information about output formats and rendering

### Architecture (architecture/)

- **adr/**: Architecture Decision Records explaining design choices
  - `0001-schema-first-outputs.md`: Schema-driven output design
  - `0002-logging-stderr-json-pretty.md`: Logging architecture decisions

### Standards (standards/)

- **agentic-attribution.md**: AI agent attribution standards
- **application-environment.md**: XDG-compliant directory structure
- **lifecycle-maturity.md**: Development lifecycle framework

### SOPs (sop/)

- **lifecycle-phase-acceptance-criteria.md**: Phase transition requirements
- **logging-sop.md**: Logging standard operating procedures
- **repository-operations-sop.md**: Git workflow and commit standards
- **schema-first-sop.md**: Schema development procedures

### User Guide (user-guide/)

- **commands/**: Individual command documentation
  - `envinfo.md`: Environment information command
  - `inspect.md`: XML inspection command
  - `version.md`: Version information command
  - `docs.md`: This documentation command
  - `validate.md`: Configuration validation command
- **inspect-workflow.md**: XML inspection and dialect detection workflow

## Path Resolution

The `show` command supports flexible path resolution:

### Direct Paths

```bash
sumpter docs show user-guide/commands/inspect.md
sumpter docs show standards/application-environment.md
```

### Short Paths (without .md extension)

```bash
sumpter docs show user-guide/commands/inspect
sumpter docs show standards/application-environment
```

### Automatic docs/ Prefix Addition

If a path doesn't exist directly, the command automatically tries with a `docs/` prefix:

```bash
sumpter docs show user-guide/commands/inspect
# Tries: user-guide/commands/inspect
# Then:  docs/user-guide/commands/inspect
```

## Use Cases

### Quick Reference

- **Command Help**: Get detailed usage information for any command
- **Configuration**: Understand configuration options and file locations
- **Standards**: Review development and operational standards
- **Architecture**: Learn about design decisions and rationale

### Offline Documentation

- **No Internet Required**: All documentation is embedded in the binary
- **Consistent**: Documentation version matches binary version
- **Portable**: Works on any system where Sumpter runs

### Development Support

- **SOP Compliance**: Follow standard operating procedures
- **Best Practices**: Learn recommended workflows and patterns
- **Troubleshooting**: Access diagnostic and debugging information

### Learning and Training

- **New User Onboarding**: Comprehensive introduction to Sumpter
- **Feature Discovery**: Learn about capabilities and use cases
- **Advanced Usage**: Deep dive into complex features

## Error Handling

### File Not Found

```bash
Documentation for 'nonexistent' not found in embedded docs.

Available documentation:
  - standards/ (development standards)
  - sop/ (standard operating procedures)
  - user-guide/ (command documentation)
  - examples/ (sample files)

Use 'sumpter docs list' to see all available files.
```

### Invalid Path

```bash
Error accessing embedded docs: file does not exist
```

## Integration with Help System

The docs command complements Sumpter's built-in help system:

```bash
# Built-in help (brief)
sumpter inspect --help

# Detailed documentation
sumpter docs show user-guide/commands/inspect
```

## Notes

- All documentation is embedded at build time using the `embed-assets.sh` script
- Documentation is versioned with the binary (no external dependencies)
- Uses the `internal/assets` package for embedded file access
- Supports both `.md` extension and extension-less paths
- Automatic path resolution for user convenience
