package inspect

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/net/html/charset"
)

// DialectDetector performs dialect detection on XML streams
type DialectDetector struct {
	registry *DialectRegistry
	logger   Logger
	options  DetectorOptions
}

// NewDialectDetector creates a new dialect detector
func NewDialectDetector(registry *DialectRegistry, logger Logger, options DetectorOptions) *DialectDetector {
	if options.MaxTokens == 0 {
		options.MaxTokens = 500
	}
	if options.MinConfidence == 0 {
		options.MinConfidence = 0.5
	}

	return &DialectDetector{
		registry: registry,
		logger:   logger,
		options:  options,
	}
}

// DetectDialect analyzes an XML stream and returns the best matching dialect
func (d *DialectDetector) DetectDialect(reader io.Reader) (*DetectionResult, error) {
	decoder := xml.NewDecoder(reader)
	// Wire a CharsetReader so XML files declaring non-UTF-8 encodings (e.g.
	// "ISO-8859-1", "windows-1252") are decoded correctly instead of erroring
	// with "Decoder.CharsetReader is nil".
	decoder.CharsetReader = charset.NewReaderLabel

	// Track element and attribute observations
	elementCounts := make(map[string]int)
	attributeCounts := make(map[string]int)
	namespaceCounts := make(map[string]int)

	tokenCount := 0
	maxTokens := d.options.MaxTokens

	// Analyze tokens
	for tokenCount < maxTokens {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("XML parsing error during detection: %w", err)
		}

		tokenCount++

		switch element := token.(type) {
		case xml.StartElement:
			// Track element name (local name only for namespace flexibility)
			elementName := element.Name.Local
			elementCounts[elementName]++

			// Track namespace if present
			if element.Name.Space != "" {
				namespaceCounts[element.Name.Space]++
			}

			// Track attributes
			for _, attr := range element.Attr {
				attrName := attr.Name.Local
				attributeCounts[attrName]++
			}
		}
	}

	d.logger.Debug("detection analysis complete",
		zap.Int("tokens_processed", tokenCount),
		zap.Int("unique_elements", len(elementCounts)),
		zap.Int("unique_attributes", len(attributeCounts)),
		zap.Any("elements", elementCounts),
		zap.Any("attributes", attributeCounts))

	// Find best matching dialect
	return d.findBestMatch(elementCounts, attributeCounts, namespaceCounts)
}

// findBestMatch compares observations against dialect patterns
func (d *DialectDetector) findBestMatch(elementCounts, attributeCounts, namespaceCounts map[string]int) (*DetectionResult, error) {
	if len(d.registry.Dialects) == 0 {
		return &DetectionResult{
			DialectName: "unknown",
			Confidence:  0.0,
			Score:       0.0,
		}, nil
	}

	var bestMatch *DetectionResult
	bestScore := 0.0

	for _, dialect := range d.registry.Dialects {
		score, breakdown, matchedPatterns := d.scoreDialect(dialect, elementCounts, attributeCounts, namespaceCounts)

		confidence := score
		if dialect.ConfidenceThreshold > 0 {
			confidence = score / dialect.ConfidenceThreshold
			if confidence > 1.0 {
				confidence = 1.0
			}
		}

		d.logger.Debug("dialect scored",
			zap.String("dialect_id", dialect.DialectID),
			zap.String("name", dialect.Name),
			zap.Float64("score", score),
			zap.Float64("confidence", confidence),
			zap.Float64("threshold", dialect.ConfidenceThreshold))

		if confidence >= d.options.MinConfidence && score > bestScore {
			result := &DetectionResult{
				DialectName: dialect.Name,
				Confidence:  confidence,
				Score:       score,
			}

			if d.options.IncludeBreakdown {
				result.ScoreBreakdown = breakdown
				result.MatchedPatterns = matchedPatterns
			}

			bestMatch = result
			bestScore = score
		}
	}

	if bestMatch == nil {
		return &DetectionResult{
			DialectName: "unknown",
			Confidence:  0.0,
			Score:       0.0,
		}, nil
	}

	d.logger.Info("dialect detected",
		zap.String("dialect_name", bestMatch.DialectName),
		zap.Float64("confidence", bestMatch.Confidence),
		zap.Float64("score", bestMatch.Score))

	return bestMatch, nil
}

// scoreDialect calculates how well a dialect matches the observed XML structure
func (d *DialectDetector) scoreDialect(dialect Dialect, elementCounts, attributeCounts, namespaceCounts map[string]int) (float64, map[string]float64, []string) {
	totalScore := 0.0
	breakdown := make(map[string]float64)
	var matchedPatterns []string

	for _, pattern := range dialect.Patterns {
		score := d.scorePattern(pattern, elementCounts, attributeCounts, namespaceCounts)
		d.logger.Debug("pattern scored",
			zap.String("dialect", dialect.DialectID),
			zap.String("pattern_id", pattern.PatternID),
			zap.String("selector", pattern.Selector),
			zap.Float64("score", score),
			zap.Float64("weight", pattern.Weight))
		if score > 0 {
			totalScore += score * pattern.Weight
			breakdown[pattern.PatternID] = score * pattern.Weight
			matchedPatterns = append(matchedPatterns, pattern.Name)
		}
	}

	return totalScore, breakdown, matchedPatterns
}

// scorePattern evaluates a single pattern against observations
func (d *DialectDetector) scorePattern(pattern Pattern, elementCounts, attributeCounts, namespaceCounts map[string]int) float64 {
	selector := pattern.Selector

	// Simple selector matching (can be enhanced with proper XPath support)
	if strings.HasPrefix(selector, "local-name()=") {
		// Element name selector like local-name()='POSLog'
		elementName := strings.Trim(strings.TrimPrefix(selector, "local-name()="), "'\"")

		// Check if any path contains this element name
		for elementPath := range elementCounts {
			// Split path by dots and check each segment
			parts := strings.Split(elementPath, ".")
			for _, part := range parts {
				if part == elementName {
					return 1.0
				}
			}
		}
	} else if strings.HasPrefix(selector, "@") {
		// Attribute selector like @BusinessDayDate
		attrName := strings.TrimPrefix(selector, "@")
		if count, exists := attributeCounts[attrName]; exists && count > 0 {
			return 1.0
		}
	}

	return 0.0
}
