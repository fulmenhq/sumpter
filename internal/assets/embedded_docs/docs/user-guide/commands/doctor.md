# Doctor Command

Environment setup and diagnostic tools for Sumpter configuration.

## Usage

```bash
sumpter doctor [command] [flags]
```

## Description

The `doctor` command provides interactive tools for setting up and diagnosing your Sumpter environment. It helps users configure environment variables, set up configuration files, and troubleshoot common issues in an OS-neutral way.

## Subcommands

### setup

Set up Sumpter environment variables and directories.

```bash
sumpter doctor setup [flags]
```

**Description**: Interactive setup wizard for configuring SUMPTER_HOME and related environment variables.

**Flags**:

- `--home`: Custom SUMPTER_HOME directory (detect automatically if not specified)
- `--generate-script`: Generate setup script instead of interactive setup
- `--shell`: Shell type for script generation (bash, zsh, fish, powershell)
- `--dry-run`: Show what would be done without making changes

**Examples**:

Generate setup script for bash:

```bash
sumpter doctor setup --generate-script --shell bash
```

Interactive setup with custom home:

```bash
sumpter doctor setup --home /custom/sumpter/path
```

### check

Check Sumpter environment and configuration.

```bash
sumpter doctor check
```

**Description**: Run diagnostic checks on your Sumpter installation and environment.

**Checks Performed**:

- Environment variables (SUMPTER_HOME, SUMPTER_WORKDIR)
- Directory paths and permissions
- Configuration file validity
- Basic functionality verification

### config

Configuration file setup and management.

```bash
sumpter doctor config [subcommand] [flags]
```

**Description**: Interactive setup and management of Sumpter configuration files.

#### config list

List available configuration templates.

```bash
sumpter doctor config list
```

**Description**: Show all available configuration templates that can be set up interactively.

#### config setup

Interactive setup for configuration files.

```bash
sumpter doctor config setup <target> [flags]
```

**Description**: Guided setup wizard for specific configuration files.

**Supported Targets**:

- `retrieve-sec-edgar`: SEC EDGAR data retrieval configuration

**Flags**:

- `--output`: Custom output path for the config file

**Examples**:

Set up SEC EDGAR retrieve configuration:

```bash
sumpter doctor config setup retrieve-sec-edgar
```

Set up with custom output path:

```bash
sumpter doctor config setup retrieve-sec-edgar --output /custom/path/retrieve.yaml
```

#### config validate

Validate existing configuration files.

```bash
sumpter doctor config validate <target>
```

**Description**: Check that existing configuration files are valid and properly formatted.

## Examples

### Environment Setup

```bash
# Interactive environment setup
sumpter doctor setup

# Generate setup script for zsh
sumpter doctor setup --generate-script --shell zsh

# Dry run to see what would be configured
sumpter doctor setup --dry-run
```

### Environment Diagnostics

```bash
# Check environment and configuration
sumpter doctor check
```

Output shows:

- Environment variables status
- Directory paths and accessibility
- Configuration file validation

### Configuration Management

```bash
# List available configuration templates
sumpter doctor config list

# Set up SEC EDGAR retrieve configuration
sumpter doctor config setup retrieve-sec-edgar

# Validate existing configuration
sumpter doctor config validate retrieve
```

## SEC EDGAR Setup Process

When running `sumpter doctor config setup retrieve-sec-edgar`, the wizard:

1. **Path Discovery**: Automatically finds your SUMPTER_HOME directory
2. **Compliance Notice**: Explains SEC requirements for user agent identification
3. **Interactive Prompts**:
   - Company name (validated to prevent placeholders)
   - Contact email (validated format and domain)
   - Rate limits (requests per second, 1-8)
   - Burst limit (default 5)
4. **Configuration Preview**: Shows the generated YAML before writing
5. **Confirmation**: Asks for user approval before creating files
6. **Testing Option**: Offers to test the configuration with a dry-run

### Generated Configuration

The wizard creates a `retrieve.yaml` file with:

```yaml
version: "retrieve/v0.1.0"
realms:
  finance:
    enabled: true
    client:
      user_agent: "Your Company contact@yourcompany.com"
      timeout_seconds: 30
    rate_limits:
      requests_per_second: 6
      burst_limit: 3
      backoff_seconds: 1
    endpoints:
      sec_edgar_base: "https://data.sec.gov"
```

## Validation Rules

### Company Name Validation

- Cannot be empty
- Must be at least 2 characters
- Blocks common placeholders: "test company", "my company", "company name", "your company", "example company"

### Email Validation

- Must contain "@" and "." characters
- Blocks placeholder domains: "yourcompany.com", "example.com", "testcompany.com", "company.com", "placeholder.com", "test.com"

### SEC Compliance

- User agent format: "Company Name contact@email.com"
- Warns users about SEC blocking invalid user agents
- Guides users toward providing real company information

## Use Cases

- **First-Time Setup**: Configure SUMPTER_HOME and environment variables
- **Configuration Management**: Set up retrieve configurations for different data sources
- **Troubleshooting**: Diagnose environment and configuration issues
- **Compliance**: Ensure SEC EDGAR configurations meet regulatory requirements
- **Migration**: Update configurations when moving between environments

## Notes

- All setup operations are OS-neutral and provide platform-specific instructions
- Configuration files are validated against JSON schemas
- The doctor command never modifies existing files without user confirmation
- SEC EDGAR setup includes compliance warnings and validation
- Environment setup can generate shell scripts for automation
