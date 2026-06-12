package uriio

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// ListRequest asks for the objects under a reference to be enumerated.
type ListRequest struct {
	// Reference is the listing root: a bare path, a file:// URI, or an s3:// URI.
	Reference string

	// Recursive walks nested directories (local) or the full key space under the
	// prefix (cloud). When false, only the immediate level is listed.
	Recursive bool
}

// ListEntry is a single enumerated object.
type ListEntry struct {
	// LogicalURI is the canonical identity of the object.
	LogicalURI string

	// LocalPath is the local filesystem path for local entries; empty for cloud
	// entries, which must be acquired before reading.
	LocalPath string

	// Size is the object size in bytes.
	Size int64
}

// Listing is the result of a List call.
type Listing struct {
	// Scheme is the storage backend that was listed.
	Scheme Scheme

	// Entries are the enumerated objects, in a stable (sorted) order.
	Entries []ListEntry

	// FullBucketScan is true when a cloud listing had an empty prefix (the whole
	// bucket). The caller can warn or gate this blast-radius case.
	FullBucketScan bool
}

// List enumerates the objects under a reference.
//
// Delivery scope: local references enumerate regular files under the path (the
// single file itself when the path is a file). Cloud references (s3://) return
// ErrSchemeNotImplemented; cloud prefix listing with include/exclude pattern
// matching (gonimbus pkg/match) lands with the cloud read boundary in a later
// v0.2.0 delivery. Pattern filtering is therefore not applied locally yet; the
// caller's existing discovery handles local filtering until the boundaries unify.
//
// ctx is unused for local enumeration, which is synchronous, but is part of the
// stable signature for the cloud listing path to come.
func List(ctx context.Context, req ListRequest) (*Listing, error) {
	_ = ctx // reserved for the cloud listing path (later delivery)

	ref, err := Classify(req.Reference)
	if err != nil {
		return nil, err
	}
	if ref.Scheme != SchemeLocal {
		return nil, notImplemented("listing", ref)
	}

	info, err := os.Stat(ref.LocalPath)
	if err != nil {
		return nil, fmt.Errorf("uriio: stat %q: %w", ref.LocalPath, err)
	}

	listing := &Listing{Scheme: SchemeLocal}
	if !info.IsDir() {
		listing.Entries = append(listing.Entries, ListEntry{
			LogicalURI: ref.LogicalURI,
			LocalPath:  ref.LocalPath,
			Size:       info.Size(),
		})
		return listing, nil
	}

	walk := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			// Skip nested directories' contents when non-recursive, but always
			// descend the root itself.
			if !req.Recursive && path != ref.LocalPath {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		fi, infoErr := d.Info()
		if infoErr != nil {
			return infoErr
		}
		listing.Entries = append(listing.Entries, ListEntry{
			LogicalURI: path,
			LocalPath:  path,
			Size:       fi.Size(),
		})
		return nil
	}

	if err := filepath.WalkDir(ref.LocalPath, walk); err != nil {
		return nil, fmt.Errorf("uriio: list %q: %w", ref.LocalPath, err)
	}
	return listing, nil
}
