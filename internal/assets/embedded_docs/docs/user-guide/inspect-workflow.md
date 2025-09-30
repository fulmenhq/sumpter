# XML Inspection and Dialect Detection Workflow

Comprehensive guide to XML inspection, dialect detection, and custom dialect development.

## Overview

Sumpter's `inspect` command provides powerful XML analysis capabilities with automatic dialect detection. This workflow guide covers:

- **Matching Cases**: How Sumpter identifies known XML dialects
- **Dialect Development**: Creating custom dialect definitions
- **Registry Extensions**: Extending the dialect registry system

## Quick Start

### Basic Inspection

```bash
# Inspect any XML file
sumpter inspect data.xml

# Save results to file
sumpter inspect data.xml --output report.md

# JSON output for programmatic processing
sumpter inspect data.xml --format json
```

### Dialect Detection

```bash
# Automatic dialect detection (enabled by default)
sumpter inspect financial-data.xml

# Use custom dialect directory
sumpter inspect data.xml --dialects-dir ./my-dialects
```

## Matching Cases: Known Dialect Detection

### How Dialect Detection Works

Sumpter uses a **pattern-based matching system** to identify XML dialects:

1. **Pattern Matching**: Compares XML structure against predefined patterns
2. **Confidence Scoring**: Calculates statistical confidence in matches
3. **Metadata Extraction**: Extracts format-specific information
4. **Fallback Handling**: Gracefully handles unknown formats

### Built-in Dialects

#### SEC EDGAR Dialect

**Purpose**: U.S. Securities and Exchange Commission regulatory filings

**Matching Criteria**:

```yaml
patterns:
  - pattern_id: "sec-header"
    selector: "local-name()='SEC-HEADER'"
    weight: 0.9
  - pattern_id: "acceptance-datetime"
    selector: "local-name()='ACCEPTANCE-DATETIME'"
    weight: 0.8
  - pattern_id: "xbrl-instance"
    selector: "local-name()='xbrl'"
    weight: 0.8
```

**Detection Example**:

```bash
sumpter inspect sec-filing.xml
```

Output:

```markdown
## Dialect Detection

- **Detected Dialect:** SEC EDGAR
- **Confidence:** 95.2%
- **Detection Score:** 0.87
- **Matched Patterns:** sec-header, acceptance-datetime, xbrl-instance
```

**Supported Formats**:

- Form 10-K (Annual Reports)
- Form 10-Q (Quarterly Reports)
- Form 8-K (Current Reports)
- XBRL Instance Documents
- Corporate Filings

#### Weather XML Dialect

**Purpose**: Meteorological data and aviation weather reports

**Matching Criteria**:

```yaml
patterns:
  - pattern_id: "weather-report"
    selector: "local-name()='report'"
    weight: 0.7
  - pattern_id: "weather-metar"
    selector: "local-name()='metar'"
    weight: 0.8
  - pattern_id: "aviation-weather"
    selector: "local-name()='aviation'"
    weight: 0.7
```

**Detection Example**:

```bash
sumpter inspect weather-data.xml
```

Output:

```markdown
## Dialect Detection

- **Detected Dialect:** Weather XML
- **Confidence:** 87.3%
- **Detection Score:** 0.76
- **Matched Patterns:** weather-metar, aviation-weather
```

**Supported Formats**:

- NOAA METAR Reports
- Aviation Weather (TAF)
- Weather Forecasts
- Meteorological Observations

### Detection Algorithm

#### Pattern Scoring

Each pattern contributes to the overall dialect score:

```yaml
# Pattern definition
- pattern_id: "example-pattern"
  name: "Example Pattern"
  selector: "local-name()='ExampleElement'"
  weight: 0.8
  ecosystem: "domain-specific"
```

**Score Calculation**:

- **Presence**: 1.0 if pattern found, 0.0 if not found
- **Weight**: Pattern importance (0.0 to 1.0)
- **Frequency**: Bonus for multiple occurrences
- **Uniqueness**: Bonus for rare patterns

#### Confidence Thresholds

- **High Confidence** (>0.8): Strong dialect match
- **Medium Confidence** (0.6-0.8): Probable dialect match
- **Low Confidence** (0.4-0.6): Possible dialect match
- **Unknown** (<0.4): No dialect identified

#### Multi-Dialect Scenarios

When multiple dialects match:

```markdown
## Dialect Detection

- **Primary Dialect:** SEC EDGAR (87.3%)
- **Secondary Dialect:** XBRL Generic (45.2%)
- **Recommendation:** Use SEC EDGAR patterns for extraction
```

## Custom Dialect Development

### Creating a New Dialect

#### Step 1: Analyze Sample XML

```xml
<!-- sample-industry.xml -->
<IndustryData xmlns="http://industry.example.com/schema">
  <Header>
    <DocumentID>IND-2024-001</DocumentID>
    <Timestamp>2024-01-15T10:30:00Z</Timestamp>
    <Version>1.0</Version>
  </Header>
  <Records>
    <Record id="R001">
      <Field1>Value1</Field1>
      <Field2>Value2</Field2>
    </Record>
  </Records>
</IndustryData>
```

