# Public-Data Sample Provenance

These three XML samples are genuine public open-government / public-domain
data, captured 2026-06-03. Each was sliced to a handful of records to stay
small (all < 50 KB) while preserving well-formed XML: the root element, its
namespace declarations, and a few complete records. Well-formedness is
verified with `xmllint --noout`.

They pair with the recipes in
[`examples/config/extract/`](../../config/extract/) and are referenced from the
[Public-Data Examples](../../../docs/user-guide/public-data-examples.md) guide.

## usgs-quakeml-sample.xml — scientific / geophysics

- **Source**: USGS / ANSS FDSN event web service, QuakeML 1.2 format.
- **Capture**: FDSN `event` query (`format=quakeml`, magnitude ≥ 5, one week
  of 2024-01), then sliced to the first 6 `<event>` blocks, preserving the
  `<q:quakeml>` root, namespace declarations, and `<eventParameters>` wrapper.
- **License**: Public domain — USGS information products are U.S. Government
  works, not subject to U.S. copyright (17 U.S.C. § 105). See the USGS
  [copyrights and credits policy](https://www.usgs.gov/information-policies-and-instructions/copyrights-and-credits).

## nws-cap-alerts-sample.xml — geospatial / public-safety

- **Source**: US National Weather Service active public alerts API
  (`api.weather.gov`), Atom 1.0 wrapping OASIS CAP v1.2.
- **Capture**: active-alerts feed (`Accept: application/atom+xml`), then sliced
  to the first 6 `<entry>` blocks, preserving the `<feed>` root, Atom + `cap:`
  namespace declarations, and feed-level header elements.
- **Note**: Alert content is time-sensitive — these entries reflect alerts
  active at capture time. A later refresh would carry different alerts with the
  same structure.
- **License**: Public domain — NWS/NOAA data are U.S. Government works
  (17 U.S.C. § 105). See the NWS [disclaimer](https://www.weather.gov/disclaimer)
  ("Information presented … is considered public information and may be
  distributed or copied").

## govinfo-uslm-bill-sample.xml — government / regulatory

- **Source**: US GPO govinfo, United States Legislative Markup (USLM) XML.
  Enrolled bill `BILLS-118hr9566enr` (118th Congress, H.R. 9566, "SHARE IT
  Act"), used as-is (~40 KB, no slicing needed).
- **License**: Public domain (17 U.S.C. § 105). The file carries its own
  in-band notice: `<dc:rights>Pursuant to Title 17 Section 105 of the United
States Code, this file is not subject to copyright protection and is in the
public domain.</dc:rights>`. See the GPO
  [copyright and use policy](https://ask.gpo.gov/s/article/What-are-the-copyright-and-use-policies-of-govinfo-content).
