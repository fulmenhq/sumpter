# EnvInfo Command

Display comprehensive environment and system information for Sumpter.

## Usage

```bash
sumpter envinfo [flags]
sumpter envinfo [command]
```

## Description

The `envinfo` command provides detailed insights into the system environment, network configuration, and XML processing capabilities. It's designed to help diagnose setup issues and validate system readiness for XML transformation workflows.

## Commands

### Main Command

```bash
sumpter envinfo [flags]
```

Displays all environment information in a comprehensive format.

### Subcommands

- `system`: Show system information only
- `paths`: Show application paths only
- `vars`: Show environment variables only
- `xml`: Show XML processing capabilities only
- `network`: Show network information only

## Flags

### Main Command Flags

- `--all`, `-a`: Show all environment variables (default: show key variables only)
- `--export`, `-e`: Output environment variables in shell export format
- `--filter`: Filter variables by key (case-insensitive substring match)
- `--json`, `-j`: Output in JSON format
- `--network`: Include network interface information
- `--external-ip`: Include external IP detection
- `--verbose`, `-v`: Enable verbose output
- `--xml`: Include XML processing capabilities information

## Examples

### Basic Environment Information

```bash
sumpter envinfo
```

Output:

```
🖥️  System Information
==================================================
OS              | linux
Architecture    | amd64
Go Version      | go1.21.0
CPU Cores       | 8
Hostname        | workstation
Working Dir     | /home/user/projects
Timestamp       | 2024-01-15T14:30:25Z

📄 XML Processing Capabilities
==================================================
Streaming       | true
Memory Target   | input-streaming only
Encodings       | UTF-8, UTF-16, ISO-8859-1, Windows-1252
Outputs         | NDJSON, Parquet, DuckDB, Markdown

🏠 Application Environment
==================================================
Home            | /home/user/.local/share/sumpter
WorkDir         | /home/user/projects
Cache           | /home/user/.cache/sumpter
Logs            | /home/user/.local/state/sumpter/logs
Configs         | /home/user/.config/sumpter
Temp            | /tmp/sumpter

🌍 Environment Variables
==================================================
HOME            | /home/user
USER            | user
PATH            | /usr/local/bin:/usr/bin:/bin
SHELL           | /bin/bash
TERM            | xterm-256color
PWD             | /home/user/projects
SUMPTER_HOME    | /home/user/.local/share/sumpter

📊 Stats
==================================================
Total Vars      | 45
Filtered Vars   | 7
Key Vars        | 7
```

### Show All Environment Variables

```bash
sumpter envinfo --all
```

### Filter Environment Variables

```bash
sumpter envinfo --filter PATH
```

### JSON Output

```bash
sumpter envinfo --json
```

### Export Format

```bash
sumpter envinfo --export --filter SUMPTER
```

Output:

```bash
export SUMPTER_HOME="/home/user/.local/share/sumpter"
export SUMPTER_LOG_LEVEL="info"
```

### Include Network Information

```bash
sumpter envinfo --network --external-ip
```

### Subcommand: System Information Only

```bash
sumpter envinfo system
```

### Subcommand: Environment Variables Only

```bash
sumpter envinfo vars --all --filter AWS
```

### Subcommand: XML Capabilities Only

```bash
sumpter envinfo xml
```

Output:

```
📄 XML Processing Capabilities
==================================================
Streaming       | true
Memory Target   | input-streaming only
Encodings       | UTF-8, UTF-16, ISO-8859-1, Windows-1252
Outputs         | NDJSON, Parquet, DuckDB, Markdown
```

## Information Sections

### System Information

- **OS**: Operating system (linux, darwin, windows)
- **Architecture**: CPU architecture (amd64, arm64)
- **Go Version**: Go runtime version used by Sumpter
- **CPU Cores**: Number of available CPU cores
- **Hostname**: System hostname
- **Working Dir**: Current working directory
- **Timestamp**: Current timestamp in RFC3339 format
- **External IP**: Public IP address (when --external-ip is used)

### XML Processing Capabilities

- **Streaming**: Whether streaming XML processing is supported
- **Memory Target**: Current memory contract
- **Encodings**: Supported character encodings
- **Outputs**: Available output formats

### Application Environment

- **Home**: Sumpter's home directory (XDG_DATA_HOME/sumpter)
- **WorkDir**: Current working directory
- **Cache**: Cache directory (XDG_CACHE_HOME/sumpter)
- **Logs**: Log directory (XDG_STATE_HOME/sumpter/logs)
- **Configs**: Configuration directory (XDG_CONFIG_HOME/sumpter)
- **Temp**: Temporary directory (/tmp/sumpter)

### Environment Variables

Shows environment variables with automatic security filtering:

- **Key Variables**: Important variables (HOME, USER, PATH, etc.)
- **SUMPTER\_ Variables**: Sumpter-specific configuration
- **Filtered Results**: When using --filter flag
- **Security**: Sensitive values are redacted (**_redacted_**)

### Network Information (Optional)

- **Interfaces**: Network interface details
- **External IP**: Public IP address detection

## Security Features

### Automatic Redaction

Environment variables containing sensitive patterns are automatically redacted:

- `secret`, `token`, `apikey`, `password`, `key`, `cert`
- `database_url`, `xml_catalog`, `credential`

### Safe Display

- Values longer than 50 characters are truncated
- Sensitive information is never displayed in full

## Use Cases

- **System Diagnostics**: Verify system compatibility with Sumpter
- **Environment Setup**: Check configuration and paths
- **Network Validation**: Verify network connectivity for data sources
- **Security Audit**: Review environment variables for sensitive data
- **Support Requests**: Provide comprehensive system information
- **CI/CD Integration**: Use JSON output for automated environment checks
- **XML Readiness**: Validate XML processing capabilities

## Notes

- Uses XDG Base Directory specification for path resolution
- Network detection requires appropriate permissions
- External IP detection uses public services (may be rate-limited)
- JSON output includes all available information
- Security filtering is applied to all output formats
