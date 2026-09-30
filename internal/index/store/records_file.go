//go:build cgo && seekablezstd

package store

import (
	"fmt"
	"strings"
)

// recordsFileName validates the records_file a record-index-szst/v0.1.2
// header names. The header's directory is the trust root: the value must be a
// single file name in it, never a path, a parent reference, or empty.
func recordsFileName(name string) error {
	switch {
	case name == "":
		return fmt.Errorf("invalid header: records_file is required")
	case name == "." || name == "..":
		return fmt.Errorf("invalid header: records_file %q is not a file name", name)
	case strings.ContainsAny(name, "/\\\x00"):
		return fmt.Errorf("invalid header: records_file %q must be a file name beside the header, not a path", name)
	}
	return nil
}
