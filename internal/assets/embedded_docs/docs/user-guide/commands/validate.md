# Validate Command

Validate Sumpter configuration files against their JSON schemas.

## Usage

```bash
sumpter validate [config-file] [flags]
sumpter validate [flags]
sumpter validate artifact-descriptor <descriptor.json> --contract-base <dir>
```

## Description

The `validate` command validates Sumpter configuration files to ensure they conform to the expected schema structure and values. It supports validation of main configuration, logger configuration, and PII configuration files.

It also validates portable data artifact descriptors against an explicit local
contract base. The artifact descriptor path uses the host-less capability token
`contract: data-artifact/v0`; `--contract-base` points at a trusted local
directory containing `contract.json` and the relative entry schema named by
that manifest.

## Parameters

- `config-file`: Path to specific configuration file to validate (optional)

## Flags

- `--dir`, `-d`: Directory containing config files to validate
- `--json`, `-j`: Output results in JSON format

### Artifact Descriptor Flags

- `--contract-base`: Local directory containing `contract.json` and the entry schema
- `--json`: Output descriptor validation results in JSON format

## Examples

### Validate All Default Configurations

```bash
sumpter validate
```

Output:

```
Configuration Validation Results
==============================

Total files: 2
Valid files: 2
Invalid files: 0
Total errors: 0

File: /home/user/.config/sumpter/sumpter.yaml
Status: ✅ Valid

File: /home/user/.config/sumpter/logger.yaml
Status: ✅ Valid

🎉 All configuration files are valid!
```

### Validate Specific Configuration File

```bash
sumpter validate /path/to/custom-config.yaml
```

### Validate Configuration Directory

```bash
sumpter validate --dir ./configs
```

### JSON Output Format

```bash
sumpter validate --json
```

Output:

```json
{
  "files": {
    "/home/user/.config/sumpter/sumpter.yaml": {
      "is_valid": true,
      "errors": []
    },
    "/home/user/.config/sumpter/logger.yaml": {
      "is_valid": true,
      "errors": []
    }
  },
  "summary": {
    "total_files": 2,
    "valid_files": 2,
    "invalid_files": 0,
    "total_errors": 0
  }
}
```

### Validate a Data Artifact Descriptor

```bash
sumpter validate artifact-descriptor descriptor.json \
  --contract-base tests/fixtures/data-artifact-contract/v0
```

Output:

```
Artifact descriptor: descriptor.json
Contract: contract: data-artifact/v0 (sha256:ef6de3f16bd988555a4e063d32e4f15478b56bf6f45121e890d89504ee468a01)
Status: valid
```

Use `--json` to include the resolved contract baseline, released tag, entry
schema, and validation result in machine-readable form.

### Data Artifact Contract Baseline Hash

The pinned `contract_baseline.resolved_bundle_sha256` is reproducible from the
local contract base. Sumpter hashes the contract bundle with SHA-256 using this
exact file order and logical path prefix:

1. `schemas/data-artifact/v0/contract.json`
2. `schemas/data-artifact/v0/artifact-descriptor.schema.json`

For each file, the hash input appends:

```text
logicalPath \x00 fileBytes \x00
```

The resulting digest for the Crucible `v0.1.18` baseline is:

```text
sha256:ef6de3f16bd988555a4e063d32e4f15478b56bf6f45121e890d89504ee468a01
```

## Configuration Files

### Main Configuration (sumpter.yaml)

Located at: `$XDG_CONFIG_HOME/sumpter/sumpter.yaml`

Validates:

- Version compatibility
- Logging configuration structure
- PII configuration structure
- Paths configuration
- Performance settings
- Telemetry configuration

### Logger Configuration (logger.yaml)

Located at: `$XDG_CONFIG_HOME/sumpter/logger.yaml`

Validates:

- Logger version
- Log level settings
- Output format configuration
- File logging settings
- Rotation configuration

### PII Configuration (pii.yaml)

Located at: `$XDG_CONFIG_HOME/sumpter/pii.yaml`

Validates:

- PII version
- Mode settings (safe/unsafe)
- Safe-only configuration
- Redaction patterns

