# Sumpter

**Crush XML. Haul Data. Ship Insights. Thrive on Scale.**

[![Go Version](https://img.shields.io/badge/go-1.26%2B-blue)]()
[![CI Status](https://github.com/fulmenhq/sumpter/actions/workflows/ci.yml/badge.svg)](https://github.com/fulmenhq/sumpter/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache%202.0-green)]()
[![Docker Pulls](https://img.shields.io/docker/pulls/sumpterhq/sumpter)]()

Sumpter is a streaming XML extraction engine for the inputs where the obvious tools break: large files, variant-heavy schemas, and recipe-driven outputs to NDJSON or Parquet. It's format-agnostic — the engine bakes in no vertical's schemas or record types; the shapes you extract live in recipes you author.

---

## 🧭 Why Sumpter?

Sumpter is the streaming XML extraction engine for production pipelines: gigabyte-class regulatory filings (think XBRL on the scale of SEC EDGAR), variant-heavy scientific XML where the schema is more guideline than contract, and any pipeline that needs reproducible recipe-driven extraction into NDJSON or Parquet with reconciliation primitives built in. If that's the shape of what you process, Sumpter is built for you. If you're doing ad-hoc XML inspection on small files, `xmlstarlet` or `xq` are still the fast answer — and we'll point you there.

---

## ⏱️ See it in 30 seconds

Inspect an XML file, run an extraction recipe against it, and read back structured records — using only the bundled `examples/` corpus, no external data required:

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

---

## 📂 Project status: alpha

Sumpter is in **alpha** — for us that's about _interface stability_, not maturity. The CLI surface, recipe schema, and DSL may still change between releases as we converge on stable contracts. The engine runs real extraction workloads, gates every change behind tests (coverage thresholds rise from the alpha 50% baseline toward beta), and ships on a clean `govulncheck` security baseline.

**What alpha means for you:** pin a version, skim the release notes before upgrading, and expect occasional breaking changes to recipes or flags. **What it doesn't mean:** that Sumpter is untested or unused.

**Contributions are welcome** — issues, design discussion, and pull requests. See [CONTRIBUTING.md](CONTRIBUTING.md); for anything beyond a small fix, open an issue first so we can point you at in-flight work. The road to beta is about freezing the recipe/DSL/adapter contracts and raising coverage, not about whether the core works.

**Memory contract:** XML input is tokenized incrementally where the streaming path applies. JSON/NDJSON file output is bounded with respect to emitted result count for sequential runs and record-index parallel runs: records stream through `RecordSink`, and the parallel route uses bounded reorder/backpressure instead of retaining the full output slice. Unambiguous record-index parallel runs enforce `min_occurrences` from index counts before publishing output and can still use the streaming route. This does not make every extract mode bounded end-to-end: DOM/non-streaming input can still load a document, and Parquet, mixed JSON+Parquet, sequential `min_occurrences`, and ambiguous indexed floors intentionally remain buffered. See [ADR-0005](docs/architecture/adr/0005-hybrid-streaming-xml-architecture.md) and [ADR-0009](docs/architecture/adr/0009-record-sink-output-streaming-contract.md).

Security patches target the latest `0.3.x` release; see [SECURITY.md](SECURITY.md) for the supported-versions matrix and private reporting. For governance, see [MAINTAINERS.md](MAINTAINERS.md).

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

**Inspect XML Structure**

```bash
# Analyze XML structure
./dist/sumpter inspect ./examples/data/sample-widget-order.xml --progress

# JSON report
./dist/sumpter inspect ./examples/data/sample-widget-order.xml --format json
```

**Build and Use Record Indexes**

```bash
# Build index for parallel extraction
./dist/sumpter index build large-file.xml \
  --selector "//Record" \
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

Quickly inspect resolved paths, system details, and XML capabilities. All subcommands support `--json` and map to versioned schemas under `schemas/envinfo/v0.1.0/`.

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

The repository ships a corpus of self-contained, runnable examples — synthetic WidgetCo/GearCo orders that double as recipe-authoring references and extraction smoke tests. Start at [`examples/README.md`](examples/README.md) for the full case-by-feature index, or run them all with `make examples`.

The public-data exemplars are deliberately drawn from **five different verticals** to show the engine is domain-neutral: **financial filings** (SEC EDGAR XBRL), **genomics** (NCBI ClinVar variant archives), **geophysics** (USGS QuakeML seismic catalogs), **public-safety geospatial** (NWS CAP alerts), and **government/legal** (GovInfo USLM bills) — all public-domain, so every example ships runnable by anyone. ClinVar's ~50 GB release is sumpter's canonical scale test — it drove the streaming and seekable-index architecture. See [`docs/user-guide/public-data-examples.md`](docs/user-guide/public-data-examples.md) for acquisition and recipes, the [SEC EDGAR XBRL walkthrough](docs/appnotes/sourcedata/finance/sec-edgar-usage.md), and the [ClinVar parallel-extraction runbook](docs/runbooks/clinvar-parallel.md).

---

## 🔑 Features

- **Streaming input parsing and JSONL output**: Gigabyte-class XML inputs are tokenized incrementally where the streaming path applies, and JSON/NDJSON file output streams records through the record-sink path for sequential runs and record-index parallel runs with memory bounded by parser state, active record work, writer buffers, and the configured reorder window for parallel runs. Parquet, mixed-output, sequential `min_occurrences`, and ambiguous indexed-floor paths remain buffered in v0.2.0.
- **Record Indexing**: Build seekable indexes for parallel extraction of multi-GB XML files
- **Compressed Indexes**: Seekable-zstd format reduces index size 10-20x with O(1) random access
- **Parallel Extraction**: Worker pools seek directly to record offsets without parsing predecessors
- **Multi-recipe single-pass extraction**: apply many extract recipes to one input set in a single parse-once pass (`recipes run extract-multi`) — each input file is read and parsed once, then fanned to every recipe, with isolated per-recipe output trees. See [Run multiple recipes in one pass](docs/extract-workflow.md#run-multiple-recipes-in-one-pass-extract-multi).
- **Aggregate output mode**: stream one NDJSON file per recipe across many inputs (`--output-mode aggregate`) instead of one file per input — deterministic ordering (aggregate ordinals follow `--file-list` order), rolling shards (`--aggregate-max-records` / `--aggregate-max-bytes`), and per-shard provenance digests, for both local and `s3://` destinations. See [Aggregate output mode](docs/extract-workflow.md#aggregate-output-mode---output-mode-aggregate).
- **Parallel input processing at scale**: spread an `extract-multi` run across N workers with `--input-workers N` to process **thousands of input files and beyond** concurrently — each worker handles an input's parse plus its full per-recipe application, while a single ordered committer keeps output **byte-identical at every worker count**. Size it by measuring with `--stats` rather than by core count. See [Parallel input processing](docs/extract-workflow.md#parallel-input-processing-with---input-workers).
- **Encoding resilience**: Normalize to UTF-8, handle BOMs and legacy encodings
- **Structure discovery**: `inspect` surfaces element paths, attributes, and samples
- **Integrity verification**: SHA-256 checksums at file and record level
- **Cloud sources and outputs**: read source data from and publish results to S3-compatible object storage (`s3://`), with credential handles (no secrets in recipe YAML). See [Cloud Sources and Outputs](docs/extract-workflow.md#cloud-sources-and-outputs-s3-compatible).
- **Reference-table lookup**: recipes load external reference tables once per run and query them from field mappings — `in_reference` (membership) and `lookup_reference` (key→value enrichment) — from a contained local path or an `s3://` object. See [Reference Tables](docs/extract-workflow.md#reference-tables) and the [DSL reference](docs/dsl-reference.md).
- **List-typed recipe parameters**: parameters can be lists of strings with `starts_with_any`, `value_in`, and `string_length` predicates for set-based classification.
- **Observability**: Structured logs, progress tracking, and diagnostics
- **Portable data-artifact profile (opt-in)**: emit host-less `data-artifact/v0` descriptors and field catalogs, protection floors with Parquet page-metadata suppression, an opt-in `--validate-output` ladder, and a guarded provenance `value_profile` — all byte-compatible when unused. See [Data-Artifact Producer Profile](docs/data-artifact-producer-profile.md).
- **Process-run flight recorder (opt-in)**: for long-running `extract-multi` batches, publish a host-less `process-run/v0` process card and append-only NDJSON stream so operators can discover the run, follow settled progress, and read the authoritative terminal — with an optional reference-only bridge to published data-artifact descriptors. Additive telemetry under a platform runtime directory; default extract paths unchanged when unused. See [Process-run producer notes](docs/process-run.md).

---

## 📐 Design Principles

- **Performance & Scale**: built for 100MB–10GB XML without DOM crashes, and for high-volume runs over thousands of input files with configurable worker parallelism.
- **Resilience & Simplicity**: tolerant of malformed and variant-heavy XML.
- **Clarity**: reports and outputs easy for humans and tooling.
- **Observability**: progress, metrics, and logging from Day 1.

See also:

- SOP: `docs/sop/schema-first-sop.md` (JSON-first, schema-validated IO)
- SOP: `docs/sop/logging-sop.md` (stderr-first logging with JSON/pretty)
- ADRs: `docs/architecture/adr/` (design decisions and rationale)

---

## 📦 Capabilities

Available today:

- ✅ XML inspection and structure discovery
- ✅ Record indexing with byte offsets and checksums
- ✅ Seekable-zstd compressed indexes (10-20x smaller, CGO/source builds)
- ✅ Parallel extraction with worker pools
- ✅ Streaming mode for very large XML files
- ✅ Sequential NDJSON output with sidecar manifests and record-sink streaming
- ✅ Parquet secondary output (buffered in v0.2.0)
- ✅ Recipe applicability gates and schema-backed dispositions
- ✅ Multi-file continue-on-error failure manifests
- ✅ Document-order `_runtime.record_num` semantics for single-selector extraction
- ✅ Record-sink streaming contract and sequential sink primitives
- ✅ Streaming record-index writers during index build
- ✅ Multi-recipe single-pass extraction (`extract-multi`) — parse each input once, fan to every recipe
- ✅ Parallel input processing for `extract-multi` (`--input-workers`) — concurrent across many inputs, byte-identical at every worker count, tunable with `--stats`
- ✅ Aggregate output mode (`--output-mode aggregate`) — one streamed NDJSON file per recipe, local or `s3://`, with rolling shards + per-shard digests
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
