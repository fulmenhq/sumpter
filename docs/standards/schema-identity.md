# Schema Identity

Sumpter's schemas under [`schemas/`](../../schemas/) form one bundle with
host-independent identifiers, per-version manifests, and a generated catalog.
Every reference resolves inside the bundle; nothing is fetched from a network.

## Identifiers

Two identifiers do different jobs:

| Identifier | Form | Example |
| --- | --- | --- |
| Capability token (in `contract.json`, one per family major) | `contract: sumpter.<family>/v<major>` | `contract: sumpter.index/v0` |
| Resource `$id` (in every schema file) | `contract://sumpter.<family>/v<version>/<file>` | `contract://sumpter.index/v0.1.2/record-index.schema.json` |

Resource id rules:

- Scheme `contract`, authority `sumpter.<family>` in lower case, and no
  userinfo, port, query, or fragment.
- The first path segment is the exact version and matches the file's version
  directory; the second is the file name. `provenance/v1.json` is the one
  exception to the directory layout; its id is
  `contract://sumpter.provenance/v1/provenance.schema.json`.
- A resource matches its capability by **family and major**, parsed from both
  (`v0.1.2` has major `0`; `v1` has major `1`), not by string prefix.
- Every schema file has an id, and no two share one.

Emitted runtime tokens are separate from resource ids. For example, the
`sumpter.provenance/v1` value in provenance manifests and the
`extract-record-envelope/v0` value in extract records are unchanged.

### Hierarchical and opaque ids

Sumpter resource ids use the hierarchical `contract://` form because some
families, such as `envinfo`, split one document across sibling files joined by
relative `$ref`s. A relative reference resolves against its document's id, and
an opaque id such as `contract:sumpter.envinfo/v0.1.0/complete.schema.json`
has no path to resolve against: `network.schema.json` would become
`contract:///network.schema.json` and lose its family and version. The
[3leaps crucible](https://github.com/3leaps/crucible) contracts use the opaque
`contract:<family>/v0/<file>` form, which is safe there only because no
crucible family has a cross-file reference. The capability tokens follow the
same grammar in both.

### References

- A relative `$ref` resolves within its own family and version, because the
  family is the id's authority: `../` cannot leave it. A relative reference
  that tries to reach another family resolves to an id that is not in the
  bundle and fails the check.
- A reference to another family or version must name an absolute
  `contract://` id, and the target must be listed as a companion in the
  referring directory's manifest.
- `#…` fragments are removed before the check, so a local reference such as
  `#/definitions/x` stays in its own document and a fragment on another
  document is checked against that document's id.

For a schema in `recipes/v0.1.0/` whose manifest lists
`../../extract/v0.1.0/file-signature-schema.yaml` as a companion:

| `$ref` | Resolves to | Result |
| --- | --- | --- |
| `applicability.schema.yaml` | `contract://sumpter.recipes/v0.1.0/applicability.schema.yaml` | passes: same family and version |
| `../../extract/v0.1.0/file-signature-schema.yaml` | `contract://sumpter.recipes/extract/v0.1.0/file-signature-schema.yaml` | fails: a relative reference cannot leave its family |
| `contract://sumpter.extract/v0.1.0/file-signature-schema.yaml` | itself | passes: absolute id of a declared companion |
| `contract://sumpter.extract/v0.1.0/failures.schema.json` | itself | fails: another family and not a declared companion |

## Manifests

Each version directory has a `contract.json` that owns its schema files. The
`capability`, `entry_schema`, and `object_schemas` fields follow the crucible
manifest grammar; `kinds` and `companions` are additional fields.

```json
{
  "capability": "contract: sumpter.recipes/v0",
  "entry_schema": "recipe.schema.yaml",
  "object_schemas": { "applicability": "applicability.schema.yaml" },
  "kinds": { "entry": "input", "applicability": "input" },
  "companions": [
    "../../extract/v0.1.0/file-signature-schema.yaml",
    "../../extract/v0.1.0/extract-record-match-schema.yaml"
  ]
}
```

- `entry_schema` is required: the document a consumer starts from.
- `object_schemas` lists the directory's other schema files by name.
- `kinds` gives `input` or `output` for `entry` and for every
  `object_schemas` name, and nothing else.
- `companions` are paths, relative to the manifest, to schemas in other
  directories that this directory's schemas may reference. They must stay under
  `schemas/`.
- Every schema file is owned by exactly one manifest, the one in its own
  directory, and all manifests for one family major carry the same capability
  token.

Entry schemas:

| Family | Entry schema |
| --- | --- |
| `config` | `sumpter-config.schema.json` |
| `dialects` | `dialect-registry.schema.yaml` |
| `envinfo` | `complete.schema.json` |
| `extract` | `extract-record-envelope.schema.json` |
| `index` | `record-index.schema.json` |
| `inspect` | `inspect-report.schema.yaml` |
| `provenance` | `v1.json` |
| `recipes` | `recipe.schema.yaml` |
| `retrieve` | `retrieve-config.schema.yaml` |

## Catalog

[`schemas/index.json`](../../schemas/index.json) is generated from the bundle
and checked in. Each resource row carries its `id`, `capability`, exact
`version`, `kind`, `path`, and `sha256`. `current` names each family's newest
version and that version's entry schema id.

`current` entries move when a new version is added. Consumers should pin
exact-version resource ids and use `current` only to discover the newest
version.

## Offline Resolution

Before any schema is compiled, every `$ref` in the bundle is resolved against
its document's id and checked: it must equal the id of a resource in the
bundle, stay in its own family and version unless the target is a declared
companion, and use the `contract` scheme. A reference that fails the check is
reported and the schema is never compiled, so no reference can reach a
validator's network loader.

External validators resolve the bundle the same way: register every schema by
its catalog id and do not configure a remote loader. With
[Ajv](https://ajv.js.org/), for example:

```js
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";

const ajv = new Ajv2020({ strict: true });
addFormats(ajv);
for (const resource of catalog.resources) {
  ajv.addSchema(readSchemaFile(resource.path)); // parsed schemas/<path>; its $id equals resource.id
}
const validate = ajv.getSchema("contract://sumpter.envinfo/v0.1.0/complete.schema.json");
```

CI runs this against the envinfo family in
[`tests/interop/ajv`](../../tests/interop/ajv/).

## Checks

```bash
make schema-contract-check   # ids, manifests, references, catalog is current
make schema-catalog          # regenerate schemas/index.json
```

`make schema-contract-check` runs in `make check-all`. It also confirms that the
copy of the config schemas the binary validates configuration against,
`internal/assets/embedded_schemas/config/`, matches `schemas/config/`.
