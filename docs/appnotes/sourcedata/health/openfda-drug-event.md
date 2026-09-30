---
title: "openFDA Drug Adverse Event Exports"
description: "Extracting one record per adverse event report from openFDA drug event bulk exports with a record-scoped JSON recipe"
date: "2026-09-29"
author: "Sumpter Team"
tags: ["health", "openfda", "json", "pharmacovigilance"]
---

# openFDA Drug Adverse Event Exports

This application note describes a recipe for the openFDA drug adverse event
bulk exports. It covers the source, the recipe, the routes it runs on, and the
caveats of the data. No openFDA data ships with the repo.

> openFDA states: do not rely on openFDA to make decisions regarding medical
> care. Adverse event reports are unverified and do not establish that a drug
> caused an event.

## Source

| Item | Value |
| --- | --- |
| Publisher | U.S. Food and Drug Administration, openFDA |
| Download index | `https://api.fda.gov/download.json` |
| Partition URL pattern | `https://download.open.fda.gov/drug/event/<YYYYqN>/drug-event-NNNN-of-NNNN.json.zip` |
| Update cadence | Periodic; the download index carries `export_date` per dataset |
| Format | One JSON document per partition, zip-compressed |
| Terms | `https://open.fda.gov/terms/`, license `https://open.fda.gov/license/` |
| Documentation reviewed | 2026-09-29 |

The download index lists every partition of every dataset with its URL, size
and record count. A drug event partition is one JSON document:

```json
{
  "meta": { "disclaimer": "...", "terms": "...", "license": "...", "last_updated": "YYYY-MM-DD" },
  "results": [
    {
      "safetyreportid": "...",
      "receivedate": "YYYYMMDD",
      "serious": "1",
      "seriousnessdeath": "1",
      "primarysource": { "reportercountry": "..." },
      "patient": {
        "patientsex": "...",
        "reaction": [{ "reactionmeddrapt": "..." }],
        "drug": [{ "medicinalproduct": "...", "drugcharacterization": "1" }]
      }
    }
  ]
}
```

Coded values (`serious`, `patientsex`, `drugcharacterization`, …) are strings.
`drugcharacterization` `1` marks a suspect drug.

## Recipe

The recipe pair lives in [`examples/config/extract/`](../../../../examples/config/extract/):

- [`openfda-drug-event-signature.yaml`](../../../../examples/config/extract/openfda-drug-event-signature.yaml)
  — `format_type: json`, `match_scope: record`. Each `results` item is
  admitted when it carries a `safetyreportid`.
- [`openfda-drug-event-extract.yaml`](../../../../examples/config/extract/openfda-drug-event-extract.yaml)
  — one record per `results` item: `safetyreportid`, `receivedate`, `serious`,
  `seriousness_death`, `reporter_country`, `patient_sex`, `reactions` (MedDRA
  preferred terms, in report order), `drug_count`, and `suspect_products`
  (`medicinalproduct` of drugs with `drugcharacterization` `1`).

Coded values are kept as strings, as published.

## Routes

| Route | Eligible | Notes |
| --- | --- | --- |
| Whole document | Yes | Default below the large-file threshold; holds the document in memory |
| Record by record | Yes | Used above the large-file threshold; the signature is record-scoped |
| Record index | Yes | Index selector `results`; parallel workers |

Decompressed partitions can be well above the large-file threshold. Prefer
the record-by-record or record-index route for them rather than
`--allow-large-files`.

## Reproduction

Paths below are placeholders. Pick a partition URL from the download index.

```bash
# 1. Retrieve and decompress one partition (outside sumpter).
curl -fsS -o /path/to/work/drug-event.json.zip \
  https://download.open.fda.gov/drug/event/<YYYYqN>/drug-event-NNNN-of-NNNN.json.zip
unzip -p /path/to/work/drug-event.json.zip > /path/to/work/drug-event.json

# 2. Record-by-record extraction.
sumpter extract files \
  --files /path/to/work/drug-event.json \
  --signature-config-path examples/config/extract/openfda-drug-event-signature.yaml \
  --extract-config-path examples/config/extract/openfda-drug-event-extract.yaml \
  --output-path /path/to/work/out-seq

# 3. Record-index extraction.
sumpter index build /path/to/work/drug-event.json \
  --input-format json -s results -o /path/to/work/drug-event -p=false
sumpter index verify /path/to/work/drug-event.json \
  -i /path/to/work/drug-event.recordindex.json --input-format json --verify-records
sumpter extract files \
  --files /path/to/work/drug-event.json \
  --signature-config-path examples/config/extract/openfda-drug-event-signature.yaml \
  --extract-config-path examples/config/extract/openfda-drug-event-extract.yaml \
  --record-index /path/to/work/drug-event.recordindex.json --workers 4 \
  --output-path /path/to/work/out-idx
```

Record counts can be checked against the partition's `records` value in the
download index. This note does not claim that they match for any particular
partition.

## Caveats

- A JSON `null` binds absent, and so does a missing member. A report without
  `primarysource.reportercountry` has no `reporter_country` field.
- `reactions` and `suspect_products` keep report order and duplicates.
- A record-index JSON record larger than `--max-record-size-mb` (100 MiB by
  default for JSON) fails the input.
- openFDA exports are refreshed. Counts from two exports of the same quarter
  are not expected to match.
