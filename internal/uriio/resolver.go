package uriio

import (
	"fmt"
	"strings"

	gonimbuss3 "github.com/3leaps/gonimbus/pkg/provider/s3"
)

// DefaultHandleName is the implicit credential handle used when a cloud
// reference declares none. It resolves through the AWS SDK default credential
// chain (environment, shared config/credentials, instance/role providers).
const DefaultHandleName = "default"

// Resolver turns a credential handle name into a resolved HandleConfig, layering
// CLI handle->profile overrides on top of the loaded credentials config. The
// precedence is CLI override > credentials config > default chain.
type Resolver struct {
	config          *CredentialsConfig
	profileByHandle map[string]string
}

// NewResolver builds a resolver from an optional credentials config and optional
// CLI --credential handle=profile overrides. A nil config behaves as empty, so a
// run with no credentials file can still use the implicit default handle.
func NewResolver(config *CredentialsConfig, cliProfiles map[string]string) *Resolver {
	if config == nil {
		config = &CredentialsConfig{Handles: map[string]HandleConfig{}}
	}
	return &Resolver{config: config, profileByHandle: cliProfiles}
}

// Resolve returns the HandleConfig for a handle name.
//
// The implicit "default" handle is allowed even when absent from the config — it
// resolves through the AWS default credential chain. Any other named handle must
// be defined; a missing one is a loud, actionable error (so a recipe referencing
// an undeclared handle fails at load, not at first I/O). A CLI --credential
// override replaces the handle's profile and supersedes any literal keys, which
// keeps secrets out of argv.
func (r *Resolver) Resolve(name string) (HandleConfig, error) {
	if name == "" {
		name = DefaultHandleName
	}

	hc, defined := r.config.Handles[name]
	profile, overridden := r.profileByHandle[name]

	if !defined && !overridden {
		if name != DefaultHandleName {
			return HandleConfig{}, fmt.Errorf(
				"uriio: credentials handle %q is not defined; declare it in the credentials config "+
					"or pass --credential %s=<profile>", name, name)
		}
		hc = HandleConfig{} // implicit default -> AWS default chain
	}

	if overridden {
		// A CLI profile reference defines or supersedes the handle's profile and
		// supersedes any literal keys, keeping secrets out of argv.
		hc.Profile = profile
		hc.AccessKeyID = ""
		hc.SecretAccessKey = ""
	}
	return hc, nil
}

// endpointPosture describes the two documented credential postures so callers
// and tests can reason about ambient vs hermetic behavior.
//
//   - Ambient/default-chain: empty Endpoint and no explicit keys. The AWS SDK
//     resolves region/profile/endpoint from the environment and shared config at
//     construction. Convenient, but ambient AWS_ENDPOINT_URL* settings can
//     redirect traffic — an operator-convenience posture.
//   - Hermetic: explicit credentials/region and a non-empty Endpoint (or the
//     embedding process suppresses ambient endpoint settings). Nothing is
//     inherited from the environment. This is the posture for unattended/CI runs.

// s3Config builds the gonimbus s3.Config for a handle + bucket. The cleartext
// secret is materialized here, at the construction boundary just before s3.New,
// and is never returned to callers, logged, formatted, or persisted.
// It is unexported precisely to keep the cleartext blast radius inside this
// package.
func (r *Resolver) s3Config(handle, bucket string) (gonimbuss3.Config, error) {
	hc, err := r.Resolve(handle)
	if err != nil {
		return gonimbuss3.Config{}, err
	}
	return gonimbuss3.Config{
		Bucket:          bucket,
		Region:          hc.Region,
		Endpoint:        hc.Endpoint,
		Profile:         hc.Profile,
		AccessKeyID:     hc.AccessKeyID.Reveal(),     // cleartext lives only on this transient value
		SecretAccessKey: hc.SecretAccessKey.Reveal(), // cleartext lives only on this transient value
		ForcePathStyle:  hc.ForcePathStyle,
	}, nil
}

// validateEndpointPosture enforces the static TLS guard: a custom endpoint must
// be https:// unless the handle explicitly opts in with insecure: true. gonimbus
// accepts http:// endpoints and exposes no verify knob, so a plaintext endpoint
// would put credentials and data on the wire. The check is static (no I/O), so
// it is safe to run at resolve time before any connection is attempted.
func validateEndpointPosture(name string, hc HandleConfig) error {
	endpoint := strings.TrimSpace(hc.Endpoint)
	if endpoint == "" {
		return nil
	}
	if strings.HasPrefix(strings.ToLower(endpoint), "http://") && !hc.Insecure {
		return fmt.Errorf(
			"uriio: credentials handle %q uses a non-TLS endpoint %q; "+
				"custom endpoints must be https:// — set insecure: true to override (not recommended)",
			name, endpoint)
	}
	return nil
}
