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

Replace both placeholder values before running. Take `PARTITION_URL` from
the download index; `WORK_PARENT` is an existing directory outside the
repository. Each run creates its own new directory under `WORK_PARENT` and
writes the download, source, index and outputs only there; nothing already
in `WORK_PARENT` is read, changed or deleted. Run directories are kept for
you to clean up.

The steps run in a subshell with `set -eu`: a failed download, an archive
that does not hold exactly one `.json` member, or any failed command ends
the block with a non-zero status before later steps run. The member is
written to a temporary file that is renamed only after extraction succeeds.

```bash
PARTITION_URL='https://download.open.fda.gov/drug/event/YYYYqN/drug-event-NNNN-of-NNNN.json.zip'
WORK_PARENT='/path/to/work'
(
set -eu
RUN="$(mktemp -d "$WORK_PARENT/drug-event.XXXXXX")"
echo "run directory: $RUN" >&2

# 1. Retrieve one partition and extract its single JSON member (outside
#    sumpter). Stop with a non-zero status unless there is exactly one.
curl -fsS -o "$RUN/drug-event.json.zip" "$PARTITION_URL"
MEMBERS="$(unzip -Z1 "$RUN/drug-event.json.zip")"
if [ "$(printf '%s\n' "$MEMBERS" | wc -l)" -ne 1 ] || [ "${MEMBERS%.json}" = "$MEMBERS" ]; then
  echo "stop: expected exactly one .json member, got: $MEMBERS" >&2
  exit 1
fi
unzip -p "$RUN/drug-event.json.zip" "$MEMBERS" > "$RUN/drug-event.json.part"
mv "$RUN/drug-event.json.part" "$RUN/drug-event.json"

# 2. Record-by-record extraction.
sumpter extract files \
  --files "$RUN"/drug-event.json \
  --signature-config-path examples/config/extract/openfda-drug-event-signature.yaml \
  --extract-config-path examples/config/extract/openfda-drug-event-extract.yaml \
  --output-path "$RUN"/out-seq

# 3. Record-index extraction.
sumpter index build "$RUN"/drug-event.json \
  --input-format json -s results -o "$RUN"/drug-event -p=false
sumpter index verify "$RUN"/drug-event.json \
  -i "$RUN"/drug-event.recordindex.json --input-format json --verify-records
sumpter extract files \
  --files "$RUN"/drug-event.json \
  --signature-config-path examples/config/extract/openfda-drug-event-signature.yaml \
  --extract-config-path examples/config/extract/openfda-drug-event-extract.yaml \
  --record-index "$RUN"/drug-event.recordindex.json --workers 4 \
  --output-path "$RUN"/out-idx
)
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
