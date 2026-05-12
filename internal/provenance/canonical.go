package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	jsoncanonicalizer "github.com/cyberphone/json-canonicalization/go/src/webpki.org/jsoncanonicalizer"
	"golang.org/x/text/unicode/norm"
	"gopkg.in/yaml.v3"
)

const (
	hashPrefix       = "sha256:"
	maxSafeJSONInt   = int64(1<<53 - 1)
	minSafeJSONInt   = -maxSafeJSONInt
	recipeHashGlue   = "\n---\n"
	yamlIntTag       = "!!int"
	yamlFloatTag     = "!!float"
	yamlStringTag    = "!!str"
	yamlBoolTag      = "!!bool"
	yamlNullTag      = "!!null"
	yamlTimestampTag = "!!timestamp"
)

// CanonicalizeYAML parses YAML into generic JSON-compatible data, normalizes
// string keys and values to NFC, and returns RFC 8785 JCS bytes.
func CanonicalizeYAML(data []byte) ([]byte, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("parse YAML: %w", err)
	}
	if len(root.Content) == 0 {
		return nil, fmt.Errorf("YAML document is empty")
	}

	value, err := yamlNodeToJSON(root.Content[0])
	if err != nil {
		return nil, err
	}

	jsonData, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("marshal normalized YAML as JSON: %w", err)
	}

	canonical, err := jsoncanonicalizer.Transform(jsonData)
	if err != nil {
		return nil, fmt.Errorf("canonicalize JSON using RFC 8785 JCS: %w", err)
	}
	return canonical, nil
}

// RecipeContentHash returns the ADR-0006 recipe content hash over raw
// signature.yaml and extract.yaml bytes. The manifest is intentionally out of
// scope; callers pass recipe version and ownership metadata separately.
func RecipeContentHash(signatureYAML, extractYAML []byte) (string, error) {
	signatureCanonical, err := CanonicalizeYAML(signatureYAML)
	if err != nil {
		return "", fmt.Errorf("canonicalize signature YAML: %w", err)
	}
	extractCanonical, err := CanonicalizeYAML(extractYAML)
	if err != nil {
		return "", fmt.Errorf("canonicalize extract YAML: %w", err)
	}

	h := sha256.New()
	_, _ = h.Write(signatureCanonical)
	_, _ = h.Write([]byte(recipeHashGlue))
	_, _ = h.Write(extractCanonical)

	return hashPrefix + hex.EncodeToString(h.Sum(nil)), nil
}

func yamlNodeToJSON(node *yaml.Node) (interface{}, error) {
	if node == nil {
		return nil, nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return nil, nil
		}
		return yamlNodeToJSON(node.Content[0])
	case yaml.MappingNode:
		return yamlMappingToJSON(node)
	case yaml.SequenceNode:
		values := make([]interface{}, 0, len(node.Content))
		for _, child := range node.Content {
			value, err := yamlNodeToJSON(child)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		return values, nil
	case yaml.ScalarNode:
		return yamlScalarToJSON(node)
	case yaml.AliasNode:
		return nil, fmt.Errorf("YAML aliases are not supported in canonical provenance input")
	default:
		return nil, fmt.Errorf("unsupported YAML node kind %d", node.Kind)
	}
}

func yamlMappingToJSON(node *yaml.Node) (map[string]interface{}, error) {
	if len(node.Content)%2 != 0 {
		return nil, fmt.Errorf("invalid YAML mapping node")
	}

	values := make(map[string]interface{}, len(node.Content)/2)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		if keyNode.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("YAML mapping keys must be scalar strings")
		}
		key := norm.NFC.String(keyNode.Value)
		if key == "" {
			return nil, fmt.Errorf("YAML mapping keys must not be empty")
		}
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("duplicate YAML mapping key after NFC normalization: %q", key)
		}

		value, err := yamlNodeToJSON(node.Content[i+1])
		if err != nil {
			return nil, err
		}
		values[key] = value
	}

	return values, nil
}

func yamlScalarToJSON(node *yaml.Node) (interface{}, error) {
	switch node.Tag {
	case yamlNullTag:
		return nil, nil
	case yamlBoolTag:
		return strconv.ParseBool(strings.ToLower(node.Value))
	case yamlStringTag, yamlTimestampTag:
		return norm.NFC.String(node.Value), nil
	case yamlIntTag:
		return parseSafeJSONInt(node.Value)
	case yamlFloatTag:
		return parseFiniteFloat(node.Value)
	default:
		// Preserve unknown scalar tags as strings rather than guessing at
		// domain-specific semantics.
		return norm.NFC.String(node.Value), nil
	}
}

func parseSafeJSONInt(raw string) (interface{}, error) {
	value := strings.ReplaceAll(raw, "_", "")
	value = strings.TrimPrefix(value, "+")

	if strings.HasPrefix(value, "-") {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse YAML integer %q: %w", raw, err)
		}
		if parsed < minSafeJSONInt {
			return nil, fmt.Errorf("YAML integer %q exceeds RFC 8785/I-JSON safe integer range", raw)
		}
		return parsed, nil
	}

	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("parse YAML integer %q: %w", raw, err)
	}
	if parsed > uint64(maxSafeJSONInt) {
		return nil, fmt.Errorf("YAML integer %q exceeds RFC 8785/I-JSON safe integer range", raw)
	}
	return int64(parsed), nil
}

func parseFiniteFloat(raw string) (interface{}, error) {
	value := strings.ReplaceAll(raw, "_", "")
	switch strings.ToLower(value) {
	case ".nan", "+.nan", "-.nan", ".inf", "+.inf", "-.inf":
		return nil, fmt.Errorf("YAML float %q is not finite", raw)
	}

	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return nil, fmt.Errorf("parse YAML float %q: %w", raw, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil, fmt.Errorf("YAML float %q is not finite", raw)
	}
	return parsed, nil
}
