//go:build cgo && seekablezstd && !unix

package store

import (
	"fmt"
	"os"
)

type anchoredDir struct{}

func openAnchoredDir(path string) (*anchoredDir, error) {
	return nil, fmt.Errorf("index directory %q: anchored open is not supported on this platform", path)
}

func (*anchoredDir) Close() error { return nil }

func (*anchoredDir) openRegular(name string) (*os.File, error) {
	return nil, fmt.Errorf("%s: no-follow open is not supported on this platform", name)
}

func (*anchoredDir) readFile(name string) ([]byte, error) {
	return nil, fmt.Errorf("%s: anchored read is not supported on this platform", name)
}

func descriptorPath(*os.File) (string, error) {
	return "", fmt.Errorf("records store: descriptor path is not supported on this platform")
}