#### Step 2: Identify Key Patterns

**Structural Patterns**:

- Root element: `IndustryData`
- Header section: `IndustryData.Header`
- Record elements: `IndustryData.Records.Record`
- ID attributes: `@id`

**Content Patterns**:

- Document IDs: `IND-YYYY-NNN` format
- Timestamps: ISO 8601 format
- Version numbers: Semantic versioning

#### Step 3: Create Dialect Definition

```yaml
# custom-dialects/industry-data.yaml
dialect_id: "industry-data"
name: "Industry Data XML"
description: "Custom XML format for industry-specific data exchange"
status: "active"
priority: "medium"
realm: "industry"
data_sensitivity: "medium"
confidence_threshold: 0.7
format_type: "xml"

patterns:
  - pattern_id: "industry-root"
    name: "Industry data root element"
    selector: "local-name()='IndustryData'"
    weight: 0.9
    ecosystem: "industry"

  - pattern_id: "industry-header"
    name: "Industry document header"
    selector: "local-name()='Header'"
    weight: 0.8
    ecosystem: "document"

  - pattern_id: "industry-record"
    name: "Industry data record"
    selector: "local-name()='Record'"
    weight: 0.7
    ecosystem: "data"

  - pattern_id: "document-id"
    name: "Document ID attribute"
    selector: "@id"
    weight: 0.6
    ecosystem: "document"

  - pattern_id: "industry-timestamp"
    name: "Industry timestamp element"
    selector: "local-name()='Timestamp'"
    weight: 0.5
    ecosystem: "temporal"

tags:
  - "industry"
  - "custom"
  - "data-exchange"
  - "records"

use_cases:
  - "Industry data processing"
  - "Regulatory compliance"
  - "Data integration"
  - "Analytics pipeline"

xml_standards:
  - "Industry XML Schema v1.0"
  - "ISO 20022 Financial Services"
```

#### Step 4: Test the Dialect

```bash
# Create dialect directory
mkdir -p custom-dialects

# Save dialect definition
# (save the YAML above to custom-dialects/industry-data.yaml)

# Test dialect detection
sumpter inspect sample-industry.xml --dialects-dir ./custom-dialects
```

Expected output:

```markdown
## Dialect Detection

- **Detected Dialect:** Industry Data XML
- **Confidence:** 92.1%
- **Detection Score:** 0.83
- **Matched Patterns:** industry-root, industry-header, industry-record
```

### Advanced Pattern Techniques

#### XPath Selectors

**Element Selection**:

```yaml
# Match any element with specific local name
selector: "local-name()='ElementName'"

# Match elements in specific namespace
selector: "local-name()='ElementName' and namespace-uri()='http://example.com'"

# Match elements at specific path
selector: "//RootElement/ChildElement"
```

**Attribute Selection**:

```yaml
# Match specific attribute
selector: "@attributeName"

# Match attribute with specific value pattern
selector: "@id[starts-with(., 'PREFIX-')]"
```

**Content-Based Selection**:

```yaml
# Match elements containing specific text
selector: "contains(., 'specific-text')"

# Match elements with numeric content
selector: "number(.) = number(.)"  # Is numeric
```

#### Pattern Weight Optimization

**High Weight Patterns** (0.8-1.0):

- Unique identifiers for the format
- Required root elements
- Format-specific metadata

**Medium Weight Patterns** (0.5-0.7):

- Common structural elements
- Standard attributes
- Repeated patterns

**Low Weight Patterns** (0.1-0.4):

- Optional elements
- Generic attributes
- Fallback patterns

### Dialect Registry Organization

#### Directory Structure

```
dialects/
├── official/           # Built-in dialects (read-only)
│   ├── sec-edgar.yaml
│   └── weather-xml.yaml
├── custom/            # User-defined dialects
│   ├── industry-data.yaml
│   └── finance-extended.yaml
└── shared/            # Team-shared dialects
    ├── company-standard.yaml
    └── project-specific.yaml
```

#### Registry Loading Priority

1. **Built-in Dialects**: Always loaded first
2. **Custom Directory**: User-specified `--dialects-dir`
3. **Registry Merging**: Patterns combined across sources
4. **Conflict Resolution**: Higher priority dialects win

## Registry Extension Patterns

### Blend Operations

**Use Case**: Extend existing dialect with additional patterns

```yaml
# extended-sec-edgar.yaml
dialect_id: "sec-edgar"
operation: "blend"

patterns:
  - pattern_id: "custom-extension"
    name: "Custom SEC extension"
    selector: "local-name()='CustomElement'"
    weight: 0.6
    ecosystem: "custom"
```

### Override Operations

**Use Case**: Replace built-in dialect patterns

```yaml
# override-weather.yaml
dialect_id: "weather-xml"
operation: "override"

patterns:
  - pattern_id: "weather-metar"
    name: "Enhanced METAR detection"
    selector: "local-name()='metar' or local-name()='METAR'"
    weight: 0.9
    ecosystem: "meteorology"
```

