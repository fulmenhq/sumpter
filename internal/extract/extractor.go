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
	"github.com/fulmenhq/sumpter/internal/extract/transforms"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/validation"
	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

var (
	extractValidatorOnce sync.Once
	extractValidator     *validation.SchemaValidator
	extractValidatorErr  error
	transformRegistry    = transforms.NewTransformRegistry()
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

	if err := prepareExtractConfig(&cfg); err != nil {
		return nil, fmt.Errorf("failed to prepare extract config: %w", err)
	}

	return &cfg, nil
}

func prepareExtractConfig(cfg *ExtractRecordMatch) error {
	if cfg == nil {
		return fmt.Errorf("extract config is nil")
	}

	cfg.prepareOnce.Do(func() {
		if len(cfg.OutputSchema) > 0 && cfg.OutputValidator == nil {
			schemaBytes, err := json.Marshal(cfg.OutputSchema)
			if err != nil {
				cfg.prepareErr = fmt.Errorf("failed to marshal output schema: %w", err)
				return
			}
			validator, err := schema.NewValidatorFromBytes(schemaBytes)
			if err != nil {
				cfg.prepareErr = fmt.Errorf("failed to prepare output validator: %w", err)
				return
			}
			cfg.OutputValidator = validator
		}

		for i := range cfg.MatchSelectors {
			selector := &cfg.MatchSelectors[i]
			if strings.TrimSpace(selector.XPath) == "" {
				continue
			}
			if selector.CompiledXPath == nil {
				compiled, err := xpath.Compile(selector.XPath)
				if err != nil {
					cfg.prepareErr = fmt.Errorf("failed to compile match selector %q: %w", selector.XPath, err)
					return
				}
				selector.CompiledXPath = compiled
			}
		}

		for i := range cfg.FieldMappings {
			if err := compileFieldMapping(&cfg.FieldMappings[i]); err != nil {
				cfg.prepareErr = err
				return
			}
		}
	})

	return cfg.prepareErr
}

func compileFieldMapping(mapping *FieldMapping) error {
	if mapping == nil {
		return nil
	}

	if strings.TrimSpace(mapping.XPath) != "" && mapping.CompiledXPath == nil {
		compiled, err := xpath.Compile(mapping.XPath)
		if err != nil {
			return fmt.Errorf("failed to compile XPath %q for field %q: %w", mapping.XPath, mapping.OutputField, err)
		}
		mapping.CompiledXPath = compiled
	}

	for i := range mapping.ItemMapping {
		if err := compileFieldMapping(&mapping.ItemMapping[i]); err != nil {
			return err
		}
	}

	for i := range mapping.Polymorphic {
		pm := &mapping.Polymorphic[i]
		if strings.TrimSpace(pm.MatchXPath) != "" && pm.CompiledMatchXPath == nil {
			compiled, err := xpath.Compile(pm.MatchXPath)
			if err != nil {
				return fmt.Errorf("failed to compile polymorphic match XPath %q: %w", pm.MatchXPath, err)
			}
			pm.CompiledMatchXPath = compiled
		}
		for j := range pm.FieldMappings {
			if err := compileFieldMapping(&pm.FieldMappings[j]); err != nil {
				return err
			}
		}
	}

	return nil
}

