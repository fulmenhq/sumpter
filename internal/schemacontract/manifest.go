package schemacontract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ManifestFile is the per-version-directory contract manifest name.
const ManifestFile = "contract.json"

// EntryKindKey is the kinds key for the entry schema.
const EntryKindKey = "entry"

// Manifest declares the resources a version directory owns. capability,
// entry_schema, and object_schemas keep the crucible manifest grammar; kinds
// and companions are additive fields that crucible tooling ignores.
type Manifest struct {
	Capability  string `json:"capability"`
	EntrySchema string `json:"entry_schema"`
	// ObjectSchemas lists the directory's other owned files by name.
	ObjectSchemas map[string]string `json:"object_schemas,omitempty"`
	// Kinds maps "entry" and every object_schemas name to "input" or "output".
	Kinds map[string]string `json:"kinds"`
	// Companions are resources in other directories the owned schemas may reference.
	Companions []string `json:"companions,omitempty"`
}

// owned lists the manifest's resources as kinds key → file name.
func (m Manifest) owned() map[string]string {
	out := map[string]string{EntryKindKey: m.EntrySchema}
	for name, file := range m.ObjectSchemas {
		out[name] = file
	}
	return out
}

// KindOf returns the kind declared for a file owned by the manifest.
func (m Manifest) KindOf(file string) string {
	for key, f := range m.owned() {
		if f == file {
			return m.Kinds[key]
		}
	}
	return ""
}

var capabilityPattern = regexp.MustCompile(`^contract: sumpter\.([a-z0-9][a-z0-9-]*)/v(\d+)$`)

// Capability is a parsed capability token.
type Capability struct {
	Family string
	Major  int
}

// ParseCapability parses "contract: sumpter.<family>/v<major>".
func ParseCapability(token string) (Capability, error) {
	m := capabilityPattern.FindStringSubmatch(token)
	if m == nil {
		return Capability{}, fmt.Errorf("capability %q must be \"contract: sumpter.<family>/v<major>\"", token)
	}
	major, _ := strconv.Atoi(m[2])
	return Capability{Family: m[1], Major: major}, nil
}

// LoadManifests reads every contract.json under fsys, keyed by its directory.
// Unknown fields are rejected.
func LoadManifests(fsys fs.FS) (map[string]Manifest, error) {
	manifests := map[string]Manifest{}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || path.Base(p) != ManifestFile {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.DisallowUnknownFields()
		var m Manifest
		if err := dec.Decode(&m); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		manifests[path.Dir(p)] = m
		return nil
	})
	return manifests, err
}

// manifestDirExceptions are manifest directories that are not
// <family>/<version>, mapped to the version their resources carry.
var manifestDirExceptions = map[string]string{
	"provenance": "v1",
}

// manifestLocation returns the family and version a manifest directory implies.
func manifestLocation(dir string) (family, version string, err error) {
	if v, ok := manifestDirExceptions[dir]; ok {
		return dir, v, nil
	}
	parts := strings.Split(dir, "/")
	if len(parts) != 2 || !versionPattern.MatchString(parts[1]) {
		return "", "", fmt.Errorf("manifest must live in a <family>/<version> directory")
	}
	return parts[0], parts[1], nil
}

// companionPath cleans a companion path relative to its manifest directory and
// requires it to stay inside the bundle.
func companionPath(dir, rel string) (string, error) {
	if rel == "" || strings.HasPrefix(rel, "/") || strings.Contains(rel, "\\") {
		return "", fmt.Errorf("companion %q must be a relative slash path", rel)
	}
	target := path.Clean(path.Join(dir, rel))
	if target == "." || target == ".." || strings.HasPrefix(target, "../") {
		return "", fmt.Errorf("companion %q escapes the schema bundle", rel)
	}
	return target, nil
}

// CompanionsFrom returns the companion ids each resource may reference: the
// resources named by the companions of the manifest in its own directory.
func CompanionsFrom(b *Bundle, manifests map[string]Manifest) Companions {
	byPath := map[string]Resource{}
	for _, r := range b.Resources {
		byPath[r.Path] = r
	}
	return func(r Resource) map[string]bool {
		m, ok := manifests[path.Dir(r.Path)]
		if !ok || len(m.Companions) == 0 {
			return nil
		}
		allowed := map[string]bool{}
		for _, rel := range m.Companions {
			if target, err := companionPath(path.Dir(r.Path), rel); err == nil {
				if res, ok := byPath[target]; ok && res.ID != "" {
					allowed[res.ID] = true
				}
			}
		}
		return allowed
	}
}

