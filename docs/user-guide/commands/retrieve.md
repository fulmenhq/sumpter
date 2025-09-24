# Retrieve Command

The `retrieve` command provides data acquisition capabilities through APIs, web scraping, or file system operations, organizing data for processing.

## Usage

```bash
sumpter retrieve [command] [flags]
```

## Available Subcommands

### `copy`

Copy data from various sources to destinations.

**Sources can be:**
- API endpoints (e.g., `sec-edgar://AAPL/10-K/2024`)
- File paths (e.g., `/path/to/files/*.xml`)
- Recipe references (e.g., `recipe://finance/sec-edgar`)

**Destinations can be:**
- Local paths
- Cloud storage URIs (future support)

```bash
sumpter retrieve copy <source> <destination>
```

### `find`

Recursively find files matching patterns in directory trees.

Useful for discovering data files in complex directory structures. Outputs file paths that can be used with other commands.

```bash
sumpter retrieve find [flags]
```

**Flags:**
- `--input-path`: Input path to search (directory)
- `--include-pattern`: File inclusion pattern (default: "*")
- `--exclude-pattern`: File exclusion pattern
- `--max-depth`: Maximum directory depth to search (0 = unlimited)
- `--follow-symlinks`: Follow symbolic links
- `--format`: Output format: text, json (default: "text")
- `--output-path`: Output file path (stdout if not specified)
- `--progress`: Show progress indicators
- `--flatten`: Output flattened relative paths instead of absolute paths

### `recipe`

Execute data acquisition recipes for specific realms and domains.

Configuration is loaded from `retrieve.yaml` (use `--config-path` to override).

**Supported realms:** finance
**Supported domain-tags:** sec-edgar

```bash
sumpter retrieve recipe <realm> <domain-tag> [flags]
```

**Example:**
```bash
sumpter retrieve recipe finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024
```

**Flags:**
- `--ticker`: Stock ticker symbol (for finance/sec-edgar)
- `--filing-type`: Filing type (e.g., 10-K, 10-Q) (for finance/sec-edgar)
- `--year`: Filing year (for finance/sec-edgar)

## Global Flags

- `--config-path`: Path to retrieve configuration file (default: `SUMPTER_HOME/configs/retrieve.yaml`)
- `--output-base`: Base output directory for acquired data (default: `SUMPTER_HOME/work`)

## Configuration

The retrieve command uses a YAML configuration file located at `SUMPTER_HOME/configs/retrieve.yaml`.

### Example Configuration

```yaml
version: "retrieve/v0.1.0"
realms:
  finance:
    enabled: true
    client:
      user_agent: "Your Company Name contact@yourcompany.com"
      timeout_seconds: 30
    rate_limits:
      requests_per_second: 8
      burst_limit: 5
      backoff_seconds: 1
    endpoints:
      sec_edgar_base: "https://data.sec.gov"
    options:
      # Realm-specific options
```

### Configuration Schema

- `version`: Configuration schema version (must be "retrieve/v0.1.0")
- `realms`: Realm-specific configuration
  - `enabled`: Whether this realm is enabled
  - `client`: HTTP client configuration
    - `user_agent`: User-Agent header (required for SEC compliance)
    - `timeout_seconds`: Request timeout
  - `rate_limits`: Rate limiting settings
    - `requests_per_second`: Max requests per second
    - `burst_limit`: Maximum burst requests
    - `backoff_seconds`: Backoff time when rate limited
  - `endpoints`: API endpoints and base URLs
  - `options`: Additional realm-specific configuration

## Migration from Previous Versions

If you were using the old `sourcedata` command, update your configuration:

1. Rename `sourcedata-config.yaml` to `retrieve.yaml`
2. Update the version from `"sourcedata/v0.1.0"` to `"retrieve/v0.1.0"`
3. Move the config file from `SUMPTER_HOME/configs/` to `SUMPTER_HOME/config/`
4. Update command usage from `sumpter sourcedata finance sec-edgar` to `sumpter retrieve recipe finance sec-edgar`

## Examples

### Find XML files in a directory

```bash
sumpter retrieve find --input-path ./data --include-pattern "*.xml" --format json
```

### Download SEC EDGAR filings

```bash
sumpter retrieve recipe finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024
```

### Copy files with progress

```bash
sumpter retrieve copy /source/files/*.xml /destination/ --progress
```