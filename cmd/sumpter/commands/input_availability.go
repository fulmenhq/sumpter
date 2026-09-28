package commands

import (
	"fmt"
	"path/filepath"
	"strings"
)

// What a relative input reference resolves against, named in diagnostics in
// place of a resolved path.
const (
	baseKindWorkingDir = "working directory"
	baseKindRecipeDir  = "recipe directory"
)

// noInputsMatchedError reports a run whose input root exists but matched no
// files. It names the root as the operator wrote it and the patterns, and says
// what a relative root is relative to; it never prints a resolved path.
func noInputsMatchedError(opts *ExtractOptions) error {
	root := opts.inputDisplay
	if root == "" {
		root = opts.InputPath
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no input files matched under %s", root)
	if opts.IncludePattern != "" || opts.ExcludePattern != "" {
		b.WriteString(" (")
		if opts.IncludePattern != "" {
			fmt.Fprintf(&b, "include %q", opts.IncludePattern)
		}
		if opts.ExcludePattern != "" {
			if opts.IncludePattern != "" {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "exclude %q", opts.ExcludePattern)
		}
		b.WriteString(")")
	}
	if kind := relativeBaseKind(root, opts.inputBaseKind); kind != "" {
		fmt.Fprintf(&b, " relative to the %s", kind)
	}
	return fmt.Errorf("%s", b.String())
}

// relativeBaseKind returns the base kind for a relative local reference, and
// "" for an absolute path or a URI, which need no base.
func relativeBaseKind(ref, kind string) string {
	if ref == "" || filepath.IsAbs(ref) || strings.Contains(ref, "://") {
		return ""
	}
	if kind == "" {
		return baseKindWorkingDir
	}
	return kind
}
