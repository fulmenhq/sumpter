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

## 🔑 Features

- **Streaming-first**: Token-by-token parsing with constant memory profile (<50MB RSS)
- **Record Indexing**: Build seekable indexes for parallel extraction of multi-GB XML files
- **Compressed Indexes**: Seekable-zstd format reduces index size 10-20x with O(1) random access
- **Parallel Extraction**: Worker pools seek directly to record offsets without parsing predecessors
- **Encoding resilience**: Normalize to UTF-8, handle BOMs and legacy encodings
- **Structure discovery**: `inspect` surfaces element paths, attributes, and samples
- **Integrity verification**: SHA-256 checksums at file and record level
- **Observability**: Structured logs, progress tracking, and diagnostics

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

**Current Version:** v0.1.2 (Alpha)

Core capabilities available:
- ✅ XML inspection and structure discovery
- ✅ Record indexing with byte offsets and checksums
- ✅ Seekable-zstd compressed indexes (10-20x smaller)
- ✅ Parallel extraction with worker pools
- ✅ Streaming mode for 50GB+ files
- 🔄 NDJSON output (in progress)
- 🔜 Parquet & DuckDB outputs (planned)

See `docs/releases/` for detailed release notes and `docs/user-guide/` for workflow documentation.

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
