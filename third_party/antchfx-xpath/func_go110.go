//go:build go1.10
// +build go1.10

package xpath

import "strings"

func newStringBuilder() stringBuilder {
	return &strings.Builder{}
}
