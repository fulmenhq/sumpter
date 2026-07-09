package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/fulmenhq/sumpter/internal/validation"
)

// Opt-in extract output validation ladder (data-artifact producer profile).
// Default is off so high-volume runs stay byte-compatible with prior behavior.
const (
	validateOutputOff            = "off"
	validateOutputSidecars       = "sidecars"
	validateOutputArtifact       = "artifact"
	validateOutputEnvelopeSample = "envelope-sample"
	validateOutputStrict         = "strict"
)

// envelopeSampleStride validates the first record, every Nth record, and the last.
const envelopeSampleStride = 100

func normalizeValidateOutput(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		return validateOutputOff
	}
	return mode
}

func validateOutputIncludes(mode, rung string) bool {
	switch normalizeValidateOutput(mode) {
	case validateOutputOff:
		return false
	case validateOutputSidecars:
		return rung == validateOutputSidecars
	case validateOutputArtifact:
		return rung == validateOutputSidecars || rung == validateOutputArtifact
	case validateOutputEnvelopeSample:
		return rung == validateOutputSidecars || rung == validateOutputArtifact || rung == validateOutputEnvelopeSample
	case validateOutputStrict:
		return rung == validateOutputSidecars || rung == validateOutputArtifact || rung == validateOutputEnvelopeSample || rung == validateOutputStrict
	default:
		return false
	}
}

func validateValidateOutputOptions(opts *ExtractOptions) error {
	if opts == nil {
		return nil
	}
	mode := normalizeValidateOutput(opts.ValidateOutput)
	switch mode {
	case validateOutputOff:
		opts.ValidateOutput = validateOutputOff
		return nil
	case validateOutputSidecars, validateOutputArtifact, validateOutputEnvelopeSample, validateOutputStrict:
		opts.ValidateOutput = mode
	default:
		return fmt.Errorf("--validate-output must be one of off, sidecars, artifact, envelope-sample, strict (got %q)", opts.ValidateOutput)
	}

	if strings.TrimSpace(opts.OutputPath) == "" {
		return fmt.Errorf("--validate-output requires --output-path")
	}
	if opts.NoManifest {
		return fmt.Errorf("--validate-output cannot be combined with --no-manifest because sidecars validation requires the provenance manifest")
	}
	if opts.DryRun {
		return fmt.Errorf("--validate-output cannot be combined with --dry-run")
	}
	if validateOutputIncludes(mode, validateOutputArtifact) {
		if !opts.ArtifactDescriptor {
			return fmt.Errorf("--validate-output %s requires --artifact-descriptor", mode)
		}
		if strings.TrimSpace(opts.ArtifactContractBase) == "" {
			return fmt.Errorf("--validate-output %s requires --contract-base", mode)
		}
	}
	return nil
}

// maybeValidateExtractOutput is the end-of-run local re-check after sidecars land.
//
// Cloud outputs (opts.outputSession != nil) are intentionally skipped here:
// Publish removes the staging file after PutObject, and re-opening via
// openOutputTarget would create a fresh empty staging path. Cloud validation
// therefore runs write-time on the complete staged file *before* Publish
// (see validateOutputSidecarBytes / maybeValidateEnvelopeFileBeforePublish).
func maybeValidateExtractOutput(opts *ExtractOptions, manifest provenance.Manifest) error {
	if opts == nil || !validateOutputIncludes(opts.ValidateOutput, validateOutputSidecars) {
		return nil
	}
	if opts.outputSession != nil {
		return nil
	}
	return runValidateExtractOutputLocal(opts, manifest)
}

func runValidateExtractOutputLocal(opts *ExtractOptions, manifest provenance.Manifest) error {
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return err
	}

	if validateOutputIncludes(opts.ValidateOutput, validateOutputSidecars) {
		if err := validateLocalSidecarFile(opts, provenance.ManifestFileName, validator.ValidateProvenanceManifest); err != nil {
			return err
		}
		if err := validateOptionalLocalSidecar(opts, "failures.json", validator.ValidateFailureManifest); err != nil {
			return err
		}
		if err := validateOptionalLocalSidecar(opts, "dispositions.json", validator.ValidateDispositionSummary); err != nil {
			return err
		}
	}
	// Artifact rung was already enforced at write-time for --artifact-descriptor
	// (descriptor/catalog validate before Publish). Local re-check of those files
	// is covered by the same write path; skip a second open here.
	if validateOutputIncludes(opts.ValidateOutput, validateOutputEnvelopeSample) {
		allRows := normalizeValidateOutput(opts.ValidateOutput) == validateOutputStrict
		if err := validateLocalRecordEnvelopes(opts, manifest, validator, allRows); err != nil {
			return err
		}
	}
	return nil
}

func newEmbeddedSchemaValidator() (*validation.SchemaValidator, error) {
	schemaFS, err := assets.GetSchemasFS()
	if err != nil {
		return nil, fmt.Errorf("load embedded schemas for --validate-output: %w", err)
	}
	return validation.NewSchemaValidatorFromFS(schemaFS), nil
}

type sidecarValidateFunc func(data []byte, name string) (*validation.ValidationResult, error)

