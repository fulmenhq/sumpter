package regulatory

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/utils"
	"go.uber.org/zap"
)

// SecEdgarClient handles SEC EDGAR data sourcing
type SecEdgarClient struct {
	httpClient  *http.Client
	rateLimiter *time.Ticker
}

// NewSecEdgarClient creates a new SEC EDGAR client with rate limiting
func NewSecEdgarClient(userAgent string, requestsPerSecond float64) *SecEdgarClient {
	// Hard limit: Cap at 8 requests per second for safety (SEC allows 10/sec)
	// This is enforced regardless of user configuration
	originalRate := requestsPerSecond
	if requestsPerSecond > 8.0 {
		requestsPerSecond = 8.0
	}
	if requestsPerSecond <= 0 {
		requestsPerSecond = 8.0 // Default to safe limit
	}

	// Log if rate was capped
	if originalRate != requestsPerSecond {
		logger := logging.GetLogger()
		logger.Warn("Rate limit capped for SEC compliance",
			zap.Float64("requested", originalRate),
			zap.Float64("capped_to", requestsPerSecond))
	}

	// Convert to ticker interval (requests per second -> duration between requests)
	interval := time.Duration(float64(time.Second) / requestsPerSecond)
	ticker := time.NewTicker(interval)

	return &SecEdgarClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &userAgentTransport{
				userAgent: userAgent,
				inner:     http.DefaultTransport,
			},
		},
		rateLimiter: ticker,
	}
}

// userAgentTransport adds SEC-compliant User-Agent header
type userAgentTransport struct {
	userAgent string
	inner     http.RoundTripper
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("User-Agent", t.userAgent)
	return t.inner.RoundTrip(req)
}

// Close cleans up resources
func (c *SecEdgarClient) Close() {
	c.rateLimiter.Stop()
}

// DownloadFiling downloads SEC EDGAR filing for given parameters
func (c *SecEdgarClient) DownloadFiling(ticker, filingType, year, outputBase string) error {
	logger := logging.GetLogger()
	logger.Info("Starting SEC EDGAR download",
		zap.String("ticker", ticker),
		zap.String("filing_type", filingType),
		zap.String("year", year),
		zap.String("output_base", outputBase))

	// Get CIK
	cik, err := c.getCIK(ticker)
	if err != nil {
		return fmt.Errorf("failed to get CIK for %s: %w", ticker, err)
	}

	// Create output directory with command-specific structure
	commandDir := "retrieve"
	companyDir := strings.ToLower(ticker)
	filingDir := strings.ToLower(strings.ReplaceAll(filingType, "-", ""))
	outputDir := filepath.Join(outputBase, commandDir, companyDir, filingDir)
	if err := os.MkdirAll(outputDir, 0750); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Find recent filing
	filing, err := c.findRecentFiling(cik, filingType, year)
	if err != nil {
		return fmt.Errorf("failed to find filing: %w", err)
	}

	// Download XML files
	return c.downloadFilingFiles(filing, outputDir)
}

// getCIK looks up CIK for ticker
func (c *SecEdgarClient) getCIK(ticker string) (string, error) {
	// Hardcoded common tickers
	ciks := map[string]string{
		"AAPL":  "0000320193",
		"MSFT":  "0000789019",
		"GOOGL": "0001652044",
		"AMZN":  "0001018724",
		"GM":    "0001467858",
	}

	if cik, exists := ciks[strings.ToUpper(ticker)]; exists {
		return cik, nil
	}

	// Fallback to SEC ticker list
	<-c.rateLimiter.C // Rate limit
	resp, err := c.httpClient.Get("https://www.sec.gov/include/ticker.txt")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	lines := strings.Split(string(body), "\n")
	for _, line := range lines {
		parts := strings.Split(strings.TrimSpace(line), "\t")
		if len(parts) >= 2 && strings.EqualFold(parts[0], ticker) {
			return fmt.Sprintf("%010s", parts[1]), nil
		}
	}

	return "", fmt.Errorf("CIK not found for ticker %s", ticker)
}

// FilingInfo holds information about a SEC filing
type FilingInfo struct {
	CIK       string
	Accession string
	Date      string
	Form      string
	Documents []string // URLs to XML documents
}

