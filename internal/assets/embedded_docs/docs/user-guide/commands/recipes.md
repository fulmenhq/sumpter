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

- `--path` _(required)_: Target directory for the new recipe workspace
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
- `--parameter key=value`: Inject or override a recipe parameter; repeat the flag for multiple values. The value is a literal string unless it is a valid JSON array of strings, which becomes a **list parameter** (read by the `starts_with_any` / `value_in` DSL helpers). Quote the array for your shell, e.g. `--parameter prefixes='["NM_","NR_"]'`. A value that merely contains commas stays a literal string; list members must be non-empty strings (numbers/booleans/objects/nested/mixed arrays are rejected). List values are emitted into `extract.data` as a JSON array like scalar parameters unless withheld.
- `--signature`, `--extract`: Override the manifest asset paths for debugging

When no overrides are provided the manifest supplies signature/extract config paths, input discovery strategy, output format, worker count, progress settings, source extraction patterns, and any `defaults.parameters` values. Generic parameters are injected into every record after file-level source captures, and `--parameter` overrides the same key from the manifest. Parameter and source capture keys must not collide with `field_mappings[].output_field`; Sumpter fails the run instead of silently replacing content-derived fields. Internally the command delegates to `sumpter extract files`, so the low-level CLI remains available for direct debugging.

Successful extract runs always write the requested output artifact, including
the legitimate zero-record case. Empty JSONL outputs are zero-byte files, and
empty Parquet outputs are schema-only files with zero rows. Selectors that
declare `min_occurrences: N` with `N > 0` opt into fail-loud enforcement; if a
source yields fewer matches than the selector's floor, the command exits
non-zero before writing payload output or `manifest.json`.

Recipes may declare an optional `assets.applicability` YAML asset with a binary
XPath predicate. Applicability runs before signature matching. When the
predicate evaluates false, the file is reported as `not_applicable`, no records
are extracted, and the condition does not count as a failure. When the predicate
evaluates true, the recipe proceeds through signature matching and extraction as
usual. Recipe runs that declare applicability add disposition fields to the
provenance input entries and write a lightweight `dispositions.json` summary at
the output root. The summary includes `schema_version: extract-dispositions/v0.1.0`
and is schema-backed by `schemas/extract/v0.1.0/dispositions.schema.json`.

The applicability asset is a standalone file referenced by
`assets.applicability`. Its predicate fields must be nested under the top-level
`applicability:` key:

```yaml
applicability:
  type: xpath
  expression: "boolean(/*[local-name()='Document'])"
  description: "Only run this recipe for document-style XML inputs"
```

Do not place `type:` or `expression:` at the asset file top level; the schema
requires `applicability.type` and `applicability.expression`.

Multi-file runs may opt into per-file failure isolation with
`--continue-on-error`. In v0 this flag requires `--output-path`; successful
input files still emit their normal output artifacts, recoverable per-file
failures are written to `<output-path>/failures.json`, and the command exits
non-zero when any file failed. Output-write and failure-manifest-write errors
remain terminal. The failure manifest is schema-backed by
`schemas/extract/v0.1.0/failures.schema.json` and uses the closed reason set
`parse_error`, `signature_mismatch`, `min_occurrences_violation`,
`validation_error`, and `internal_error`.

Recipe extract configs may derive scalar fields with Sumpter DSL expressions.
For the full expression grammar, function set, and parser behavior contracts,
see [Sumpter DSL Reference](../../dsl-reference.md). For conditional
relabeling, use ternary syntax:

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

### `run extract-multi`

Apply several extract recipes to **one** input set in a single parse-once pass.

```bash
sumpter recipes run extract-multi <workspace>... [flags]
```

Each input file is read and parsed **once**, then dispatched to every recipe, so
the read/parse cost is amortized from ~N× to 1× across N recipes — the dominant
cost at high file counts (many small files). Each recipe writes to its own
`<output-path>/<recipe-id>/` subdirectory (records, `manifest.json`, and
`dispositions.json` / `failures.json` when applicable); output, formats,
`defaults.parameters`, reference tables, and credential handles are per recipe
(from each manifest). The input set, `--output-path` root, and run-level controls
(`--continue-on-error`, credentials, the shared run id resolved once from
`--run-id` → `SUMPTER_RUN_ID` → generated) are shared.

