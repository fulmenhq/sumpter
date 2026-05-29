# Inspect Command

Inspect XML file structure, encoding, and content patterns.

## Usage

```bash
sumpter inspect [file] [flags]
```

## Description

The `inspect` command performs a comprehensive streaming analysis of XML files to understand their structure, encoding, and content patterns. It uses streaming input parsing for large files (100MB+) with configurable sampling to balance speed and insight.

## Parameters

- `file`: Path to XML file to inspect (use `-` for stdin)

## Flags

### Core Options

- `--output`, `-o`: Output file (default: stdout)
- `--format`, `-f`: Output format: `markdown` (default) or `json`
- `--max-paths`: Maximum number of unique paths to track (default: 200)
- `--samples-per-path`: Number of text samples to collect per path (default: 2)

### Encoding Options

- `--force-encoding`: Force specific encoding (e.g., `windows-1252`)

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
  "version": "inspect-report/v0.1.0",
  "input": {
    "path": "data.xml",
    "size_bytes": 2621440,
    "encoding_detected": "UTF-8"
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
