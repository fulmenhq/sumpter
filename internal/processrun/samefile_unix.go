//go:build unix

package processrun

import (
	"os"
	"syscall"
)

func sameFile(a, b os.FileInfo) bool {
	if a == nil || b == nil {
		return false
	}
	sa, okA := a.Sys().(*syscall.Stat_t)
	sb, okB := b.Sys().(*syscall.Stat_t)
	if !okA || !okB {
		return false
	}
	return sa.Dev == sb.Dev && sa.Ino == sb.Ino
}
