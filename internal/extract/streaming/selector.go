package streaming

import (
	"fmt"
	"strings"
	"unicode"
)

// RecordSelector is the supported streaming/index record boundary grammar.
type RecordSelector struct {
	Raw         string
	ElementName string
}

// ParseRecordSelector validates the streaming/index record selector grammar.
// Supported forms are a single local element name ("Record") or descendant
// local element selector ("//Record").
func ParseRecordSelector(selector string) (RecordSelector, error) {
	raw := strings.TrimSpace(selector)
	if raw == "" {
		return RecordSelector{}, unsupportedRecordSelectorError(selector)
	}

	name := raw
	if strings.HasPrefix(name, "//") {
		name = strings.TrimSpace(strings.TrimPrefix(name, "//"))
	} else if strings.HasPrefix(name, "/") {
		return RecordSelector{}, unsupportedRecordSelectorError(selector)
	}

	if strings.ContainsAny(name, "/[]:|@()='\"") || !isLocalElementName(name) {
		return RecordSelector{}, unsupportedRecordSelectorError(selector)
	}

	return RecordSelector{
		Raw:         raw,
		ElementName: name,
	}, nil
}

// ValidateRecordSelector reports whether selector is supported for
// streaming/index record boundary detection.
func ValidateRecordSelector(selector string) error {
	_, err := ParseRecordSelector(selector)
	return err
}

func unsupportedRecordSelectorError(selector string) error {
	return fmt.Errorf("record selector %q is not yet supported for streaming/index mode; supported forms are a single local element name (Name) or descendant local element selector (//Name)", strings.TrimSpace(selector))
}

func isLocalElementName(name string) bool {
	for i, r := range name {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
			continue
		}
		if r != '_' && r != '-' && r != '.' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return name != ""
}
