package uriio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// redactedMarker is substituted for any secret value across logs, structured
// logging, JSON, formatted output, and errors.
const redactedMarker = "[redacted]"

// Secret is a credential value — an S3 access key id or secret access key — that
// must never be logged, formatted, serialized, or wrapped into an error in
// cleartext.
//
// It redacts itself across every output surface: fmt verbs (%v/%s/%+v/%#v via
// String/GoString), structured logging (slog.LogValuer), JSON (MarshalJSON), and
// YAML (MarshalYAML). The cleartext is reachable only through Reveal, which
// callers invoke solely at the boundary that hands the value to the provider
// SDK. Gonimbus's s3.Config carries the key as a plain string with no redaction
// and does not zero it, so keeping the cleartext inside this type until the
// s3.New call site is the embedder's responsibility.
type Secret string

// String returns the redaction marker for a non-empty secret, or "" when unset.
func (s Secret) String() string {
	if s == "" {
		return ""
	}
	return redactedMarker
}

// GoString redacts under the %#v verb.
func (s Secret) GoString() string { return s.String() }

// LogValue redacts under structured logging.
func (s Secret) LogValue() slog.Value { return slog.StringValue(s.String()) }

// MarshalJSON redacts when serialized to JSON.
func (s Secret) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// MarshalYAML redacts when serialized to YAML. Without this, the string-alias
// underlying type would emit the cleartext value.
func (s Secret) MarshalYAML() (interface{}, error) { return s.String(), nil }

// Reveal returns the cleartext secret. Call this only at the provider
// construction boundary; never pass the result to a logger, formatter, error, or
// persisted artifact.
func (s Secret) Reveal() string { return string(s) }

// IsZero reports whether the secret is unset.
func (s Secret) IsZero() bool { return s == "" }

// HandleConfig is the resolved configuration for one named credential handle.
// It is the sumpter-owned, redaction-safe representation; the gonimbus s3.Config
// (which carries the cleartext key) is built transiently at provider
// construction and never stored or logged.
type HandleConfig struct {
	// Profile is the AWS shared-config profile name to resolve credentials from.
	Profile string `yaml:"profile,omitempty"`
	// Region is the AWS region (or the region an S3-compatible store requires).
	Region string `yaml:"region,omitempty"`
	// Endpoint is a custom endpoint for S3-compatible stores. Empty for AWS S3.
	Endpoint string `yaml:"endpoint,omitempty"`
	// ForcePathStyle forces path-style URLs (required by most S3-compatible stores).
	ForcePathStyle bool `yaml:"force_path_style,omitempty"`
	// Insecure opts in to a non-TLS (http://) endpoint. It is a loud, explicit
	// footgun guard: a plaintext endpoint puts credentials and data on the wire.
	// The connect-time enforcement lands with the cloud read boundary.
	Insecure bool `yaml:"insecure,omitempty"`
	// Anonymous opts the handle into unsigned, anonymous reads of a public bucket.
	// It is read-only by construction: the underlying provider rejects every
	// mutating operation, and Sumpter additionally rejects an anonymous handle on
	// any write target before staging (a write boundary never reaches the library
	// error). Anonymous is mutually exclusive with all credential material — a
	// Profile, literal keys, or a --credential override — and is rejected at config
	// load (or CLI resolve) if combined. TLS endpoint posture still applies: an
	// anonymous request still goes on the wire.
	Anonymous bool `yaml:"anonymous,omitempty"`
	// AccessKeyID is an explicit access key id. Permitted but discouraged; prefer
	// a Profile handle. If set, SecretAccessKey must also be set. It is a Secret
	// so the AKIA-style identifier never leaks through logs/JSON/YAML/format/errors.
	AccessKeyID Secret `yaml:"access_key_id,omitempty"`
	// SecretAccessKey is an explicit secret key. Permitted but discouraged. It is
	// a Secret so it never leaks through logs/JSON/YAML/format/errors.
	SecretAccessKey Secret `yaml:"secret_access_key,omitempty"`
}

// HasLiteralKeys reports whether the handle carries inline credential material
// (as opposed to a profile/default-chain reference).
func (h HandleConfig) HasLiteralKeys() bool {
	return !h.AccessKeyID.IsZero() || !h.SecretAccessKey.IsZero()
}

