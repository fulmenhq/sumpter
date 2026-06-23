# Release Notes

This file contains release notes for up to the **three most recent releases** in reverse chronological order. For the complete release history, see the [CHANGELOG](CHANGELOG.md) or the [docs/releases/](docs/releases/) directory.

Retention policy: latest 3 versions inline; older versions retained at `docs/releases/v<semver>.md`.

---

## v0.2.2 (2026-06-23)

**Multi-recipe throughput: apply many extract recipes to one input set in a single parse-once pass, plus the run-level parameters and derive-only capture fields that make it adoptable.**

v0.2.2 is a feature minor driven by real-world dogfeeding of large multi-projection extraction runs. The headline is **`extract-multi`**: when several recipes extract different projections from the **same** input set, the engine now reads and parses each input file once and dispatches the parsed document to every recipe — amortizing the per-recipe re-parse (the dominant cost at high file counts) from ~N× to 1× across N recipes. Two ergonomics features make it adoptable for production pipelines: a shared run-level `--parameter` passthrough, and derive-only `source_extraction` captures. The v0.2.0/0.2.1 surface (cloud I/O, reference-table lookup, list/file-list inputs) is unchanged.

### What's new (summary)

- **Multi-recipe single-pass extract (`extract-multi`)** - `recipes run extract-multi <workspace>...` parses each input file once, then fans the parsed document to every signature-matched recipe. Each recipe writes to its own isolated `<output-path>/<recipe-id>/` subdirectory; per-recipe output, formats, `defaults.parameters`, reference tables, and credential handles come from each recipe's own manifest, while the input set, output root, and run-level controls (and a single shared run id) are shared. Failure handling follows the input-vs-recipe boundary — a read/parse failure is input-level, while applicability/signature/extraction/`min_occurrences`/output failures are recipe-level and isolated under `--continue-on-error`. JSON/NDJSON output only in v0; the streaming/large-file path is not supported (a too-large file is rejected). See the worked example in [`docs/extract-workflow.md`](docs/extract-workflow.md).
- **Shared run-level `--parameter` (`multi-parameter`)** - a repeatable `--parameter key=value` on `extract-multi` layers over **every** recipe's `defaults.parameters` with the same override / collision / typed-value (scalar or JSON-list) semantics as single-recipe `recipes run extract --parameter`, satisfies each recipe's `parameters_required` independently, and is injected into every recipe's records — for the per-run keys every recipe shares (e.g. a per-run provenance/runtime stamp). A shared key colliding with any recipe's `field_mappings[].output_field` fails the run at plan-load preflight; secret-shaped parameter values are redacted by key in the provenance argv.
- **Derive-only `source_extraction` captures (`internal-fields`)** - a `source_extraction` pattern may set `internal: true`, making its named captures usable in `field_mappings[].expression` scope but **never emitted** into the record body on any sink — a true intermediate with no stray column. Defaults to `false`; internal captures still honor `source_extraction_required` and the collision checks. A capture name declared on both an internal and a non-internal pattern is rejected at plan validation.

### Behavior changes (please review before upgrading)

- **All-additive.** `extract-multi` is a new subcommand; the shared `--parameter` and `internal: true` are opt-in. Existing single-recipe `recipes run extract` behavior, output formats, and the provenance sidecar schema are unchanged. A recipe with no `internal: true` pattern and no `extract-multi` run behaves byte-for-byte as in 0.2.1.
- **`internal: true` is an output-shaping control, not a redaction mechanism** - the captured value still lives in expression scope and a recipe author can deliberately re-emit it; do not rely on it to keep a sensitive value out of output.

### Deferred / follow-ups

- **Input-handle provenance in multi runs** (record the effective shared input handle) and a **`relative_path` log-message tidy** are tracked fast-follows.
- **Parquet/other formats and the streaming/large-file path for `extract-multi`**, plus **cloud range-reads, cloud-side indexing, GCS/Azure providers, and repair modes**, remain roadmap items.

### Release notes

- `VERSION` is `0.2.2`. Binaries from this tag emit `v0.2.2` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.2` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.2.md`](docs/releases/v0.2.2.md) for the full release narrative.

---

## v0.2.1 (2026-06-20)

**Two dogfood-driven fixes: present-but-empty string elements bind `""` instead of erroring, and a batch file-list input that skips directory enumeration.**

v0.2.1 is a focused patch off real-world v0.2.0 dogfood feedback. It carries two independent fixes — a present-but-empty XML string element now binds a defined `""` instead of an undefined value, and a new batch file-list input hands the engine an exact file set with no directory walk. The v0.2.0 surface (cloud I/O, reference-table lookup, list-typed parameters) is unchanged.

### What's new (summary)

