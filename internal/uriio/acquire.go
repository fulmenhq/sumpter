package uriio

import "context"

// AcquireRequest asks for a source reference to be made available as a local file.
type AcquireRequest struct {
	// Reference is the source: a bare path, a file:// URI, or an s3:// URI.
	Reference string
}

// AcquiredSource is a source object made available on the local filesystem.
//
// Per the "cloud at edges, local core" model, every source — local now, and
// cloud in a later delivery — is presented to the extraction core as a local
// file. Consumers read LocalPath; provenance, diagnostics, logs, and output-
// pattern derivation use LogicalURI. For staged cloud sources LocalPath is an
// internal working path that must not leak into published artifacts; for local
// sources LocalPath and the logical path coincide.
type AcquiredSource struct {
	// LogicalURI is the canonical identity of the source for provenance and logs.
	LogicalURI string

	// LocalPath is the local filesystem path the extraction core reads from.
	LocalPath string

	// Scheme is the source's storage backend.
	Scheme Scheme

	// cleanup releases any resources staged for this source. nil for local
	// sources, which stage nothing.
	cleanup func() error
}

// Cleanup releases resources staged for this source. It is idempotent and safe
// to call on every path: success, handled failure, and early error. For local
// sources it is a no-op. A non-nil return should be surfaced by the caller with
// sanitized context and must not mask an earlier extraction or output error.
func (s *AcquiredSource) Cleanup() error {
	if s == nil || s.cleanup == nil {
		return nil
	}
	err := s.cleanup()
	s.cleanup = nil // idempotent: subsequent calls are no-ops
	return err
}

// Acquire resolves a source reference to a local file.
//
// Delivery scope: local references (bare paths and file://) resolve directly to
// their filesystem path with a no-op cleanup — byte-for-byte the historical
// behavior. Cloud references (s3://) return ErrSchemeNotImplemented; the cloud
// read boundary (GetObject + stage-to-local) lands in a later v0.2.0 delivery.
//
// ctx is unused for local resolution, which is synchronous, but is part of the
// stable signature for the cloud GetObject path to come.
func Acquire(ctx context.Context, req AcquireRequest) (*AcquiredSource, error) {
	_ = ctx // reserved for the cloud acquire/stage path (later delivery)

	ref, err := Classify(req.Reference)
	if err != nil {
		return nil, err
	}

	switch ref.Scheme {
	case SchemeLocal:
		return &AcquiredSource{
			LogicalURI: ref.LogicalURI,
			LocalPath:  ref.LocalPath,
			Scheme:     SchemeLocal,
			cleanup:    nil,
		}, nil
	default:
		return nil, notImplemented("source acquisition", ref)
	}
}
