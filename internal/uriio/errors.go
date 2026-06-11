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

// EnsureLocal classifies ref and returns an actionable ErrSchemeNotImplemented
// error if it targets a backend whose I/O is not yet wired (cloud). It is the
// edge fast-fail counterpart to Acquire/OpenOutput: callers use it to reject a
// cloud reference before any work begins, with one consistent message. Local and
// file:// references return nil; genuinely unsupported schemes (e.g. gs://)
// return the underlying classification error. op names the operation for the
// message (e.g. "source input", "result output").
func EnsureLocal(op, ref string) error {
	classified, err := Classify(ref)
	if err != nil {
		return err
	}
	if classified.IsCloud() {
		return notImplemented(op, classified)
	}
	return nil
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
