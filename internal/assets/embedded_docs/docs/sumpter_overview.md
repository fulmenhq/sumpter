# Sumpter Overview

**Crush XML. Haul Data. Ship Insights. Thrive on Scale.**

---

## 1. Problem Background

Enterprises still rely heavily on XML for transactions, trades, and compliance data. These files are:

- **Massive**: 100MB–10GB+ logs and reports.
- **Variant-heavy**: 5–10 vendor dialects per domain (e.g., competing retail POS dialects, FIXML variants).
- **Malformed**: encoding issues, mixed namespaces, partial truncation.
- **Critical**: used in compliance reporting, finance, and retail operations.

Traditional DOM parsers crash on size. Heavy ETL tools require weeks of configuration. Custom scripts lack resilience, observability, and reuse.

---

## 2. Sumpter’s Solution

Sumpter is a **Go-based streaming XML engine** designed for:

- **Streaming-first performance**: token-by-token parsing with <50MB RSS target.
- **Resilience**: UTF‑8 normalization, BOM handling, namespace strategies, error modes.
- **Config-driven extraction**: YAML-first configs validated against JSON Schema.
- **Inspection and diagnostics**: auto-config generation with >80% accuracy.
- **Analytics-ready outputs**: NDJSON (streaming), Parquet, DuckDB.
- **Observability**: Prometheus metrics, structured logs, health endpoints.

This combination enables teams to move from raw XML to queryable tables **in minutes, not weeks**.

---

## 3. Architecture at a Glance

```
┌───────────────┐   ┌───────────────────┐   ┌───────────────────┐   ┌─────────────────┐
│   Input        │──▶│  Stream Processor │──▶│  Extraction Engine │──▶│   Writers        │
│ (File/Stdin)   │   │ (encoding/xml)    │   │ (XPath, Filters)   │   │ (NDJSON, Parquet │
└───────────────┘   └───────────────────┘   └───────────────────┘   │ DuckDB, Preview) │
                                                                      └─────────────────┘
                        ▲                     │
                        │                     ▼
                  ┌─────────────────────────────────┐
                  │      Observability Layer        │
                  │  Logs • Metrics • Healthchecks  │
                  └─────────────────────────────────┘
```

**Key design choices:**

- **XMLWindow**: lightweight struct with offsets and local names only.
- **Adaptive backpressure**: dynamic buffer sizing based on memory usage.
- **Namespace strategies**: ignore, strict, auto-detect.
- **Error modes**: skip, repair (warned), fail.

---

## 4. Usage Scenarios

### Retail (POS Transaction Journals)

- Inspect POS logs → auto-config → extract transactions.
- Output: `transactions.parquet` for BI queries.

### Finance (FIXML)

- Inspect FIXML allocations → generate config → extract trades.
- Output: `fixml.duckdb` for regulatory analytics.

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

- **Week 2**: Schema evolution tooling; normalized outputs.
- **Week 3**: 200MB/s throughput target; parallel file processing.
- **Week 4**: Enterprise integrations (Kafka, Arrow, Studio UI).

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

Sumpter combines **streaming speed**, **diagnostic clarity**, and **enterprise resilience** to solve the XML integration crisis. With Fulmen’s “Thrive on Scale” ethos at its core, Sumpter is positioned to become the definitive open-source tool for turning XML chaos into analytics-ready data.
