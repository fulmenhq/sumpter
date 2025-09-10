# Sumpter Envinfo JSON Schemas

This directory contains JSON Schema definitions for the output formats of the `sumpter envinfo` command and its subcommands.

## Schema Files

### Core Schemas

- **`system.schema.json`** - System information (OS, architecture, Go version, CPU cores, hostname, working directory, timestamp)
- **`paths.schema.json`** - Application directory paths (home, workdir, cache, logs, configs, temp)
- **`vars.schema.json`** - Environment variables (with PII protection)
- **`xml.schema.json`** - XML processing capabilities (encodings, memory targets, supported outputs)
- **`network.schema.json`** - Network interface information and external IP

### Composite Schemas

- **`complete.schema.json`** - Complete output from main `sumpter envinfo` command (includes all sections)

## Usage

These schemas are used to:

1. **Validate JSON output** from envinfo commands
2. **Document expected structure** for AI developers and tools
3. **Ensure consistency** across different output formats
4. **Support programmatic consumption** of envinfo data

## Command Mapping

| Command                   | Schema                 | Description                |
| ------------------------- | ---------------------- | -------------------------- |
| `sumpter envinfo system`  | `system.schema.json`   | System information only    |
| `sumpter envinfo paths`   | `paths.schema.json`    | Application paths only     |
| `sumpter envinfo vars`    | `vars.schema.json`     | Environment variables only |
| `sumpter envinfo xml`     | `xml.schema.json`      | XML capabilities only      |
| `sumpter envinfo network` | `network.schema.json`  | Network information only   |
| `sumpter envinfo`         | `complete.schema.json` | All information combined   |

## JSON Output Examples

All subcommands support `--json` flag for structured output:

```bash
# Get system info as JSON
sumpter envinfo system --json

# Get application paths as JSON
sumpter envinfo paths --json

# Get all info as JSON
sumpter envinfo --json
```

## Schema Versioning

Schemas follow semantic versioning:

- **Major version**: Breaking changes to structure
- **Minor version**: New optional fields or backward-compatible changes
- **Patch version**: Bug fixes, documentation updates

Current version: `v0.1.0`

## Validation

You can validate envinfo JSON output against these schemas using any JSON Schema validator:

```bash
# Example using a JSON Schema validator
sumpter envinfo system --json | jq . | jsonschema -i /dev/stdin schemas/envinfo/v0.1.0/system.schema.json
```
