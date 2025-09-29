package extract

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/antchfx/xmlquery"
	xpath "github.com/antchfx/xpath"
	"github.com/fulmenhq/goneat/pkg/schema"
	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/validation"
	"gopkg.in/yaml.v3"
)

var (
	extractValidatorOnce sync.Once
	extractValidator     *validation.SchemaValidator
	extractValidatorErr  error
)

func getExtractSchemaValidator() (*validation.SchemaValidator, error) {
	extractValidatorOnce.Do(func() {
		schemaFS, err := assets.GetSchemasFS()
		if err != nil {
			extractValidatorErr = fmt.Errorf("failed to access embedded schemas: %w", err)
			return
		}
		extractValidator = validation.NewSchemaValidatorFromFS(schemaFS)
	})
	return extractValidator, extractValidatorErr
}

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

	validator, err := getExtractSchemaValidator()
	if err != nil {
		return nil, fmt.Errorf("failed to load extract schema validator: %w", err)
	}

	validationResult, err := validator.ValidateExtractConfig(data, path)
	if err != nil {
		return nil, fmt.Errorf("schema validation failed for %s: %w", path, err)
	}
	if !validationResult.IsValid() {
		return nil, fmt.Errorf("extract config validation failed for %s:\n%s", path, validationResult.ErrorSummary())
	}

	var cfg ExtractRecordMatch
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse extract config: %w", err)
	}

	if len(cfg.OutputSchema) > 0 {
		schemaBytes, err := json.Marshal(cfg.OutputSchema)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal output schema: %w", err)
		}
		validator, err := schema.NewValidatorFromBytes(schemaBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to compile output schema: %w", err)
		}
		cfg.OutputValidator = validator
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
				if cfg.OutputValidator != nil {
					res, err := cfg.OutputValidator.Validate(record)
					if err != nil {
						return nil, fmt.Errorf("output schema validation failed: %w", err)
					}
					if !res.Valid {
						return nil, fmt.Errorf("output schema validation failed:\n%s", formatValidationErrors(res.Errors))
					}
				}
				records = append(records, record)
			}
		}
	}

	return records, nil
}

// extractValue extracts a value using XPath
func extractValue(node *xmlquery.Node, mapping FieldMapping) (interface{}, error) {
	typeName := strings.ToLower(mapping.Type)

	if typeName == "array" {
		return extractArrayValue(node, mapping)
	}

	if mapping.Transform == "exists" {
		exists, err := evaluateExists(node, mapping.XPath)
		if err != nil {
			return nil, err
		}
		return exists, nil
	}

	if mapping.XPath == "" {
		return nil, nil
	}

	value, err := evaluateXPathValue(node, mapping.XPath)
	if err != nil {
		return nil, err
	}

	switch typeName {
	case "number":
		return coerceNumber(value)
	case "integer":
		return coerceInteger(value)
	case "boolean":
		return coerceBoolean(value)
	case "string", "":
		return coerceString(value)
	default:
		return value, nil
	}
}

func extractArrayValue(node *xmlquery.Node, mapping FieldMapping) (interface{}, error) {
	nodes, err := xmlquery.QueryAll(node, mapping.XPath)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
	}

	if len(nodes) == 0 {
		return nil, nil
	}

	if len(mapping.Polymorphic) > 0 {
		var items []map[string]interface{}
		for _, itemNode := range nodes {
			pm, targetNode, err := resolvePolymorphicMapping(itemNode, mapping.Polymorphic)
			if err != nil {
				return nil, err
			}
			if pm == nil {
				continue
			}
			if targetNode == nil {
				targetNode = itemNode
			}

			record := make(map[string]interface{})
			for _, fieldMap := range pm.FieldMappings {
				value, err := extractValue(targetNode, fieldMap)
				if err != nil {
					return nil, fmt.Errorf("failed to extract polymorphic field %s: %w", fieldMap.OutputField, err)
				}
				if value != nil {
					record[fieldMap.OutputField] = value
				}
			}

			if pm.ItemType != "" {
				if _, exists := record["item_type"]; !exists {
					record["item_type"] = pm.ItemType
				}
			}

			if len(record) > 0 {
				items = append(items, record)
			}
		}

		if len(items) == 0 {
			return nil, nil
		}
		return items, nil
	}

	if len(mapping.ItemMapping) > 0 {
		var items []map[string]interface{}
		for _, itemNode := range nodes {
			item := make(map[string]interface{})
			for _, itemMap := range mapping.ItemMapping {
				value, err := extractValue(itemNode, itemMap)
				if err != nil {
					return nil, err
				}
				item[itemMap.OutputField] = value
			}
			items = append(items, item)
		}
		if len(items) == 0 {
			return nil, nil
		}
		return items, nil
	}

	var values []interface{}
	for _, itemNode := range nodes {
		val := strings.TrimSpace(itemNode.InnerText())
		if val != "" {
			values = append(values, val)
		}
	}

	if len(values) == 0 {
		return nil, nil
	}

	return values, nil
}

