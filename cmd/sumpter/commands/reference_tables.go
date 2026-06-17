package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/reftable"
	"github.com/fulmenhq/sumpter/internal/validation/dsl"
)

// buildReferenceRegistry resolves and loads the recipe-declared reference tables into
// an immutable, run-scoped registry. It applies --reference-table source overrides
// (source only; format/columns/caps stay recipe-declared so a CLI refresh cannot
// drift the schema) and enforces the C1 local-path containment control on every
// source.
//
// When load is false (a dry run) it still validates declaration shape, override
// targets, and source containment/resolvability — proving the run would work — but
// reads no table bytes, so a dry run never pulls a (potentially large) table. It
// returns the registry (nil when no tables are declared, or on a dry run) and the
// provenance entries for the loaded tables (sidecar-only, no row values).
func buildReferenceRegistry(opts *ExtractOptions, load bool) (*reftable.Registry, []provenance.ReferenceTable, error) {
	decls := opts.ReferenceTableDecls
	if len(decls) == 0 {
		if len(opts.ReferenceTableOverrides) > 0 {
			return nil, nil, fmt.Errorf("--reference-table given but the recipe declares no reference_tables")
		}
		return nil, nil, nil
	}

	// Index declarations by name in declaration order; reject duplicates up front (the
	// registry also rejects them, but a dry run never builds one).
	byName := make(map[string]*recipesmanifest.ReferenceTableDecl, len(decls))
	order := make([]string, 0, len(decls))
	for i := range decls {
		name := strings.TrimSpace(decls[i].Name)
		if name == "" {
			return nil, nil, fmt.Errorf("reference table declaration %d: name is required", i)
		}
		if _, dup := byName[name]; dup {
			return nil, nil, fmt.Errorf("reference table %q is declared more than once", name)
		}
		d := decls[i]
		byName[name] = &d
		order = append(order, name)
	}

	// Apply --reference-table name=source overrides (source only). A table overridden
	// more than once fails loud rather than silently taking the last value: two
	// conflicting sources for the same table is operator error, and a silent last-wins
	// would make the effective authority table (and its provenance) depend on argument
	// order.
	overridden := make(map[string]struct{}, len(opts.ReferenceTableOverrides))
	for _, raw := range opts.ReferenceTableOverrides {
		name, src, ok := strings.Cut(raw, "=")
		name = strings.TrimSpace(name)
		src = strings.TrimSpace(src)
		if !ok || name == "" || src == "" {
			return nil, nil, fmt.Errorf("invalid --reference-table %q: expected name=source", raw)
		}
		d, found := byName[name]
		if !found {
			return nil, nil, fmt.Errorf("--reference-table %q: no reference table named %q is declared", raw, name)
		}
		if _, dup := overridden[name]; dup {
			return nil, nil, fmt.Errorf("--reference-table %q: table %q is overridden more than once (conflicting sources; specify it at most once)", raw, name)
		}
		overridden[name] = struct{}{}
		d.Source = src
	}

	root := strings.TrimSpace(opts.ReferenceTableRoot)
	if root == "" {
		return nil, nil, fmt.Errorf("reference tables declared but no workspace root is set for containment")
	}

	tables := make([]*reftable.Table, 0, len(order))
	prov := make([]provenance.ReferenceTable, 0, len(order))
	for _, name := range order {
		d := byName[name]
		spec := referenceSpec(d)
		if err := spec.Validate(); err != nil {
			return nil, nil, err
		}
		// C1 containment: resolve the workspace-relative source under root, rejecting
		// absolute paths, ".." escapes, and symlinked components. This reads no table
		// bytes (it only stats the path), so it runs on dry runs too — a dry run still
		// proves the effective (possibly overridden) source is reachable and contained.
		absPath, err := reftable.ResolveLocalSource(root, d.Source)
		if err != nil {
			return nil, nil, err
		}
		spec.Source = reftable.SourceMetadata{LogicalURI: d.Source}
		if !load {
			continue // dry run: validated + contained, but not read
		}
		table, err := loadReferenceTable(spec, absPath)
		if err != nil {
			return nil, nil, err
		}
		tables = append(tables, table)
		prov = append(prov, provenance.ReferenceTable{
			Name:          table.Name(),
			Source:        d.Source,
			Format:        string(table.Format()),
			Mode:          string(table.Mode()),
			RowCount:      table.RowCount(),
			ContentSHA256: table.ContentSHA256(),
			MaxRows:       spec.MaxRows,
			MaxBytes:      effectiveReferenceMaxBytes(spec),
		})
	}

	if !load {
		return nil, nil, nil // dry run loaded nothing
	}
	registry, err := reftable.NewRegistry(tables)
	if err != nil {
		return nil, nil, err
	}
	// Deterministic sidecar order, matching registry.Tables().
	sort.Slice(prov, func(i, j int) bool { return prov[i].Name < prov[j].Name })
	return registry, prov, nil
}