## Validation Process

### Schema Loading

1. **YAML-first Approach**: Attempts to load YAML schema first
2. **JSON Fallback**: Falls back to JSON schema for backward compatibility
3. **Embedded Schemas**: Uses embedded schemas from `internal/assets`

### Validation Steps

1. **File Discovery**: Locates configuration files in standard locations
2. **Schema Resolution**: Matches configuration type to appropriate schema
3. **Structure Validation**: Validates against JSON schema constraints
4. **Type Checking**: Ensures correct data types for all fields
5. **Required Fields**: Verifies presence of mandatory configuration fields

### Error Reporting

- **Detailed Errors**: Specific field and constraint violations
- **Line Numbers**: Location information for YAML/JSON parsing errors
- **Path Context**: JSONPath-style error locations
- **Multiple Errors**: Reports all validation issues, not just the first

## Error Examples

### Schema Violation

```bash
Configuration Validation Results
==============================

Total files: 1
Valid files: 0
Invalid files: 1
Total errors: 2

File: /home/user/.config/sumpter/sumpter.yaml
Status: ❌ Invalid (2 errors)
  1. /logging/level: must be one of [debug, info, warn, error] (line 15)
  2. /performance/maxMemoryMB: must be less than or equal to 1024 (line 25)

validation failed with 2 errors across 1 files
```

### Missing Required Field

```bash
File: /home/user/.config/sumpter/logger.yaml
Status: ❌ Invalid (1 errors)
  1. /version: field is required (line 1)
```

### Type Mismatch

```bash
File: /home/user/.config/sumpter/pii.yaml
Status: ❌ Invalid (1 errors)
  1. /safeOnly: expected boolean, got string (line 8)
```

## Use Cases

### Configuration Setup

- **Initial Setup**: Validate configuration after initial setup
- **Migration**: Check configuration compatibility after updates
- **Template Validation**: Verify configuration templates

### CI/CD Integration

- **Automated Validation**: Include in CI pipelines
- **Deployment Checks**: Validate before deployment
- **Configuration Drift**: Detect configuration changes

### Troubleshooting

- **Configuration Issues**: Diagnose configuration-related problems
- **Schema Updates**: Verify compatibility with new schema versions
- **Debugging**: Identify configuration syntax errors

### Development

- **Schema Testing**: Test schema changes against sample configurations
- **Documentation**: Ensure configuration examples are valid
- **Code Changes**: Validate impact of configuration structure changes

## Schema Locations

### Embedded Schemas

- `schemas/config/v0.1.0/sumpter-config.schema.json`
- `schemas/config/v0.1.0/logger-config.schema.json`
- `schemas/config/v0.1.0/pii-config.schema.json`

### Schema Versions

- **v0.1.0**: Current schema version for all configuration types
- **Backward Compatibility**: Supports older configuration formats
- **Version Validation**: Ensures configuration version matches schema

## Integration with Configuration System

### Automatic Discovery

The validate command integrates with Sumpter's configuration system:

```bash
# Uses same path resolution as main application
sumpter validate  # Validates default locations
```

### Configuration Loader

- **Shared Logic**: Uses same configuration loader as main application
- **Path Resolution**: Follows XDG Base Directory specification
- **Environment Variables**: Respects SUMPTER\_\* environment variables

## Best Practices

### Regular Validation

```bash
# Add to development workflow
make validate-config
sumpter validate
```

### CI/CD Integration

```yaml
# .github/workflows/validate.yml
- name: Validate Configuration
  run: |
    sumpter validate --json | jq '.summary.valid_files == .summary.total_files'
```

### Error Handling

```bash
# Check validation status in scripts
if ! sumpter validate >/dev/null 2>&1; then
    echo "Configuration validation failed"
    sumpter validate  # Show detailed errors
    exit 1
fi
```

## Notes

- Uses `goneat` library for JSON schema validation
- Supports both YAML and JSON configuration formats
- Embedded schemas ensure validation works offline
- Detailed error messages help with configuration debugging
- JSON output format enables programmatic validation checking
