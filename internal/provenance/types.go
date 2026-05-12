package provenance

// RuntimeOptions carries the per-record provenance fields that are safe to
// place in _runtime. Recipe-specific code resolves values before passing them
// here so this package remains recipe-domain-agnostic.
type RuntimeOptions struct {
	RunID             string
	SumpterVersion    string
	RecipeVersion     string
	RecipeContentHash string
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
