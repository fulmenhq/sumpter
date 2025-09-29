---
title: "SEC EDGAR Data Acquisition"
description: "Guide for acquiring SEC EDGAR financial data using Sumpter's retrieve command"
date: "2025-09-23"
author: "Sumpter Team"
tags: ["finance", "sec-edgar", "data-acquisition", "xml"]
---

# SEC EDGAR Data Acquisition

This application note explains how to use Sumpter's `retrieve` command to acquire SEC EDGAR financial data, specifically XML filings from public companies.

## Overview

The SEC EDGAR (Electronic Data Gathering, Analysis, and Retrieval) system contains financial disclosures from public companies. Sumpter provides automated tools to download these filings using the official SEC REST APIs, ensuring compliance and reliability.

## Prerequisites

- Sumpter binary installed and configured
- Output directory with write permissions
- Internet connection for SEC API access
- Optional: API key for enhanced data access (see Configuration section)

**Note**: Sumpter uses official SEC APIs with built-in rate limiting (1 request/second) to comply with SEC guidelines. For high-volume data acquisition, consider using commercial financial data providers with enhanced API access.

## Technical Details

Sumpter accesses SEC EDGAR data through official REST APIs:

- **Submissions API**: `https://data.sec.gov/submissions/CIK{cik}.json` - Retrieves filing metadata and document URLs
- **Direct Document Downloads**: Downloads XBRL XML files from SEC-provided URLs
- **Rate Limiting**: Configurable but hard-capped at 8 requests per second for SEC compliance (default: 8)
- **User-Agent**: Includes contact information as required by SEC

This approach ensures reliable, compliant access to SEC data without web scraping.

## Configuration

### API Key Setup (Optional)

While SEC EDGAR data is publicly accessible without authentication, some commercial financial data providers offer enhanced APIs with higher rate limits and additional data. If using such a service:

1. **Obtain an API Key**: Sign up with your chosen financial data provider (e.g., Intrinio, Alpha Vantage, Polygon.io) and obtain your API key.

2. **Set Environment Variable**: Store your API key securely as an environment variable. Never commit API keys to version control.

   ```bash
   export SUMPTER_FINANCE_API_KEY="your-api-key-here"
   ```

3. **Create Retrieve Configuration**: Use the interactive setup wizard to create a properly configured retrieve file:

    ```bash
    sumpter doctor config setup retrieve-sec-edgar
    ```

    This will guide you through:
    - Setting up your company name and contact email for SEC compliance
    - Configuring rate limits appropriate for SEC EDGAR access
    - Validating the configuration against the schema

    **Alternative**: If you prefer manual setup, copy the example configuration from `examples/config/retrieve/retrieve-config.yaml` to `SUMPTER_HOME/configs/retrieve.yaml` (where SUMPTER_HOME is typically `~/Library/Application Support/Sumpter` on macOS, `~/.local/share/sumpter` on Linux, or `%AppData%\Sumpter` on Windows):

    ```yaml
    version: "retrieve/v0.1.0"
    realms:
      finance:
        enabled: true
        client:
          user_agent: "Your Company Name contact@yourcompany.com"
        rate_limits:
          requests_per_second: 8
    ```

   **Important**: The `user_agent` field is required and must follow SEC guidelines: "Company Name contact@email.com". Do not use generic or placeholder values. The doctor command ensures compliance with these requirements.

4. **Load Configuration**: Sumpter will automatically load this configuration and use the API key from the environment variable.

**Security Note**: API keys should never be stored in the repository. Each user must obtain and configure their own keys. The configuration schema supports environment variable references to keep sensitive data secure.

## Usage

### Basic Command Structure

```bash
sumpter retrieve recipe finance sec-edgar \
  --ticker TICKER \
  --filing-type FILING_TYPE \
  --year YEAR
```

### Parameters

- `--ticker`: Stock ticker symbol (e.g., AAPL, MSFT)
- `--filing-type`: Filing type (e.g., 10-K, 10-Q, 8-K)
- `--year`: Fiscal year for the filing
- `--output-dir`: Directory to save downloaded files

### Supported Filing Types

- `10-K`: Annual report
- `10-Q`: Quarterly report
- `8-K`: Current report
- `DEF 14A`: Proxy statement

## Examples

### Download Apple Inc. 10-K for 2024

```bash
sumpter retrieve recipe finance sec-edgar \
  --ticker AAPL \
  --filing-type 10-K \
  --year 2024
```

This creates the directory structure:
```
./data/filings/
└── aapl/
    └── 10k/
        ├── xbrl-000032019324000106-2024-09-01.xml
        └── ...
```

### Download Multiple Companies

```bash
for ticker in AAPL MSFT GOOGL; do
  sumpter retrieve recipe finance sec-edgar \
    --ticker $ticker \
    --filing-type 10-K \
    --year 2024
done
```

## Manual Download Instructions

Due to SEC automation restrictions, automated downloads may fail. In such cases, follow these manual steps:

1. **Find the Filing**: Visit https://www.sec.gov/edgar/searchedgar/companysearch.html

2. **Search by Ticker**: Enter the company ticker and click "Search"

3. **Filter by Filing Type and Year**: Use the filters to find the desired filing

4. **Access Filing Details**: Click on the filing accession number

5. **Download XML Files**: Look for XML/XBRL files in the "Data Files" section

6. **Save to Expected Structure**: Save files to:
   ```
   <output-dir>/<ticker>/<filing-type>/<filename>.xml
   ```

## Troubleshooting

### Rate Limiting

**Error**: HTTP 429 or connection refused

**Solution**: Wait and retry, or use manual download method

### CIK Not Found

**Error**: "CIK not found for ticker"

**Solution**: Verify ticker symbol is correct and company is publicly traded

### No Filings Found

**Error**: "no known filing for CIK"

**Solution**: Check that the company filed the requested type in the given year

### Permission Denied

**Error**: Cannot create output directory

**Solution**: Ensure output directory is writable or create parent directories

### User Agent Issues

**Error**: HTTP 403 Forbidden or bot detection

**Solution**:
- Verify your `user_agent` in the retrieve config follows the format: "Company Name contact@email.com"
- Use your actual company name and valid contact email
- Do not use generic values like "Sample Company" or placeholder emails
- The SEC blocks requests with improper or missing user agents

### Configuration File Missing

**Error**: "retrieve config file not found"

**Solution**:
- Copy the example from `examples/config/retrieve/retrieve-config.yaml`
- Place it at `SUMPTER_HOME/configs/retrieve.yaml`
- Use `sumpter envinfo paths` to find your SUMPTER_HOME location
- Edit the file to include your actual company name and contact email
- The example file contains detailed comments and proper structure

## Data Format

Downloaded files are in XBRL (eXtensible Business Reporting Language) XML format, containing structured financial data with standardized tags.

## Next Steps

After downloading, use Sumpter's `inspect` command to analyze the XML structure:

```bash
sumpter inspect --file ./data/filings/aapl/10k/filing.xml
```

Or extract specific data using the `extract` command with appropriate XPath expressions.

For automated workflows, integrate the retrieve configuration into your CI/CD pipeline, ensuring API keys are provided via secure environment variables.