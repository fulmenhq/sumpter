package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/spf13/cobra"
)

// resolveProvenanceRootSelection applies flag-over-environment precedence and
// resolves the root before command planning. The selected root value is kept
// out of every error produced here.
func resolveProvenanceRootSelection(flagValue string, flagSet bool) (string, bool, bool, *provenance.Root, error) {
	value := ""
	fromFlag := false
	if flagSet {
		if strings.TrimSpace(flagValue) == "" {
			return "", false, false, nil, fmt.Errorf("--provenance-root cannot be empty")
		}
		value = flagValue
		fromFlag = true
	} else if envValue, ok := os.LookupEnv(provenanceRootEnvVar); ok {
		if strings.TrimSpace(envValue) == "" {
			return "", false, false, nil, fmt.Errorf("%s cannot be empty", provenanceRootEnvVar)
		}
		value = envValue
	}
	if value == "" {
		return "", false, false, nil, nil
	}

	root, err := provenance.ResolveRoot(value)
	if err != nil {
		return "", false, false, nil, fmt.Errorf("provenance root must be an existing directory")
	}
	return value, true, fromFlag, root, nil
}

func resolveProvenanceRootFlag(cmd *cobra.Command, opts *ExtractOptions) error {
	if cmd == nil || opts == nil {
		return nil
	}
	value, selected, fromFlag, root, err := resolveProvenanceRootSelection(
		opts.ProvenanceRoot,
		cmd.Flags().Changed("provenance-root"),
	)
	if err != nil {
		return err
	}
	if !selected {
		return nil
	}
	opts.ProvenanceRoot = value
	opts.ProvenanceRootSet = true
	opts.ProvenanceRootFromFlag = fromFlag
	opts.provenanceRoot = root
	return nil
}

func resolveRecipeProvenanceRootFlag(cmd *cobra.Command, opts *recipeRunExtractOptions) error {
	if cmd == nil || opts == nil {
		return nil
	}
	value, selected, fromFlag, root, err := resolveProvenanceRootSelection(
		opts.ProvenanceRoot,
		cmd.Flags().Changed("provenance-root"),
	)
	if err != nil {
		return err
	}
	if !selected {
		return nil
	}
	opts.ProvenanceRoot = value
	opts.ProvenanceRootSet = true
	opts.ProvenanceRootFromFlag = fromFlag
	opts.provenanceRoot = root
	return nil
}

func resolveExtractMultiProvenanceRootFlag(cmd *cobra.Command, opts *recipeRunExtractMultiOptions) error {
	if cmd == nil || opts == nil {
		return nil
	}
	value, selected, fromFlag, root, err := resolveProvenanceRootSelection(
		opts.ProvenanceRoot,
		cmd.Flags().Changed("provenance-root"),
	)
	if err != nil {
		return err
	}
	if !selected {
		return nil
	}
	opts.ProvenanceRoot = value
	opts.ProvenanceRootSet = true
	opts.ProvenanceRootFromFlag = fromFlag
	opts.provenanceRoot = root
	return nil
}

func ensureProvenanceRoot(opts *ExtractOptions) error {
	if opts == nil {
		return nil
	}
	if !opts.ProvenanceRootSet && strings.TrimSpace(opts.ProvenanceRoot) == "" {
		if envValue, ok := os.LookupEnv(provenanceRootEnvVar); ok {
			if strings.TrimSpace(envValue) == "" {
				return fmt.Errorf("%s cannot be empty", provenanceRootEnvVar)
			}
			opts.ProvenanceRoot = envValue
			opts.ProvenanceRootSet = true
		}
	}
	if !opts.ProvenanceRootSet && strings.TrimSpace(opts.ProvenanceRoot) != "" {
		opts.ProvenanceRootSet = true
	}
	if !opts.ProvenanceRootSet {
		return nil
	}
	if strings.TrimSpace(opts.ProvenanceRoot) == "" {
		return fmt.Errorf("--provenance-root cannot be empty")
	}
	if opts.provenanceRoot != nil {
		return nil
	}
	root, err := provenance.ResolveRoot(opts.ProvenanceRoot)
	if err != nil {
		return fmt.Errorf("provenance root must be an existing directory")
	}
	opts.provenanceRoot = root
	return nil
}

func provenanceRootActive(opts *ExtractOptions) bool {
	return opts != nil && opts.ProvenanceRootSet && opts.provenanceRoot != nil
}

// provenanceRootLocalPath returns the local filesystem path for a resolved
// local input. Cloud identities are explicitly skipped by provenance-root.
func provenanceRootLocalPath(localPath, logical string) (string, bool) {
	if ref, err := uriio.Classify(logical); err == nil {
		if ref.IsCloud() {
			return "", false
		}
		return ref.LocalPath, true
	}
	if ref, err := uriio.Classify(localPath); err == nil {
		if ref.IsCloud() {
			return "", false
		}
		return ref.LocalPath, true
	}
	return localPath, true
}

