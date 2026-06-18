package commands

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/reftable"
	"github.com/fulmenhq/sumpter/internal/uriio"
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
func buildReferenceRegistry(ctx context.Context, opts *ExtractOptions, runID string, load bool) (*reftable.Registry, []provenance.ReferenceTable, error) {
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

	// A run-scoped cloud session (created lazily on the first s3:// table) and a
	// no-network resolver (used to validate cloud handles on a dry run). The session's
	// staging directory is removed when this function returns — each table is fully
	// read into memory by Load, so the staged file is not needed afterward.
	var session *uriio.Session
	var resolver *uriio.Resolver

	tables := make([]*reftable.Table, 0, len(order))
	prov := make([]provenance.ReferenceTable, 0, len(order))
	for _, name := range order {
		d := byName[name]
		spec := referenceSpec(d)
		if err := spec.Validate(); err != nil {
			return nil, nil, err
		}

		ref, cerr := uriio.Classify(d.Source)
		if cerr != nil {
			return nil, nil, fmt.Errorf("reference table %q: %w", name, cerr)
		}

		if ref.IsCloud() {
			// Cloud (s3://) source. Containment is the uriio staging boundary
			// (traversal-guarded), not the local C1 path rule. The credential handle is
			// a name only (FU-2 posture); an override may swap the URI but reuses the
			// declared handle.
			//
			// A reference table is exactly one object. Reject a prefix or glob here —
			// this is a static check (no network), so it fails the same on a dry run as
			// on a real run, rather than only surfacing deep in acquire.
			if ref.IsPattern() || ref.IsPrefix() {
				return nil, nil, fmt.Errorf("reference table %q: source %s must be a single object, not a prefix or glob pattern", name, ref.LogicalURI)
			}
			handle := strings.TrimSpace(d.CredentialsHandle)
			if handle == "" {
				handle = uriio.DefaultHandleName
			}
			if verr := uriio.ValidateHandleName(handle); verr != nil {
				return nil, nil, fmt.Errorf("reference table %q: %w", name, verr)
			}
			spec.Source = reftable.SourceMetadata{LogicalURI: ref.LogicalURI, CredentialsHandle: handle}
			if !load {
				// Dry run: validate the handle resolves (config-only, no network); do
				// not acquire the object.
				if resolver == nil {
					r, rerr := referenceResolver(opts)
					if rerr != nil {
						return nil, nil, rerr
					}
					resolver = r
				}
				if _, rerr := resolver.Resolve(handle); rerr != nil {
					return nil, nil, fmt.Errorf("reference table %q: %w", name, rerr)
				}
				continue
			}
			if session == nil {
				s, serr := newCloudSession(opts, runID)
				if serr != nil {
					return nil, nil, serr
				}
				session = s
				defer func() { _ = session.Close() }()
			}
			table, terr := loadCloudReferenceTable(ctx, session, spec, ref.LogicalURI, handle)
			if terr != nil {
				return nil, nil, terr
			}
			tables = append(tables, table)
			prov = append(prov, referenceProvEntry(table, spec, ref.LogicalURI, handle))
			continue
		}

		// Local source. C1 containment: resolve the workspace-relative source under
		// root, rejecting absolute paths, ".." escapes, and symlinked components. This
		// reads no table bytes (it only stats the path), so it runs on dry runs too — a
		// dry run still proves the effective (possibly overridden) source is reachable
		// and contained.
		if root == "" {
			return nil, nil, fmt.Errorf("reference table %q: a local source requires a workspace root for containment", name)
		}
		absPath, rerr := reftable.ResolveLocalSource(root, d.Source)
		if rerr != nil {
			return nil, nil, rerr
		}
		spec.Source = reftable.SourceMetadata{LogicalURI: d.Source}
		if !load {
			continue // dry run: validated + contained, but not read
		}
		table, terr := loadReferenceTable(spec, absPath)
		if terr != nil {
			return nil, nil, terr
		}
		tables = append(tables, table)
		prov = append(prov, referenceProvEntry(table, spec, d.Source, ""))
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

// referenceResolver builds a credential-handle resolver from the run's credentials
// config + CLI overrides. It performs no network I/O — used to confirm a cloud
// reference table's handle resolves during a dry run.
func referenceResolver(opts *ExtractOptions) (*uriio.Resolver, error) {
	cliProfiles, err := uriio.ParseCredentialOverrides(opts.CredentialOverrides)
	if err != nil {
		return nil, err
	}
	var credCfg *uriio.CredentialsConfig
	if strings.TrimSpace(opts.CredentialsPath) != "" {
		credCfg, err = uriio.LoadCredentialsConfig(opts.CredentialsPath)
		if err != nil {
			return nil, err
		}
	}
	return uriio.NewResolver(credCfg, cliProfiles), nil
}

// loadCloudReferenceTable acquires an s3:// reference table to the run staging
// directory with a pre-read size cap (C2 — an oversized object is rejected before it
// can fill staging disk) and loads it. The staged file is removed when the session
// closes.
func loadCloudReferenceTable(ctx context.Context, session *uriio.Session, spec reftable.Spec, source, handle string) (*reftable.Table, error) {
	acquired, err := session.AcquireBounded(ctx, source, handle, effectiveReferenceMaxBytes(spec))
	if err != nil {
		return nil, fmt.Errorf("reference table %q: %w", spec.Name, err)
	}
	// Release the staged copy as soon as the table is read into memory: Load fully
	// consumes the bytes, so holding the staged file is unnecessary, and freeing the
	// key-derived staging path lets another declaration that names the SAME s3://
	// object stage it too (one physical authority table can back several logical
	// reference tables — local sources already allow this). The session's Close still
	// sweeps anything left behind.
	defer func() { _ = acquired.Cleanup() }()
	f, err := os.Open(acquired.LocalPath) // #nosec G304 - staged under the run dir by uriio (traversal-guarded)
	if err != nil {
		return nil, fmt.Errorf("reference table %q: cannot open staged source", spec.Name)
	}
	table, lerr := reftable.Load(spec, f)
	_ = f.Close()
	return table, lerr
}

// referenceProvEntry builds the sidecar provenance entry for a loaded table. source
// is the effective (possibly overridden) logical source; handle is the logical
// credential handle name for a cloud source (empty for local).
func referenceProvEntry(table *reftable.Table, spec reftable.Spec, source, handle string) provenance.ReferenceTable {
	return provenance.ReferenceTable{
		Name:              table.Name(),
		Source:            source,
		CredentialsHandle: handle,
		Format:            string(table.Format()),
		Mode:              string(table.Mode()),
		RowCount:          table.RowCount(),
		ContentSHA256:     table.ContentSHA256(),
		MaxRows:           spec.MaxRows,
		MaxBytes:          effectiveReferenceMaxBytes(spec),
	}
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
