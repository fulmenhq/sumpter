package uriio

import (
	"context"
	"fmt"

	gonimbusprovider "github.com/3leaps/gonimbus/pkg/provider"
)

// Head checks that a single s3:// object exists and is readable, reading only
// its metadata: it never fetches object bytes and stages nothing. A missing or
// denied object returns an error matching ErrObjectNotFound or
// ErrObjectAccessDenied.
func (s *Session) Head(ctx context.Context, reference, handle string) error {
	ref, err := Classify(reference)
	if err != nil {
		return err
	}
	if ref.Scheme != SchemeS3 {
		return notImplemented("object metadata check", ref)
	}
	if ref.IsPattern() || ref.IsPrefix() {
		return fmt.Errorf("uriio: head needs a single object, not a prefix/pattern (%s)", ref.LogicalURI)
	}
	prov, err := s.pool.Provider(ctx, handle, ref.Bucket)
	if err != nil {
		return err
	}
	herr := s.retryTransient(ctx, func() error {
		_, e := prov.Head(ctx, ref.Key)
		return e
	})
	return classifyObjectError("head", herr, ref.LogicalURI, s.pool.redactionSecrets(handle))
}

// classifyObjectError maps a provider error from op ("head" or "get") to a
// uriio error by type: not-found and access-denied become the typed sentinels,
// and anything else a redacted message. The provider error itself is not
// wrapped, so its text cannot leak through the chain.
func classifyObjectError(op string, err error, logicalURI string, secrets []string) error {
	switch {
	case err == nil:
		return nil
	case gonimbusprovider.IsNotFound(err):
		return fmt.Errorf("uriio: %s %s: %w", op, logicalURI, ErrObjectNotFound)
	case gonimbusprovider.IsAccessDenied(err):
		return fmt.Errorf("uriio: %s %s: %w", op, logicalURI, ErrObjectAccessDenied)
	}
	return fmt.Errorf("uriio: %s %s failed: %s", op, logicalURI, cloudOpError(err, secrets))
}