// validateOutputSidecarBytes validates marshaled sidecar bytes when the
// sidecars rung is active. Used write-time before Publish so cloud staging is
// still present and no post-publish re-open is required.
func validateOutputSidecarBytes(opts *ExtractOptions, data []byte, name string, validate sidecarValidateFunc) error {
	if opts == nil || !validateOutputIncludes(opts.ValidateOutput, validateOutputSidecars) {
		return nil
	}
	if validate == nil {
		return fmt.Errorf("validate-output sidecars: missing validator for %s", name)
	}
	result, err := validate(data, name)
	if err != nil {
		return fmt.Errorf("validate-output sidecars: %s: %w", name, err)
	}
	if !result.IsValid() {
		return fmt.Errorf("validate-output sidecars: %s failed: %s", name, result.ErrorSummary())
	}
	return nil
}

// maybeValidateEnvelopeFileBeforePublish validates a complete NDJSON file on the
// local (or staging) path before Publish removes a cloud staging file.
func maybeValidateEnvelopeFileBeforePublish(opts *ExtractOptions, localPath, displayName string) error {
	if opts == nil || !validateOutputIncludes(opts.ValidateOutput, validateOutputEnvelopeSample) {
		return nil
	}
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return err
	}
	allRows := normalizeValidateOutput(opts.ValidateOutput) == validateOutputStrict
	return validateNDJSONEnvelopeFile(localPath, displayName, validator, allRows)
}

func validateLocalSidecarFile(opts *ExtractOptions, name string, validate sidecarValidateFunc) error {
	// Local-only end-of-run path: join under OutputPath without re-opening the
	// cloud output seam (which would allocate a new staging object key).
	localPath := outputRefJoin(opts.OutputPath, name)
	if ref, err := uriio.Classify(localPath); err == nil && ref.IsCloud() {
		return fmt.Errorf("validate-output sidecars: unexpected cloud path at local re-check for %s", name)
	}
	data, err := os.ReadFile(localPath) // #nosec G304 - local extract sidecar written by this run
	if err != nil {
		return fmt.Errorf("validate-output sidecars: read %s: %w", name, err)
	}
	return validateOutputSidecarBytes(opts, data, name, validate)
}

func validateOptionalLocalSidecar(opts *ExtractOptions, name string, validate sidecarValidateFunc) error {
	localPath := outputRefJoin(opts.OutputPath, name)
	data, err := os.ReadFile(localPath) // #nosec G304 - optional local extract sidecar
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("validate-output sidecars: read %s: %w", name, err)
	}
	return validateOutputSidecarBytes(opts, data, name, validate)
}

func validateLocalRecordEnvelopes(opts *ExtractOptions, manifest provenance.Manifest, validator *validation.SchemaValidator, allRows bool) error {
	for _, output := range manifest.Outputs {
		format := strings.ToLower(strings.TrimSpace(output.Format))
		if format != "json" && format != "ndjson" && format != "jsonl" {
			continue
		}
		path := strings.TrimSpace(output.Path)
		if path == "" {
			continue
		}
		if ref, err := uriio.Classify(path); err == nil && ref.IsCloud() {
			// Cloud record streams were validated write-time before Publish.
			continue
		}
		localPath := path
		if !filepath.IsAbs(localPath) && opts != nil && strings.TrimSpace(opts.OutputPath) != "" {
			localPath = outputRefJoin(opts.OutputPath, path)
		}
		if err := validateNDJSONEnvelopeFile(localPath, path, validator, allRows); err != nil {
			return err
		}
	}
	return nil
}

func validateNDJSONEnvelopeFile(localPath, displayName string, validator *validation.SchemaValidator, allRows bool) error {
	f, err := os.Open(localPath) // #nosec G304 - extract output path already written by this run
	if err != nil {
		return fmt.Errorf("validate-output envelope: open %s: %w", displayName, err)
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	// Extract records can be large; allow up to 10 MiB per line for validation.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	lineNo := 0
	var lastLine []byte
	var lastLineNo int
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		lineNo++
		// Keep a copy of the last non-empty line for sample mode.
		lastLine = append(lastLine[:0], line...)
		lastLineNo = lineNo

		if allRows || lineNo == 1 || lineNo%envelopeSampleStride == 0 {
			if err := validateEnvelopeLine(validator, line, displayName, lineNo); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("validate-output envelope: read %s: %w", displayName, err)
	}
	if !allRows && lastLineNo > 1 && lastLineNo%envelopeSampleStride != 0 {
		if err := validateEnvelopeLine(validator, lastLine, displayName, lastLineNo); err != nil {
			return err
		}
	}
	return nil
}

func validateEnvelopeLine(validator *validation.SchemaValidator, line []byte, displayName string, lineNo int) error {
	label := fmt.Sprintf("%s:%d", displayName, lineNo)
	result, err := validator.ValidateExtractRecordEnvelope(line, label)
	if err != nil {
		return fmt.Errorf("validate-output envelope: %s: %w", label, err)
	}
	if !result.IsValid() {
		return fmt.Errorf("validate-output envelope: %s failed: %s", label, result.ErrorSummary())
	}
	return nil
}

// provenanceSidecarValidator adapts the embedded schema validator for write-time
// provenance checks without forcing callers to construct the validator themselves.
func provenanceSidecarValidator() (sidecarValidateFunc, error) {
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return nil, err
	}
	return validator.ValidateProvenanceManifest, nil
}

func failureSidecarValidator() (sidecarValidateFunc, error) {
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return nil, err
	}
	return validator.ValidateFailureManifest, nil
}

func dispositionSidecarValidator() (sidecarValidateFunc, error) {
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return nil, err
	}
	return validator.ValidateDispositionSummary, nil
}
