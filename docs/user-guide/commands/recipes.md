# Recipes Command

The `recipes` command manages recipe workspaces and orchestrates recipe-driven automation such as SEC EDGAR acquisition and XML extraction.

## Usage

```bash
sumpter recipes [command] [flags]
```

## Available Subcommands

### `init`

Scaffold a new recipe workspace with standard folders, templated README, and recipe manifest.

```bash
sumpter recipes init --path <target> [--id <recipe-id>] [--git-init]
```

**Flags:**

- `--path` *(required)*: Target directory for the new recipe workspace
- `--id`: Recipe identifier injected into templates
- `--git-init`: Initialize a git repository inside the workspace

The command creates the base directory, populates `signature/`, `extract/`, `validation/`, `testdata/`, and `outputs/`, and renders template files from the embedded assets bundle.

### `run extract`

Execute an extract recipe using the manifest defaults (`recipe.yaml`).

```bash
sumpter recipes run extract <workspace> [flags]
```

**Key Flags:**

- `--manifest`: Path to the manifest (default: `recipe.yaml` relative to the workspace)
- `--input-path` / `--files`: Override manifest input discovery
- `--include-pattern` / `--exclude-pattern`: Override glob patterns
- `--output-path` / `--output-pattern`: Override output destinations
- `--format`: Override output format (`json`, `structured`, `ndjson`, etc.)
- `--client-id`, `--site-id`: Blend identifiers into the output payload
- `--parameter key=value`: Inject or override a recipe parameter; repeat the flag for multiple values
- `--signature`, `--extract`: Override the manifest asset paths for debugging

When no overrides are provided the manifest supplies signature/extract config paths, input discovery strategy, output format, worker count, progress settings, source extraction patterns, and any `defaults.parameters` values. Generic parameters are injected into every record after file-level source captures, and `--parameter` overrides the same key from the manifest. Parameter and source capture keys must not collide with `field_mappings[].output_field`; Sumpter fails the run instead of silently replacing content-derived fields. Internally the command delegates to `sumpter extract files`, so the low-level CLI remains available for direct debugging.

Successful extract runs always write the requested output artifact, including
the legitimate zero-record case. Empty JSONL outputs are zero-byte files, and
empty Parquet outputs are schema-only files with zero rows. Selectors that
declare `min_occurrences: N` with `N > 0` opt into fail-loud enforcement; if a
source yields fewer matches than the selector's floor, the command exits
non-zero before writing payload output or `manifest.json`.

Recipe extract configs may derive scalar fields with Sumpter DSL expressions.
For conditional relabeling, use ternary syntax:

```yaml
field_mappings:
  - output_field: widget_status_friendly
    expression: 'widget_status == "online" ? "ready" : widget_status'
    type: string
```

The ternary condition must evaluate to a boolean, only the selected branch is
evaluated, and the result type is the selected branch's value. Branch values
should be compatible with the declared `type:` for fixed-schema outputs such as
Parquet.

### `retrieve`

Execute a recipe to acquire upstream data (currently SEC EDGAR filings).

```bash
sumpter recipes retrieve <realm> <domain-tag> [flags]
```

**Supported realms:** `finance`

**Supported domain-tags:** `sec-edgar`

**Flags:**

- `--output-base`: Base directory for retrieved artifacts (defaults to `$SUMPTER_HOME/work/`)
- `--config-path`: Optional path to `retrieve.yaml` (defaults to `$SUMPTER_HOME/configs/retrieve.yaml`)
- `--ticker`: Stock ticker symbol (required for `finance sec-edgar`)
- `--filing-type`: Filing type, e.g., `10-K`, `10-Q` (required for `finance sec-edgar`)
- `--year`: Filing year (required for `finance sec-edgar`)

The command validates write access to the output directory, loads the retrieve configuration, enforces SEC user-agent requirements, and downloads the requested filing into the structured workspace.

## Recipe Manifest (`recipe.yaml`)

Every workspace includes a manifest that documents the recipe metadata and runtime defaults:

```yaml
version: "recipe/v0.1.0"
kind: "extract"
id: retail_daily_sales_v1
display_name: "Retail Daily Sales"
created_at: "2025-10-02T14:00:00Z"
assets:
  signature: signature/retail-journal-signature.yaml
  extract: extract/retail-journal-extract.yaml
  validation: ""
defaults:
  input:
    mode: path
    path: testdata
    include_pattern: "*.xml"
    exclude_pattern: ""
    max_depth: 0
    follow_symlinks: false
  output:
    format: json
    path: outputs
    pattern: extract-{}.jsonl
  client_id: ""
  site_id: ""
  parameters:
    region_id: "westcoast"
    tenant_id: "1234"
  parameters_required:
    - tenant_id
  source_extraction:
    - id: filename-date-token
      source: filename
      pattern: '^(?P<business_date>\d{4}-\d{2}-\d{2})-.*\.xml$'
    - id: path-site-identifier
      source: relative_path
      pattern: '^sites/(?P<source_site_id>[a-z0-9-]+)/'
  source_extraction_required:
    - business_date
  workers: 1
  progress: false
```

- **`assets`** points at the core configuration files required to run the recipe.
- **`defaults.input`** defines how the runner discovers XML (directory scanning or explicit file list).
- **`defaults.output`** controls output formatting and destination, allowing NDJSON/structured JSON switches later.
- **`defaults.client_id` / `site_id`** pre-populate metadata for downstream consumers.
- **`defaults.parameters`** injects arbitrary string parameters into every record; **`defaults.parameters_required`** fails the run if a required key does not resolve from the manifest or CLI.
- **`defaults.source_extraction`** injects named regexp captures from the source `filename`, `relative_path`, or `absolute_path` once per file; **`defaults.source_extraction_required`** fails before parameter merging if a required capture is absent. `relative_path` requires a root from `--input-path` or `defaults.input.path`.
- **`kind`** distinguishes extract vs. acquire recipes; additional kinds can be introduced without changing the runner syntax.

The manifest is validated against `schemas/recipes/v0.1.0/recipe.schema.yaml`. Use `sumpter recipes init` to scaffold a workspace and then drop your signature/extract configs into the generated folders. For low-level debugging you can still call the extract command directly:

```bash
sumpter extract files \
  --signature-config-path signature/retail-journal-signature.yaml \
  --extract-config-path extract/retail-journal-extract.yaml \
  --files testdata/sample.xml
```

## Examples

### Scaffold a Recipe Workspace

```bash
sumpter recipes init \
  --path ./recipes/customer/retail-daily-sales \
  --id retail_daily_sales_v1 \
  --git-init
```

### Download an SEC EDGAR Filing

```bash
sumpter recipes retrieve finance sec-edgar \
  --ticker AAPL \
  --filing-type 10-K \
  --year 2024
```

The filing is saved under `$SUMPTER_HOME/work/retrieve/<ticker>/<filing-type>/…` when default paths are in use.

### Execute an Extract Recipe from the Manifest

```bash
sumpter recipes run extract ./recipes/customer/retail-daily-sales \
  --input-path ./recipes/customer/retail-daily-sales/testdata \
  --output-path ./recipes/customer/retail-daily-sales/outputs/retail.json
```

The command resolves signature/extract configs via the manifest, applies defaults for include patterns and output format, and delegates to the extract engine.
