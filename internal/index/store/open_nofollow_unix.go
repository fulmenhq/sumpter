//go:build cgo && seekablezstd && unix

package store

import (
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// anchoredDir is an open directory. Files are opened relative to it, so a
// later rename or replacement of the directory's path does not change which
// directory they come from.
type anchoredDir struct {
	f *os.File
}

func openAnchoredDir(path string) (*anchoredDir, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open index directory: %w", err)
	}
	return &anchoredDir{f: os.NewFile(uintptr(fd), path)}, nil
}

func (d *anchoredDir) Close() error { return d.f.Close() }

// openRegular opens name, a single file name in the directory, without
// following a symbolic link, and requires a regular file. The caller owns the
// returned file.
func (d *anchoredDir) openRegular(name string) (*os.File, error) {
	fd, err := unix.Openat(int(d.f.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %s: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, fmt.Errorf("%s is not a regular file", name)
	}
	return f, nil
}

// readFile reads name from the directory, without following a link.
func (d *anchoredDir) readFile(name string) ([]byte, error) {
	f, err := d.openRegular(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return io.ReadAll(f)
}

// descriptorPath is a path that names the already-open file f, so a reader
// that only accepts paths opens exactly that file, not whatever a name in the
// directory refers to now.
func descriptorPath(f *os.File) (string, error) {
	p := fmt.Sprintf("/dev/fd/%d", f.Fd())
	want, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("records store: %w", err)
	}
	// Open the path the way the reader will and compare what it yields; a
	// stat of the path itself can describe the descriptor entry instead.
	probe, err := os.Open(p) // #nosec G304 - descriptor path of a file this process opened
	if err != nil {
		return "", fmt.Errorf("records store: descriptor path unavailable: %w", err)
	}
	got, err := probe.Stat()
	_ = probe.Close()
	if err != nil {
		return "", fmt.Errorf("records store: descriptor path unavailable: %w", err)
	}
	if !os.SameFile(want, got) {
		return "", fmt.Errorf("records store: descriptor path does not name the open file")
	}
	return p, nil
}
