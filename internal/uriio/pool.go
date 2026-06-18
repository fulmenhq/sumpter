package uriio

import (
	"context"
	"fmt"
	"sync"

	gonimbuss3 "github.com/3leaps/gonimbus/pkg/provider/s3"
)

// ProviderPool lazily constructs and caches gonimbus S3 providers for the
// lifetime of a run.
//
// One provider is created per unique (handle, bucket): a handle describes an
// account/endpoint/region, while gonimbus binds the bucket into the provider's
// config, so the bucket is part of the cache key. Providers are safe for
// concurrent use; the pool serializes construction. The cleartext s3.Config
// exists only for the duration of the s3.New call and is never retained.
type ProviderPool struct {
	resolver  *Resolver
	mu        sync.Mutex
	providers map[string]*gonimbuss3.Provider
}

// NewProviderPool builds a pool over a resolver.
func NewProviderPool(resolver *Resolver) *ProviderPool {
	return &ProviderPool{
		resolver:  resolver,
		providers: map[string]*gonimbuss3.Provider{},
	}
}

// redactionSecrets returns the literal secret cleartext values configured for a
// handle, for scrubbing from provider/SDK error strings. Profile and CLI-override
// handles resolve their credentials inside the AWS SDK and expose no cleartext
// here, so the slice is empty for them.
func (p *ProviderPool) redactionSecrets(handle string) []string {
	return p.resolver.redactionSecrets(handle)
}

// poolKey is the cache key for a (handle, bucket) pair, normalizing the empty
// handle to the default so "" and "default" share one provider.
func poolKey(handle, bucket string) string {
	if handle == "" {
		handle = DefaultHandleName
	}
	return handle + "\x00" + bucket
}

// Provider returns the S3 provider for a handle + bucket, constructing it once
// and reusing it for the run lifetime.
func (p *ProviderPool) Provider(ctx context.Context, handle, bucket string) (*gonimbuss3.Provider, error) {
	if bucket == "" {
		return nil, fmt.Errorf("uriio: provider requires a bucket")
	}
	key := poolKey(handle, bucket)

	p.mu.Lock()
	defer p.mu.Unlock()

	if prov, ok := p.providers[key]; ok {
		return prov, nil
	}

	cfg, err := p.resolver.s3Config(handle, bucket)
	if err != nil {
		return nil, err
	}
	prov, err := gonimbuss3.New(ctx, cfg)
	if err != nil {
		// The error comes from the AWS SDK config loader and describes a
		// config/credential-resolution problem, not the secret value; cfg (which
		// holds the cleartext key) goes out of scope here and is never logged.
		// Route it through cloudOpError (classify + redact) like every other
		// cloud-op error, with %s (not %w) so downstream wrapping cannot re-expose
		// the raw SDK error — completing the "no raw cloud-op error surfaces"
		// invariant for the one remaining provider-construction site.
		return nil, fmt.Errorf("uriio: construct s3 provider for handle %q: %s", handleLabel(handle), cloudOpError(err, p.redactionSecrets(handle)))
	}
	p.providers[key] = prov
	return prov, nil
}

// Len reports the number of pooled providers (one per unique handle+bucket).
func (p *ProviderPool) Len() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.providers)
}

// Close releases all pooled providers, attempting every close and returning the
// first error encountered.
func (p *ProviderPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	var firstErr error
	for key, prov := range p.providers {
		if err := prov.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		delete(p.providers, key)
	}
	return firstErr
}

// handleLabel returns a display label for a handle name (the empty handle shows
// as the default). The handle name is an operator-chosen label, not a secret.
func handleLabel(handle string) string {
	if handle == "" {
		return DefaultHandleName
	}
	return handle
}