### Replace Operations

**Use Case**: Completely replace a built-in dialect

```yaml
# replacement-dialect.yaml
dialect_id: "weather-xml"
operation: "replace"

patterns:
  # Completely new pattern set
  - pattern_id: "new-pattern-1"
    # ... new patterns
```

## Best Practices

### Pattern Development

#### Start Simple

```yaml
# Begin with core identifying patterns
patterns:
  - pattern_id: "root-element"
    selector: "local-name()='RootElement'"
    weight: 0.9
```

#### Test Incrementally

```bash
# Test after each pattern addition
sumpter inspect test.xml --dialects-dir ./dialects
```

#### Validate Across Samples

```bash
# Test with multiple sample files
for file in samples/*.xml; do
  echo "Testing $file:"
  sumpter inspect "$file" --dialects-dir ./dialects | grep "Dialect"
done
```

### Performance Considerations

#### Pattern Efficiency

- Prefer `local-name()` over complex XPath
- Use attribute selectors for fast matching
- Avoid expensive content-based patterns

#### Registry Size

- Limit to essential patterns (10-20 per dialect)
- Use appropriate weight values
- Remove unused patterns

### Maintenance

#### Version Control

```bash
# Track dialect changes
git add dialects/
git commit -m "Add industry data dialect v1.0"
```

#### Documentation

```yaml
# Include comprehensive metadata
description: "Detailed description of dialect purpose and scope"
use_cases:
  - "Primary use case"
  - "Secondary use case"
xml_standards:
  - "Relevant standards"
  - "Schema versions"
```

#### Testing

```bash
# Create test files for dialect validation
# test-dialects/
# ├── industry-data-test.xml
# ├── expected-output.json
# └── test-script.sh
```

## Troubleshooting

### Common Issues

#### Low Confidence Scores

**Problem**: Dialect detection shows low confidence

**Solutions**:

```yaml
# Increase pattern weights
patterns:
  - pattern_id: "key-pattern"
    weight: 0.9  # Increase from 0.7

# Add more specific patterns
patterns:
  - pattern_id: "specific-pattern"
    selector: "local-name()='UniqueElement'"
    weight: 0.8
```

#### False Positives

**Problem**: Incorrect dialect detection

**Solutions**:

```yaml
# Add negative patterns
patterns:
  - pattern_id: "not-other-format"
    selector: "not(local-name()='OtherFormatElement')"
    weight: 0.3

# Increase specificity
selector: "local-name()='Element' and @type='specific-type'"
```

#### Pattern Conflicts

**Problem**: Multiple dialects match the same elements

**Solutions**:

```yaml
# Use namespace qualification
selector: "local-name()='Element' and namespace-uri()='http://specific.namespace'"

# Add context-specific patterns
selector: "//RootElement/SpecificPath/Element"
```

### Debug Mode

```bash
# Enable detailed logging
SUMPTER_LOG_LEVEL=debug sumpter inspect file.xml

# Check pattern matching details
sumpter inspect file.xml --format json | jq '.dialect.score_breakdown'
```

## Integration Examples

### CI/CD Pipeline

```yaml
# .github/workflows/dialect-test.yml
name: Test Custom Dialects
on: [push, pull_request]

jobs:
  test-dialects:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - name: Test Dialect Detection
        run: |
          for file in test-data/*.xml; do
            sumpter inspect "$file" --dialects-dir ./dialects --format json > result.json
            # Validate expected dialect detection
            jq -e '.dialect.dialect_name == "Expected Dialect"' result.json
          done
```

### Programmatic Usage

```bash
# Extract dialect information for processing
DIALECT_INFO=$(sumpter inspect data.xml --format json | jq '.dialect')

# Use dialect information to select processing pipeline
if [[ $(echo "$DIALECT_INFO" | jq -r '.dialect_name') == "SEC EDGAR" ]]; then
    echo "Processing SEC filing..."
    # SEC-specific processing
fi
```

## Advanced Topics

### Multi-Version Dialects

```yaml
# Support multiple schema versions
dialect_id: "industry-data-v2"
name: "Industry Data XML v2.0"
extends: "industry-data"

patterns:
  # Additional v2.0 patterns
  - pattern_id: "v2-feature"
    selector: "local-name()='NewElement'"
    weight: 0.7
```

### Conditional Patterns

```yaml
# Patterns that depend on other patterns
patterns:
  - pattern_id: "conditional-pattern"
    selector: "//RootElement[ConditionalElement]/TargetElement"
    weight: 0.6
    requires: "conditional-element"
```

### Metadata Extraction

```yaml
# Extract format-specific metadata
metadata_patterns:
  - pattern_id: "version-extraction"
    selector: "/RootElement/@version"
    metadata_key: "schema_version"

  - pattern_id: "id-extraction"
    selector: "/RootElement/Header/ID"
    metadata_key: "document_id"
```

This comprehensive workflow enables you to effectively use Sumpter's inspection capabilities and extend them with custom dialects tailored to your specific XML processing needs.