- **Present-but-empty string elements bind `""` (`empty-element-bind`)** - an XPath string field over a present-but-empty element now binds the empty string (a defined value) instead of undefined, agreeing with `boolean()` presence, so the `has_x ? f(string_x) : default` guard pattern no longer crashes with `undefined variable`. The v0.2.0 doc-note workaround already shipped; this is the real fix.
- **Batch file-list input (`input-prune`)** - `extract --file-list <path>` and recipe `defaults.input.files_from` read a newline-delimited list of input references (local paths or `s3://`/`file://` URIs; `#` comments and blanks ignored), feeding the existing batch path with **no directory walk** and without the `--files` argv ceiling. Relative local entries resolve against the list file's directory; order preserved; unsupported scheme / empty list fail loud with line context. Mutually exclusive with `--files` / `--input-path`. Cloud entries are verified end to end (moto).
- **`--input-path` discovery visibility (`input-prune`)** - directory enumeration now announces its start, reports matched count + elapsed, and warns loudly past a slow threshold; no change to discovery results.

### Behavior changes (please review before upgrading)

- **Present-but-empty binding changed.** A recipe that previously errored `undefined variable` on a present-but-empty element now produces `""` for that field. Absent vs present-but-empty remains distinguishable (`boolean()` is `false` vs `true`); only the present-but-empty _string binding_ changed from undefined to `""`. To reject empty elements, add an explicit guard (`string_length(field) > 0 ? … : …`).
- **No other output-format changes.** The v0.2.0 cloud-I/O, reference-table, list-parameter, and provenance behavior is unchanged; the platform matrix is identical to 0.2.0.

### Deferred

- **File-list single-read refactor**, **`files_from`/`mode` precedence docs-or-validation**, and **directory-prune + streaming discovery** (plus a secondary `--input-glob` shortcut) are tracked follow-ups.
- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, DuckDB/Arrow, service health endpoints, Prometheus metrics, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.2.1`. Binaries from this tag emit `v0.2.1` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.1` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.1.md`](docs/releases/v0.2.1.md) for the full release narrative.

---

## v0.2.0 (2026-06-18)

**S3-compatible cloud I/O, external reference-table lookup, list-typed recipe parameters, and a parallel-extract correctness fix.**

v0.2.0 is the first minor release after the 0.1.x line. It makes S3-compatible cloud I/O first-class across the engine, adds external reference-table lookups and list-typed recipe parameters for richer recipe-driven classification and enrichment, and fixes a parallel-extraction data race — backed by a new race-detector gate in CI. The cloud URI read/write thread deferred since v0.1.8 lands here.

### What's new (summary)

- **Cloud URI I/O (S3-compatible)** - `s3://` sources and outputs (and the provenance sidecar) across `extract`, `index`, recipe runs, and `inspect`, using named credential handles (references, never secrets). Includes parallel/record-index cloud sources, anonymous public-bucket reads (read-only), a truly-dry `--dry-run` (no acquire/stage), and classify+redact of cloud-I/O errors at a single seam. Provenance records the logical source/destination and handle name only.
- **External reference-table lookup** - recipes load reference tables once per run and query them with the `in_reference` (membership) and `lookup_reference` (key→value) DSL functions, from a contained local path or an `s3://` object; CSV/TSV/NDJSON, `max_rows`/`max_bytes` caps, with local-path containment, a cloud pre-read size cap, config-validation pre-flight, and sidecar-only provenance (no row values).
- **List-typed recipe parameters** - parameters may be lists of strings with the `starts_with_any`, `value_in`, and `string_length` predicates; a `--parameter key='["a","b"]'` override supplies a list without a recipe edit.
- **Scoped race-detector CI gate** - `make test-race-parallel` is wired into CI over the parallel-extract and index packages plus the canonical repro.

### Behavior changes (please review before upgrading)

- **Cloud I/O is opt-in and back-compatible.** A run that references no `s3://` URI does no credential or network work; local/bare paths behave byte-for-byte as before.
- **Parallel-extract data races fixed** (detached index-header snapshot + per-worker XPath plans); no output/provenance behavior change, no hot-path locks.
- **Provenance sidecar schema (`sumpter.provenance/v1`)** gained an additive `reference_tables` block; existing manifests remain valid.
- **No output-format changes** to existing local extraction/index paths.

### Deferred

- **Cloud range-reads, cloud-side indexing, GCS/Azure providers, and >5 GiB multipart cloud output** remain follow-on work (an unsupported URI returns an actionable error today).
- **DuckDB, Arrow, service health endpoints, Prometheus metrics, adaptive backpressure, and repair modes** remain roadmap items.

### Release notes

- `VERSION` is `0.2.0`. Binaries from this tag emit `v0.2.0` via `sumpter version`.
- `make release-guard-tag-version SUMPTER_RELEASE_TAG=v0.2.0` is the intended tag/version sanity check.
- The public-surface/confidentiality check remains an out-of-band pre-tag gate before tagging and publication.

See [`docs/releases/v0.2.0.md`](docs/releases/v0.2.0.md) for the full release narrative.