A repeatable `--parameter key=value` is a **shared run-level override** applied to
every recipe in the pass: it layers over each recipe's `defaults.parameters` (CLI
wins uniformly), satisfies each recipe's `parameters_required` independently,
supports the same scalar/JSON-list typed values as single-recipe
[`run extract --parameter`](#run-extract), and is injected into every
recipe's records — use it for genuinely per-run keys every recipe shares (e.g. a
per-run provenance stamp). A shared key colliding with any recipe's
`field_mappings[].output_field` fails the run at plan-load preflight, before
output is written. `--parameter` is not a credential transport; secret-shaped
keys are redacted by key in the provenance argv.

Failure handling follows the input-vs-recipe boundary: a read/parse/acquire
failure is input-level (every recipe sees it); an applicability, signature,
extraction, `min_occurrences`, or output failure is recipe-level — isolated to
that recipe's own `failures.json` (under `--continue-on-error`) and never aborts
the others.

**Scope (v0):** JSON/NDJSON output only (a recipe declaring another format such
as Parquet is rejected — use single-recipe `run extract`); no streaming/large-file
path (a file large enough to route to streaming is rejected, and
`--allow-large-files` does not relax this); no cross-recipe joins/ordering. See
[Run multiple recipes in one pass](../../extract-workflow.md) for the full
walkthrough.

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
  applicability: applicability/applicability.yaml
  validation: ""
defaults:
  cadence: daily-rolling
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
    uniform_schema: false
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
      pattern: "^sites/(?P<source_site_id>[a-z0-9-]+)/"
  source_extraction_required:
    - business_date
  workers: 1
  progress: false
```

- **`assets`** points at the core configuration files required to run the recipe.
- **`defaults.output.uniform_schema`** optionally emits every declared `output_schema.properties` key on each record, using JSON `null` for absent values.
- **`defaults.input`** defines how the runner discovers XML (directory scanning or explicit file list).
- **`defaults.output`** controls output formatting and destination, allowing NDJSON/structured JSON switches later.
- **`defaults.cadence`** records operator-readable run cadence intent such as `daily-rolling`, `weekly`, `weekly-2x`, `on-demand`, `hourly`, `monthly`, or `quarterly`.
- **`defaults.client_id` / `site_id`** pre-populate metadata for downstream consumers.
- **`defaults.parameters`** injects parameters into every record — each value is a string or a list of strings; **`defaults.parameters_required`** fails the run if a required key does not resolve from the manifest or CLI (an empty list counts as provided; an empty scalar string does not).
- **`defaults.source_extraction`** injects named regexp captures from the source `filename`, `relative_path`, or `absolute_path` once per file — each capture is emitted directly as a record field, so it can **tag every record by which file/grain produced it** (e.g. a `^(?P<grain>unit|batch)-` filename prefix for provenance or grain classification) without re-deriving it downstream; **`defaults.source_extraction_required`** fails before parameter merging if a required capture is absent. `relative_path` requires a root from `--input-path` or `defaults.input.path`; `filename` and `absolute_path` need none, so they work with `--files` / `--file-list`.
- **`kind`** distinguishes extract vs. acquire recipes; additional kinds can be introduced without changing the runner syntax.

### Cadence (operator-readable metadata)

`defaults.cadence` is optional recipe metadata for operators and
orchestrators that select recipes for scheduled triggers. Sumpter validates the
value as a lowercase kebab-case label, copies it into recipe-backed
`manifest.json` provenance when present, and otherwise does not consume it at
runtime. It does not schedule work, enforce timing windows, or supply a default
for unannotated recipes.

Use values that match your orchestration vocabulary. Common labels include
`daily-rolling`, `weekly`, `weekly-2x`, `on-demand`, `hourly`, `monthly`, and
`quarterly`, but Sumpter treats them as guidance rather than an enum. A future
operator metadata extension slot may absorb this field through a compatibility
and deprecation cycle; current recipes can adopt `defaults.cadence` without
depending on that future shape.

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