// ProcessFile processes a single file for extraction
func ProcessFile(filePath string, sigCfg *FileSignature, extCfg *ExtractRecordMatch, externalFields map[string]interface{}) ExtractResult {
	logger := logging.GetLogger()
	if logger == nil {
		logger = zap.NewNop()
	}
	logger.Debug("Starting file processing", zap.String("file", filePath))

	result := ExtractResult{File: filePath}

	// Read file content
	logger.Debug("Reading file content", zap.String("file", filePath))
	content, err := os.ReadFile(filePath) // #nosec G304 - filePath comes from user-provided file list or directory scan
	if err != nil {
		logger.Error("Failed to read file", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to read file: %w", err)
		return result
	}
	logger.Debug("File read successfully", zap.String("file", filePath), zap.Int("size", len(content)))

	// Parse XML document
	logger.Debug("Parsing XML document", zap.String("file", filePath))
	doc, err := xmlquery.Parse(strings.NewReader(string(content)))
	if err != nil {
		logger.Error("Failed to parse XML", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to parse XML: %w", err)
		return result
	}
	logger.Debug("XML parsed successfully", zap.String("file", filePath))

	// Check if file matches signature
	logger.Debug("Checking signature match", zap.String("file", filePath), zap.String("signature", sigCfg.SignatureID))
	matches, err := matchesSignature(doc, sigCfg)
	if err != nil {
		logger.Error("Failed to check signature", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to check signature: %w", err)
		return result
	}
	logger.Debug("Signature check complete", zap.String("file", filePath), zap.Bool("matches", matches))

	if !matches {
		// File doesn't match signature, return empty result
		logger.Debug("File does not match signature", zap.String("file", filePath))
		return result
	}

	if err := prepareExtractConfig(extCfg); err != nil {
		logger.Error("Failed to prepare extract config", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to prepare extract config: %w", err)
		return result
	}

	// Extract records
	logger.Debug("Starting record extraction", zap.String("file", filePath), zap.String("record_type", extCfg.RecordType))
	records, err := extractRecords(doc, extCfg, externalFields)
	if err != nil {
		logger.Error("Failed to extract records", zap.String("file", filePath), zap.Error(err))
		result.Error = fmt.Errorf("failed to extract records: %w", err)
		return result
	}
	logger.Debug("Record extraction complete", zap.String("file", filePath), zap.Int("record_count", len(records)))

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
	// Handle different types of XPath expressions
	selector := strings.TrimSpace(pattern.Selector)

	// For boolean expressions (like count(...) > 0), evaluate as value
	if strings.Contains(selector, ">") || strings.Contains(selector, "<") || strings.Contains(selector, "=") ||
		strings.Contains(selector, " and ") || strings.Contains(selector, " or ") ||
		strings.HasPrefix(selector, "boolean(") {
		// For count(...) > 0 expressions, evaluate the count and compare
		if strings.HasPrefix(selector, "count(") && strings.HasSuffix(selector, ") > 0") {
			countPath := selector[6 : len(selector)-5] // Extract path from count(path) > 0
			nodes, err := xmlquery.QueryAll(doc, countPath)
			if err != nil {
				return false
			}
			return len(nodes) > 0
		}
		// For other boolean expressions, fall back to false for now
		return false
	}

	// For count() expressions without comparison, evaluate the count
	if strings.HasPrefix(selector, "count(") && strings.HasSuffix(selector, ")") {
		countPath := selector[6 : len(selector)-1] // Extract path from count(path)
		nodes, err := xmlquery.QueryAll(doc, countPath)
		if err != nil {
			return false
		}
		// Return true if count > 0
		return len(nodes) > 0
	}

	// For node selectors, check if any nodes match
	nodes, err := xmlquery.QueryAll(doc, selector)
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

	for i := range cfg.MatchSelectors {
		selector := &cfg.MatchSelectors[i]
		nodes, err := evaluateNodeSet(doc, selector.CompiledXPath, selector.XPath)
		if err != nil {
			return nil, fmt.Errorf("failed to evaluate XPath %s: %w", selector.XPath, err)
		}
		if len(nodes) == 0 {
			continue
		}

		for _, node := range nodes {
			record := make(map[string]interface{})

			// Apply field mappings
			for j := range cfg.FieldMappings {
				mapping := &cfg.FieldMappings[j]
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
func extractValue(node *xmlquery.Node, mapping *FieldMapping) (interface{}, error) {
	if mapping == nil {
		return nil, nil
	}

	typeName := strings.ToLower(mapping.Type)

	if typeName == "array" {
		return extractArrayValue(node, mapping)
	}

	if mapping.XPath == "" {
		return nil, nil
	}

	value, err := evaluateXPathValue(node, mapping.XPath, mapping.CompiledXPath)
	if err != nil {
		return nil, err
	}

	if mapping.Transform == "exists" {
		exists, err := evaluateExists(node, mapping.XPath, mapping.CompiledXPath)
		if err != nil {
			return nil, err
		}
		return exists, nil
	}

	if mapping.Transform != "" {
		transformed, err := transformRegistry.Apply(mapping.Transform, fmt.Sprintf("%v", value), mapping.TransformParams)
		if err != nil {
			return nil, err
		}
		return transformed, nil
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

func extractArrayValue(node *xmlquery.Node, mapping *FieldMapping) (interface{}, error) {
	if mapping == nil {
		return nil, nil
	}

	nodes, err := evaluateNodeSet(node, mapping.CompiledXPath, mapping.XPath)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate XPath %s: %w", mapping.XPath, err)
	}

	if len(nodes) == 0 {
		return nil, nil
	}

	if len(mapping.Polymorphic) > 0 {
		var items []map[string]interface{}
		for _, sourceNode := range nodes {
			for i := range mapping.Polymorphic {
				pm := &mapping.Polymorphic[i]
				targetNodes, err := resolvePolymorphicTargets(sourceNode, pm)
				if err != nil {
					return nil, err
				}
				for _, targetNode := range targetNodes {
					record := make(map[string]interface{})
					for j := range pm.FieldMappings {
						fieldMap := &pm.FieldMappings[j]
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
			for j := range mapping.ItemMapping {
				itemMap := &mapping.ItemMapping[j]
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

func evaluateExists(node *xmlquery.Node, expr string, compiled *xpath.Expr) (bool, error) {
	if node == nil {
		return false, nil
	}
	if strings.TrimSpace(expr) == "" && compiled == nil {
		return false, nil
	}

	nodes, err := evaluateNodeSet(node, compiled, expr)
	if err != nil {
		return false, fmt.Errorf("failed to evaluate XPath %s: %w", expr, err)
	}
	return len(nodes) > 0, nil
}

func evaluateXPathValue(node *xmlquery.Node, expr string, compiled *xpath.Expr) (interface{}, error) {
	if node == nil {
		return nil, nil
	}
	if strings.TrimSpace(expr) == "" && compiled == nil {
		return nil, nil
	}

	xp := compiled
	var err error
	if xp == nil {
		xp, err = xpath.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile XPath %s: %w", expr, err)
		}
	}

	value := xp.Evaluate(xmlquery.CreateXPathNavigator(node))
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

func evaluateNodeSet(node *xmlquery.Node, compiled *xpath.Expr, expr string) ([]*xmlquery.Node, error) {
	if node == nil {
		return nil, nil
	}

	xp := compiled
	var err error
	if xp == nil {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			return nil, nil
		}
		xp, err = xpath.Compile(expr)
		if err != nil {
			return nil, fmt.Errorf("failed to compile XPath %q: %w", expr, err)
		}
	}

	value := xp.Evaluate(xmlquery.CreateXPathNavigator(node))
	switch v := value.(type) {
	case *xpath.NodeIterator:
		return collectNodes(v), nil
	case xpath.NodeIterator:
		return collectNodes(&v), nil
	case bool:
		if v {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	case float64:
		if v != 0 {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	case string:
		if strings.TrimSpace(v) != "" {
			return []*xmlquery.Node{}, nil
		}
		return nil, nil
	default:
		return nil, nil
	}
}

func collectNodes(iter *xpath.NodeIterator) []*xmlquery.Node {
	var nodes []*xmlquery.Node
	if iter == nil {
		return nodes
	}

	for iter.MoveNext() {
		if nav, ok := iter.Current().(*xmlquery.NodeNavigator); ok && nav != nil {
			if current := nav.Current(); current != nil {
				nodes = append(nodes, current)
			}
		}
	}
	return nodes
}

func resolvePolymorphicTargets(node *xmlquery.Node, mapping *PolymorphicMapping) ([]*xmlquery.Node, error) {
	if mapping == nil {
		return nil, nil
	}

	if strings.TrimSpace(mapping.MatchXPath) != "" || mapping.CompiledMatchXPath != nil {
		return evaluateNodeSet(node, mapping.CompiledMatchXPath, mapping.MatchXPath)
	}

	if strings.TrimSpace(mapping.ElementType) != "" {
		matches := findChildrenByNameAll(node, mapping.ElementType)
		if strings.EqualFold(node.Data, mapping.ElementType) {
			matches = append([]*xmlquery.Node{node}, matches...)
		}
		return matches, nil
	}

	return []*xmlquery.Node{node}, nil
}

func findChildrenByNameAll(node *xmlquery.Node, name string) []*xmlquery.Node {
	var result []*xmlquery.Node
	if node == nil {
		return result
	}

	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == xmlquery.ElementNode && strings.EqualFold(child.Data, name) {
			result = append(result, child)
		}
	}

	return result
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
