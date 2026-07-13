//go:build !unix

package processrun

import "os"

// sameFile is a conservative fallback when inode identity is unavailable.
func sameFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	// Without inode identity, require equal size and modtime as a weak check.
	return a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}
