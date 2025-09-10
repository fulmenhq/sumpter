package assets

import (
	"embed"
	"io/fs"
)

//go:embed embedded_docs
var DocsFS embed.FS

//go:embed embedded_schemas
var SchemasFS embed.FS

//go:embed embedded_examples
var ExamplesFS embed.FS

// GetDocsFS returns the embedded documentation filesystem
func GetDocsFS() (fs.FS, error) {
	return fs.Sub(DocsFS, "embedded_docs")
}

// GetSchemasFS returns the embedded schemas filesystem
func GetSchemasFS() (fs.FS, error) {
	return fs.Sub(SchemasFS, "embedded_schemas")
}

// GetExamplesFS returns the embedded examples filesystem
func GetExamplesFS() (fs.FS, error) {
	return fs.Sub(ExamplesFS, "embedded_examples")
}
