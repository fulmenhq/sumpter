// Package uriio is Sumpter's storage-reference dispatch seam: the single point
// that turns a user-supplied storage reference (a bare local path, a file://
// URI, or an s3:// URI) into concrete local-filesystem I/O.
//
// # Cloud at the edges, local in the core
//
// Sumpter's cloud delivery follows a "cloud at the edges, local core" model.
// Cloud logic lives only at two boundaries:
//
//   - the read boundary, where a source reference is acquired and (for cloud
//     sources, in a later delivery) staged to a local working file; and
//   - the write boundary, where produced local artifacts are published to the
//     destination.
//
// Everything between the boundaries — temp files, working state, and all index
// operations — runs on the local filesystem. The extraction core therefore only
// ever sees local paths; it never learns whether a source originated in the
// cloud. uriio is where that translation happens.
//
// # Delivery scope
//
// This delivery wires the seam with local resolution only. Local references
// (bare paths and file://) resolve directly to their filesystem path, byte-for-
// byte preserving Sumpter's historical behavior. Cloud references (s3://) are
// recognized and rejected with an actionable error (ErrSchemeNotImplemented);
// the cloud read/write boundaries land in later v0.2.0 deliveries against
// gonimbus pkg/provider/s3.
//
// # Logical identity vs local path
//
// Acquire returns both a LogicalURI (the canonical identity of the source) and a
// LocalPath (where the core reads from). Provenance, diagnostics, logs, and
// output-pattern derivation must use the LogicalURI. For staged cloud sources
// the LocalPath is an internal working path that must never leak into published
// artifacts; for local sources the two coincide.
//
// # Supported gonimbus surface
//
// uriio is the only Sumpter package that imports gonimbus. It depends solely on
// the gonimbus library-consumer surface (pkg/uri, and in later deliveries
// pkg/match and pkg/provider/s3). It must not import gonimbus CLI, server, or
// index-store packages; the dependency-boundary test in this package enforces
// that.
package uriio
