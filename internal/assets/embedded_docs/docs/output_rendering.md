Output Rendering Guide

Mapping inspect JSON SSOT to human-friendly outputs.

---

## Inputs

- Inspect JSON conforms to `schemas/inspect-report/v0.1.0/inspect-report.schema.yaml`.

---

## Markdown

- Header: file path, size, encoding, elapsed, throughput.
- Sections: top paths with counts; per-path top attributes; optional samples.
- Stable ordering: count desc, key asc.

---

## HTML

- Same content as Markdown; use simple, responsive table and code styles.
- Include a collapsible panel for long path lists.

---

## CSV (Optional)

- Fields: path,count,top_attributes (semicolon-separated `name=count`).

---

## PDF

- Render from HTML using a headless browser (CI optional job).

---

## Notes

- Rendering is derived; the JSON report remains the SSOT.
- Add anchors/ids to enable deep links from dashboards.
