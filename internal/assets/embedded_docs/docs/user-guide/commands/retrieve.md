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
- `--include-pattern`: File inclusion pattern (default: "\*")
- `--exclude-pattern`: File exclusion pattern
- `--max-depth`: Maximum directory depth to search (0 = unlimited)
- `--follow-symlinks`: Follow symbolic links
- `--format`: Output format: text, json (default: "text")
- `--output-path`: Output file path (stdout if not specified)
- `--progress`: Show progress indicators
- `--flatten`: Output flattened relative paths instead of absolute paths

> **Note:** Recipe-driven acquisition now lives under the dedicated `sumpter recipes` command. Use `sumpter recipes retrieve …` when you need to run a data-source recipe such as SEC EDGAR. The `retrieve` command continues to provide low-level file discovery and copy utilities.

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

## Examples

### Find XML files in a directory

```bash
sumpter retrieve find --input-path ./data --include-pattern "*.xml" --format json
```

### Download SEC EDGAR filings

```bash
sumpter recipes retrieve finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024
```

### Copy files with progress

```bash
sumpter retrieve copy /source/files/*.xml /destination/ --progress
```
