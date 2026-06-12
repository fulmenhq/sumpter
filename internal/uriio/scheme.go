package uriio

import (
	"fmt"
	"path/filepath"
	"strings"

	gonimburi "github.com/3leaps/gonimbus/pkg/uri"
)

// Scheme identifies the storage backend a reference resolves to.
type Scheme string

const (
	// SchemeLocal is the local filesystem: bare paths and file:// URIs.
	SchemeLocal Scheme = "local"

	// SchemeS3 is AWS S3 and S3-compatible object storage (s3://).
	SchemeS3 Scheme = "s3"
)

// String returns the scheme label.
func (s Scheme) String() string { return string(s) }

// Ref is a classified storage reference.
//
// For local references, LocalPath is the filesystem path to use and LogicalURI
// is its canonical form. For cloud references, Bucket/Key/Pattern describe the
// object(s); LocalPath is empty until the reference is acquired or staged.
type Ref struct {
	// Raw is the reference exactly as supplied by the caller.
	Raw string

	// Scheme is the resolved storage backend.
	Scheme Scheme

	// LogicalURI is the canonical identity used for provenance, manifests, logs,
	// and output-pattern derivation. For bare local paths this is the raw path.
	LogicalURI string

	// LocalPath is the local filesystem path for SchemeLocal references. It is
	// empty for cloud references, which must be acquired/staged before reading.
	LocalPath string

	// Bucket is the object-store bucket for cloud references.
	Bucket string

	// Key is the object key, or the listing prefix when Pattern is set, for
	// cloud references.
	Key string

	// Pattern is the original glob for cloud references whose key contained glob
	// metacharacters. Empty when the reference is a single key or prefix.
	Pattern string
}

// IsCloud reports whether the reference targets a cloud backend.
func (r Ref) IsCloud() bool { return r.Scheme != SchemeLocal }

// IsPattern reports whether the reference's key carried glob metacharacters.
func (r Ref) IsPattern() bool { return r.Pattern != "" }

// IsPrefix reports whether the reference denotes a listing prefix (a trailing
// slash, or an empty key meaning the whole bucket) rather than a single object.
func (r Ref) IsPrefix() bool {
	return r.Scheme == SchemeS3 && (r.Key == "" || strings.HasSuffix(r.Key, "/"))
}

// Classify parses a storage reference into a scheme-tagged Ref.
//
// A reference containing "://" is parsed as a URI: file:// and s3:// are
// supported, anything else returns ErrUnsupportedScheme. Any reference without a
// scheme is treated as a bare local filesystem path — relative or absolute,
// exactly as Sumpter has always accepted — and is not touched on disk here;
// resolution happens at acquire/open time.
func Classify(ref string) (Ref, error) {
	if ref == "" {
		return Ref{}, ErrEmptyReference
	}

	// Bare path (no scheme): preserve historical local-path semantics. Note we
	// look for "://" specifically — a Windows drive path like C:\dir has a colon
	// but no "://", so it is correctly treated as a bare local path.
	if !strings.Contains(ref, "://") {
		return Ref{
			Raw:        ref,
			Scheme:     SchemeLocal,
			LogicalURI: ref,
			LocalPath:  ref,
		}, nil
	}

	parsed, err := gonimburi.ParseURI(ref)
	if err != nil {
		return Ref{}, fmt.Errorf("uriio: parse %q: %w", ref, err)
	}

	switch parsed.Provider {
	case "file":
		// gonimbus stores the absolute local path in Key as a slash path; convert
		// back to the OS separator for filesystem use.
		return Ref{
			Raw:        ref,
			Scheme:     SchemeLocal,
			LogicalURI: parsed.String(),
			LocalPath:  filepath.FromSlash(parsed.Key),
		}, nil
	case "s3":
		return Ref{
			Raw:        ref,
			Scheme:     SchemeS3,
			LogicalURI: parsed.String(),
			Bucket:     parsed.Bucket,
			Key:        parsed.Key,
			Pattern:    parsed.Pattern,
		}, nil
	default:
		return Ref{}, fmt.Errorf("%w: %s", ErrUnsupportedScheme, parsed.Provider)
	}
}
