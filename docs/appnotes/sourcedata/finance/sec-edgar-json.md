---
title: "SEC EDGAR JSON APIs"
description: "Extracting XBRL facts from SEC companyfacts and filing history from SEC submissions with JSON recipes"
date: "2026-09-29"
author: "Sumpter Team"
tags: ["finance", "sec-edgar", "json", "xbrl"]
---

# SEC EDGAR JSON APIs

This application note describes two recipes for the SEC EDGAR JSON APIs:
`companyfacts` (XBRL facts per company) and `submissions` (filing history per
company). It covers the sources, the recipes, the routes they run on, and how
they differ from the XBRL recipe. No SEC data ships with the repo.

## Sources

| Item | companyfacts | submissions |
| --- | --- | --- |
| Per-company URL | `https://data.sec.gov/api/xbrl/companyfacts/CIK##########.json` | `https://data.sec.gov/submissions/CIK##########.json` |
| Bulk archive | `https://www.sec.gov/Archives/edgar/daily-index/xbrl/companyfacts.zip` | `https://www.sec.gov/Archives/edgar/daily-index/bulkdata/submissions.zip` |
| Bulk rebuild | Nightly | Nightly |
| Content | All facts for one company from non-custom taxonomies | Company metadata and filing history |
| Format | One JSON document per company | One JSON document per company |
| Documentation reviewed | 2026-09-29 | 2026-09-29 |

`##########` is the 10-digit CIK, zero-padded. The APIs need no
authentication. Automated access must follow the SEC fair-access rules,
including a declared `User-Agent` with contact details and the published
request-rate limit; see the SEC developer resources and
[SEC EDGAR Data Acquisition](sec-edgar-usage.md).

### companyfacts shape

```json
{
  "cik": 0,
  "entityName": "...",
  "facts": {
    "us-gaap": {
      "<Concept>": {
        "label": "...",
        "description": "...",
        "units": {
          "USD": [
            { "end": "YYYY-MM-DD", "val": 0, "accn": "...", "fy": 0, "fp": "...", "form": "...", "filed": "YYYY-MM-DD", "frame": "..." }
          ]
        }
      }
    }
  }
}
```

### submissions shape

```json
{
  "cik": "...",
  "name": "...",
  "tickers": ["..."],
  "filings": {
    "recent": {
      "accessionNumber": ["...", "..."],
      "filingDate": ["...", "..."],
      "form": ["...", "..."]
    },
    "files": [{ "name": "CIK##########-submissions-001.json" }]
  }
}
```

`filings.recent` is columnar: parallel arrays, one entry per filing, covering
at least one year or 1,000 filings. `filings.files` names further JSON files
holding older filings.

## companyfacts recipe

- [`sec-edgar-companyfacts-signature.yaml`](../../../../examples/config/extract/sec-edgar-companyfacts-signature.yaml)
  — `format_type: json`, document scope; admits a document with both `/cik`
  and `/facts`.
- [`sec-edgar-companyfacts-usd-extract.yaml`](../../../../examples/config/extract/sec-edgar-companyfacts-usd-extract.yaml)
  — one record per item of `/facts/us-gaap/*/units/USD`. Fields: `cik`,
  `entity_name` (from the document root), `taxonomy` and `concept` (the
  ancestor member names, via `local-name()`), `period_end`, `value`,
  `value_text`, `accession`, `fiscal_year`, `fiscal_period`, `form`, `filed`,
  `frame`.

Other units (`shares`, `USD/shares`, `pure`) and taxonomies (`dei`, `ifrs-full`,
`srt`) are not selected; widen the selector to include them.

### Routes

| Route | Eligible | Notes |
| --- | --- | --- |
| Whole document | Yes | Default below the large-file threshold |
| Record by record | No | Each fact reads `cik` and `entityName` from the document root, so the signature is document-scoped |
| Record index | No | Refused: record-index JSON extraction requires `match_scope: record` |

