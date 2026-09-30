---
title: "USGS Earthquake GeoJSON Feeds"
description: "Extracting one record per earthquake from USGS GeoJSON summary feeds with a record-scoped JSON recipe"
date: "2026-09-29"
author: "Sumpter Team"
tags: ["science", "geophysics", "usgs", "json", "geojson"]
---

# USGS Earthquake GeoJSON Feeds

This application note describes a recipe for the USGS earthquake GeoJSON
summary feeds. It covers the source, the recipe, the routes it runs on, and
how it differs from the QuakeML recipe. No feed data ships with the repo.

## Source

| Item | Value |
| --- | --- |
| Publisher | U.S. Geological Survey, Earthquake Hazards Program |
| Feed URL pattern | `https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/<level>_<period>.geojson` |
| `<level>` | `significant`, `4.5`, `2.5`, `1.0`, `all` |
| `<period>` | `hour`, `day`, `week`, `month` |
| Update cadence | Every minute |
| Format | GeoJSON (RFC 7946) `FeatureCollection` |
| Terms | USGS information products are in the U.S. public domain |
| Documentation reviewed | 2026-09-29 |

Each feed is one JSON document:

```json
{
  "type": "FeatureCollection",
  "metadata": { "generated": 0, "url": "...", "title": "...", "count": 0 },
  "features": [
    {
      "type": "Feature",
      "properties": { "mag": 0.0, "place": "...", "time": 0, "status": "...", "tsunami": 0, "magType": "..." },
      "geometry": { "type": "Point", "coordinates": [0.0, 0.0, 0.0] },
      "id": "..."
    }
  ],
  "bbox": [0, 0, 0, 0, 0, 0]
}
```

`geometry.coordinates` is an ordered array: longitude, latitude, depth in km.

## Recipe

The recipe pair lives in [`examples/config/extract/`](../../../../examples/config/extract/):

- [`usgs-geojson-feature-signature.yaml`](../../../../examples/config/extract/usgs-geojson-feature-signature.yaml)
  — `format_type: json`, `match_scope: record`. Each feature is admitted when
  it carries both `properties` and `geometry`.
- [`usgs-geojson-feature-extract.yaml`](../../../../examples/config/extract/usgs-geojson-feature-extract.yaml)
  — one record per `features` item: `event_id`, `magnitude`,
  `magnitude_type`, `place`, `time_ms`, `status`, `tsunami`, `longitude`,
  `latitude`, `depth_km`.

Coordinates are read by position (`geometry/coordinates[1]` is longitude).
`properties.time` is epoch milliseconds, kept as an integer.

## Routes

| Route | Eligible | Notes |
| --- | --- | --- |
| Whole document | Yes | Default below the large-file threshold |
| Record by record | Yes | Used above the large-file threshold; the signature is record-scoped |
| Record index | Yes | Index selector `features` |

## Reproduction

Paths below are placeholders.

```bash
# 1. Retrieve one feed (outside sumpter).
curl -fsS -o /path/to/work/all_day.geojson \
  https://earthquake.usgs.gov/earthquakes/feed/v1.0/summary/all_day.geojson

# 2. Whole-document extraction.
sumpter extract files \
  --files /path/to/work/all_day.geojson \
  --signature-config-path examples/config/extract/usgs-geojson-feature-signature.yaml \
  --extract-config-path examples/config/extract/usgs-geojson-feature-extract.yaml \
  --output-path /path/to/work/out-seq

# 3. Record-index extraction.
sumpter index build /path/to/work/all_day.geojson \
  --input-format json -s features -o /path/to/work/all_day -p=false
sumpter index verify /path/to/work/all_day.geojson \
  -i /path/to/work/all_day.recordindex.json --input-format json --verify-records
sumpter extract files \
  --files /path/to/work/all_day.geojson \
  --signature-config-path examples/config/extract/usgs-geojson-feature-signature.yaml \
  --extract-config-path examples/config/extract/usgs-geojson-feature-extract.yaml \
  --record-index /path/to/work/all_day.recordindex.json --workers 4 \
  --output-path /path/to/work/out-idx
```

The feed file has no `.json` extension, so pass it with `--files` rather than
a directory walk with the default `*.json` pattern.

## Comparison with QuakeML

The [QuakeML recipe](../../../user-guide/public-data-examples.md#usgs-quakeml-seismic-event-catalogs--scientific--research)
reads the FDSN `event` service. The two formats carry overlapping but not
identical content.

| Aspect | GeoJSON feed | QuakeML |
| --- | --- | --- |
| Record | `features` item | `event` element |
| Location | positional `coordinates` array | named `latitude`/`longitude`/`depth` with uncertainties |
| Depth unit | km | m |
| Time | epoch milliseconds | ISO 8601 string |
| Namespaces | none | QuakeML plus the ANSS catalog extension |
| Selection | fixed summary windows | query parameters |

This note makes no row-parity claim between the two sources for the same
window: they are separate products with separate update timing.

## Caveats

- A JSON `null` binds absent. A feature whose `mag` is `null` has no
  `magnitude` field in its record; `magnitude` is not required by the output
  schema.
- Values typed `number` or `integer` are decoded as 64-bit floats. Feed values
  are well inside the exact range.
- The feeds change every minute. Record counts from two retrievals of the same
  feed are not expected to match.
- Whole-document extraction holds the parsed document in memory; the
  record-by-record and record-index routes do not.
