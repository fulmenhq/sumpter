package extract

import (
	"fmt"
	"strings"

	"github.com/fulmenhq/sumpter/internal/docnode"
)

// Input format tokens accepted in this release.
const (
	FormatXML  = "xml"
	FormatJSON = "json"
)

// jsonNoAttributesHint is the fix carried by every attribute-axis rejection
// under json input.
const jsonNoAttributesHint = "JSON has no attributes: use k instead of @k"

// NormalizeInputFormat validates an input format token. An empty token means
// xml. Reserved and later-release tokens fail with their reason.
func NormalizeInputFormat(token string) (string, error) {
	switch t := strings.ToLower(strings.TrimSpace(token)); t {
	case "", FormatXML:
		return FormatXML, nil
	case FormatJSON:
		return FormatJSON, nil
	case "ndjson":
		return "", fmt.Errorf("input format %q is not supported: line-delimited JSON input arrives in a later release", token)
	case "protobuf":
		return "", fmt.Errorf("input format %q is reserved, not implemented", token)
	default:
		return "", fmt.Errorf("unknown input format %q (supported: xml, json)", token)
	}
}

// ResolveInputFormat returns the effective input format for a run and records
// it on sig.FormatType. In recipe mode the recipe's defaults.input.format is
// authoritative and the signature's format_type must agree with it (an
// omitted format_type means xml). Otherwise the signature's format_type
// decides. File names are never consulted. Under json, expressions that use
// the attribute or namespace axis, prefixed name tests, and namespace maps are
// rejected here, before any input is read.
func ResolveInputFormat(recipeFormat string, recipeMode bool, sig *FileSignature, ext *ExtractRecordMatch, app *ApplicabilityConfig) (string, error) {
	if sig == nil {
		return "", fmt.Errorf("signature config is nil")
	}
	sigFormat, err := NormalizeInputFormat(sig.FormatType)
	if err != nil {
		return "", fmt.Errorf("signature format_type: %w", err)
	}
	effective := sigFormat
	if recipeMode {
		effective, err = NormalizeInputFormat(recipeFormat)
		if err != nil {
			return "", fmt.Errorf("recipe defaults.input.format: %w", err)
		}
		if effective != sigFormat {
			if strings.TrimSpace(sig.FormatType) == "" {
				return "", fmt.Errorf("recipe defaults.input.format is %q but signature format_type defaults to xml: add \"format_type: %s\" to the signature", effective, effective)
			}
			return "", fmt.Errorf("recipe defaults.input.format %q does not match signature format_type %q", effective, sigFormat)
		}
	}
	if effective == FormatJSON {
		if err := checkJSONConfig(sig, ext, app); err != nil {
			return "", err
		}
	}
	sig.FormatType = effective
	return effective, nil
}

// checkJSONConfig enforces the json totality rule over every recipe XPath.
func checkJSONConfig(sig *FileSignature, ext *ExtractRecordMatch, app *ApplicabilityConfig) error {
	if len(sig.Namespaces) > 0 {
		return fmt.Errorf("signature namespaces: json input has no namespaces; remove the namespaces map")
	}
	for _, p := range sig.MatchPatterns {
		if err := checkJSONXPath(fmt.Sprintf("signature selector (pattern %q)", p.PatternID), p.Selector); err != nil {
			return err
		}
	}
	if app != nil && strings.EqualFold(strings.TrimSpace(app.Applicability.Type), "xpath") {
		if err := checkJSONXPath("applicability expression", app.Applicability.Expression); err != nil {
			return err
		}
	}
	if ext == nil {
		return nil
	}
	if len(ext.Namespaces) > 0 {
		return fmt.Errorf("extract namespaces: json input has no namespaces; remove the namespaces map")
	}
	for i, s := range ext.MatchSelectors {
		if err := checkJSONXPath(fmt.Sprintf("match selector %d", i+1), s.XPath); err != nil {
			return err
		}
	}
	return checkJSONMappings("field mapping", ext.FieldMappings)
}