// CredentialsConfig is the parsed sumpter credentials file: a set of named
// handles. It never holds bucket names — a handle describes an account /
// endpoint / region, and the bucket comes from the s3:// URI at I/O time.
type CredentialsConfig struct {
	Handles map[string]HandleConfig `yaml:"handles"`
}

// hasAnyLiteralKeys reports whether any handle carries inline credential material.
func (c *CredentialsConfig) hasAnyLiteralKeys() bool {
	for _, h := range c.Handles {
		if h.HasLiteralKeys() {
			return true
		}
	}
	return false
}

var handleNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validHandleName reports whether name is a portable handle slug. Kebab-case is
// the documented convention; this is the permissive validation.
func validHandleName(name string) bool {
	return handleNamePattern.MatchString(name)
}

// ValidateHandleName checks that name is a portable credential-handle slug,
// returning an actionable error if not. It is the shared rule for every handle
// reference surface — the credentials config, --credential overrides, and the
// --input/output-credentials-handle selectors — so a malformed name (e.g. a
// key-shaped value) is rejected identically everywhere.
func ValidateHandleName(name string) error {
	if !validHandleName(name) {
		return fmt.Errorf("invalid credentials handle name %q (allowed: %s)", name, handleNamePattern.String())
	}
	return nil
}

// LoadCredentialsConfig reads, validates, and returns a credentials config.
//
// The parse is fail-closed: an unknown or typo'd field — e.g. "insecur: true" —
// is an error, never a silent no-op into the insecure default. If any handle
// carries literal keys, the file must be owner-only:
// a group/world-accessible literal-key file is rejected with an actionable
// message. Handle/profile-only files are exempt from the permission check.
func LoadCredentialsConfig(path string) (*CredentialsConfig, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("uriio: stat credentials config: %w", err)
	}
	data, err := os.ReadFile(path) // #nosec G304 - user-specified credentials config path
	if err != nil {
		return nil, fmt.Errorf("uriio: read credentials config: %w", err)
	}

	cfg, err := parseCredentialsConfig(data)
	if err != nil {
		return nil, err
	}

	if cfg.hasAnyLiteralKeys() {
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			return nil, fmt.Errorf(
				"uriio: credentials config %s carries literal keys but is group/world-accessible (mode %#o); "+
					"restrict it to owner-only (chmod 0600) or use a profile handle instead",
				path, perm)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// parseCredentialsConfig strictly decodes the credentials YAML.
func parseCredentialsConfig(data []byte) (*CredentialsConfig, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true) // S10: reject unknown/typo'd fields rather than silently dropping them.

	var cfg CredentialsConfig
	if err := dec.Decode(&cfg); err != nil {
		if errors.Is(err, io.EOF) {
			return &CredentialsConfig{Handles: map[string]HandleConfig{}}, nil
		}
		return nil, fmt.Errorf("uriio: parse credentials config: %w", err)
	}
	if cfg.Handles == nil {
		cfg.Handles = map[string]HandleConfig{}
	}
	return &cfg, nil
}

// validate checks handle names, the both-or-neither literal-key rule, and the
// static TLS endpoint posture.
func (c *CredentialsConfig) validate() error {
	for name, h := range c.Handles {
		if err := ValidateHandleName(name); err != nil {
			return fmt.Errorf("uriio: %w", err)
		}
		if !h.AccessKeyID.IsZero() != !h.SecretAccessKey.IsZero() {
			return fmt.Errorf("uriio: credentials handle %q: access_key_id and secret_access_key must be set together", name)
		}
		// PA2: anonymous is mutually exclusive with any credential material. Reject
		// at load rather than silently letting one win — anonymous is a distinct,
		// read-only posture, not a fallback.
		if h.Anonymous && (h.Profile != "" || h.HasLiteralKeys()) {
			return fmt.Errorf("uriio: credentials handle %q: anonymous is mutually exclusive with profile/access_key_id/secret_access_key (anonymous is read-only public-bucket access; drop the credential fields or the anonymous flag)", name)
		}
		if err := validateEndpointPosture(name, h); err != nil {
			return err
		}
	}
	return nil
}
