package xml

import (
	"sort"
	"strings"
)

// NamespaceDeclaration records one in-scope XML namespace declaration. Prefix is
// empty for the default namespace. Record indexes persist it with these field
// names.
type NamespaceDeclaration struct {
	Prefix string `json:"prefix"`
	URI    string `json:"uri"`
}

// NormalizeNamespaceDeclarations sorts and deduplicates namespace declarations.
func NormalizeNamespaceDeclarations(declarations []NamespaceDeclaration) []NamespaceDeclaration {
	latest := map[string]string{}
	for _, decl := range declarations {
		prefix := strings.TrimSpace(decl.Prefix)
		if prefix == "xml" || prefix == "xmlns" {
			continue
		}
		uri := strings.TrimSpace(decl.URI)
		if uri == "" {
			continue
		}
		latest[prefix] = uri
	}
	prefixes := make([]string, 0, len(latest))
	for prefix := range latest {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	out := make([]NamespaceDeclaration, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, NamespaceDeclaration{Prefix: prefix, URI: latest[prefix]})
	}
	return out
}