// findRecentFiling finds a recent filing matching criteria using SEC API
func (c *SecEdgarClient) findRecentFiling(cik, filingType, year string) (*FilingInfo, error) {
	logger := logging.GetLogger()

	// Use SEC Submissions API
	apiURL := fmt.Sprintf("https://data.sec.gov/submissions/CIK%s.json", cik)

	<-c.rateLimiter.C
	resp, err := c.httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch submissions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse JSON response
	var submissions struct {
		CIK     string `json:"cik"`
		Filings struct {
			Recent struct {
				AccessionNumber     []string `json:"accessionNumber"`
				FilingDate          []string `json:"filingDate"`
				Form                []string `json:"form"`
				PrimaryDocument     []string `json:"primaryDocument"`
				DocumentFormatFiles []struct {
					DocumentURL []string `json:"documentUrl"`
					Type        []string `json:"type"`
					Sequence    []string `json:"sequence"`
				} `json:"documentFormatFiles"`
			} `json:"recent"`
		} `json:"filings"`
	}

	if err := json.Unmarshal(body, &submissions); err != nil {
		logger.Error("JSON unmarshal error", zap.Error(err))
		logger.Info("Raw JSON response", zap.String("body", string(body)))
		return nil, fmt.Errorf("failed to parse JSON: %w", err)
	}

	// Find matching filing (for now, return most recent 10-K)
	recent := submissions.Filings.Recent
	for i := 0; i < len(recent.Form); i++ {
		if recent.Form[i] == filingType {
			// Check year if provided
			if year != "" {
				filingYear := strings.Split(recent.FilingDate[i], "-")[0]
				if filingYear != year {
					continue
				}
			}

			// Collect XML document URLs
			var documents []string

			// Try to find XBRL files based on primary document filename
			baseURL := fmt.Sprintf("https://www.sec.gov/Archives/edgar/data/%s/%s/", cik, strings.ReplaceAll(recent.AccessionNumber[i], "-", ""))

			// Extract base name from primary document (remove .htm extension)
			primaryBase := strings.TrimSuffix(recent.PrimaryDocument[i], ".htm")
			primaryBase = strings.TrimSuffix(primaryBase, ".html")

			// Common XBRL file extensions
			xbrlExtensions := []string{"", "_cal", "_def", "_lab", "_pre"}
			xbrlTypes := []string{".xml", ".xsd"}

			for _, ext := range xbrlExtensions {
				for _, typeExt := range xbrlTypes {
					filename := primaryBase + ext + typeExt
					docURL := baseURL + filename

					// Quick check if file exists (HEAD request)
					<-c.rateLimiter.C
					headResp, err := c.httpClient.Head(docURL)
					if err == nil && headResp.StatusCode == 200 {
						documents = append(documents, docURL)
					}
				}
			}

			if len(documents) == 0 {
				logger.Warn("No XML documents found in filing",
					zap.String("accession", recent.AccessionNumber[i]),
					zap.String("cik", cik))
			}

			logger.Info("Found matching filing",
				zap.String("cik", cik),
				zap.String("form", recent.Form[i]),
				zap.String("accession", recent.AccessionNumber[i]),
				zap.String("date", recent.FilingDate[i]),
				zap.Int("xml_docs", len(documents)))

			return &FilingInfo{
				CIK:       cik,
				Accession: recent.AccessionNumber[i],
				Date:      recent.FilingDate[i],
				Form:      recent.Form[i],
				Documents: documents,
			}, nil
		}
	}

	return nil, fmt.Errorf("no %s filing found for CIK %s in year %s", filingType, cik, year)
}

// downloadFilingFiles downloads XML files from a filing
func (c *SecEdgarClient) downloadFilingFiles(filing *FilingInfo, outputDir string) error {
	logger := logging.GetLogger()

	logger.Info("Downloading XML files from filing",
		zap.String("cik", filing.CIK),
		zap.String("accession", filing.Accession),
		zap.Int("document_count", len(filing.Documents)))

	downloaded := 0
	for _, docURL := range filing.Documents {
		// Extract filename from URL
		parts := strings.Split(docURL, "/")
		filename := parts[len(parts)-1]

		// Create output filename with accession and date
		outputFilename := fmt.Sprintf("%s-%s-%s.xml",
			strings.TrimSuffix(filename, filepath.Ext(filename)),
			filing.Accession,
			filing.Date)
		outputPath := filepath.Join(outputDir, outputFilename)

		logger.Info("Downloading XML file",
			zap.String("url", docURL),
			zap.String("output_path", outputPath))

		<-c.rateLimiter.C
		if err := c.downloadFile(docURL, outputPath); err != nil {
			logger.Error("Failed to download XML file",
				zap.String("url", docURL),
				zap.Error(err))
			continue
		}
		downloaded++
	}

	if downloaded == 0 {
		return fmt.Errorf("no XML files downloaded")
	}

	logger.Info("Downloaded XML files", zap.Int("count", downloaded))
	return nil
}

// parseFilingIndexXML parses the SEC filing index XML to find XML documents
// Deprecated: This function is no longer used and may be removed in future versions
//
//nolint:unused
func (c *SecEdgarClient) parseFilingIndexXML(indexXML, baseURL string) []string {
	var documents []string

	// Simple XML parsing to find document entries
	lines := strings.Split(indexXML, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "<document>") || strings.Contains(line, "<file>") {
			// Look for filename in the next lines
			continue
		}
		if strings.Contains(line, "<filename>") && strings.Contains(line, ".xml") {
			// Extract filename
			start := strings.Index(line, "<filename>") + 10
			end := strings.Index(line[start:], "</filename>")
			if end > 0 {
				filename := line[start : start+end]
				if strings.Contains(filename, ".xml") {
					// Build full URL
					docURL := strings.TrimSuffix(baseURL, "index.xml") + filename
					documents = append(documents, docURL)
				}
			}
		}
	}

	return documents
}

// downloadFile downloads a file from URL to local path
func (c *SecEdgarClient) downloadFile(url, filepath string) (err error) {
	resp, err := c.httpClient.Get(url)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Get current working directory for path validation
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate user-provided file path
	if err := utils.ValidateUserPathForCreate(filepath, utils.RootCwd, cwd); err != nil {
		return fmt.Errorf("invalid download path: %w", err)
	}

	out, err := os.Create(filepath) // #nosec G304 - Path validated by ValidateUserPathForCreate
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err = io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("failed to write response body: %w", err)
	}

	return nil
}
