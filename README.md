# Sumpter

**Crush XML. Haul Data. Ship Insights. Thrive on Scale.**

[![Go Version](https://img.shields.io/badge/go-1.25%2B-blue)]()
[![CI Status](https://github.com/sumpterhq/sumpter/actions/workflows/test.yml/badge.svg)]()
[![License](https://img.shields.io/badge/license-Apache%202.0-green)]()
[![Docker Pulls](https://img.shields.io/docker/pulls/sumpterhq/sumpter)]()

Sumpter is a high-performance, Go-based streaming XML engine that transforms massive, malformed, and variant-heavy XML into clean, analytics-ready tables. With sub-second inspection, auto-generated extraction configs, and resilient outputs to Parquet, DuckDB, or NDJSON, Sumpter helps teams **start fast and thrive on scale**. Built for enterprises where XML still runs the world, Sumpter makes the messy manageable — with speed, safety, and clarity.

---

## 🚀 Quickstart

**Requirements**

- Go 1.25+
- Standard build toolchain

**Build from source**

```bash
# Option A: using Makefile
make build

# Option B: direct go build
go build -o bin/sumpter ./cmd/sumpter
```

**Run Inspect**

```bash
# Markdown report to stdout
bin/sumpter inspect --file ./data/retail_pos.xml --progress

# JSON report to file
bin/sumpter inspect --file ./data/finance_fixml.xml \
  --format json --output ./out/report.json --max-paths 500

# Read from stdin with forced encoding
cat ./data/vendor_sample.xml | bin/sumpter inspect --file - --force-encoding windows-1252
```

---

## 🧰 Environment Info (JSON-first)

Quickly inspect resolved paths, system details, and XML capabilities. All subcommands support `--json` and map to versioned schemas under `schemas/envinfo/v0.1.0/`.

```bash
# Show application paths (home, workdir, cache, logs, configs, temp)
bin/sumpter envinfo paths --json | jq .

# Full environment info (system, vars subset, paths)
bin/sumpter envinfo --json | jq .

# System-only
bin/sumpter envinfo system --json | jq .
```

See `schemas/envinfo/README.md` for details and validation examples.

---

## 🔑 Features

- **Streaming-first**: token-by-token parsing, constant memory profile (<50MB RSS).
- **Encoding resilience**: normalize to UTF-8, handle BOMs and legacy encodings.
- **Structure discovery**: `inspect` surfaces element paths, attributes, and samples.
- **Config generation**: `--generate-config` produces starter YAML with >80% accuracy.
- **Outputs**: NDJSON (Day 3), Parquet & DuckDB (Day 4).
- **Observability**: structured logs, Prometheus metrics, health endpoints.

---

## 📐 Design Principles

- **Performance & Scale**: built for 100MB–10GB XML without DOM crashes.
- **Resilience & Simplicity**: tolerant of malformed and variant-heavy XML.
- **Clarity**: reports and outputs easy for humans and tooling.
- **Observability**: progress, metrics, and logging from Day 1.

See also:

- SOP: `docs/sop/schema-first-sop.md` (JSON-first, schema-validated IO)
- SOP: `docs/sop/logging-sop.md` (stderr-first logging with JSON/pretty)
- ADRs: `docs/architecture/adr/` (design decisions and rationale)

---

## 📂 Project Status

We are in the bootstrap phase, focused on:

- Reliable `inspect` command
- Config generation accuracy
- Extraction to NDJSON, Parquet, DuckDB
- Test corpus strategy (synthetic + hybrid)

See `docs/sumpter_overview.md` for a deeper backgrounder.

Schemas: See `schemas/` for versioned JSON Schemas (SSOT). Inspect JSON conforms to `schemas/inspect-report/v0.1.0/inspect-report.schema.json`. Rendering guidance: `docs/output_rendering.md`.

---

## 🤝 Contributing

We welcome issues and PRs. Please:

- Be constructive and include context/reproduction steps.
- Respect coding and testing standards.
- Security concerns? Open a private advisory or contact maintainers.

Formal `CONTRIBUTING.md` coming soon.

---

## 🏛 Governance & Funding

Sumpter is part of the **FulmenHQ** ecosystem, funded by **3 Leaps**, and maintained by Dave Thompson (`@3leapsdave`) with contributors.

---

## 📜 License

Apache 2.0 (to be finalized).

---

## 🏠 Application Environment

Sumpter uses an enterprise-friendly home/workdir layout with user overrides. See the environment standard for full precedence rules and locations:

See `docs/standards/application-environment.md`.

# Test commit to verify goneat hooks
