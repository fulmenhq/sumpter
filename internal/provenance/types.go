package provenance

// RuntimeOptions carries the per-record provenance fields that are safe to
// place in _runtime. Recipe-specific code resolves values before passing them
// here so this package remains recipe-domain-agnostic.
type RuntimeOptions struct {
	RunID             string
	SumpterVersion    string
	RecipeVersion     string
	RecipeContentHash string

	// SourceURI is the logical identity of the source being processed: a bare
	// path, a file:// URI, or an s3:// URI. The caller sets it per-file so the
	// in-core identity surfaces (_runtime.source_file, file-boundary summaries,
	// ExtractResult.LogicalURI) record the logical source rather than a staged
	// local working path. Empty means "use the local read path" — the local-
	// source case, where the logical identity and the read path coincide.
	//
	// It is deliberately omitted from RuntimeFields(): it overrides the
	// source_file value rather than adding a new _runtime field.
	SourceURI string
}

// SourceIdentity returns the logical source identity, falling back to the
// supplied local path when no SourceURI was set. For local sources the logical
// identity and the read path are the same, so this is the historical value.
func (o RuntimeOptions) SourceIdentity(localPath string) string {
	if o.SourceURI != "" {
		return o.SourceURI
	}
	return localPath
}

// RuntimeFields returns the non-empty runtime provenance fields.
func (o RuntimeOptions) RuntimeFields() map[string]interface{} {
	fields := make(map[string]interface{}, 4)
	if o.RunID != "" {
		fields["run_id"] = o.RunID
	}
	if o.SumpterVersion != "" {
		fields["sumpter_version"] = o.SumpterVersion
	}
	if o.RecipeVersion != "" {
		fields["recipe_version"] = o.RecipeVersion
	}
	if o.RecipeContentHash != "" {
		fields["recipe_content_hash"] = o.RecipeContentHash
	}
	return fields
}
