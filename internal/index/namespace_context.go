package index

import (
	"fmt"
	"sort"
	"strings"
)

// NamespaceContextTable deduplicates namespace contexts while preserving a
// compact, deterministic table for persisted indexes.
type NamespaceContextTable struct {
	contexts []NamespaceContext
	ids      map[string]int
}

// NewNamespaceContextTable creates a table with context 0 reserved for the
// empty namespace context.
func NewNamespaceContextTable() *NamespaceContextTable {
	t := &NamespaceContextTable{ids: map[string]int{}}
	t.RefFor(nil)
	return t
}

// RefFor returns the table ID for declarations, adding a new context when
// needed. Empty URI declarations are not persisted because an undeclaration
// required on the record root is already present in the sliced source bytes.
func (t *NamespaceContextTable) RefFor(declarations []NamespaceDeclaration) int {
	if t.ids == nil {
		t.ids = map[string]int{}
	}
	normalized := NormalizeNamespaceDeclarations(declarations)
	key := namespaceContextKey(normalized)
	if id, ok := t.ids[key]; ok {
		return id
	}
	id := len(t.contexts)
	t.contexts = append(t.contexts, NamespaceContext{
		ID:           id,
		Declarations: normalized,
	})
	t.ids[key] = id
	return id
}

// Contexts returns a detached copy of the table.
func (t *NamespaceContextTable) Contexts() []NamespaceContext {
	out := make([]NamespaceContext, len(t.contexts))
	for i := range t.contexts {
		out[i] = NamespaceContext{
			ID:           t.contexts[i].ID,
			Declarations: append([]NamespaceDeclaration(nil), t.contexts[i].Declarations...),
		}
	}
	return out
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

// NamespaceContextByID returns a lookup table for persisted namespace contexts.
func NamespaceContextByID(contexts []NamespaceContext) map[int][]NamespaceDeclaration {
	lookup := make(map[int][]NamespaceDeclaration, len(contexts))
	for _, ctx := range contexts {
		lookup[ctx.ID] = NormalizeNamespaceDeclarations(ctx.Declarations)
	}
	return lookup
}

// ValidateNamespaceContextSupport fails loudly when a namespace-bound recipe is
// used with an index that cannot supply per-record namespace context.
func ValidateNamespaceContextSupport(idx *RecordIndex, namespaceBound bool) error {
	if !namespaceBound {
		return nil
	}
	if idx == nil {
		return fmt.Errorf("record index header is missing")
	}
	if idx.Version != SchemaVersion && strings.HasPrefix(idx.Version, "record-index/") {
		return fmt.Errorf("record index %s lacks namespace context; rebuild the index with record-index/v0.1.2 or newer for namespace-bound extraction", idx.Version)
	}
	if len(idx.NamespaceContexts) == 0 {
		return fmt.Errorf("record index lacks namespace context; rebuild the index with record-index/v0.1.2 or newer for namespace-bound extraction")
	}
	return nil
}

func namespaceContextKey(declarations []NamespaceDeclaration) string {
	if len(declarations) == 0 {
		return ""
	}
	var b strings.Builder
	for _, decl := range declarations {
		b.WriteString(decl.Prefix)
		b.WriteByte('=')
		b.WriteString(decl.URI)
		b.WriteByte('\n')
	}
	return b.String()
}
