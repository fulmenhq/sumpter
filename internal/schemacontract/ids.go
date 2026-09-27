package schemacontract

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// IDScheme is the scheme of every resource $id in the bundle.
const IDScheme = "contract"

// idAuthorityPrefix prefixes the family in a resource $id authority.
const idAuthorityPrefix = "sumpter."

var (
	familyPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	versionPattern = regexp.MustCompile(`^v(\d+)(\.\d+\.\d+)?$`)
)

// idExceptions lists resources whose file layout does not follow
// <family>/<version>/<file>. The map value is the $id the file must carry.
var idExceptions = map[string]string{
	"provenance/v1.json": "contract://sumpter.provenance/v1/provenance.schema.json",
}

// ResourceID is a parsed resource $id:
// contract://sumpter.<Family>/<Version>/<File>.
type ResourceID struct {
	Family  string
	Version string
	File    string
}

// Major returns the major version number ("v0.1.2" → 0, "v1" → 1).
func (r ResourceID) Major() int {
	m := versionPattern.FindStringSubmatch(r.Version)
	n, _ := strconv.Atoi(m[1])
	return n
}

// String renders the canonical $id.
func (r ResourceID) String() string {
	return IDScheme + "://" + idAuthorityPrefix + r.Family + "/" + r.Version + "/" + r.File
}

// ParseID parses and validates a resource $id. The hierarchical form is
// required so relative $refs resolve against their own family and version;
// the opaque "contract:family/..." form loses that base and is rejected.
func ParseID(id string) (ResourceID, error) {
	u, err := url.Parse(id)
	if err != nil {
		return ResourceID{}, fmt.Errorf("invalid $id %q: %w", id, err)
	}
	if u.Scheme != IDScheme {
		return ResourceID{}, fmt.Errorf("$id %q: scheme must be %q", id, IDScheme)
	}
	if u.Opaque != "" {
		return ResourceID{}, fmt.Errorf("$id %q: opaque form is not allowed; use %s://%s<family>/<version>/<file>", id, IDScheme, idAuthorityPrefix)
	}
	if u.User != nil || u.Port() != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(id, "#") {
		return ResourceID{}, fmt.Errorf("$id %q: userinfo, port, query, and fragment are not allowed", id)
	}
	host := u.Host
	if host != strings.ToLower(host) {
		return ResourceID{}, fmt.Errorf("$id %q: authority must be lower-case", id)
	}
	family, ok := strings.CutPrefix(host, idAuthorityPrefix)
	if !ok || !familyPattern.MatchString(family) {
		return ResourceID{}, fmt.Errorf("$id %q: authority must be %s<family>", id, idAuthorityPrefix)
	}
	segments := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(segments) != 2 || segments[1] == "" {
		return ResourceID{}, fmt.Errorf("$id %q: path must be /<version>/<file>", id)
	}
	if !versionPattern.MatchString(segments[0]) {
		return ResourceID{}, fmt.Errorf("$id %q: version segment %q must look like v1 or v0.1.2", id, segments[0])
	}
	parsed := ResourceID{Family: family, Version: segments[0], File: segments[1]}
	if parsed.String() != id {
		return ResourceID{}, fmt.Errorf("$id %q is not in canonical form (%s)", id, parsed.String())
	}
	return parsed, nil
}

// expectedID returns the $id a bundle file must carry, derived from its path.
func expectedID(p string) (string, error) {
	if id, ok := idExceptions[p]; ok {
		return id, nil
	}
	parts := strings.Split(p, "/")
	if len(parts) != 3 {
		return "", fmt.Errorf("%s: schema files must live at <family>/<version>/<file>", p)
	}
	id := ResourceID{Family: parts[0], Version: parts[1], File: parts[2]}.String()
	if _, err := ParseID(id); err != nil {
		return "", fmt.Errorf("%s: %w", p, err)
	}
	return id, nil
}

// CheckIDs verifies that every resource carries the $id its path implies and
// that no two resources share an $id.
func CheckIDs(b *Bundle) []error {
	var errs []error
	seen := map[string]string{}
	for _, r := range b.Resources {
		if r.ID == "" {
			errs = append(errs, fmt.Errorf("%s: missing $id", r.Path))
			continue
		}
		if _, err := ParseID(r.ID); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", r.Path, err))
			continue
		}
		want, err := expectedID(r.Path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if r.ID != want {
			errs = append(errs, fmt.Errorf("%s: $id %q does not match its location (want %q)", r.Path, r.ID, want))
		}
		if prev, ok := seen[r.ID]; ok {
			errs = append(errs, fmt.Errorf("%s: duplicate $id %q (also %s)", r.Path, r.ID, prev))
			continue
		}
		seen[r.ID] = r.Path
	}
	return errs
}

// sameFamilyVersion reports whether two parsed ids share family and version.
func sameFamilyVersion(a, b ResourceID) bool {
	return a.Family == b.Family && a.Version == b.Version
}
