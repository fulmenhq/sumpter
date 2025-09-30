package transforms

// TransformFunc represents a transformation function
type TransformFunc func(value string, params map[string]interface{}) (string, error)

// Transform represents a registered transform
type Transform struct {
	Name        string        `json:"name" yaml:"name"`
	Description string        `json:"description" yaml:"description"`
	Category    string        `json:"category" yaml:"category"`
	Function    TransformFunc `json:"-" yaml:"-"`
}

// TransformRegistry holds all available transforms
type TransformRegistry struct {
	RegistryVersion string                `json:"registry_version" yaml:"registry_version"`
	Transforms      map[string]*Transform `json:"transforms" yaml:"transforms"`
	Categories      map[string][]string   `json:"categories" yaml:"categories"`
}
