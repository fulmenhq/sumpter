package commands

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/uriio"
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

// inputUnavailableError reports an input that could not be read. Its text
// carries the input as displayed elsewhere and a bounded class, never the raw
// OS error; the OS error stays reachable through Unwrap for classification.
type inputUnavailableError struct {
	display string
	class   string
	err     error
}

func (e *inputUnavailableError) Error() string {
	return "input unavailable: " + e.display + ": " + e.class
}

func (e *inputUnavailableError) Unwrap() error { return e.err }

// unavailableClass returns the bounded class of an error that means an input
// could not be read, and false for any other error.
func unavailableClass(err error) (string, bool) {
	switch {
	case err == nil:
		return "", false
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, uriio.ErrObjectNotFound):
		return "not found", true
	case errors.Is(err, fs.ErrPermission), errors.Is(err, uriio.ErrObjectAccessDenied):
		return "permission denied", true
	}
	return "", false
}

// failureDetail is the disposition detail recorded for a failed input: the
// bounded class for an unavailable input, and the error text otherwise.
func failureDetail(reason extract.DispositionReason, err error) string {
	if err == nil {
		return ""
	}
	if reason == extract.DispositionReasonInputUnavailable {
		if class, ok := unavailableClass(err); ok {
			return "input unavailable: " + class
		}
	}
	return err.Error()
}

// afterLocalInputPreflight, when set by a test, runs between the local input
// preflight and the first read, so a test can make an input unavailable after
// it passed preflight.
var afterLocalInputPreflight func()

// preflightLocalInputs opens and closes each explicit local input before any
// input is processed, so under fail-fast a missing or unreadable input stops
// the run before a sibling writes records. Under --continue-on-error it does
// nothing: each input is read in turn and an unavailable one is recorded as
// input_unavailable while the others proceed. Staged cloud inputs were read at
// acquisition and are skipped.
func preflightLocalInputs(opts *ExtractOptions, files []string, logicalByLocal map[string]string) error {
	if opts.ContinueOnError {
		return nil
	}
	for _, file := range files {
		if _, staged := logicalByLocal[file]; staged {
			continue
		}
		if ref, err := uriio.Classify(file); err != nil || ref.Scheme != uriio.SchemeLocal {
			continue
		}
		if err := checkLocalInput(opts, file); err != nil {
			return err
		}
	}
	return nil
}

// checkLocalInput opens and closes a local input without reading it, and
// returns a bounded input-unavailable error when it is missing or unreadable.
func checkLocalInput(opts *ExtractOptions, file string) error {
	f, err := os.Open(file) // #nosec G304 - operator-selected input path
	if err == nil {
		return f.Close()
	}
	if class, ok := unavailableClass(err); ok {
		return &inputUnavailableError{display: provenanceRootInputLabel(opts, file, file), class: class, err: err}
	}
	return err
}

// checkPreviewInputs is the dry run's availability check. Each explicitly named
// input (--files, file-list entries, recipe files) must exist and be readable:
// local files are opened and closed, and s3:// objects get a metadata-only
// HEAD. Inputs found by walking or listing --input-path already exist and are
// not checked again.
func checkPreviewInputs(ctx context.Context, opts *ExtractOptions, session *uriio.Session, refs []string) error {
	if strings.TrimSpace(opts.InputPath) != "" {
		return nil
	}
	for _, ref := range refs {
		r, err := uriio.Classify(ref)
		if err != nil {
			return err
		}
		if r.Scheme == uriio.SchemeLocal {
			if err := checkLocalInput(opts, r.LocalPath); err != nil {
				return err
			}
			continue
		}
		if session == nil {
			return fmt.Errorf("cloud input %s needs a cloud session", ref)
		}
		if err := session.Head(ctx, ref, resolvedInputHandle(opts)); err != nil {
			if class, ok := unavailableClass(err); ok {
				return &inputUnavailableError{display: ref, class: class, err: err}
			}
			return err
		}
	}
	return nil
}

// applyInputUnavailable marks a failed result whose error means the input could
// not be read with the input_unavailable reason and its bounded detail,
// replacing a reason and detail derived from the raw error.
func applyInputUnavailable(result *extract.ExtractResult) {
	if _, ok := unavailableClass(result.Error); !ok {
		return
	}
	result.Disposition = extract.DispositionFailed
	result.DispositionReason = extract.DispositionReasonInputUnavailable
	result.DispositionDetail = failureDetail(extract.DispositionReasonInputUnavailable, result.Error)
}

// inputFailureError is the fail-fast error for an input that failed: a bounded
// input-unavailable error when raw means the input could not be read, and
// otherwise "failed to process file" wrapping shown, the error as it may be
// displayed.
func inputFailureError(display string, raw, shown error) error {
	if class, ok := unavailableClass(raw); ok {
		return &inputUnavailableError{display: display, class: class, err: raw}
	}
	return fmt.Errorf("failed to process file %s: %w", display, shown)
}
