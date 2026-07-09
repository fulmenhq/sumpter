package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
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

// maybeValidateExtractOutput runs the opt-in output validation ladder after
// sidecars (and optional artifact descriptor) have been written. No-op when off.
func maybeValidateExtractOutput(opts *ExtractOptions, manifest provenance.Manifest) error {
	if opts == nil || !validateOutputIncludes(opts.ValidateOutput, validateOutputSidecars) {
		return nil
	}
	return runValidateExtractOutput(opts, manifest)
}

func runValidateExtractOutput(opts *ExtractOptions, manifest provenance.Manifest) error {
	validator, err := newEmbeddedSchemaValidator()
	if err != nil {
		return err
	}

	if validateOutputIncludes(opts.ValidateOutput, validateOutputSidecars) {
		if err := validateExtractSidecars(opts, validator); err != nil {
			return err
		}
	}
	if validateOutputIncludes(opts.ValidateOutput, validateOutputArtifact) {
		if err := validateExtractArtifactSidecars(opts); err != nil {
			return err
		}
	}
	if validateOutputIncludes(opts.ValidateOutput, validateOutputEnvelopeSample) {
		allRows := normalizeValidateOutput(opts.ValidateOutput) == validateOutputStrict
		if err := validateExtractRecordEnvelopes(opts, manifest, validator, allRows); err != nil {
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

func validateExtractSidecars(opts *ExtractOptions, validator *validation.SchemaValidator) error {
	manifestData, manifestName, err := readExtractSidecarBytes(opts, provenance.ManifestFileName)
	if err != nil {
		return fmt.Errorf("validate-output sidecars: read %s: %w", provenance.ManifestFileName, err)
	}
	result, err := validator.ValidateProvenanceManifest(manifestData, manifestName)
	if err != nil {
		return fmt.Errorf("validate-output sidecars: %s: %w", provenance.ManifestFileName, err)
	}
	if !result.IsValid() {
		return fmt.Errorf("validate-output sidecars: %s failed: %s", provenance.ManifestFileName, result.ErrorSummary())
	}

	if err := validateOptionalSidecar(opts, validator, "failures.json", validator.ValidateFailureManifest); err != nil {
		return err
	}
	if err := validateOptionalSidecar(opts, validator, "dispositions.json", validator.ValidateDispositionSummary); err != nil {
		return err
	}
	return nil
}

type sidecarValidateFunc func(data []byte, name string) (*validation.ValidationResult, error)

func validateOptionalSidecar(opts *ExtractOptions, _ *validation.SchemaValidator, name string, validate sidecarValidateFunc) error {
	localPath, err := localSidecarPath(opts, name)
	if err != nil {
		return fmt.Errorf("validate-output sidecars: resolve %s: %w", name, err)
	}
	data, err := os.ReadFile(localPath) // #nosec G304 - extract sidecar written by this run
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("validate-output sidecars: read %s: %w", name, err)
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

func validateExtractArtifactSidecars(opts *ExtractOptions) error {
	resolved, err := artifactcontract.ResolveBaseline(opts.ArtifactContractBase)
	if err != nil {
		return fmt.Errorf("validate-output artifact: resolve contract base: %w", err)
	}

	descriptorData, descriptorName, err := readExtractSidecarBytes(opts, dataartifact.DescriptorFileName)
	if err != nil {
		return fmt.Errorf("validate-output artifact: read %s: %w", dataartifact.DescriptorFileName, err)
	}
	result, err := artifactcontract.ValidateDescriptorBytes(resolved, descriptorData, descriptorName)
	if err != nil {
		return fmt.Errorf("validate-output artifact: %s: %w", dataartifact.DescriptorFileName, err)
	}
	if !result.Valid {
		return fmt.Errorf("validate-output artifact: %s failed: %s", dataartifact.DescriptorFileName, artifactValidationSummary(result))
	}

	catalogData, catalogName, err := readExtractSidecarBytes(opts, dataartifact.FieldCatalogRef)
	if err != nil {
		return fmt.Errorf("validate-output artifact: read %s: %w", dataartifact.FieldCatalogRef, err)
	}
	catalogResult, err := artifactcontract.ValidateFieldCatalogBytes(resolved, catalogData, catalogName)
	if err != nil {
		return fmt.Errorf("validate-output artifact: %s: %w", dataartifact.FieldCatalogRef, err)
	}
	if !catalogResult.Valid {
		return fmt.Errorf("validate-output artifact: %s failed: %s", dataartifact.FieldCatalogRef, artifactValidationSummary(catalogResult))
	}
	return nil
}

func validateExtractRecordEnvelopes(opts *ExtractOptions, manifest provenance.Manifest, validator *validation.SchemaValidator, allRows bool) error {
	for _, output := range manifest.Outputs {
		format := strings.ToLower(strings.TrimSpace(output.Format))
		if format != recipesmanifest.OutputFormatJSON && format != recipesmanifest.OutputFormatNDJSON && format != "jsonl" {
			continue
		}
		path := strings.TrimSpace(output.Path)
		if path == "" {
			continue
		}
		// Output.Path may be a logical relative name or absolute/local path.
		localPath, err := resolveManifestOutputLocalPath(opts, path)
		if err != nil {
			return fmt.Errorf("validate-output envelope: resolve %s: %w", path, err)
		}
		if err := validateNDJSONEnvelopeFile(localPath, path, validator, allRows); err != nil {
			return err
		}
	}
	return nil
}

func resolveManifestOutputLocalPath(opts *ExtractOptions, outputPath string) (string, error) {
	if filepath.IsAbs(outputPath) {
		return outputPath, nil
	}
	// Prefer reading through the output seam so cloud staging paths resolve.
	if local, err := localSidecarPath(opts, outputPath); err == nil {
		return local, nil
	}
	joined := outputRefJoin(opts.OutputPath, outputPath)
	if filepath.IsAbs(joined) {
		return joined, nil
	}
	return localSidecarPath(opts, joined)
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

func readExtractSidecarBytes(opts *ExtractOptions, name string) ([]byte, string, error) {
	localPath, err := localSidecarPath(opts, name)
	if err != nil {
		return nil, name, err
	}
	data, err := os.ReadFile(localPath) // #nosec G304 - extract sidecar written by this run
	if err != nil {
		return nil, localPath, err
	}
	return data, name, nil
}

func localSidecarPath(opts *ExtractOptions, name string) (string, error) {
	path := outputRefJoin(opts.OutputPath, name)
	tgt, err := openOutputTarget(context.Background(), opts, path)
	if err != nil {
		return "", err
	}
	return tgt.LocalPath, nil
}
