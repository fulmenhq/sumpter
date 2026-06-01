# Sumpter Overview

**Crush XML. Haul Data. Ship Insights. Thrive on Scale.**

---

## 1. Problem Background

Enterprises still rely heavily on XML for transactions, trades, and compliance data. These files are:

- **Massive**: 100MB–10GB+ logs and reports (ClinVar releases run multi-GB compressed, multi-TB uncompressed across history).
- **Variant-heavy**: multiple vendor or release dialects per domain (e.g., XBRL taxonomy variants across regulators, ClinVar revisions across releases, FIXML variants across brokerages, POS-journal dialects across vendors).
- **Malformed**: encoding issues, mixed namespaces, partial truncation.
- **Critical**: used in compliance reporting, financial filings, clinical research, regulatory submissions, and operational analytics.

Traditional DOM parsers crash on size. Heavy ETL tools require weeks of configuration. Custom scripts lack resilience, observability, and reuse.

---

## 2. Sumpter’s Solution

Sumpter is a **Go-based XML extraction engine** designed for:

- **Streaming input parsing**: token-by-token XML reads without loading whole documents; extracted records are buffered per file before output until the post-v0.1.6 record-sink refactor lands.
- **Resilience**: UTF-8 normalization, BOM handling, and explicit fail-fast behavior for malformed inputs.
- **Config-driven extraction**: YAML-first configs validated against JSON Schema.
- **Inspection and diagnostics**: structure reports, encoding detection, and environment diagnostics.
- **Analytics-ready outputs**: JSON/NDJSON records and Parquet projections.
- **Operational visibility**: structured logs and machine-readable command output.

This combination enables teams to move from raw XML to queryable tables **in minutes, not weeks**.

Roadmap items such as DuckDB output, service health endpoints, Prometheus metrics,
adaptive backpressure, repair modes, and end-to-end output streaming are tracked
separately from the current public capability surface.

---

## 3. Architecture at a Glance

```
┌───────────────┐   ┌───────────────────┐   ┌───────────────────┐   ┌─────────────────┐
│   Input        │──▶│  Stream Processor │──▶│  Extraction Engine │──▶│   Writers        │
│ (File/Stdin)   │   │ (encoding/xml)    │   │ (XPath, Filters)   │   │ (JSON/NDJSON,    │
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

- **Input streaming first**: parse XML incrementally and avoid DOM-scale memory growth.
- **Recipe-owned shape**: extracted fields and output schemas are declared outside the engine.
- **Versioned schemas**: command outputs and recipe formats have explicit schema contracts.
- **Fail-fast safety**: malformed inputs and invalid recipes fail clearly instead of silently repairing data.

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

- **Record-sink streaming**: reduce output buffering and clarify end-to-end memory bounds.
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

Sumpter combines **streaming speed**, **diagnostic clarity**, and **enterprise resilience** to solve the XML integration crisis. With Fulmen’s “Thrive on Scale” ethos at its core, Sumpter is positioned to become the definitive open-source tool for turning XML chaos into analytics-ready data.
