# Inspect Command

Inspect XML (or, with `--input-format json`, JSON) document structure, encoding, and content patterns.

## Usage

```bash
sumpter inspect [file] [flags]
```

## Description

The `inspect` command profiles XML by default or a JSON document with `--input-format json`. Both routes parse incrementally and report paths, counts and bounded samples. XML encoding, attributes and record-analysis options are XML-specific; JSON reports value kinds and follows the JSON node model. NDJSON inspection is refused. See the [input-route matrix](../../extract-workflow.md#input-route-support).

## Parameters

- `file`: XML or JSON source to inspect (use `-` for stdin); JSON requires `--input-format json`

## Flags

### Core Options

- `--output`, `-o`: Output file (default: stdout)
- `--format`, `-f`: Output format: `markdown` (default) or `json`
- `--max-paths`: Maximum number of unique paths to track (default: 200)
- `--samples-per-path`: Number of text samples to collect per path (default: 2)

### Encoding Options

- `--force-encoding`: Force specific encoding (e.g., `windows-1252`)

### Input Format

`inspect` reports on XML by default. `--input-format json` profiles one JSON
document instead. The format is always declared, never detected from the
content or the file name.

- `--input-format`: Input syntax, `xml` (default) or `json`.

Under `json`, `inspect` walks the document incrementally, retaining parser,
nesting/duplicate-key state and capped report metadata rather than a DOM, and
reports every key path the way extraction sees it (see the
[document node model](../../standards/document-node-model.md)): a member whose
value is an array produces one element per item, an empty array produces no
element, and the items of a top-level array are named `item`.
A null value creates an element with no text child, but its mapped field is
absent; an empty array creates no element, so its mapped field is also absent
(not []). A null item inside an array of scalars is dropped from the mapped
array.

- **`input.format`** is `json`; `encoding_detected` is `UTF-8`. One leading
  UTF-8 byte-order mark is accepted; UTF-16 and UTF-32 input is refused with
  "JSON input must be UTF-8".
- **`paths[].value_kinds`** counts each path's occurrences by value kind
  (`string`, `number`, `bool`, `null`, `object`, `array`); the counts always sum
  to `count`. `{"tags": ["a", "b"]}` gives path `tags` with count 2 and
  `string: 2`; `array` appears only for an array nested directly in an array
  (`{"m": [[1, 2], [3]]}` gives `m` with `array: 2` and `m.m` with `number: 3`).
- **Samples** are scalar values, with the same limits as XML (`--samples-per-path`,
  100-character truncation, `samples_truncated`); `null` has no sample.
- **`attributes`** is always `[]` and `caps.attributes_truncated` is always
  `false`: JSON has no attributes.
- The record-analysis sections (`record_candidates`, `streaming_analysis`,
  `oom_summary`) are omitted, and the Markdown header reads
  `# JSON Inspection Report`.
- The input is checked exactly as extraction checks it: invalid UTF-8,
  duplicate keys, nesting deeper than 1024, a second top-level value, truncated
  input, a top-level scalar, and empty input all fail with no report, using the
  same error text as extraction. Errors carry byte offsets, not input excerpts.

Number samples preserve source text, including integers above 2^53. Parsing
does not round them, but XPath numeric evaluation does use floating point;
exact identifiers in recipes need string mappings and `value_text`. Incremental
parsing is not a fixed-RSS promise for arbitrarily deep/wide objects.

**Paths.** Every path entry carries `segments`, the verbatim node names from the
root, which are the path's authoritative identity. `path` is a display string:
the segments joined with `.`, where a `.` or `\` inside a segment is escaped
with `\`. The JSON key `"a.b"` has path `a\.b` (written `"a\\.b"` in the JSON
report) and `segments: ["a.b"]`; the nested keys `{"a": {"b": 1}}` have path
`a.b` and `segments: ["a", "b"]`.
XML element names that contain `.` are escaped the same way. Tools that build
selectors should read `segments`.

**Refusals.** These only refuse; they never select a format.

- Without `--input-format json`, input whose first non-whitespace byte (after
  any UTF-8 byte-order mark) is not `<` is refused with "input does not look
  like XML; use --input-format json for JSON input".
- With `--input-format json`, input whose first non-whitespace byte is `<` is
  refused with "input looks like XML; drop --input-format json".
- Gzip-compressed input is refused in both modes with "inspect does not read
  gzip input; decompress it first (for example: gunzip -k <file>)".
- `--force-encoding` does not apply to JSON (JSON input must be UTF-8), and
  record analysis (`--analyze-records`) is not supported for `json` or
  `ndjson` input in this release; each is refused before any input is read.
- `--input-format ndjson` is not supported by `inspect` in this release.

**Generating a starter config from JSON.** `--generate-config` with
`--input-format json` writes one `extract.yaml`, loadable as it is. The record
selector is the most-repeated object path at the shallowest depth (or
`--record-selector`, any XPath). Selectors are built from key names as the
[document node model](../../standards/document-node-model.md#selecting-keys-that-are-not-xml-names)
describes, so keys that are not XML names are selected exactly. Field types
come from the JSON value kinds. When `//` plus the record name selects exactly
the detected records, the config uses that selector and the signature declares
`match_scope: record` with a record-relative pattern, so the recipe reads the
same inputs below and above the large-file threshold; otherwise the signature
keeps document scope and the header says an input above the threshold needs
`--allow-large-files`. JSON input is declared by a signature's `format_type`,
so the header carries two commented, ready-to-copy blocks, a signature with
`format_type: json` and a recipe manifest fragment with
`defaults.input.format: json`, and names the first run:

```bash
sumpter inspect data.json --input-format json --generate-config --output extract.yaml
# 1. copy the signature block from the header to extract-signature.yaml
# 2. run the command the header names:
sumpter extract files --signature-config-path extract-signature.yaml \
  --extract-config-path extract.yaml --files data.json --output-path extract-out
```

All reports, XML and JSON, use `inspect-report/v0.1.2`. It adds `input.format`
and `paths[].segments` to every report and `paths[].value_kinds` to JSON
reports; an XML report is otherwise unchanged from v0.1.1.

### Performance Options

- `--progress`, `-p`: Show progress for large files

### Content Options

- `--include-attributes`: Include attribute analysis (default: true)

### Validation Options

- `--validate-output`: Validate JSON output against schema

### Dialect Options

- `--dialects-dir`: Directory containing custom dialect definitions

## Examples

### Basic File Inspection

```bash
sumpter inspect data.xml
```

### Inspect from Standard Input

```bash
cat data.xml | sumpter inspect -
```

### Inspect a JSON Document

```bash
sumpter inspect data.json --input-format json --format json
```

### JSON Output Format

```bash
sumpter inspect data.xml --format json
```

### Save to File

```bash
sumpter inspect data.xml --output report.md
```

### Force Specific Encoding

```bash
sumpter inspect legacy.xml --force-encoding windows-1252
```

### Limit Analysis Depth

```bash
sumpter inspect large.xml --max-paths 100 --samples-per-path 1
```

### Progress Monitoring

```bash
sumpter inspect huge.xml --progress
```

### Custom Dialect Directory

```bash
sumpter inspect data.xml --dialects-dir ./my-dialects
```

### Validate Output

```bash
sumpter inspect data.xml --format json --validate-output
```

## Output Formats

### Markdown Format (Default)

```markdown
# XML Inspection Report

**File:** data.xml
**Size:** 2.5 MB
**Encoding:** UTF-8

## Performance

- **Duration:** 1250 ms
- **Throughput:** 2.0 MB/s
- **Memory Peak:** 45.2 MB

## Top Paths

| Path                    | Count | Attributes | Samples |
| ----------------------- | ----- | ---------- | ------- |
| Envelope.Header.Message | 1500  | 3          | 2       |
| Envelope.Body.Payload   | 1500  | 0          | 2       |
| Envelope.Body.Metadata  | 1500  | 5          | 2       |

### Attributes for Envelope.Header.Message

| Attribute | Count |
| --------- | ----- |
| id        | 1500  |
| timestamp | 1500  |
| version   | 1500  |

### Samples for Envelope.Header.Message

- `MSG-2024-001`
- `MSG-2024-002`

## Dialect Detection

- **Detected Dialect:** SEC EDGAR
- **Confidence:** 95.2%
- **Detection Score:** 0.87
```

### JSON Format

```json
{
  "version": "inspect-report/v0.1.2",
  "input": {
    "path": "data.xml",
    "size_bytes": 2621440,
    "encoding_detected": "UTF-8",
    "compressed": false,
    "compression": "none",
    "format": "xml"
  },
  "metrics": {
    "bytes_processed": 2621440,
    "elapsed_ms": 1250,
    "throughput_bytes_per_sec": 2097152,
    "replacement_count": 0,
    "rss_peak_mb": 45.2
  },
  "paths": [
    {
      "path": "Envelope.Header.Message",
      "segments": ["Envelope", "Header", "Message"],
      "count": 1500,
      "attributes": [
        { "name": "id", "count": 1500 },
        { "name": "timestamp", "count": 1500 },
        { "name": "version", "count": 1500 }
      ],
      "samples": ["MSG-2024-001", "MSG-2024-002"]
    }
  ],
  "caps": {
    "paths_truncated": false,
    "attributes_truncated": false,
    "samples_truncated": false
  },
  "dialect": {
    "dialect_name": "SEC EDGAR",
    "confidence": 0.952,
    "score": 0.87,
    "matched_patterns": ["sec-header", "acceptance-datetime"],
    "metadata": { "form_type": "10-K", "fiscal_year": "2023" }
  },
  "metadata": {
    "generator": "sumpter inspect",
    "timestamp": "2024-01-15T14:30:25Z"
  }
}
```

## Analysis Features

### Encoding Detection

- **BOM Detection**: UTF-8, UTF-16, UTF-32 BOM recognition
- **XML Declaration**: Encoding from `<?xml version="1.0" encoding="..."?>`
- **Charset Detection**: Automatic encoding detection using `golang.org/x/net/html/charset`
- **Force Override**: Manual encoding specification with `--force-encoding`

### Structure Analysis

- **Path Tracking**: Hierarchical element path construction (dot notation)
- **Element Counting**: Frequency analysis of XML elements
- **Depth Analysis**: Maximum nesting depth calculation
- **Namespace Detection**: XML namespace identification

### Content Sampling

- **Text Extraction**: Sample text content from elements
- **Attribute Analysis**: Attribute name and value pattern analysis
- **Truncation Handling**: Configurable limits to prevent memory issues
- **Type Inference**: Basic data type detection for attributes

### Performance Monitoring

- **Memory Usage**: Peak RSS tracking
- **Throughput**: Processing speed in bytes/second
- **Duration**: Total analysis time
- **Progress**: Real-time progress for large files

### Dialect Detection

- **Pattern Matching**: Predefined patterns for known XML formats
- **Confidence Scoring**: Statistical confidence in dialect identification
- **Metadata Extraction**: Format-specific metadata collection
- **Custom Dialects**: User-defined dialect support

## Built-in Dialects

### SEC EDGAR

- **Purpose**: U.S. Securities and Exchange Commission filings
- **Patterns**: SEC-HEADER, ACCEPTANCE-DATETIME, FILING-VALUES
- **Standards**: EDGAR XML, XBRL 2.1, US GAAP Taxonomy
- **Use Cases**: Financial reporting, regulatory compliance

### Weather XML

- **Purpose**: Meteorological data and aviation weather reports
- **Patterns**: METAR, aviation weather, forecast data
- **Standards**: NOAA METAR XML, WMO Weather XML
- **Use Cases**: Weather data processing, aviation operations

## Error Handling

### File Access Errors

```bash
Error: failed to open file: open data.xml: no such file or directory
```

### XML Parsing Errors

```bash
Error: XML parsing error: xml: invalid UTF-8 sequence
```

### Encoding Errors

```bash
Error: encoding detection failed: invalid character encoding
```

### Schema Validation Errors

```bash
Error: output invalid against schema: 2 error(s)
```

## Use Cases

### Data Discovery

- **Unknown XML Format**: Understand structure of unfamiliar XML files
- **Data Mapping**: Identify elements for ETL pipeline design
- **Schema Inference**: Generate basic schema from sample data

### Quality Assurance

- **Encoding Validation**: Verify correct character encoding
- **Structure Verification**: Confirm expected XML structure
- **Content Sampling**: Review actual data patterns

### Performance Planning

- **Size Assessment**: Evaluate file size for processing capacity
- **Memory Estimation**: Predict memory requirements
- **Throughput Analysis**: Measure processing performance

### Compliance Checking

- **Regulatory Formats**: Validate SEC EDGAR, XBRL compliance
- **Industry Standards**: Check against domain-specific XML schemas
- **Data Quality**: Assess data completeness and consistency

### Development Support

- **API Design**: Understand XML structure for API development
- **Database Design**: Plan XML-to-relational mapping
- **Testing**: Generate test data based on real structure

## Best Practices

### Large Files

- Use `--progress` for files >100MB
- Reduce `--max-paths` for very large files
- Consider `--samples-per-path 1` for memory efficiency

### Encoding Issues

- Try `--force-encoding` for files with incorrect encoding declarations
- Use UTF-8 for best compatibility
- Check for BOM in binary files

### Performance Optimization

- Balance `--max-paths` with analysis requirements
- Use `--samples-per-path 0` to disable text sampling
- Consider JSON output for programmatic processing

### Dialect Detection

- Use `--dialects-dir` for custom domain-specific patterns
- Review confidence scores for dialect identification
- Combine with manual inspection for complex formats

## Notes

- Streaming input parsing avoids loading the full XML document into memory
- Default limits prevent excessive memory consumption
- Progress reporting uses stderr to avoid interfering with output
- Schema validation requires `goneat` library
- Custom dialects extend built-in pattern recognition