func preflightProvenanceRootInputs(opts *ExtractOptions, files []string, logicalByLocal map[string]string) error {
	if !provenanceRootActive(opts) {
		return nil
	}
	for i, file := range files {
		logical := logicalIdentity(file, logicalByLocal)
		localPath, local := provenanceRootLocalPath(file, logical)
		if !local {
			continue
		}
		if _, err := opts.provenanceRoot.Relative(localPath); err != nil {
			return fmt.Errorf("input %d is outside the provenance root", i+1)
		}
	}
	return nil
}

func preflightProvenanceRootReferences(opts *ExtractOptions, refs []string) error {
	if !provenanceRootActive(opts) {
		return nil
	}
	for i, refValue := range refs {
		ref, err := uriio.Classify(refValue)
		if err == nil && ref.IsCloud() {
			continue
		}
		localPath := refValue
		if err == nil {
			localPath = ref.LocalPath
		}
		if _, err := opts.provenanceRoot.Relative(localPath); err != nil {
			return fmt.Errorf("input %d is outside the provenance root", i+1)
		}
	}
	return nil
}

func aggregatePreflightOrder(opts *ExtractOptions, files []string) []string {
	if opts == nil || !isAggregateMode(opts) || strings.TrimSpace(opts.InputPath) == "" {
		return files
	}
	ordered := append([]string(nil), files...)
	sort.Strings(ordered)
	return ordered
}

// applyProvenanceRootInputPath replaces the default sanitized path for one
// local input when the opt-in root mode is active. Cloud URIs retain their
// logical identity and all non-input path surfaces keep the old sanitizer.
func applyProvenanceRootInputPath(opts *ExtractOptions, input *provenance.Input, localPath, logical string) error {
	if !provenanceRootActive(opts) || input == nil {
		return nil
	}
	localPath, local := provenanceRootLocalPath(localPath, logical)
	if !local {
		return nil
	}
	relative, err := opts.provenanceRoot.Relative(localPath)
	if err != nil {
		return fmt.Errorf("input is outside the provenance root")
	}
	input.Path = relative
	return nil
}

// provenanceRootInputLabel is for diagnostics only. Extraction and source
// extraction continue to receive the logical identity, while logs and errors
// use a root-relative local label so the selected host path cannot leak.
func provenanceRootInputLabel(opts *ExtractOptions, localPath, logical string) string {
	if logical == "" {
		logical = localPath
	}
	if !provenanceRootActive(opts) {
		return logical
	}
	candidate, local := provenanceRootLocalPath(localPath, logical)
	if !local {
		return logical
	}
	if relative, err := opts.provenanceRoot.Relative(candidate); err == nil {
		return relative
	}
	return "<input>"
}

func provenanceRootDiagnosticText(opts *ExtractOptions, text string) string {
	if !provenanceRootActive(opts) || opts.provenanceRoot == nil {
		return text
	}
	return opts.provenanceRoot.RedactText(text)
}

type provenanceRootRedactedError struct {
	message string
	cause   error
}

func (e provenanceRootRedactedError) Error() string { return e.message }

func (e provenanceRootRedactedError) Unwrap() error { return e.cause }

func provenanceRootRedactError(opts *ExtractOptions, err error) error {
	if err == nil || !provenanceRootActive(opts) {
		return err
	}
	return provenanceRootRedactedError{
		message: provenanceRootDiagnosticText(opts, err.Error()),
		cause:   err,
	}
}

// provenanceRootInputError keeps local input paths out of root-mode diagnostics.
// The regular private-input sanitizer is intentionally unchanged in unset mode;
// root mode replaces the input with its lexical root-relative label first, then
// removes either retained root spelling from operating-system diagnostics.
func provenanceRootInputError(opts *ExtractOptions, err error, localPath, logical string, privatePaths ...string) error {
	if err == nil || !provenanceRootActive(opts) {
		return err
	}
	display := provenanceRootInputLabel(opts, localPath, logical)
	paths := append([]string{localPath, logical}, privatePaths...)
	return provenanceRootRedactError(opts, sanitizePrivateInputError(err, display, paths...))
}

func provenanceRootInputText(opts *ExtractOptions, text, localPath, logical string, privatePaths ...string) string {
	if !provenanceRootActive(opts) {
		return text
	}
	display := provenanceRootInputLabel(opts, localPath, logical)
	paths := append([]string{localPath, logical}, privatePaths...)
	return provenanceRootDiagnosticText(opts, sanitizePrivateInputText(text, display, paths...))
}

func provenanceRootOptionsForRecipe(opts *recipeRunExtractOptions) *ExtractOptions {
	if opts == nil {
		return nil
	}
	return &ExtractOptions{
		ProvenanceRoot:         opts.ProvenanceRoot,
		ProvenanceRootSet:      opts.ProvenanceRootSet,
		ProvenanceRootFromFlag: opts.ProvenanceRootFromFlag,
		provenanceRoot:         opts.provenanceRoot,
	}
}
