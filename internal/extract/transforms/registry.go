package transforms

import (
	"fmt"
	"sort"
)

// NewTransformRegistry creates a new transform registry with built-in transforms
func NewTransformRegistry() *TransformRegistry {
	registry := &TransformRegistry{
		RegistryVersion: "v0.1.0",
		Transforms:      make(map[string]*Transform),
		Categories:      make(map[string][]string),
	}

	// Register built-in transforms
	registry.registerStringTransforms()
	registry.registerNumericTransforms()
	// Add more categories as needed

	return registry
}

// Register adds a transform to the registry
func (r *TransformRegistry) Register(transform *Transform) {
	r.Transforms[transform.Name] = transform
	if r.Categories[transform.Category] == nil {
		r.Categories[transform.Category] = []string{}
	}
	r.Categories[transform.Category] = append(r.Categories[transform.Category], transform.Name)
}

// Get retrieves a transform by name
func (r *TransformRegistry) Get(name string) (*Transform, error) {
	transform, exists := r.Transforms[name]
	if !exists {
		return nil, fmt.Errorf("transform '%s' not found", name)
	}
	return transform, nil
}

// List returns all transform names
func (r *TransformRegistry) List() []string {
	names := make([]string, 0, len(r.Transforms))
	for name := range r.Transforms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ListByCategory returns transform names grouped by category
func (r *TransformRegistry) ListByCategory() map[string][]string {
	result := make(map[string][]string)
	for category, names := range r.Categories {
		sorted := make([]string, len(names))
		copy(sorted, names)
		sort.Strings(sorted)
		result[category] = sorted
	}
	return result
}

// Describe returns detailed information about a transform
func (r *TransformRegistry) Describe(name string) (*Transform, error) {
	return r.Get(name)
}

// Apply applies a transform to a value
func (r *TransformRegistry) Apply(name string, value string, params map[string]interface{}) (string, error) {
	transform, err := r.Get(name)
	if err != nil {
		return "", err
	}
	return transform.Function(value, params)
}

// registerStringTransforms registers string manipulation transforms
func (r *TransformRegistry) registerStringTransforms() {
	r.Register(&Transform{
		Name:        "trim",
		Description: "Remove leading and trailing whitespace",
		Category:    "string",
		Function:    TrimTransform,
	})

	r.Register(&Transform{
		Name:        "ltrim",
		Description: "Remove leading whitespace",
		Category:    "string",
		Function:    LTrimTransform,
	})

	r.Register(&Transform{
		Name:        "rtrim",
		Description: "Remove trailing whitespace",
		Category:    "string",
		Function:    RTrimTransform,
	})

	r.Register(&Transform{
		Name:        "upper",
		Description: "Convert to uppercase",
		Category:    "string",
		Function:    UpperTransform,
	})

	r.Register(&Transform{
		Name:        "lower",
		Description: "Convert to lowercase",
		Category:    "string",
		Function:    LowerTransform,
	})

	r.Register(&Transform{
		Name:        "title",
		Description: "Convert to title case",
		Category:    "string",
		Function:    TitleTransform,
	})

	r.Register(&Transform{
		Name:        "replace",
		Description: "Replace substrings in the value",
		Category:    "string",
		Function:    ReplaceTransform,
	})

	r.Register(&Transform{
		Name:        "blind_string",
		Description: "Blind sensitive string data with configurable masking",
		Category:    "string",
		Function:    BlindStringTransform,
	})
}

// registerNumericTransforms registers numeric transforms
func (r *TransformRegistry) registerNumericTransforms() {
	// Placeholder for future numeric transforms
}
