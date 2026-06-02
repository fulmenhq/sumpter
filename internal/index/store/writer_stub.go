//go:build !cgo || !seekablezstd

package store

func newBinaryRecordStream(_ string) (binaryRecordStream, error) {
	return nil, ErrSeekableZstdNotAvailable
}
