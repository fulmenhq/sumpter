#!/usr/bin/env bun

import { $ } from "bun";

// Script to download SEC EDGAR XML files for a given ticker and date
// Usage: bun run scripts/download-sec-edgar.ts <ticker> <date> [count] [output-base]

async function main() {
  const args = process.argv.slice(2);
  if (args.length < 2) {
    console.error("Usage: bun run scripts/download-sec-edgar.ts <ticker> <date> [count] [output-base]");
    console.error("Example: bun run scripts/download-sec-edgar.ts AAPL 2024-09-01 5 ~/docs/playground/public/finance/sec-edgar");
    process.exit(1);
  }

  const ticker = args[0].toUpperCase();
  const dateStr = args[1];
  const count = parseInt(args[2] || "5");
  const outputBase = args[3] || ".scratchpad/data/finance/sec-edgar";

  console.log(`Downloading up to ${count} large XML files for ${ticker} around ${dateStr}`);

  // Get CIK for ticker
  const cik = await getCIK(ticker);
  if (!cik) {
    console.error(`Could not find CIK for ticker ${ticker}`);
    process.exit(1);
  }

  console.log(`Found CIK: ${cik} for ${ticker}`);

  // Get filings for the company
  const filings = await getFilings(cik, dateStr, count * 2); // Get more to filter

  // Download large XML files
  const baseDir = outputBase.replace(/^~/, process.env.HOME || "");
  const companyDir = `${baseDir}/${ticker.toLowerCase()}`;
  const filingDir = `${companyDir}/10k`; // Assume 10-K for now
  await $`mkdir -p ${filingDir}`;

  let downloaded = 0;
  for (const filing of filings) {
    if (downloaded >= count) break;

    const xmlFiles = await getXMLFiles(cik, filing);
    for (const xmlFile of xmlFiles) {
      if (xmlFile.size > 10000) { // >10KB for testing
        const dateFormatted = filing.date.replace(/[^0-9]/g, "").slice(0, 8); // YYYYMMDD
        const filename = `${ticker.toLowerCase()}-${filing.accession}-${dateFormatted}-${xmlFile.name}`;
        const outputPath = `${filingDir}/${filename}`;
        console.log(`Downloading ${xmlFile.name} (${xmlFile.size} bytes) to ${outputPath}`);
        try {
          await $`curl -L -o ${outputPath} ${xmlFile.url}`;
          downloaded++;
          if (downloaded >= count) break;
        } catch (e) {
          console.error(`Failed to download ${xmlFile.url}: ${e}`);
        }
      }
    }
  }

  console.log(`Downloaded ${downloaded} files`);
}

async function getCIK(ticker: string): Promise<string | null> {
  // Hardcode common tickers for now
  const knownCIKs: Record<string, string> = {
    "AAPL": "0000320193",
    "GM": "0001467858",
    "MSFT": "0000789019",
    "AMZN": "0001018724",
    "GOOGL": "0001652044",
  };

  if (knownCIKs[ticker]) {
    return knownCIKs[ticker];
  }

  // Fallback to API
  try {
    const response = await fetch("https://www.sec.gov/include/ticker.txt");
    const text = await response.text();
    const lines = text.split("\n");
    for (const line of lines) {
      const parts = line.split("\t");
      if (parts.length >= 2 && parts[0].toUpperCase() === ticker) {
        return parts[1].padStart(10, "0");
      }
    }
  } catch (e) {
    console.error("Failed to fetch ticker list:", e);
  }
  return null;
}

async function getFilings(cik: string, dateStr: string, maxCount: number): Promise<Array<{accession: string, date: string}>> {
  const filings: Array<{accession: string, date: string}> = [];

  try {
    // Get company filings page
    const url = `https://www.sec.gov/cgi-bin/browse-edgar?action=getcompany&CIK=${cik}&owner=exclude&count=${maxCount}`;
    const response = await fetch(url);
    const html = await response.text();

    // Parse HTML for filing links (simple regex)
    const filingRegex = /<a href="\/Archives\/edgar\/data\/\d+\/([^\/]+)\/[^"]*">([^<]+)<\/a>/g;
    let match;
    while ((match = filingRegex.exec(html)) !== null) {
      const accession = match[1];
      const date = match[2];
      if (date.includes(dateStr.split("-")[0])) { // Rough date match
        filings.push({ accession, date });
      }
    }
  } catch (e) {
    console.error("Failed to fetch filings:", e);
  }

  return filings.slice(0, maxCount);
}

async function getXMLFiles(cik: string, filing: {accession: string, date: string}): Promise<Array<{name: string, url: string, size: number}>> {
  const xmlFiles: Array<{name: string, url: string, size: number}> = [];

  try {
    // Get filing index
    const cik = filing.accession.split("")[0]; // Approximate
    const url = `https://www.sec.gov/Archives/edgar/data/${cik}/${filing.accession}/`;
    const response = await fetch(url);
    const html = await response.text();

    // Parse for XML files (simple)
    const fileRegex = /<a href="([^"]*\.xml)">([^<]+)<\/a>\s*(\d+)/g;
    let match;
    while ((match = fileRegex.exec(html)) !== null) {
      const url = match[1];
      const name = match[2];
      const size = parseInt(match[3]);
      xmlFiles.push({ name, url: `https://www.sec.gov${url}`, size });
    }
  } catch (e) {
    console.error("Failed to fetch filing files:", e);
  }

  return xmlFiles;
}

main().catch(console.error);
