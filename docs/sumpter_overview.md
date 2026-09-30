# Sumpter Overview

**Recipe-driven extraction from XML, JSON, and NDJSON, with route-specific streaming and indexed processing.**

| Input route     | Supported source formats           |
| --------------- | ---------------------------------- |
| Extract files   | `xml`, `json`, `ndjson`            |
| Inspect         | `xml`, `json`                      |
| Record analysis | `xml`                              |
| Record indexes  | `xml`, `json`; uncompressed source |
| Extract-multi   | `xml`                              |

JSON streaming and indexed extraction, and NDJSON extraction, need a record-scoped recipe and an eligible selector and output. NDJSON is not an inspect or index input; JSON and NDJSON record analysis is refused. JSON DOM extraction loads a document; not every route streams. The indexed source document must be uncompressed, but the index may use JSON or optional seekable-zstd storage. The authoritative [input-route matrix](extract-workflow.md#input-route-support) defines eligibility.

---

## 1. Problem Background

Data pipelines consume XML documents, JSON documents, and line-delimited JSON records. These inputs can be:

- **Massive**: 100MB–10GB+ logs and reports (ClinVar releases run multi-GB compressed, multi-TB uncompressed across history).
- **Variant-heavy**: multiple vendor or release dialects per domain (e.g., XBRL taxonomy variants across regulators, ClinVar revisions across releases, FIXML variants across brokerages, POS-journal dialects across vendors).
- **Malformed**: encoding issues, mixed namespaces, partial truncation.
- **Critical**: used in compliance reporting, financial filings, clinical research, regulatory submissions, and operational analytics.

Traditional DOM parsers crash on size. Heavy ETL tools require weeks of configuration. Custom scripts lack resilience, observability, and reuse.

---

## 2. Sumpter’s Solution

Sumpter is a **Go-based, recipe-driven extraction engine** designed for:

- **Route-specific streaming and JSONL output**: token-by-token XML reads where applicable and eligible JSON/NDJSON record parsing. JSON/NDJSON file output streams through record sinks on eligible sequential/indexed routes with bounded output-count state and parallel reordering. Input DOM, active records, nesting and duplicate-key state still cost memory; a file-spanning JSON record or wide object may need input-scale memory. Parquet, mixed-output, sequential `min_occurrences`, and ambiguous indexed floors remain buffered. Indexed JSON records default to a 100 MiB cap; XML's zero limit remains unlimited.
- **Resilience**: XML encoding normalization, UTF-8-only JSON/NDJSON with one leading UTF-8 BOM accepted, and explicit failures for malformed inputs, including duplicate JSON keys and invalid UTF-8.
- **Config-driven extraction**: YAML-first configs validated against JSON Schema.
- **One DSL and XPath grammar**: format-specific XML/JSON twin recipes can yield identical typed `extract.data`; JSON has no attributes or namespaces, and `null` binds absent rather than XML's empty string. Parsing retains number lexemes, but XPath numeric evaluation uses floating point; preserve identifiers above 2^53 with string mappings and `value_text`. See the [document node model](standards/document-node-model.md).
- **Namespace-portable XML recipes**: an opt-in `namespaces:` map binds XPath prefixes to namespace URIs, so an XML recipe extracts the same fields across different literal prefixes or a default namespace. JSON/NDJSON refuse namespace maps and axes.
- **Inspection and diagnostics**: structure reports, encoding detection, and environment diagnostics.
- **Analytics-ready outputs**: JSON/NDJSON records and Parquet projections.
- **Operational visibility**: structured logs and machine-readable command output.
- **Optional portable data-artifact profile**: opt-in artifact descriptors, field
  catalogs, protection declarations, guarded value profiles, and
  `--validate-output` — additive and byte-compatible when unused. See
  [Data-Artifact Producer Profile](data-artifact-producer-profile.md).
- **Optional process-run flight recorder**: for long-running `extract-multi`
  batches, opt-in `process-run/v0` process card and event stream
  (observe-only) so operators can discover the run and read settled progress
  plus the authoritative terminal — with an optional reference-only bridge to
  published data-artifact descriptors. See
  [Process-run producer notes](process-run.md).
- **Derive-only field mappings**: top-level `field_mappings[].internal: true`
  gives same-record helpers without stray columns or portable field-catalog
  entries — compute once, reuse in later expressions, never emit. See
  [Extract workflow](extract-workflow.md).
- **Correct XPath field arithmetic**: predicated `sum(...)` × a context-sensitive
  factor evaluates operands against the right node context — no more
  silent-wrong sign totals. Factor-first authoring notes are in the extract
  workflow guide.

Recipes define the source shape and the emitted fields; they are not unchanged across input formats. Start with the [worked examples](../examples/README.md) and public JSON notes for [USGS](appnotes/sourcedata/science/usgs-geojson.md), [SEC EDGAR](appnotes/sourcedata/finance/sec-edgar-json.md), and [openFDA](appnotes/sourcedata/health/openfda-drug-event.md). These examples are not release-binary scale evidence or cross-feed parity proofs.

Roadmap items such as DuckDB output, service health endpoints, Prometheus metrics,
adaptive backpressure, repair modes, and incremental Parquet writing are tracked
separately from the current public capability surface.

---

## 3. Architecture at a Glance

```
┌───────────────┐   ┌───────────────────┐   ┌───────────────────┐   ┌─────────────────┐
│ Declared Input │──▶│  Selected Route   │──▶│  Extraction Engine │──▶│   Writers        │
│ (XML/JSON/     │   │ (DOM, record,     │   │ (XPath, Filters)   │   │ (JSON/NDJSON,    │
│  NDJSON)       │   │  indexed)         │   │                    │   │                 │
└───────────────┘   └───────────────────┘   └───────────────────┘   │ Parquet)         │
                                                                      └─────────────────┘
                        ▲                     │
                        │                     ▼
                  ┌─────────────────────────────────┐
                  │      Observability Layer        │
                  │  Logs • JSON command output     │
                  └─────────────────────────────────┘
```

**Key design choices:**

- **Explicit routes**: XML streaming, JSON DOM or eligible streaming/indexed records, and NDJSON records; DOM and buffered outputs are not bounded end-to-end.
- **Recipe-owned shape**: extracted fields and output schemas are declared outside the engine.
- **Versioned schemas**: command outputs and recipe formats have explicit schema contracts.
- **Fail-fast safety**: malformed inputs and invalid recipes fail clearly instead of silently repairing data. Stdout/library deliveries are provisional and non-retractable; consumers must handle failure before committing results. File-backed record sinks withhold staged rows on pre-commit input/extraction failure, not every terminal error: later validation/publish failures can leave committed local files, as can earlier completed inputs. See [publication boundaries](extract-workflow.md#recordsink-streaming-contract).

---

## 4. Usage Scenarios

### Retail (POS Transaction Journals)

- Inspect POS logs → auto-config → extract transactions.
- Output: `transactions.parquet` for BI queries.

### Finance (FIXML)

- Inspect FIXML allocations → generate config → extract trades.
- Output: NDJSON or Parquet for regulatory analytics.

### General Enterprise

- Normalize vendor XML into NDJSON for pipeline ingestion.

---

## 5. Test Corpus Strategy

- **Synthetic-first**: 100% synthetic for MVP.
- **Hybrid design**: local small files + S3-hosted large files.
- **Variants included**: malformed, mixed encodings, namespace differences.
- **CI/CD integration**: corpus manifest drives automated tests and benchmarks.

This ensures reproducibility, privacy, and performance validation.

---

## 6. Roadmap

- **Streaming proof expansion**: extend large-corpus evidence for JSON/NDJSON output streaming and add incremental policies for buffered formats.
- **Additional analytics targets**: DuckDB, Arrow, and service integrations.
- **Operational integrations**: metrics, health endpoints, and richer runtime diagnostics.

---

## 7. License Discussion

Sumpter is expected to use **Apache 2.0 License**.

**Why Apache 2.0 over MIT?**

- **Patent grant**: Apache provides explicit patent rights, important for enterprise adoption.
- **Attribution & NOTICE file**: forks must acknowledge original authors, aligning with Fulmen’s attribution philosophy.
- **Dependency compatibility**: Many Go libraries (e.g., parquet-go, duckdb bindings) are Apache-friendly.
- **Service use case**: Protects contributors if code is integrated into commercial SaaS offerings.

MIT is simpler, but lacks explicit patent protection and attribution enforcement. For a project intended to scale into enterprise and service deployments, **Apache 2.0 is the safer, more future-proof choice**.

---

## 8. Summary

Sumpter combines declared input formats, recipe-owned shapes, route-specific streaming/indexing, and traceable outputs. Choose the route from the support matrix and its memory/recipe constraints, not from the file extension or a universal streaming promise.
