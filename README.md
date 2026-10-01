# Sumpter

**Recipe-driven extraction from XML, JSON, and NDJSON, with route-specific streaming and indexed processing.**

[![Go Version](https://img.shields.io/badge/go-1.26%2B-blue)](https://go.dev/doc/install)
[![CI Status](https://github.com/fulmenhq/sumpter/actions/workflows/ci.yml/badge.svg)](https://github.com/fulmenhq/sumpter/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-green)](LICENSE)
[![Docker Pulls](https://img.shields.io/docker/pulls/sumpterhq/sumpter)](https://hub.docker.com/r/sumpterhq/sumpter)

Sumpter turns declared input formats into recipe-authored records for NDJSON or Parquet pipelines. The engine is domain-neutral: it bakes in no vertical's schemas or record types. XML and JSON use one DSL and XPath grammar, with format-specific recipes rather than one unchanged recipe.

| Input route     | Supported source formats           |
| --------------- | ---------------------------------- |
| Extract files   | `xml`, `json`, `ndjson`            |
| Inspect         | `xml`, `json`                      |
| Record analysis | `xml`                              |
| Record indexes  | `xml`, `json`; uncompressed source |
| Extract-multi   | `xml`                              |

JSON streaming and indexed extraction, and NDJSON extraction, need a record-scoped recipe and an eligible selector and output. NDJSON is not an inspect or index input; JSON and NDJSON record analysis is refused. JSON DOM extraction loads a document; not every route streams. An indexed **source document** must be uncompressed; its index may use JSON storage or optional seekable-zstd storage. See the authoritative [input-route matrix and eligibility rules](docs/extract-workflow.md#input-route-support).

---

## 🧭 Why Sumpter?

Sumpter is built for reproducible recipe-driven pipelines: large XML and JSON documents, line-delimited event records, and variant-heavy feeds, with reconciliation primitives built in. If you're doing ad-hoc inspection on small files, `xmlstarlet` or `xq` for XML and `jq` for JSON may be the faster answer.

---

## ⏱️ See it in 30 seconds

Inspect a bundled WidgetCo order, run an extraction recipe, and read back structured records — no external data required. XML is the default inspect format; JSON needs `--input-format json`.

```console
$ sumpter inspect examples/cases/01-basic-extraction/input.xml
# XML Inspection Report
#
# Encoding: WINDOWS-1252
#
# ## Top Paths
# | Path                                  | Count | Attributes |
# |---------------------------------------|-------|------------|
# | WidgetCoData.Orders.Order             | 1     | 2          |
# | WidgetCoData.Orders.Order.Customer    | 1     | 0          |
# | WidgetCoData.Orders.Order.TotalAmount | 1     | 0          |

$ sumpter recipes run extract examples/cases/01-basic-extraction/recipe \
    --files examples/cases/01-basic-extraction/input.xml \
    --output-path out/

$ jq .extract.data out/records.jsonl
{
  "customer": "WidgetCo North",
  "order_id": "ORDER-1001",
  "status": "open",
  "total_amount": 42.5
}
```

<sub>Recorded with Sumpter v0.1.8 (alpha) against the bundled synthetic corpus. Pass <code>--log-level error</code> to silence startup logs as shown.</sub>

The same order as JSON (case 14) uses a format-specific recipe (`format json`, members instead of attributes) and yields the same `extract.data`:

```console
$ sumpter inspect examples/cases/14-json-basic-extraction/input.json --input-format json
# JSON Inspection Report
#
# Encoding: UTF-8
#
# ## Top Paths
# | Path                                  | Count | Attributes |
# |---------------------------------------|-------|------------|
# | WidgetCoData.Orders.Order             | 1     | 0          |
# | WidgetCoData.Orders.Order.Customer    | 1     | 0          |
# | WidgetCoData.Orders.Order.TotalAmount | 1     | 0          |

$ sumpter recipes run extract examples/cases/14-json-basic-extraction/recipe \
    --files examples/cases/14-json-basic-extraction/input.json \
    --output-path out/

$ jq .extract.data out/records.jsonl
{
  "customer": "WidgetCo North",
  "order_id": "ORDER-1001",
  "status": "open",
  "total_amount": 42.5
}
```

Line-delimited JSON is case 15 (`--variant ndjson`). See [JSON and NDJSON extraction](docs/user-guide/json-extraction.md).

---

## 📂 Project status: alpha

Sumpter is in **alpha** — for us that's about _interface stability_, not maturity. The CLI surface, recipe schema, and DSL may still change between releases as we converge on stable contracts. The engine runs real extraction workloads, gates every change behind tests (coverage thresholds rise from the alpha 50% baseline toward beta), and ships on a clean `govulncheck` security baseline.

**What alpha means for you:** pin a version, skim the release notes before upgrading, and expect occasional breaking changes to recipes or flags. **What it doesn't mean:** that Sumpter is untested or unused.

**Contributions are welcome** — issues, design discussion, and pull requests. See [CONTRIBUTING.md](CONTRIBUTING.md); for anything beyond a small fix, open an issue first so we can point you at in-flight work. The road to beta is about freezing the recipe/DSL/adapter contracts and raising coverage, not about whether the core works.

**Memory contract:** XML input is tokenized incrementally where the streaming path applies. Eligible JSON/NDJSON inputs are read record by record. JSON/NDJSON **file output** is bounded with respect to emitted result count for sequential runs and record-index parallel runs: records stream through `RecordSink`, and the parallel route uses bounded reorder/backpressure instead of retaining the full output slice. Unambiguous record-index parallel runs enforce `min_occurrences` from index counts before publishing output and can still use the streaming route. This is not an end-to-end flat-memory promise: DOM input loads a document; record routes retain active record/parser/writer state, and a file-spanning JSON record or a wide object can need input-scale memory. Parquet, mixed JSON+Parquet, sequential `min_occurrences`, and ambiguous indexed floors intentionally remain buffered. The indexed JSON record cap defaults to 100 MiB; XML's zero limit remains unlimited. See the [document node model](docs/standards/document-node-model.md), [ADR-0005](docs/architecture/adr/0005-hybrid-streaming-xml-architecture.md), and [ADR-0009](docs/architecture/adr/0009-record-sink-output-streaming-contract.md).

Security patches target the latest `0.4.x` release; see [SECURITY.md](SECURITY.md) for the supported-versions matrix and private reporting. For governance, see [MAINTAINERS.md](MAINTAINERS.md).

---

## 🚀 Quickstart

**Homebrew** (macOS arm64, Linux)

```bash
brew install fulmenhq/tap/sumpter
```

**Scoop** (Windows)

```bash
scoop bucket add fulmenhq https://github.com/fulmenhq/scoop-bucket
scoop install fulmenhq/sumpter
```

**Prebuilt binaries**

Every release publishes raw binaries for five OS/arch targets — each with `SHA256SUMS`/`SHA512SUMS` checksums plus GPG and minisign signatures — on the [releases page](https://github.com/fulmenhq/sumpter/releases/latest):

| OS      | amd64                       | arm64                       |
| ------- | --------------------------- | --------------------------- |
| Linux   | `sumpter-linux-amd64`       | `sumpter-linux-arm64`       |
| macOS   | —                           | `sumpter-darwin-arm64`      |
| Windows | `sumpter-windows-amd64.exe` | `sumpter-windows-arm64.exe` |

Download the binary for your platform, verify it against the published checksums, and put it on your `PATH`. Or install the latest tagged version with Go:

```bash
go install github.com/fulmenhq/sumpter/cmd/sumpter@latest
```

Intel Macs: build from source — `go install github.com/fulmenhq/sumpter/cmd/sumpter@latest` (or `make build`) produces a native `darwin-amd64` binary. The `darwin-amd64` prebuilt and the Homebrew formula were retired/scoped to arm64 in v0.1.10. The prebuilt binaries are CGO-free (the seekable-zstd compressed-index path needs a source build with `CGO_ENABLED=1 -tags seekablezstd`).

**Requirements**

- Go 1.26+
- Standard build toolchain
- (Optional) CGO for seekable-zstd compressed indexes

**Build from source**

```bash
# Standard build (JSON indexes only)
make build

# Build with seekable-zstd support (requires CGO)
CGO_ENABLED=1 go build -tags seekablezstd -o dist/sumpter ./cmd/sumpter
```

**Inspect XML or JSON**

```bash
# Analyze XML structure
./dist/sumpter inspect ./examples/data/sample-widget-order.xml --progress

# JSON report of an XML inspect
./dist/sumpter inspect ./examples/data/sample-widget-order.xml --format json

# Inspect a JSON document (input format is declared, never inferred)
./dist/sumpter inspect ./examples/cases/14-json-basic-extraction/input.json \
  --input-format json
```

**Build and Use Record Indexes**

```bash
# Build index for parallel extraction
./dist/sumpter index build large-file.xml \
  --selector "//Record" \
  --progress

# JSON source: declare --input-format json; the selector names a key
./dist/sumpter index build events.json \
  --input-format json \
  --selector "results" \
  --progress

# Build compressed index (10-20x smaller, requires CGO build)
./dist/sumpter index build large-file.xml \
  --selector "//Record" \
  --emit-szst

# Verify index integrity
./dist/sumpter index verify large-file.xml --index large-file.recordindex.json

# Extract with parallel workers
./dist/sumpter extract files \
  --record-index large-file.recordindex.json \
  --workers 8 \
  --output-path outputs/
```

---

## 🧰 Environment Info (JSON-first)

Quickly inspect resolved paths and system details. Ordinary human `envinfo` also shows the built-in input-route support matrix; it is not a runtime probe. `envinfo --xml` and `envinfo xml` describe XML capabilities, whose `<50MB RSS` figure is an XML input-tokenization design target, not a measured run or a JSON bound. Machine payloads remain under `schemas/envinfo/v0.1.0/`; the human route section is not added to JSON/export or single-purpose subcommands.

```bash
# Show application paths (home, workdir, cache, logs, configs, temp)
./dist/sumpter envinfo paths --json | jq .

# Full environment info (system, vars subset, paths)
./dist/sumpter envinfo --json | jq .

# System-only
./dist/sumpter envinfo system --json | jq .
```

See `schemas/envinfo/README.md` for details and validation examples.

---

## 🔎 Explore the examples

The repository ships self-contained synthetic WidgetCo/GearCo examples, including XML/JSON twins and NDJSON cases. Twins use format-specific recipes and compare typed `extract.data`, not their format-specific provenance. Start with [JSON and NDJSON extraction](docs/user-guide/json-extraction.md) or [`examples/README.md`](examples/README.md), or run them all with `make examples`.

Numbers retain source lexemes while parsing, but XPath 1.0 numeric evaluation uses floating point: use string mappings and `value_text` for exact identifiers above 2^53. JSON `null` binds absent; see the [node model](docs/standards/document-node-model.md#scalars).

Optional public-domain recipes (XML and JSON) are listed in [`docs/user-guide/public-data-examples.md`](docs/user-guide/public-data-examples.md). Those notes are recipe-authoring examples, not cross-feed parity or release-binary scale measurements. The [ClinVar parallel-extraction runbook](docs/runbooks/clinvar-parallel.md) is the XML scale walkthrough.

---

## 🔑 Features

- **Route-specific input and output streaming**: XML tokenization and eligible JSON/NDJSON record parsing are separate from JSONL output streaming. See the memory contract above; Parquet, mixed-output, sequential `min_occurrences`, and ambiguous indexed-floor paths remain buffered.
- **Record Indexing**: Build source-byte indexes over uncompressed XML or JSON for indexed and parallel extraction; NDJSON indexing is refused
- **Compressed Indexes**: Seekable-zstd format reduces index size 10-20x with O(1) random access
- **Parallel Extraction**: Worker pools seek directly to record offsets without parsing predecessors
- **Multi-recipe single-pass extraction (XML only)**: apply many extract recipes to one input set in a single parse-once pass (`recipes run extract-multi`) — each input file is read and parsed once, then fanned to every recipe, with isolated per-recipe output trees. See [Run multiple recipes in one pass](docs/extract-workflow.md#run-multiple-recipes-in-one-pass-extract-multi).
- **Aggregate output mode**: stream one NDJSON file per recipe across many inputs (`--output-mode aggregate`) instead of one file per input — deterministic ordering (aggregate ordinals follow `--file-list` order), rolling shards (`--aggregate-max-records` / `--aggregate-max-bytes`), and per-shard provenance digests, for both local and `s3://` destinations. Local aggregate commits are crash-durable by default; `--emit-input-identity` optionally binds each row to its input ordinal and parsed-byte SHA-256. See [Aggregate output mode](docs/extract-workflow.md#aggregate-output-mode---output-mode-aggregate).
- **Integrity-bound batch inputs**: `--file-list` and recipe `defaults.input.files_from` accept URI-only lines or strict JSON object lines with `uri`, `size`, and `sha256`; declared bytes are verified against the private snapshot used for parsing and fail closed on mismatch. See [Input selection](docs/extract-workflow.md#input-selection-batch-lists-directories-large-trees).
- **Provenance-root input paths**: optionally pin local inputs to a provenance root. See [Opt-in root-relative input provenance](docs/extract-workflow.md#opt-in-root-relative-input-provenance).
- **Parallel input processing at scale**: spread an `extract-multi` run across N workers with `--input-workers N` to process **thousands of input files and beyond** concurrently — each worker handles an input's parse plus its full per-recipe application, while a single ordered committer keeps output **byte-identical at every worker count**. Size it by measuring with `--stats` rather than by core count. See [Parallel input processing](docs/extract-workflow.md#parallel-input-processing-with---input-workers).
- **Encoding handling**: XML legacy encodings normalize to UTF-8; JSON/NDJSON require UTF-8 and accept one leading UTF-8 BOM
- **Structure discovery**: `inspect` surfaces XML paths/attributes or JSON key paths/value kinds, with bounded samples
- **Integrity verification**: SHA-256 checksums at file and record level
- **Cloud sources and outputs**: read source data from and publish results to S3-compatible object storage (`s3://`), with credential handles (no secrets in recipe YAML). See [Cloud Sources and Outputs](docs/extract-workflow.md#cloud-sources-and-outputs-s3-compatible).
- **Reference-table lookup**: recipes load external reference tables once per run and query them from field mappings — `in_reference` (membership) and `lookup_reference` (key→value enrichment) — from a contained local path or an `s3://` object. See [Reference Tables](docs/extract-workflow.md#reference-tables) and the [DSL reference](docs/dsl-reference.md).
- **List-typed recipe parameters**: parameters can be lists of strings with `starts_with_any`, `value_in`, and `string_length` predicates for set-based classification.
- **Observability**: Structured logs, progress tracking, and diagnostics
- **Portable data-artifact profile (opt-in)**: emit host-less `data-artifact/v0` descriptors and field catalogs, protection floors with Parquet page-metadata suppression, an opt-in `--validate-output` ladder, and a guarded provenance `value_profile` — all byte-compatible when unused. See [Data-Artifact Producer Profile](docs/data-artifact-producer-profile.md).
- **Process-run flight recorder (opt-in)**: for long-running `extract-multi` batches, publish a host-less `process-run/v0` process card and append-only NDJSON stream so operators can discover the run, follow settled progress, and read the authoritative terminal — with an optional reference-only bridge to published data-artifact descriptors. Additive telemetry under a platform runtime directory; default extract paths unchanged when unused. See [Process-run producer notes](docs/process-run.md).

---

## 📐 Design Principles

- **Performance & Scale**: route-specific streaming/indexing and configurable worker parallelism, with explicit DOM and buffered-output limits rather than universal memory promises.
- **Resilience & Simplicity**: explicit format declarations and fail-loud parsing; malformed JSON is refused, not repaired.
- **Clarity**: reports and outputs easy for humans and tooling.
- **Observability**: progress, metrics, and logging from Day 1.

See also:

- SOP: `docs/sop/schema-first-sop.md` (JSON-first, schema-validated IO)
- SOP: `docs/sop/logging-sop.md` (stderr-first logging with JSON/pretty)
- ADRs: `docs/architecture/adr/` (design decisions and rationale)

---

## 📦 Capabilities

Available today:

- ✅ XML/JSON inspection and structure discovery; record analysis remains XML only
- ✅ XML/JSON record indexing with source-byte offsets and checksums
- ✅ Seekable-zstd compressed indexes (10-20x smaller, CGO/source builds)
- ✅ Parallel extraction with worker pools
- ✅ XML streaming plus eligible record-scoped JSON streaming and NDJSON extraction
- ✅ Sequential NDJSON output with sidecar manifests and record-sink streaming
- ✅ Parquet secondary output (buffered)
- ✅ Recipe applicability gates and schema-backed dispositions
- ✅ Multi-file continue-on-error failure manifests
- ✅ Document-order `_runtime.record_num` semantics for single-selector extraction
- ✅ Record-sink streaming contract and sequential sink primitives
- ✅ Streaming record-index writers during index build
- ✅ Multi-recipe single-pass extraction (`extract-multi`, XML only) — parse each input once, fan to every recipe
- ✅ Parallel input processing for `extract-multi` (`--input-workers`) — concurrent across many inputs, byte-identical at every worker count, tunable with `--stats`
- ✅ Aggregate output mode (`--output-mode aggregate`) — one streamed NDJSON file per recipe, local or `s3://`, with rolling shards + per-shard digests; local commits are crash-durable by default and row-to-input identity is opt-in
- ✅ Integrity-bound `--file-list` / `defaults.input.files_from` JSON object lines (`uri`, `size`, `sha256`) with fail-closed parsed-byte verification
- ✅ S3-compatible cloud (`s3://`) sources and outputs with named credential handles
- ✅ External reference-table lookup (membership + key→value enrichment)
- ✅ List-typed recipe parameters with set-classification predicates
- ✅ Portable data-artifact producer profile (opt-in descriptors, catalogs, protection floors, validate ladder, guarded `value_profile`)
- ✅ Process-run flight recorder for long-running `extract-multi` (opt-in process card, event stream, optional terminal → data-artifact bridge)
- ✅ Derive-only field mappings (`field_mappings[].internal: true`) — same-record helpers without stray columns or portable field-catalog entries
- ✅ Correct XPath field arithmetic for predicated sum × context-sensitive factor (no more silent-wrong sign totals)
- 🔜 DuckDB output (planned)

See `docs/releases/` for detailed release notes and `docs/user-guide/` for workflow documentation.

---

## 🤝 Contributing

Contributions are welcome — issues, design discussion, and pull requests. Sumpter is in alpha and the surface is still moving, so for anything beyond a small, self-contained fix please open an issue first and we'll point you at in-flight work. See [CONTRIBUTING.md](CONTRIBUTING.md) for details and the road to beta, and [SECURITY.md](SECURITY.md) for reporting vulnerabilities privately.

---

## 🏛 Governance & Funding

Sumpter is part of the **FulmenHQ** ecosystem, funded by **3 Leaps**, and maintained by Dave Thompson (`@3leapsdave`) with contributors.

---

## 📜 License

Apache 2.0.

---

## 🏠 Application Environment

Sumpter uses an enterprise-friendly home/workdir layout with user overrides. See the environment standard for full precedence rules and locations:

See `docs/standards/application-environment.md`.
