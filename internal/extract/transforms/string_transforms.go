package transforms

import (
	"fmt"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// TrimTransform removes leading and trailing whitespace
func TrimTransform(value string, params map[string]interface{}) (string, error) {
	return strings.TrimSpace(value), nil
}

// LTrimTransform removes leading whitespace
func LTrimTransform(value string, params map[string]interface{}) (string, error) {
	return strings.TrimLeft(value, " \t\n\r"), nil
}

// RTrimTransform removes trailing whitespace
func RTrimTransform(value string, params map[string]interface{}) (string, error) {
	return strings.TrimRight(value, " \t\n\r"), nil
}

// UpperTransform converts to uppercase
func UpperTransform(value string, params map[string]interface{}) (string, error) {
	return strings.ToUpper(value), nil
}

// LowerTransform converts to lowercase
func LowerTransform(value string, params map[string]interface{}) (string, error) {
	return strings.ToLower(value), nil
}

// TitleTransform converts to title case
func TitleTransform(value string, params map[string]interface{}) (string, error) {
	caser := cases.Title(language.English)
	return caser.String(value), nil
}

// ReplaceTransform replaces substrings
func ReplaceTransform(value string, params map[string]interface{}) (string, error) {
	old, ok := params["old"].(string)
	if !ok {
		return "", fmt.Errorf("replace transform requires 'old' parameter")
	}
	new, ok := params["new"].(string)
	if !ok {
		return "", fmt.Errorf("replace transform requires 'new' parameter")
	}
	return strings.ReplaceAll(value, old, new), nil
}

// BlindStringTransform blinds sensitive string data with configurable masking
func BlindStringTransform(value string, params map[string]interface{}) (string, error) {
	mode := "keep_first"
	count := 4
	maskChar := "*"

	if m, ok := params["mode"].(string); ok {
		mode = m
	}
	if c, ok := params["count"].(int); ok {
		count = c
	} else if c, ok := params["count"].(float64); ok {
		count = int(c)
	}
	if mc, ok := params["mask_char"].(string); ok && len(mc) > 0 {
		maskChar = mc[:1] // Take first character
	}

	runes := []rune(value)
	if len(runes) == 0 {
		return value, nil
	}

	switch mode {
	case "keep_first":
		if count >= len(runes) {
			return strings.Repeat(maskChar, len(runes)), nil
		}
		return string(runes[:count]) + strings.Repeat(maskChar, len(runes)-count), nil
	case "keep_last":
		if count >= len(runes) {
			return strings.Repeat(maskChar, len(runes)), nil
		}
		return strings.Repeat(maskChar, len(runes)-count) + string(runes[len(runes)-count:]), nil
	case "keep_domain":
		// For email-like strings, keep domain
		if atIndex := strings.LastIndex(value, "@"); atIndex > 0 {
			local := string(runes[:atIndex])
			domain := string(runes[atIndex:])
			if len(local) <= count {
				return strings.Repeat(maskChar, len(local)) + domain, nil
			}
			return string(runes[:count]) + strings.Repeat(maskChar, len(local)-count) + domain, nil
		}
		// Fall back to keep_first
		return TrimTransform(value, map[string]interface{}{"mode": "keep_first", "count": count, "mask_char": maskChar})
	case "mask_all":
		return strings.Repeat(maskChar, len(runes)), nil
	default:
		return "", fmt.Errorf("unknown blind_string mode: %s", mode)
	}
}
