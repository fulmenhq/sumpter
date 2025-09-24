package extract

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/antchfx/xmlquery"
	"gopkg.in/yaml.v3"
)

// LoadSignatureConfig loads a signature configuration from YAML file
func LoadSignatureConfig(path string) (*FileSignature, error) {
	data, err := os.ReadFile(path) // #nosec G304 - path comes from user-provided config file path
	if err != nil {
		return nil, fmt.Errorf("failed to read signature config: %w", err)
	}

	var cfg FileSignature
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse signature config: %w", err)
	}

	return &cfg, nil
}

// LoadExtractConfig loads an extract configuration from YAML file
func LoadExtractConfig(path string) (*ExtractRecordMatch, error) {
	data, err := os.ReadFile(path) // #nosec G304 - path comes from user-provided config file path
	if err != nil {
		return nil, fmt.Errorf("failed to read extract config: %w", err)
	}

	var cfg ExtractRecordMatch
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse extract config: %w", err)
	}

	return &cfg, nil
}

// ProcessFile processes a single file for extraction
func ProcessFile(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}) ExtractResult {
	result := ExtractResult{File: filePath}

	// Read file content
	content, err := os.ReadFile(filePath) // #nosec G304 - filePath comes from user-provided file list or directory scan
	if err != nil {
		result.Error = fmt.Errorf("failed to read file: %w", err)
		return result
	}

	// Parse XML document
	doc, err := xmlquery.Parse(strings.NewReader(string(content)))
	if err != nil {
		result.Error = fmt.Errorf("failed to parse XML: %w", err)
		return result
	}

	// Check if file matches signature
	matches, err := matchesSignature(doc, sigCfg)
	if err != nil {
		result.Error = fmt.Errorf("failed to check signature: %w", err)
		return result
	}

	if !matches {
		// File doesn't match signature, return empty result
		return result
	}

	// Extract records
	records, err := extractRecords(doc, extCfg, externalFields)
	if err != nil {
		result.Error = fmt.Errorf("failed to extract records: %w", err)
		return result
	}

	result.Records = records
	return result
}

// matchesSignature checks if the document matches the signature
func matchesSignature(doc *xmlquery.Node, cfg *FileSignature) (bool, error) {
	score := 0.0
	totalWeight := 0.0

	for _, pattern := range cfg.MatchPatterns {
		totalWeight += pattern.Weight

		if matchesPattern(doc, pattern) {
			score += pattern.Weight
		}
	}

	if totalWeight == 0 {
		return false, fmt.Errorf("no patterns with weight > 0")
	}

	confidence := score / totalWeight
	return confidence >= cfg.ConfidenceThreshold, nil
}

// matchesPattern checks if a pattern matches the document
func matchesPattern(doc *xmlquery.Node, pattern MatchPattern) bool {
	// Use xmlquery to evaluate XPath
	nodes, err := xmlquery.QueryAll(doc, pattern.Selector)
	if err != nil {
		// If XPath evaluation fails, consider it not matching
		return false
	}
	// If we found any nodes matching the XPath, it matches
	return len(nodes) > 0
}

// extractRecords extracts records from document using the extract config
func extractRecords(doc *xmlquery.Node, cfg *ExtractRecordMatch, externalFields map[string]interface{}) ([]map[string]interface{}, error) {
	var records []map[string]interface{}

	for _, selector := range cfg.MatchSelectors {
		// Find all nodes matching the XPath selector
		nodes, err := xmlquery.QueryAll(doc, selector.XPath)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate XPath %s: %w", selector.XPath, err)
		}

		for _, node := range nodes {
			record := make(map[string]interface{})

			// Apply field mappings
			for _, mapping := range cfg.FieldMappings {
				value, err := extractValue(node, mapping)
				if err != nil {
					return nil, fmt.Errorf("failed to extract value for field %s: %w", mapping.OutputField, err)
				}
				if value != nil {
					record[mapping.OutputField] = value
				}
			}

			// Add external fields
			for key, value := range externalFields {
				record[key] = value
			}

			// Apply filters
			if passesFilters(record, cfg.Filters) {
				records = append(records, record)
			}
		}
	}

	return records, nil
}

// extractValue extracts a value using XPath
func extractValue(node *xmlquery.Node, mapping FieldMapping) (interface{}, error) {
	// Handle array types
	if mapping.Type == "array" && len(mapping.ItemMapping) > 0 {
		// Find all matching elements
		nodes, err := xmlquery.QueryAll(node, mapping.XPath)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
		}
		var items []map[string]interface{}
		for _, itemNode := range nodes {
			item := make(map[string]interface{})
			for _, itemMap := range mapping.ItemMapping {
				value, err := extractValue(itemNode, FieldMapping{
					XPath:     itemMap.XPath,
					Type:      itemMap.Type,
					Transform: itemMap.Transform,
				})
				if err != nil {
					return nil, err
				}
				item[itemMap.OutputField] = value
			}
			items = append(items, item)
		}
		return items, nil
	}

	// Handle special transforms
	if mapping.Transform == "exists" {
		// Check if the element exists
		nodes, err := xmlquery.QueryAll(node, mapping.XPath)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
		}
		return len(nodes) > 0, nil
	}

	// Evaluate XPath to get the value
	resultNode, err := xmlquery.Query(node, mapping.XPath)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
	}

	if resultNode == nil {
		return nil, nil
	}

	// Get the text content
	value := strings.TrimSpace(resultNode.InnerText())

	// Apply type conversion
	switch mapping.Type {
	case "number", "integer":
		if value == "" {
			return nil, nil
		}
		// Try to parse as number
		if strings.Contains(value, ".") {
			if num, err := strconv.ParseFloat(value, 64); err == nil {
				return num, nil
			}
		} else {
			if num, err := strconv.ParseInt(value, 10, 64); err == nil {
				return num, nil
			}
		}
		// If parsing fails, return as string
		return value, nil
	case "boolean":
		if value == "" {
			return false, nil
		}
		// For boolean, if value exists and is not empty, true
		return value != "", nil
	default:
		return value, nil
	}
}

// passesFilters checks if a record passes the filters
func passesFilters(record map[string]interface{}, filters map[string]interface{}) bool {
	// Simple filter implementation
	for key, condition := range filters {
		if value, exists := record[key]; exists {
			// Simple condition check (e.g., "> 0")
			if condStr, ok := condition.(string); ok {
				if strings.HasPrefix(condStr, "> ") {
					threshold := strings.TrimPrefix(condStr, "> ")
					// Basic comparison
					if valueStr, ok := value.(string); ok {
						if valueStr <= threshold {
							return false
						}
					}
				}
			}
		}
	}
	return true
}
