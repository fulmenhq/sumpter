package uriio

import (
	"errors"
	"fmt"
)

var (
	// ErrEmptyReference indicates an empty storage reference was supplied.
	ErrEmptyReference = errors.New("uriio: empty storage reference")

	// ErrUnsupportedScheme indicates a URI scheme Sumpter does not recognize at
	// all (anything other than file:// or s3://).
	ErrUnsupportedScheme = errors.New("uriio: unsupported scheme")

	// ErrSchemeNotImplemented indicates a recognized scheme whose I/O is not yet
	// wired in this build. It is returned for s3:// references until the cloud
	// read/write boundaries land in a later v0.2.0 delivery.
	ErrSchemeNotImplemented = errors.New("uriio: scheme not yet implemented")
)

// LocalPath classifies ref and returns its local filesystem path. file:// URIs
// resolve to their absolute local path; bare paths pass through unchanged. It is
// how the edge normalizes a root reference (input path, output path, record
// index) to a clean local path before that path is joined to child artifact
// names or walked for discovery — losing the scheme to a path join would mangle
// a file:// root. Cloud (s3://) references return an actionable
// ErrSchemeNotImplemented error (op names the role for the message, e.g.
// "source input"); genuinely unsupported schemes (e.g. gs://) return the
// underlying classification error.
func LocalPath(op, ref string) (string, error) {
	classified, err := Classify(ref)
	if err != nil {
		return "", err
	}
	if classified.IsCloud() {
		return "", notImplemented(op, classified)
	}
	return classified.LocalPath, nil
}

// EnsureLocal is the validate-only form of LocalPath: it returns the same error
// for cloud/unsupported references but discards the resolved path. Use it when a
// reference is resolved to a local path elsewhere (e.g. per-file at the acquire
// loop) and only its scheme needs guarding here.
func EnsureLocal(op, ref string) error {
	_, err := LocalPath(op, ref)
	return err
}

// notImplemented builds an actionable error for a recognized-but-unwired scheme.
// The message points operators at the local fallback and the roadmap without
// naming any internal milestone or consumer.
func notImplemented(op string, ref Ref) error {
	return fmt.Errorf(
		"%w: %s for %s references is not available in this build; "+
			"cloud (s3://) read/write lands in a later v0.2.0 delivery — "+
			"use a local path or a file:// URI for now (reference: %s)",
		ErrSchemeNotImplemented, op, ref.Scheme, ref.LogicalURI,
	)
}