func referenceSpec(d *recipesmanifest.ReferenceTableDecl) reftable.Spec {
	return reftable.Spec{
		Name:        strings.TrimSpace(d.Name),
		Format:      reftable.Format(strings.ToLower(strings.TrimSpace(d.Format))),
		Header:      d.Header,
		Column:      strings.TrimSpace(d.Column),
		KeyColumn:   strings.TrimSpace(d.KeyColumn),
		ValueColumn: strings.TrimSpace(d.ValueColumn),
		MaxRows:     d.MaxRows,
		MaxBytes:    d.MaxBytes,
	}
}

func loadReferenceTable(spec reftable.Spec, absPath string) (*reftable.Table, error) {
	f, err := os.Open(absPath) // #nosec G304 - path validated by reftable.ResolveLocalSource (C1 containment)
	if err != nil {
		return nil, fmt.Errorf("reference table %q: cannot open source", spec.Name)
	}
	defer func() { _ = f.Close() }()
	return reftable.Load(spec, f)
}

func effectiveReferenceMaxBytes(spec reftable.Spec) int64 {
	if spec.MaxBytes > 0 {
		return spec.MaxBytes
	}
	return reftable.DefaultMaxBytes
}

// validateReferenceTableDeclarations enforces the C3 pre-flight control: every
// reference table named by an in_reference / lookup_reference call in a field-mapping
// expression must be declared in defaults.reference_tables, and the table-name
// argument must be a string literal (record data must not select a run resource).
// Unknown or dynamic table names fail here, before extraction starts, instead of
// per-record mid-run. It needs only the declared names, so it runs on dry runs too.
func validateReferenceTableDeclarations(opts *ExtractOptions, mappings []extract.FieldMapping) error {
	declared := make(map[string]struct{}, len(opts.ReferenceTableDecls))
	for _, d := range opts.ReferenceTableDecls {
		declared[strings.TrimSpace(d.Name)] = struct{}{}
	}
	return walkReferenceMappings(mappings, declared)
}

func walkReferenceMappings(mappings []extract.FieldMapping, declared map[string]struct{}) error {
	for i := range mappings {
		m := &mappings[i]
		if strings.TrimSpace(m.Expression) != "" {
			expr, err := dsl.ParseExpression(m.Expression)
			if err != nil {
				// A genuine parse error surfaces (with full context) in the evaluator;
				// don't fail pre-flight on an unrelated expression-syntax problem.
				continue
			}
			names, err := dsl.ReferenceTableNames(expr)
			if err != nil {
				return fmt.Errorf("field %q: %w", m.OutputField, err)
			}
			for _, name := range names {
				if _, ok := declared[name]; !ok {
					return fmt.Errorf("field %q references reference table %q, which is not declared in defaults.reference_tables", m.OutputField, name)
				}
			}
		}
		if err := walkReferenceMappings(m.ItemMapping, declared); err != nil {
			return err
		}
		for j := range m.Polymorphic {
			if err := walkReferenceMappings(m.Polymorphic[j].FieldMappings, declared); err != nil {
				return err
			}
		}
	}
	return nil
}
