# Extract Command

The `extract` command runs low-level extraction directly from standalone
signature and extract configuration files and inspects registered transforms.

> **Recipe workspaces use `sumpter recipes run extract`.**
> `sumpter extract files` does **not** read `recipe.yaml`. If you pass a
> recipe's signature and extract YAML via `--signature-config-path` and
> `--extract-config-path`, manifest settings such as `defaults.parameters`,
> `defaults.parameters_internal`, `defaults.source_extraction`,
> `defaults.reference_tables`, and `assets.applicability` are left unset. To
> run a recipe directory with its manifest defaults, use
> `sumpter recipes run extract <workspace>` (or
> `sumpter recipes run extract-multi <workspace>...`). See
> [External Recipe Workspaces](../recipes.md) and the
> [Recipes Command](recipes.md).

## Usage

```bash
sumpter extract [command] [flags]
```

## Available Subcommands

### `files`

Extract structured records from XML, JSON, or NDJSON inputs using explicit
signature and extract YAML paths.

```bash
sumpter extract files \
  --signature-config-path <signature.yaml> \
  --extract-config-path <extract.yaml> \
  (--files <a,b> | --file-list <list.txt> | --input-path <dir>) \
  [flags]
```

**Required Flags:**

- `--signature-config-path`: Path to the file signature configuration YAML
- `--extract-config-path`: Path to the extract configuration YAML

**Input Selection Flags (choose one):**

- `--files`: Comma-separated list of input paths for short ad hoc sets
- `--file-list`: Newline-delimited file of input references (`path`, `s3://`, or integrity-bound JSON object lines `{"uri","size","sha256"}`)
- `--input-path`: Directory (or single file) to walk with `--include-pattern` and `--exclude-pattern`

**Key Output and Runtime Flags:**

- `--output-path`, `-o`: Output destination directory or `s3://` prefix
- `--format`, `-f` / `--formats`: Output writer format (`json`, `ndjson`, `parquet`)
- `--output-mode`: `per-input` (default) or `aggregate` (single rolling NDJSON stream across inputs; requires `--output-path` and a manifest)
- `--parameter key=value`: Repeatable parameter injected into expression scope and emitted into `extract.data` (a JSON array of strings becomes a list parameter)
- `--record-index`: Record index path for parallel indexed extraction (`--workers`, `--verify-index`, `--max-record-size-mb`)
- `--continue-on-error`: Isolate recoverable per-file failures to `<output-path>/failures.json` and continue remaining inputs (requires `--output-path`; exits non-zero when any input fails)
- `--dry-run`: Validate configuration and input resolvability without writing output
- `--provenance-root`: Require local inputs to reside strictly under the given root and record root-relative paths in `manifest.json`
- `--no-manifest`: Suppress the sidecar `manifest.json`

**Path Resolution:**

Paths passed on the command line (`--signature-config-path`,
`--extract-config-path`, `--files`, `--file-list`, `--input-path`,
`--output-path`, `--record-index`) resolve relative to the **working
directory**. Relative entries inside a `--file-list` file resolve relative to
the **list file's directory**.

### `transforms`

Inspect built-in field transforms:

```bash
sumpter extract transforms list
sumpter extract transforms describe <transform-name>
```

## Examples

### Direct Extraction with Standalone Configs

```bash
sumpter extract files \
  --signature-config-path examples/cases/01-basic-extraction/recipe/signature/widget-order-signature.yaml \
  --extract-config-path examples/cases/01-basic-extraction/recipe/extract/widget-order-extract.yaml \
  --files examples/cases/01-basic-extraction/input.xml \
  --output-path ./out
```

### Running a Recipe Workspace Instead

When working with a recipe directory that declares `recipe.yaml` defaults,
parameters, source extraction, applicability, or reference tables, run the
workspace through `recipes run extract` rather than `extract files`:

```bash
sumpter recipes run extract examples/cases/01-basic-extraction/recipe \
  --files examples/cases/01-basic-extraction/input.xml \
  --output-path ./out
```
