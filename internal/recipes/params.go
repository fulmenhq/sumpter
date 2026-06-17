package recipes

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ParamValue is a recipe parameter value: either a scalar string (the historical
// SUM-003/SUM-020 form) or a list of strings (SUM-040). Exactly one form is set.
//
// A list value carries set-membership / leading-prefix reference data that an
// operator overrides at run time (`--parameter k='["a","b"]'`) and that DSL
// `expression:` helpers read as an ordinary bare-identifier variable. The type is
// deliberately closed to string-or-[]string in this slice: numbers, booleans,
// objects, nested arrays, and mixed arrays are rejected at the parse boundary, not
// silently stringified.
type ParamValue struct {
	isList bool
	scalar string
	list   []string
}

// ScalarParam builds a scalar (string) parameter value.
func ScalarParam(s string) ParamValue { return ParamValue{scalar: s} }

// ListParam builds a list (string slice) parameter value, copying the input.
func ListParam(items []string) ParamValue {
	return ParamValue{isList: true, list: append([]string(nil), items...)}
}

// IsList reports whether the value is a list (vs a scalar string).
func (p ParamValue) IsList() bool { return p.isList }

// Scalar returns the scalar string (empty for a list value).
func (p ParamValue) Scalar() string { return p.scalar }

// List returns a copy of the list members (nil for a scalar value).
func (p ParamValue) List() []string {
	if !p.isList {
		return nil
	}
	return append([]string(nil), p.list...)
}

// IsEmpty reports whether the value counts as "not provided" for
// parameters_required. A scalar is empty when it trims to "". A list — even an
// empty list — counts as provided (an explicit empty set is a meaningful run
// input, distinct from an absent parameter).
func (p ParamValue) IsEmpty() bool {
	if p.isList {
		return false
	}
	return strings.TrimSpace(p.scalar) == ""
}

// AsScope returns the value as it enters DSL/record scope and output emission: the
// scalar string, or a fresh []string copy for a list. The copy keeps the scope's
// list independent of the stored value (callers must not mutate the parameter).
func (p ParamValue) AsScope() interface{} {
	if p.isList {
		// A non-nil slice (even for an empty list) so the value serialises as a JSON
		// array `[]`, not `null` — an empty-list parameter is an explicit empty set,
		// and it must validate against an array-typed output schema.
		out := make([]string, len(p.list))
		copy(out, p.list)
		return out
	}
	return p.scalar
}

// UnmarshalYAML decodes a recipe `defaults.parameters.<key>` value. A YAML scalar
// becomes a scalar string (unchanged from the historical map[string]string form);
// a YAML sequence becomes a strict list of strings. Non-string members, empty
// members, and nested/mapping members are rejected loudly rather than coerced.
func (p *ParamValue) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("parameter value must be a string or a list of strings: %w", err)
		}
		p.isList = false
		p.scalar = s
		p.list = nil
		return nil
	case yaml.SequenceNode:
		list := make([]string, 0, len(node.Content))
		for i, item := range node.Content {
			// Strict: only an explicit string scalar member is accepted. An unquoted
			// number/bool resolves to !!int/!!bool and is rejected here rather than
			// stringified; a nested sequence/mapping is not a ScalarNode.
			if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
				return fmt.Errorf("parameter list member %d must be a string (got %s); numbers, booleans, and nested values are not allowed", i, paramNodeTypeLabel(item))
			}
			if item.Value == "" {
				return fmt.Errorf("parameter list member %d is an empty string; empty members are not allowed (an empty prefix would match everything)", i)
			}
			list = append(list, item.Value)
		}
		p.isList = true
		p.list = list
		p.scalar = ""
		return nil
	default:
		return fmt.Errorf("parameter value must be a string or a list of strings")
	}
}

// paramNodeTypeLabel returns a human-readable label for a rejected list member.
func paramNodeTypeLabel(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "a nested list"
	case yaml.MappingNode:
		return "a mapping"
	case yaml.ScalarNode:
		if node.Tag != "" {
			return node.Tag
		}
		return "a non-string scalar"
	default:
		return "a non-string value"
	}
}