func evaluateExists(node *xmlquery.Node, expr string) (bool, error) {
	if expr == "" {
		return false, nil
	}
	nodes, err := xmlquery.QueryAll(node, expr)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate XPath %s: %w", expr, err)
	}
	return len(nodes) > 0, nil
}

func evaluateXPathValue(node *xmlquery.Node, expr string) (interface{}, error) {
	compiled, err := xpath.Compile(expr)
	if err != nil {
		return nil, fmt.Errorf("failed to compile XPath %s: %w", expr, err)
	}

	value := compiled.Evaluate(xmlquery.CreateXPathNavigator(node))
	switch v := value.(type) {
	case nil:
		return nil, nil
	case bool:
		return v, nil
	case float64:
		return v, nil
	case string:
		return strings.TrimSpace(v), nil
	case *xpath.NodeIterator:
		if v.MoveNext() {
			return strings.TrimSpace(v.Current().Value()), nil
		}
		return nil, nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func coerceString(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return nil, nil
		}
		return trimmed, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func coerceNumber(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case float64:
		return v, nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		num, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse number %q: %w", s, err)
		}
		return num, nil
	case bool:
		if v {
			return float64(1), nil
		}
		return float64(0), nil
	default:
		return nil, fmt.Errorf("unsupported numeric value type %T", value)
	}
}

func coerceInteger(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case float64:
		return int64(v), nil
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return nil, nil
		}
		if strings.Contains(s, ".") {
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("failed to parse float %q for integer conversion: %w", s, err)
			}
			return int64(f), nil
		}
		num, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("failed to parse integer %q: %w", s, err)
		}
		return num, nil
	case bool:
		if v {
			return int64(1), nil
		}
		return int64(0), nil
	default:
		return nil, fmt.Errorf("unsupported integer value type %T", value)
	}
}

func coerceBoolean(value interface{}) (interface{}, error) {
	switch v := value.(type) {
	case nil:
		return false, nil
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		switch s {
		case "", "0", "false", "no", "off":
			return false, nil
		case "1", "true", "yes", "on":
			return true, nil
		default:
			return s != "", nil
		}
	default:
		return nil, fmt.Errorf("unsupported boolean value type %T", value)
	}
}

func resolvePolymorphicMapping(node *xmlquery.Node, mappings []PolymorphicMapping) (*PolymorphicMapping, *xmlquery.Node, error) {
	for i := range mappings {
		mapping := &mappings[i]
		var target *xmlquery.Node

		if mapping.MatchXPath != "" {
			matched, err := xmlquery.Query(node, mapping.MatchXPath)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to evaluate polymorphic match XPath %s: %w", mapping.MatchXPath, err)
			}
			if matched == nil {
				continue
			}
			target = matched
		} else if mapping.ElementType != "" {
			if strings.EqualFold(node.Data, mapping.ElementType) {
				target = node
			} else if child := findChildByName(node, mapping.ElementType); child != nil {
				target = child
			} else {
				continue
			}
		} else {
			target = node
		}

		return mapping, target, nil
	}

	return nil, nil, nil
}

func findChildByName(node *xmlquery.Node, name string) *xmlquery.Node {
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xmlquery.ElementNode && strings.EqualFold(child.Data, name) {
			return child
		}
	}
	return nil
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

func formatValidationErrors(errors []schema.ValidationError) string {
	if len(errors) == 0 {
		return ""
	}
	var b strings.Builder
	for i, err := range errors {
		path := err.Path
		if path == "" {
			path = "(root)"
		}
		fmt.Fprintf(&b, "  %d. %s: %s\n", i+1, path, err.Message)
	}
	return b.String()
}
