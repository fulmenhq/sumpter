// Package schemacontract checks the published schema bundle under schemas/:
// resource identifiers, reference closure, and (in later files) manifests and
// the catalog. It never resolves anything over the network: every reference is
// audited against the bundle before any schema is handed to a compiler.
package schemacontract

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Resource is one schema file in the bundle.
type Resource struct {
	// Path is the slash-separated path relative to the bundle root, e.g.
	// "envinfo/v0.1.0/network.schema.json".
	Path string
	// ID is the resource's $id ("" when absent).
	ID string
	// Refs are the $ref values found anywhere in the document, in document order.
	Refs []string
}

// Bundle is the set of schema resources under one root.
type Bundle struct {
	Resources []Resource
}

// nonSchemaFiles are bundle files that are not schema resources.
var nonSchemaFiles = map[string]bool{
	"contract.json": true,
	"index.json":    true,
}

// LoadBundle reads every schema file (.json, .yaml, .yml) under fsys, skipping
// documentation and the manifest/catalog files.
func LoadBundle(fsys fs.FS) (*Bundle, error) {
	var b Bundle
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".json", ".yaml", ".yml":
		default:
			return nil
		}
		if nonSchemaFiles[path.Base(p)] {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		var doc interface{}
		if err := yaml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("%s: parse: %w", p, err)
		}
		top, ok := doc.(map[string]interface{})
		if !ok {
			return fmt.Errorf("%s: schema document is not an object", p)
		}
		res := Resource{Path: p}
		if id, ok := top["$id"].(string); ok {
			res.ID = id
		}
		collectRefs(doc, &res.Refs)
		b.Resources = append(b.Resources, res)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(b.Resources, func(i, j int) bool { return b.Resources[i].Path < b.Resources[j].Path })
	return &b, nil
}

// collectRefs appends every string value of a "$ref" key, at any depth.
func collectRefs(node interface{}, out *[]string) {
	switch v := node.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if k == "$ref" {
				if s, ok := v[k].(string); ok {
					*out = append(*out, s)
				}
				continue
			}
			collectRefs(v[k], out)
		}
	case []interface{}:
		for _, item := range v {
			collectRefs(item, out)
		}
	}
}

// ByID returns the resources keyed by $id (resources without an $id are omitted).
func (b *Bundle) ByID() map[string]Resource {
	m := make(map[string]Resource, len(b.Resources))
	for _, r := range b.Resources {
		if r.ID != "" {
			m[r.ID] = r
		}
	}
	return m
}
