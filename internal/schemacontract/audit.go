package schemacontract

import (
	"errors"
	"fmt"
	"net/url"
)

// Companions reports the $ids a resource may reference outside its own family
// and version (its manifest's declared companions).
type Companions func(referrer Resource) map[string]bool

// NoCompanions allows no cross-family or cross-version references.
func NoCompanions(Resource) map[string]bool { return nil }

// resolveRef resolves a $ref against its document's $id the same way the
// schema library does (net/url ResolveReference) and strips the fragment.
// It returns "" for a same-document reference such as "#/definitions/x".
func resolveRef(baseID, ref string) (string, error) {
	base, err := url.Parse(baseID)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	resolved := base.ResolveReference(r)
	resolved.Fragment = ""
	resolved.RawFragment = ""
	out := resolved.String()
	if out == baseID {
		return "", nil
	}
	return out, nil
}

// Audit checks every $ref in the bundle before anything is compiled:
// each must resolve (with its fragment removed) to the $id of a resource in
// the bundle, stay within the referrer's own family and version unless the
// target is a declared companion, and never name another scheme. A reference
// that fails the audit would otherwise reach the schema library's fetcher.
func Audit(b *Bundle, companions Companions) []error {
	if companions == nil {
		companions = NoCompanions
	}
	ids := b.ByID()
	var errs []error
	for _, r := range b.Resources {
		referrer, err := ParseID(r.ID)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: cannot audit references: %w", r.Path, err))
			continue
		}
		allowed := companions(r)
		for _, ref := range r.Refs {
			target, err := resolveRef(r.ID, ref)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: $ref %q: %w", r.Path, ref, err))
				continue
			}
			if target == "" {
				continue // same document
			}
			u, err := url.Parse(target)
			if err != nil || u.Scheme != IDScheme {
				errs = append(errs, fmt.Errorf("%s: $ref %q resolves to %q, which is not a %s:// bundle id", r.Path, ref, target, IDScheme))
				continue
			}
			if _, ok := ids[target]; !ok {
				errs = append(errs, fmt.Errorf("%s: $ref %q: resolved id %q not in catalog", r.Path, ref, target))
				continue
			}
			parsed, err := ParseID(target)
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: $ref %q: %w", r.Path, ref, err))
				continue
			}
			if !sameFamilyVersion(referrer, parsed) && !allowed[target] {
				errs = append(errs, fmt.Errorf("%s: $ref %q resolves to %q outside %s/%s and is not a declared companion", r.Path, ref, target, referrer.Family, referrer.Version))
			}
		}
	}
	return errs
}

// ErrAuditFailed is returned by Validate when the bundle fails the audit; the
// compile function is not called in that case.
var ErrAuditFailed = errors.New("schema bundle failed the reference audit")

// CompileFunc compiles and validates a document against a root schema.
type CompileFunc func() error

// Validate runs the id checks and the reference audit, and only if both pass
// calls compile. It is the gate that keeps unchecked references away from the
// schema library.
func Validate(b *Bundle, companions Companions, compile CompileFunc) error {
	errs := append(CheckIDs(b), Audit(b, companions)...)
	if len(errs) > 0 {
		return fmt.Errorf("%w: %w", ErrAuditFailed, errors.Join(errs...))
	}
	return compile()
}