A companyfacts document above the large-file threshold fails with a route
error; `--allow-large-files` parses it as one document.

## submissions recipe

- [`sec-edgar-submissions-signature.yaml`](../../../../examples/config/extract/sec-edgar-submissions-signature.yaml)
  — `format_type: json`, document scope; admits a document with
  `/filings/recent`.
- [`sec-edgar-submissions-company-extract.yaml`](../../../../examples/config/extract/sec-edgar-submissions-company-extract.yaml)
  — one record per company: `cik`, `entity_name`, `tickers`,
  `recent_filing_count`, and the `recent_accession_numbers`, `recent_forms`,
  `recent_filing_dates` and `older_filing_files` arrays.

The columnar arrays are kept as ordered arrays. The recipe does not join them
into one record per filing: that needs positional correlation across sibling
arrays, which the recipe XPath subset does not provide. Index `i` of each
array refers to the same filing.

### Routes

| Route | Eligible | Notes |
| --- | --- | --- |
| Whole document | Yes | One record per document |
| Record by record | No | Document-scoped signature |
| Record index | No | One record per document; nothing to parallelize within a file |

## Reproduction

Replace the placeholder values before running. `CIK` is a real 10-digit,
zero-padded CIK; `USER_AGENT` identifies you, per the SEC fair-access rules;
`WORK` is a directory outside the repository.

```bash
CIK='0000000000'
USER_AGENT='Example Org admin@example.org'
WORK='/path/to/work'

# 1. Retrieve one company (outside sumpter).
curl -fsS -A "$USER_AGENT" -o "$WORK/companyfacts.json" \
  "https://data.sec.gov/api/xbrl/companyfacts/CIK$CIK.json"
curl -fsS -A "$USER_AGENT" -o "$WORK/submissions.json" \
  "https://data.sec.gov/submissions/CIK$CIK.json"

# 2. Extract USD facts.
sumpter extract files \
  --files "$WORK"/companyfacts.json \
  --signature-config-path examples/config/extract/sec-edgar-companyfacts-signature.yaml \
  --extract-config-path examples/config/extract/sec-edgar-companyfacts-usd-extract.yaml \
  --output-path "$WORK"/out-facts

# 3. Extract the company filing summary.
sumpter extract files \
  --files "$WORK"/submissions.json \
  --signature-config-path examples/config/extract/sec-edgar-submissions-signature.yaml \
  --extract-config-path examples/config/extract/sec-edgar-submissions-company-extract.yaml \
  --output-path "$WORK"/out-subs
```

For the bulk archives, unzip to a directory and pass it with `--input-path`;
each per-company file is extracted independently.

## Comparison with XBRL

The [XBRL recipe](../../../user-guide/public-data-examples.md#sec-edgar-xbrl--financial-filings)
reads a filing's XBRL schema and linkbase files. companyfacts is a derived,
per-company view of reported facts across all filings.

| Aspect | companyfacts JSON | XBRL filing |
| --- | --- | --- |
| Scope | One company, all filings | One filing |
| Record | One reported fact | Schema roles, linkbases |
| Custom taxonomies | Excluded | Included |
| Namespaces | None; taxonomy is a member name | `xs:`, `link:` and filer namespaces |

This note makes no row-parity claim between companyfacts and the facts in the
underlying filings: companyfacts omits custom-taxonomy facts and is a derived
view.

## Caveats

- **Precision above 2^53.** `value` is typed `number` and is decoded as a
  64-bit float, so integers above 9,007,199,254,740,992 are rounded (for
  example, `9007199254740993` becomes `9007199254740992`). `value_text` is typed
  `string` and keeps the source number exactly. Use `value_text` when exact
  values matter.
- A JSON `null` binds absent. A fact without `frame` has no `frame` field.
- `cik` is a JSON number in companyfacts and a zero-padded string in
  submissions; both recipes emit it as a string, so the companyfacts value has
  no leading zeros.
- Whole-document extraction holds the parsed document in memory.
