//go:build !cgo || !seekablezstd

package store

// openSeekableZstdStore returns an error when seekable-zstd support is not available.
//
// To enable seekable-zstd support, rebuild with:
//
//	CGO_ENABLED=1 go build -tags seekablezstd ./...
func openSeekableZstdStore(path string) (IndexStore, error) {
	return nil, ErrSeekableZstdNotAvailable
}

// SeekableZstdAvailable returns false when built without seekable-zstd support.
func SeekableZstdAvailable() bool {
	return false
}