// CheckManifests verifies manifest placement and capability, per-resource
// kinds, companions, and that every resource is owned exactly once by the
// manifest in its own directory.
func CheckManifests(b *Bundle, manifests map[string]Manifest) []error {
	var errs []error
	byPath := map[string]Resource{}
	for _, r := range b.Resources {
		byPath[r.Path] = r
	}
	ownedBy := map[string]string{}
	capabilityByFamilyMajor := map[string]string{}

	dirs := make([]string, 0, len(manifests))
	for dir := range manifests {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)

	for _, dir := range dirs {
		m := manifests[dir]
		where := path.Join(dir, ManifestFile)
		capability, err := ParseCapability(m.Capability)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
			continue
		}
		family, version, err := manifestLocation(dir)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", where, err))
			continue
		}
		if capability.Family != family {
			errs = append(errs, fmt.Errorf("%s: capability family %q does not match directory family %q", where, capability.Family, family))
		}
		key := fmt.Sprintf("%s/v%d", capability.Family, capability.Major)
		if prev, ok := capabilityByFamilyMajor[key]; ok && prev != m.Capability {
			errs = append(errs, fmt.Errorf("%s: capability %q differs from %q declared for the same family major", where, m.Capability, prev))
		}
		capabilityByFamilyMajor[key] = m.Capability

		if m.EntrySchema == "" {
			errs = append(errs, fmt.Errorf("%s: entry_schema is required", where))
		}
		if _, ok := m.ObjectSchemas[EntryKindKey]; ok {
			errs = append(errs, fmt.Errorf("%s: object_schemas must not use the reserved name %q", where, EntryKindKey))
		}
		owned := m.owned()
		for k := range m.Kinds {
			if _, ok := owned[k]; !ok {
				errs = append(errs, fmt.Errorf("%s: kinds[%q] does not name an owned resource", where, k))
			}
		}
		keys := make([]string, 0, len(owned))
		for k := range owned {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		seenFiles := map[string]string{}
		for _, k := range keys {
			file := owned[k]
			if file == "" {
				continue
			}
			if strings.Contains(file, "/") {
				errs = append(errs, fmt.Errorf("%s: %q file %q must be a file name in this directory", where, k, file))
				continue
			}
			if prev, ok := seenFiles[file]; ok {
				errs = append(errs, fmt.Errorf("%s: %s is listed twice (%q and %q)", where, file, prev, k))
				continue
			}
			seenFiles[file] = k
			switch kind := m.Kinds[k]; kind {
			case "input", "output":
			case "":
				errs = append(errs, fmt.Errorf("%s: kinds has no entry for %q", where, k))
			default:
				errs = append(errs, fmt.Errorf("%s: kinds[%q] %q must be \"input\" or \"output\"", where, k, kind))
			}
			rp := path.Join(dir, file)
			res, ok := byPath[rp]
			if !ok {
				errs = append(errs, fmt.Errorf("%s: %q names %s, which is not a schema file", where, k, rp))
				continue
			}
			if prev, ok := ownedBy[rp]; ok {
				errs = append(errs, fmt.Errorf("%s: %s is already owned by %s", where, rp, prev))
				continue
			}
			ownedBy[rp] = where
			if id, err := ParseID(res.ID); err == nil {
				if id.Family != capability.Family || id.Major() != capability.Major {
					errs = append(errs, fmt.Errorf("%s: %s ($id %s) does not match capability %q (family and major must be equal)", where, rp, res.ID, m.Capability))
				}
				if id.Version != version {
					errs = append(errs, fmt.Errorf("%s: %s version %s does not match directory version %s", where, rp, id.Version, version))
				}
			}
		}
		for _, rel := range m.Companions {
			target, err := companionPath(dir, rel)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", where, err))
				continue
			}
			if _, ok := byPath[target]; !ok {
				errs = append(errs, fmt.Errorf("%s: companion %q does not resolve to a schema file (%s)", where, rel, target))
			}
		}
	}

	for _, r := range b.Resources {
		if _, ok := ownedBy[r.Path]; !ok {
			errs = append(errs, fmt.Errorf("%s: not owned by a %s in its directory", r.Path, ManifestFile))
		}
	}
	return errs
}
