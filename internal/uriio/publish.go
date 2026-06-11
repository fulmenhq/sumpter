package uriio

import "context"

// OutputRequest asks for a destination reference to be resolved for writing.
type OutputRequest struct {
	// Reference is the destination: a bare path, a file:// URI, or an s3:// URI.
	Reference string
}

// OutputTarget is a write destination resolved to a local working path.
//
// The write boundary writes its bytes to LocalPath using ordinary filesystem
// calls, then calls Publish to make them durable at the destination. This is the
// "publish already-produced local artifacts" contract: for local destinations
// LocalPath is the final path and Publish is a no-op (the write is already
// durable); for cloud destinations (later delivery) LocalPath is a staging file
// and Publish uploads it to the object store.
type OutputTarget struct {
	// LogicalURI is the canonical identity of the destination for provenance and
	// logs. For cloud destinations this is the object URI, never the staging path.
	LogicalURI string

	// LocalPath is the local filesystem path the write boundary writes to.
	LocalPath string

	// Scheme is the destination's storage backend.
	Scheme Scheme

	// publish finalizes the artifact at the destination. nil for local targets,
	// whose writes are already durable at LocalPath.
	publish func(context.Context) error
}

// Publish makes the bytes written to LocalPath durable at the destination. For
// local targets it is a no-op. A cloud-destination publish failure is a durable-
// output failure and must be treated as fatal by the caller (it must not be
// masked by --continue-on-error).
func (t *OutputTarget) Publish(ctx context.Context) error {
	if t == nil || t.publish == nil {
		return nil
	}
	return t.publish(ctx)
}

// OpenOutput resolves a destination reference for writing.
//
// Delivery scope: local destinations (bare paths and file://) resolve directly
// to their filesystem path with a no-op Publish — byte-for-byte the historical
// behavior. Cloud destinations (s3://) return ErrSchemeNotImplemented; the cloud
// write boundary (stage-to-local + PutObject) lands in a later v0.2.0 delivery.
//
// ctx is unused for local resolution, which is synchronous, but is part of the
// stable signature for the cloud publish path to come.
func OpenOutput(ctx context.Context, req OutputRequest) (*OutputTarget, error) {
	_ = ctx // reserved for the cloud stage/publish path (later delivery)

	ref, err := Classify(req.Reference)
	if err != nil {
		return nil, err
	}

	switch ref.Scheme {
	case SchemeLocal:
		return &OutputTarget{
			LogicalURI: ref.LogicalURI,
			LocalPath:  ref.LocalPath,
			Scheme:     SchemeLocal,
			publish:    nil,
		}, nil
	default:
		return nil, notImplemented("result publication", ref)
	}
}
