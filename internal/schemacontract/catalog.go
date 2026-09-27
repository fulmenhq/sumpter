package schemacontract

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

// CatalogFile is the generated catalog at the bundle root.
const CatalogFile = "index.json"

// Catalog lists every resource in the bundle with its identity and digest, and
// names each family's current head version.
type Catalog struct {
	Resources []CatalogEntry           `json:"resources"`
	Current   map[string]CurrentFamily `json:"current"`
}

// CatalogEntry is one resource row.
type CatalogEntry struct {
	ID         string `json:"id"`
	Capability string `json:"capability"`
	Version    string `json:"version"`
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	SHA256     string `json:"sha256"`
}

// CurrentFamily points at a family's newest version and that version's entry
// schema id. These are moving aliases; consumers should pin exact-version ids.
type CurrentFamily struct {
	Version string `json:"version"`
	Entry   string `json:"entry"`
}

// BuildCatalog builds the catalog from a bundle and its manifests. The bundle
// must already pass CheckIDs and CheckManifests.
func BuildCatalog(fsys fs.FS, b *Bundle, manifests map[string]Manifest) (*Catalog, error) {
	c := &Catalog{Current: map[string]CurrentFamily{}}
	for _, r := range b.Resources {
		m, ok := manifests[path.Dir(r.Path)]
		if !ok {
			return nil, fmt.Errorf("%s: no manifest", r.Path)
		}
		kind := m.KindOf(path.Base(r.Path))
		id, err := ParseID(r.ID)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", r.Path, err)
		}
		data, err := fs.ReadFile(fsys, r.Path)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(data)
		c.Resources = append(c.Resources, CatalogEntry{
			ID:         r.ID,
			Capability: m.Capability,
			Version:    id.Version,
			Kind:       kind,
			Path:       r.Path,
			SHA256:     "sha256:" + hex.EncodeToString(sum[:]),
		})
	}
	sort.Slice(c.Resources, func(i, j int) bool { return c.Resources[i].ID < c.Resources[j].ID })

	for dir, m := range manifests {
		family, version, err := manifestLocation(dir)
		if err != nil {
			return nil, err
		}
		cur, ok := c.Current[family]
		if ok && compareVersions(version, cur.Version) <= 0 {
			continue
		}
		next := CurrentFamily{Version: version}
		for _, r := range b.Resources {
			if r.Path == path.Join(dir, m.EntrySchema) {
				next.Entry = r.ID
			}
		}
		c.Current[family] = next
	}
	return c, nil
}

// Encode renders the catalog deterministically (indented, trailing newline).
func (c *Catalog) Encode() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(c); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// compareVersions orders "v1" and "v0.1.2" style versions numerically.
func compareVersions(a, b string) int {
	pa := strings.Split(strings.TrimPrefix(a, "v"), ".")
	pb := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