func checkJSONMappings(scope string, mappings []FieldMapping) error {
	for i := range mappings {
		m := &mappings[i]
		label := fmt.Sprintf("%s %q", scope, m.OutputField)
		if err := checkJSONXPath(label, m.XPath); err != nil {
			return err
		}
		if err := checkJSONMappings("item mapping", m.ItemMapping); err != nil {
			return err
		}
		for j := range m.Polymorphic {
			pm := &m.Polymorphic[j]
			if err := checkJSONXPath(fmt.Sprintf("%s polymorphic match_xpath", label), pm.MatchXPath); err != nil {
				return err
			}
			if err := checkJSONMappings("polymorphic mapping", pm.FieldMappings); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkJSONXPath(label, expr string) error {
	violations := xpathAxisViolations(expr)
	if len(violations) == 0 {
		return nil
	}
	v := violations[0]
	switch v.Kind {
	case axisAttribute:
		return fmt.Errorf("%s xpath %q uses the attribute axis (%q): %s", label, expr, v.Text, jsonNoAttributesHint)
	case axisNamespace:
		return fmt.Errorf("%s xpath %q uses the namespace axis (%q): json input has no namespaces", label, expr, v.Text)
	default:
		return fmt.Errorf("%s xpath %q uses the prefixed name %q: json input has no namespaces", label, expr, v.Text)
	}
}

type axisKind int

const (
	axisAttribute axisKind = iota
	axisNamespace
	axisPrefixedName
)

// axisViolation is one construct json input cannot satisfy. Start and End are
// byte offsets into the expression, so a later rewrite can work in place.
type axisViolation struct {
	Kind       axisKind
	Start, End int
	Text       string
}

// xpathAxisViolations lexes an XPath 1.0 expression outside string literals
// and reports attribute-axis steps, namespace-axis steps, and prefixed name
// tests. It over-reports rather than under-reports: a false positive is a
// loud, fixable load error.
func xpathAxisViolations(expr string) []axisViolation {
	var out []axisViolation
	n := len(expr)
	for i := 0; i < n; {
		c := expr[i]
		switch {
		case c == '"' || c == '\'':
			end := strings.IndexByte(expr[i+1:], c)
			if end < 0 {
				return out
			}
			i += end + 2
		case c == '@':
			end := i + 1
			for end < n && (isNameChar(expr[end]) || expr[end] == ':' || expr[end] == '*') {
				end++
			}
			out = append(out, axisViolation{Kind: axisAttribute, Start: i, End: end, Text: expr[i:end]})
			i = end
		case isNameStart(c):
			start := i
			for i < n && isNameChar(expr[i]) {
				i++
			}
			name := expr[start:i]
			if i+1 < n && expr[i] == ':' && expr[i+1] == ':' {
				switch name {
				case "attribute":
					out = append(out, axisViolation{Kind: axisAttribute, Start: start, End: i + 2, Text: expr[start : i+2]})
				case "namespace":
					out = append(out, axisViolation{Kind: axisNamespace, Start: start, End: i + 2, Text: expr[start : i+2]})
				}
				i += 2
				continue
			}
			if i+1 < n && expr[i] == ':' && (isNameStart(expr[i+1]) || expr[i+1] == '*') {
				end := i + 1
				for end < n && (isNameChar(expr[end]) || expr[end] == '*') {
					end++
				}
				out = append(out, axisViolation{Kind: axisPrefixedName, Start: start, End: end, Text: expr[start:end]})
				i = end
			}
		default:
			i++
		}
	}
	return out
}

func isNameStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isNameChar(c byte) bool {
	return isNameStart(c) || c == '-' || c == '.' || (c >= '0' && c <= '9')
}

// inputFormat returns the registered format recorded on the signature by
// ResolveInputFormat; an empty format_type means xml.
func inputFormat(sig *FileSignature) (docnode.Format, error) {
	token := docnode.Default
	if sig != nil && strings.TrimSpace(sig.FormatType) != "" {
		normalized, err := NormalizeInputFormat(sig.FormatType)
		if err != nil {
			return nil, err
		}
		token = normalized
	}
	format, ok := docnode.Lookup(token)
	if !ok {
		return nil, fmt.Errorf("input format %q is not registered", token)
	}
	return format, nil
}

// errJSONStreamingUnsupported is returned when a json input would need the
// streaming route, which json does not have in this release.
var errJSONStreamingUnsupported = fmt.Errorf("streaming input is not supported for json in this release; use --allow-large-files for whole-document parsing: %w", docnode.ErrRouteUnsupported)

func isJSONInput(sig *FileSignature) bool {
	return sig != nil && strings.EqualFold(strings.TrimSpace(sig.FormatType), FormatJSON)
}

// checkUnmatchedPolymorphic fails a json polymorphic mapping by element_type
// that matched nothing while items exist: bare array items all carry their
// parent key's name, so the wrapper idiom is required. XML keeps its empty
// result.
func checkUnmatchedPolymorphic(format docnode.Format, sources []docnode.Node, mapping *FieldMapping) error {
	if format == nil || format.Token() != FormatJSON {
		return nil
	}
	byElementType := false
	for i := range mapping.Polymorphic {
		if strings.TrimSpace(mapping.Polymorphic[i].ElementType) != "" {
			byElementType = true
			break
		}
	}
	if !byElementType {
		return nil
	}
	items := 0
	for _, src := range sources {
		if _, ok := src.FirstElementChild(); ok {
			items++
		}
	}
	if items == 0 {
		return nil
	}
	return fmt.Errorf("field mapping %q: no polymorphic branch matched %d items; expected the wrapper idiom, where each array item is an object keyed by its element type (for example [{\"OrderLine\": {...}}])", mapping.OutputField, items)
}
