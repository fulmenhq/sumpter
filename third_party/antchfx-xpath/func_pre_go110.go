//go:build !go1.10
// +build !go1.10

package xpath

import "bytes"

func newStringBuilder() stringBuilder {
	return &bytes.Buffer{}
}
