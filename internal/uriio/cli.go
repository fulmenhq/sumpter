package uriio

import (
	"fmt"
	"regexp"
	"strings"
)

// awsAccessKeyIDPattern matches the shape of an AWS access key id (and its
// session/role variants). No legitimate AWS profile name takes this form.
var awsAccessKeyIDPattern = regexp.MustCompile(`^(AKIA|ASIA|AIDA|AROA|AGPA|ANPA|ANVA|AIPA)[A-Z0-9]{16}$`)

// base64ish matches the character set of an AWS secret access key.
var base64ish = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// looksLikeRawAWSKey reports whether v has the shape of a raw AWS credential — an
// access key id, or a 40-character secret-key-shaped token. Profile names do not
// take these forms, so this is a safe guard against a secret being passed where a
// profile reference is expected.
func looksLikeRawAWSKey(v string) bool {
	if awsAccessKeyIDPattern.MatchString(v) {
		return true
	}
	if len(v) == 40 && base64ish.MatchString(v) {
		return true
	}
	return false
}

// ParseCredentialOverrides parses repeatable `--credential handle=profile`
// values into a handle->profile map.
//
// Each value must be handle=profile with a valid handle slug and a non-empty
// profile reference. A value that looks like a raw AWS key is rejected:
// credentials must never be passed on the command line, where they would land in
// argv, ps output, and shell history. Duplicate handles are an error.
func ParseCredentialOverrides(values []string) (map[string]string, error) {
	out := make(map[string]string, len(values))
	for _, raw := range values {
		handle, profile, ok := strings.Cut(strings.TrimSpace(raw), "=")
		if !ok {
			return nil, fmt.Errorf("uriio: --credential %q must be in handle=profile form", raw)
		}
		handle = strings.TrimSpace(handle)
		profile = strings.TrimSpace(profile)
		if handle == "" || profile == "" {
			return nil, fmt.Errorf("uriio: --credential %q must be in handle=profile form (both parts non-empty)", raw)
		}
		if !validHandleName(handle) {
			return nil, fmt.Errorf("uriio: --credential handle %q is not a valid handle name (allowed: %s)", handle, handleNamePattern.String())
		}
		if looksLikeRawAWSKey(profile) {
			return nil, fmt.Errorf(
				"uriio: --credential %s=… looks like a raw AWS credential; pass an AWS *profile name*, "+
					"not a key — keep secrets out of the command line (use a credentials config handle for explicit keys)",
				handle)
		}
		if _, dup := out[handle]; dup {
			return nil, fmt.Errorf("uriio: --credential handle %q specified more than once", handle)
		}
		out[handle] = profile
	}
	return out, nil
}
