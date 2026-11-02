# External Recipe Workspaces

A Sumpter recipe is a directory of plain YAML and reference files. The bundled
cases under `examples/cases/<n>-<name>/recipe/` are samples for study and test
runs; they are not the only place a recipe may live. In production and domain
work, keep your recipes outside the Sumpter repository — including in their own
private or team git repository — and point `sumpter recipes run extract <dir>`
at that directory.

## Directory Layout

Scaffold a new recipe workspace anywhere on disk with `recipes init`:

```bash
sumpter recipes init \
  --path ~/work/widgetco-orders-recipe \
  --id widgetco_orders_v1 \
  --git-init
```

`--git-init` initializes a standalone git repository inside the recipe directory
so you can track and push it to your own remote independently of Sumpter.

```text
widgetco-orders-recipe/
├── recipe.yaml
├── signature/
│   └── widgetco-orders-signature.yaml
├── extract/
│   └── widgetco-orders-extract.yaml
├── applicability/
│   └── applicability.yaml          # optional
├── refdata/
│   └── active_customers.csv        # optional local reference tables
├── testdata/
│   └── sample-order.xml
└── outputs/
```

| File or Manifest Key                 | Role                                                                                                                                                     |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `recipe.yaml`                        | Workspace manifest. Validated against `schemas/recipes/v0.1.0/recipe.schema.yaml`.                                                                       |
| `assets.signature`, `assets.extract` | Required signature and extract configuration files loaded for the run.                                                                                   |
| `assets.applicability`               | Optional pre-signature file gate. Predicate fields sit under a top-level `applicability:` key (`applicability.type`, `applicability.expression`). XPath. |
| `defaults.parameters`                | Scalar strings or lists of strings injected into Sumpter DSL scope and emitted into `extract.data` unless listed in `defaults.parameters_internal`.      |
| `defaults.parameters_internal`       | Same Sumpter DSL scope as `defaults.parameters`, but never written to output records. CLI `--parameter` overrides are redacted in provenance argv.       |
| `defaults.reference_tables[].source` | CSV, TSV, or NDJSON file inside the recipe directory, or an `s3://` object. A local path is contained to the recipe directory and may not escape it.     |

## Running a Recipe vs. `extract files`

Run a recipe directory with `recipes run extract` (or `recipes run extract-multi`
across multiple XML recipes):

```bash
sumpter recipes run extract ~/work/widgetco-orders-recipe \
  --files ./inputs/order-1001.xml \
  --output-path ./out
```

- **`sumpter recipes run extract <dir>`** and **`sumpter recipes run extract-multi <dir>...`**
  read `recipe.yaml` and apply all manifest defaults: `assets.applicability`,
  `defaults.input`, `defaults.output`, `defaults.parameters`,
  `defaults.parameters_internal`, `defaults.source_extraction`, and
  `defaults.reference_tables`.
- **`sumpter extract files`** does **not** read `recipe.yaml`. Passing the same
  signature and extract files directly via `--signature-config-path` and
  `--extract-config-path` runs only those two configs and leaves
  `defaults.parameters`, `defaults.parameters_internal`,
  `defaults.source_extraction`, `defaults.reference_tables`, and
  `assets.applicability` unset. See the [`extract` command guide](commands/extract.md).
- **`sumpter validate`** checks Sumpter configuration files (`sumpter.yaml`,
  `logger.yaml`, `pii.yaml`, and `artifact-descriptor`). It does not load a
  recipe directory. A recipe smoke is `sumpter recipes run extract <dir>` on a
  bundled or sample example.

## Path Bases

Relative paths resolve against three distinct bases depending on where they are
written:

| Where the path appears                                                                                                               | Resolves relative to        |
| ------------------------------------------------------------------------------------------------------------------------------------ | --------------------------- |
| Inside `recipe.yaml` (`assets.*`, `defaults.input.path`, `defaults.input.files_from`, `defaults.output.path`, local reference tables) | The **recipe directory**    |
| On the command line (`--files`, `--file-list`, `--input-path`, `--output-path`)                                                      | The **working directory**   |
| Inside a file list (`--file-list` or `defaults.input.files_from`)                                                                    | The **list file directory** |

Local `defaults.reference_tables[].source` paths (and `--reference-table name=path`
overrides) must stay strictly inside the recipe directory: absolute paths, `..`
traversal, and symlinks are refused.

## Two Languages: XPath and the Sumpter DSL

A recipe uses two distinct expression languages at different stages of
processing:

1. **XPath** selects record boundaries (`match_selectors[].xpath`), extracts or
   computes values directly from the input document tree
   (`field_mappings[].xpath`), and gates files in `assets.applicability`
   (`applicability.expression`).
   - XML, JSON, and NDJSON recipes share the same XPath grammar over the
     [document node model](../standards/document-node-model.md).
   - JSON has no attributes or namespaces: a JSON recipe writes `id` where an
     XML recipe writes `@id`.
   - Numeric XPath evaluation uses IEEE 754 floating point; keep identifiers
     above $2^{53}$ as `type: string`.

2. **Sumpter DSL** runs **after** top-level XPath fields and recipe parameters
   have been bound for a record.
   - Used in `field_mappings[].expression`,
     `validation_metadata.aggregations[].expression`,
     `validation_metadata.reconciliations`, and the reference-table functions
     `in_reference` and `lookup_reference`.
   - References extracted fields and recipe parameters as **bare names** (for
     example `tenant_id` or `total_amount`), never `$tenant_id`.
   - Put string classification, prefix/membership checks (`starts_with_any`,
     `value_in`, `string_length`), conditional relabeling (`? :`), and
     cross-field arithmetic in `expression` mappings. See the
     [Sumpter DSL Reference](../dsl-reference.md) for the complete grammar and
     function table.

```yaml
# extract/widgetco-orders-extract.yaml
field_mappings:
  - output_field: order_id
    xpath: "@id"
    type: string
  - output_field: raw_status
    xpath: "Status"
    type: string
    internal: true
  - output_field: status_label
    expression: 'raw_status == "open" ? "active" : raw_status'
    type: string
  - output_field: is_priority_tenant
    expression: "value_in(tenant_id, priority_tenants)"
    type: boolean
```

## Studying Bundled Examples and Embedded Docs

- **Embedded documentation:** `docs/dsl-reference.md` and the pages under
  `docs/user-guide/` are embedded in the `sumpter` binary. View them offline at
  any time with `sumpter docs list` and
  `sumpter docs show user-guide/recipes.md` or
  `sumpter docs show dsl-reference.md`.
- **Bundled sample recipes:** Worked examples live under
  `examples/cases/<n>-<name>/recipe/` in the source tree (and in the embedded
  examples bundle). Start with `examples/cases/01-basic-extraction/recipe` for
  XML or `examples/cases/14-json-basic-extraction/recipe` for JSON; see
  [JSON and NDJSON extraction](json-extraction.md) and the
  [Recipes Command](commands/recipes.md).
- **Deep workflow reference:** For multi-recipe runs (`extract-multi`),
  aggregate output shards, cloud (`s3://`) credential handles, reference tables,
  and provenance sidecars in full detail, see the repository
  [Extract Workflow guide](../extract-workflow.md).
