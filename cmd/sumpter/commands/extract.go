package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fulmenhq/goneat/pkg/pathfinder"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/extract/parallel"
	"github.com/fulmenhq/sumpter/internal/extract/parquetwriter"
	"github.com/fulmenhq/sumpter/internal/extract/transforms"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const runIDEnvVar = "SUMPTER_RUN_ID"

var errJSONOutput = errors.New("json output failure")

type ExtractOptions struct {
	Files                    string
	InputPath                string
	IncludePattern           string
	ExcludePattern           string
	MaxDepth                 int
	FollowSymlinks           bool
	Workers                  int
	DryRun                   bool
	ContinueOnError          bool
	Progress                 bool
	Format                   string
	Formats                  []string
	OutputPath               string
	OutputPattern            string
	OutputPatterns           map[string]string
	UniformSchema            bool
	ParquetCompression       string
	ParquetWithholdColumns   []string
	SignatureConfig          string
	ExtractConfig            string
	ApplicabilityConfig      *extract.ApplicabilityConfig
	ClientID                 string
	SiteID                   string
	ManifestParameters       map[string]string
	Parameters               []string
	ParametersRequired       []string
	SourceExtraction         []recipesmanifest.SourceExtractionPattern
	SourceExtractionRequired []string
	SourceExtractionInput    recipesmanifest.InputDefaults
	SourceExtractionRecipeID string
	RunID                    string
	NoManifest               bool
	AllowLargeFiles          bool
	CommandName              string
	Argv                     []string
	Recipe                   *provenance.Recipe
	// Parallel extraction options
	RecordIndex       string // Path to record index file
	MaxRecordSizeMB   int    // Maximum record size in MB (0 = no limit)
	SkipLargeRecords  bool   // If true, skip oversized records; if false, fail
	VerifyIndex       bool   // Run SHA verification before extraction
	RuntimeProvenance provenance.RuntimeOptions
}

func NewExtractCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract structured data from XML files and manage transforms",
		Long: `Extract structured records from XML files based on user-provided signature and extract configurations.

The command supports both direct file specification and directory scanning with glob patterns.
Files are matched against the signature configuration, and matching records are extracted
according to the extract configuration, producing structured output.`,
	}

	// Add subcommands
	cmd.AddCommand(newExtractFilesCommand())
	cmd.AddCommand(newExtractTransformsCommand())

	return cmd
}

func newExtractFilesCommand() *cobra.Command {
	opts := &ExtractOptions{}

	cmd := &cobra.Command{
		Use:   "files",
		Short: "Extract structured data from XML files",
		Long: `Extract structured records from XML files based on user-provided signature and extract configurations.

The command supports both direct file specification and directory scanning with glob patterns.
Files are matched against the signature configuration, and matching records are extracted
according to the extract configuration, producing structured output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Retrieve the allow-large-files flag from the persistent flags
			allowLargeFiles, err := cmd.InheritedFlags().GetBool("allow-large-files")
			if err != nil {
				return fmt.Errorf("failed to get allow-large-files flag: %w", err)
			}
			if cmd.Flags().Changed("format") && cmd.Flags().Changed("formats") {
				return fmt.Errorf("--format and --formats are mutually exclusive")
			}
			if cmd.Flags().Changed("formats") {
				opts.Format = ""
			}
			opts.AllowLargeFiles = allowLargeFiles
			opts.CommandName = "sumpter extract files"
			opts.Argv = buildExtractArgv(opts)
			return runExtract(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Files, "files", "", "Comma-separated list of file paths to process")
	cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Input path for processing (directory or file)")
	cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "*.xml", "File inclusion pattern (use quotes for globs: \"*.xml\")")
	cmd.Flags().StringVar(&opts.ExcludePattern, "exclude-pattern", "", "File exclusion pattern (use quotes for globs: \"temp/*\")")
	cmd.Flags().IntVar(&opts.MaxDepth, "max-depth", 0, "Maximum directory depth to scan (0 = unlimited)")
	cmd.Flags().BoolVar(&opts.FollowSymlinks, "follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().IntVar(&opts.Workers, "workers", 1, "Number of parallel workers")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview operation without execution")
	cmd.Flags().BoolVar(&opts.ContinueOnError, "continue-on-error", false, "Continue processing sibling files after recoverable per-file failures; requires --output-path")
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "json", "Output format")
	cmd.Flags().StringSliceVar(&opts.Formats, "formats", nil, "Output formats (comma-separated or repeatable; json/ndjson/parquet)")
	cmd.Flags().StringVarP(&opts.OutputPath, "output-path", "o", "", "Output destination path")
	cmd.Flags().StringVar(&opts.OutputPattern, "output-pattern", "extract-{}.json", "Output filename pattern for files mode (use {} for input filename)")
	cmd.Flags().StringVar(&opts.SignatureConfig, "signature-config-path", "", "Path to signature configuration YAML file")
	cmd.Flags().StringVar(&opts.ExtractConfig, "extract-config-path", "", "Path to extract configuration YAML file")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "Client ID to blend into extracted records")
	cmd.Flags().StringVar(&opts.SiteID, "site-id", "", "Site ID to blend into extracted records")
	cmd.Flags().StringSliceVar(&opts.Parameters, "parameter", nil, "Inject a key=value pair into every record (repeatable)")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "UUIDv7 run identifier for deterministic replay (overrides SUMPTER_RUN_ID)")
	cmd.Flags().BoolVar(&opts.NoManifest, "no-manifest", false, "Disable provenance sidecar manifest output")

	// Parallel extraction flags
	cmd.Flags().StringVar(&opts.RecordIndex, "record-index", "", "Path to record index file (enables parallel extraction)")
	cmd.Flags().IntVar(&opts.MaxRecordSizeMB, "max-record-size-mb", 0, "Maximum record size in MB (0 = no limit)")
	cmd.Flags().BoolVar(&opts.SkipLargeRecords, "skip-large-records", false, "Skip oversized records instead of failing")
	cmd.Flags().BoolVar(&opts.VerifyIndex, "verify-index", false, "Verify index integrity with SHA-256 before extraction")

	_ = cmd.MarkFlagRequired("signature-config-path")
	_ = cmd.MarkFlagRequired("extract-config-path")

	return cmd
}

func newExtractTransformsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transforms",
		Short: "Manage and inspect data transforms",
		Long:  `List available transforms, get details about specific transforms, and manage transform configurations.`,
	}

	cmd.AddCommand(newExtractTransformsListCommand())
	cmd.AddCommand(newExtractTransformsDescribeCommand())

	return cmd
}

func newExtractTransformsListCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List all available transforms",
		Long:  `Display a list of all registered transforms grouped by category.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExtractTransformsList()
		},
	}
}

func newExtractTransformsDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe [transform-name]",
		Short: "Describe a specific transform",
		Long:  `Show detailed information about a specific transform including parameters and usage.`,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExtractTransformsDescribe(args[0])
		},
	}
}

func runExtract(opts *ExtractOptions) error {
	logger := logging.GetLogger()
	logger.Debug("Starting extract command")
	startedAt := time.Now().UTC()

	// Validate options
	if opts.Files == "" && opts.InputPath == "" {
		return fmt.Errorf("must specify either --files or --input-path")
	}
	if opts.Files != "" && opts.InputPath != "" {
		return fmt.Errorf("cannot specify both --files and --input-path")
	}
	outputFormats, err := effectiveOutputFormats(opts)
	if err != nil {
		return err
	}
	if hasOutputFormat(outputFormats, recipesmanifest.OutputFormatParquet) && opts.OutputPath == "" {
		return fmt.Errorf("parquet output requires --output-path")
	}
	if opts.ContinueOnError && strings.TrimSpace(opts.OutputPath) == "" && !opts.DryRun {
		return fmt.Errorf("--continue-on-error requires --output-path")
	}
	logger.Debug("Options validated")

	// Load configurations
	logger.Debug("Loading signature config", zap.String("path", opts.SignatureConfig))
	sigCfg, err := extract.LoadSignatureConfig(opts.SignatureConfig)
	if err != nil {
		return fmt.Errorf("failed to load signature config: %w", err)
	}
	logger.Debug("Signature config loaded", zap.String("signature_id", sigCfg.SignatureID))

	logger.Debug("Loading extract config", zap.String("path", opts.ExtractConfig))
	extCfg, err := extract.LoadExtractConfig(opts.ExtractConfig)
	if err != nil {
		return fmt.Errorf("failed to load extract config: %w", err)
	}
	if err := extract.SetUniformSchema(extCfg, opts.UniformSchema); err != nil {
		return err
	}
	logger.Debug("Extract config loaded", zap.String("record_type", extCfg.RecordType))
	if err := validateParquetWithholdColumns(opts.ParquetWithholdColumns, extCfg.OutputSchema); err != nil {
		return err
	}
	if opts.Recipe != nil && len(opts.Recipe.FieldProvenance) == 0 {
		opts.Recipe.FieldProvenance = buildFieldProvenance(extCfg.FieldMappings)
	}

	runtimeProvenance, err := buildExtractRuntimeProvenance(opts)
	if err != nil {
		return err
	}

	fieldPlan, err := buildExternalFieldPlan(opts, extCfg.FieldMappings)
	if err != nil {
		return err
	}
	if err := validateSourceExtractionDeclarations(opts, extCfg.FieldMappings); err != nil {
		return err
	}
	warnLimiter := newSourceExtractionWarnLimiter(1000)

	if opts.ApplicabilityConfig != nil && opts.RecordIndex != "" {
		return fmt.Errorf("applicability declared but not supported in record-index mode")
	}

	// Route to parallel extraction if --record-index is provided
	if opts.RecordIndex != "" {
		logger.Info("Parallel extraction mode enabled", zap.String("record_index", opts.RecordIndex))
		return runParallelExtraction(opts, sigCfg, extCfg, fieldPlan, warnLimiter, runtimeProvenance)
	}

	// Continue with sequential extraction
	logger.Debug("Sequential extraction mode")

	// Get file list
	var files []string
	if opts.Files != "" {
		logger.Debug("Processing comma-separated file list", zap.String("files", opts.Files))
		files = strings.Split(opts.Files, ",")
		for i, f := range files {
			files[i] = strings.TrimSpace(f)
		}
	} else {
		logger.Debug("Discovering input files", zap.String("input_path", opts.InputPath))
		files, err = discoverInputFiles(opts)
		if err != nil {
			return fmt.Errorf("failed to find files: %w", err)
		}
	}
	logger.Debug("File discovery complete", zap.Int("file_count", len(files)))
	if opts.ApplicabilityConfig != nil {
		if strings.TrimSpace(opts.OutputPath) == "" && !opts.DryRun {
			return fmt.Errorf("applicability declared but requires --output-path for dispositions output")
		}
		if err := validateApplicabilityMode(opts, files); err != nil {
			return err
		}
	}

	// Dry run: just list files
	if opts.DryRun {
		logger.Debug("Starting dry run")
		for _, file := range files {
			fmt.Println(file)
		}
		logger.Debug("Dry run complete, exiting")
		return nil
	}

	if opts.Progress {
		logger.Info("Starting extraction",
			zap.Int("file_count", len(files)),
			zap.String("signature", sigCfg.SignatureID),
			zap.String("record_type", extCfg.RecordType))
	}

	if shouldUseSequentialJSONStreaming(opts, extCfg, outputFormats) {
		return runSequentialJSONStreamingExtraction(opts, sigCfg, extCfg, files, fieldPlan, warnLimiter, runtimeProvenance, startedAt)
	}

	// Process files
	results := make(chan extract.ExtractResult, len(files))
	manifestEnabled := shouldWriteManifest(opts)
	manifestInputs := make([]provenance.Input, 0, len(files))
	manifestOutputs := make([]provenance.Output, 0, len(files))
	countsByRecordType := make(map[string]int)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = filepath.Join(opts.OutputPath, provenance.ManifestFileName)
	}
	dispositionSummary := newDispositionSummary(len(files))
	failureManifest := newExtractFailureManifest(len(files))
	var dispositionFailure error

	// For now, serial processing
	for _, file := range files {
		externalFields, err := buildExternalFieldsForFile(file, opts, fieldPlan, warnLimiter)
		if err != nil {
			if opts.ContinueOnError {
				result := recoverableFailureResult(file, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
				results <- result
				continue
			}
			return fmt.Errorf("failed to build external fields for file %s: %w", file, err)
		}
		result := extract.ProcessFileWithApplicability(file, sigCfg, extCfg, opts.ApplicabilityConfig, externalFields, opts.AllowLargeFiles, runtimeProvenance)
		results <- result
	}
	close(results)

	// Collect and output results
	for result := range results {
		if result.Disposition == extract.DispositionFailed {
			if result.Error != nil {
				logger.Error("Failed to process file",
					zap.String("file", result.File),
					zap.Error(result.Error))
			}
			dispositionSummary.add(result, sanitizeRoots)
			failureErr := failureErrorForResult(result, sanitizeRoots)
			if opts.ContinueOnError {
				detail := result.DispositionDetail
				if detail == "" && result.Error != nil {
					detail = result.Error.Error()
				}
				failureManifest.add(result.File, result.DispositionReason, detail, sanitizeRoots)
			}
			if manifestEnabled {
				input, err := provenance.BuildInputLedger(result.File, sanitizeRoots...)
				if err != nil {
					if opts.ContinueOnError {
						logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.File), zap.Error(err))
					} else {
						return err
					}
				} else {
					input.RecordType = extCfg.RecordType
					applyInputDisposition(&input, result, sanitizeRoots)
					manifestInputs = append(manifestInputs, input)
				}
			}
			if !opts.ContinueOnError && dispositionFailure == nil {
				dispositionFailure = failureErr
			}
			continue
		}

		if result.Error != nil {
			logger.Error("Failed to process file",
				zap.String("file", result.File),
				zap.Error(result.Error))
			if opts.ContinueOnError {
				reason := failureReasonForError(result.Error)
				if reason == "" {
					reason = extract.DispositionReasonInternalError
				}
				result.Disposition = extract.DispositionFailed
				result.DispositionReason = reason
				result.DispositionDetail = result.Error.Error()
				failureManifest.add(result.File, reason, result.Error.Error(), sanitizeRoots)
				if manifestEnabled {
					input, ledgerErr := provenance.BuildInputLedger(result.File, sanitizeRoots...)
					if ledgerErr != nil {
						logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.File), zap.Error(ledgerErr))
					} else {
						input.RecordType = extCfg.RecordType
						applyInputDisposition(&input, result, sanitizeRoots)
						manifestInputs = append(manifestInputs, input)
					}
				}
				continue
			}
			return fmt.Errorf("failed to process file %s: %w", result.File, result.Error)
		}

		if result.Disposition != extract.DispositionNotApplicable {
			if err := enforceMinOccurrences(opts, extCfg, sigCfg, result.File, result.PerSelectorCounts, result.PerSelectorCountsComplete, result.SignatureMatchStatus, result.SignatureConfidence); err != nil {
				if result.Disposition == extract.DispositionApplied || opts.ContinueOnError {
					reason := failureReasonForError(err)
					if reason == "" {
						reason = extract.DispositionReasonMinOccurrencesViolation
					}
					result.Disposition = extract.DispositionFailed
					result.DispositionReason = reason
					result.DispositionDetail = err.Error()
					dispositionSummary.add(result, sanitizeRoots)
					if opts.ContinueOnError {
						failureManifest.add(result.File, reason, err.Error(), sanitizeRoots)
					}
					if manifestEnabled {
						input, ledgerErr := provenance.BuildInputLedger(result.File, sanitizeRoots...)
						if ledgerErr != nil {
							return ledgerErr
						}
						input.RecordType = extCfg.RecordType
						applyInputDisposition(&input, result, sanitizeRoots)
						manifestInputs = append(manifestInputs, input)
					}
					if !opts.ContinueOnError && dispositionFailure == nil {
						dispositionFailure = err
					}
					continue
				}
				return err
			}
		}

		if result.Disposition != "" {
			dispositionSummary.add(result, sanitizeRoots)
		}

		if manifestEnabled {
			input, err := provenance.BuildInputLedger(result.File, sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			applyInputDisposition(&input, result, sanitizeRoots)
			manifestInputs = append(manifestInputs, input)
		}

		if len(result.Records) == 0 {
			if opts.Progress {
				logger.Info("No matching records found; emitting empty output",
					zap.String("file", result.File))
			}
		} else if opts.Progress {
			logger.Info("Extracted records",
				zap.String("file", result.File),
				zap.Int("record_count", len(result.Records)))
		}

		countsByRecordType[extCfg.RecordType] += len(result.Records)

		// Output records
		if opts.OutputPath == "" {
			// Output to stdout
			for _, record := range result.Records {
				if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
					logger.Error("Failed to encode record", zap.Error(err))
				}
			}
		} else {
			for _, format := range outputFormats {
				outputFile := outputFileForFormat(opts, format, result.File)
				if err := writeRecordsForFormat(outputFile, format, result.Records, extCfg, sigCfg, opts, runtimeProvenance, result.File, manifestPath); err != nil {
					logger.Error("Failed to write output file",
						zap.String("file", outputFile),
						zap.Error(err))
					return fmt.Errorf("failed to write output %s: %w", outputFile, err)
				}
				if manifestEnabled {
					manifestOutputs = append(manifestOutputs, provenanceOutput(outputFile, format, len(result.Records), opts, sanitizeRoots...))
				}
			}
		}
		failureManifest.addApplied()
	}

	if opts.ContinueOnError && failureManifest.Failed > 0 {
		failuresPath := filepath.Join(opts.OutputPath, "failures.json")
		if err := writeExtractFailureManifest(failuresPath, failureManifest); err != nil {
			return err
		}
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(filepath.Join(opts.OutputPath, "dispositions.json"), dispositionSummary); err != nil {
			return err
		}
	}

	if manifestEnabled {
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := provenance.WriteManifest(manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written", zap.String("file", manifestPath))
	} else if opts.OutputPath == "" && !opts.NoManifest {
		logger.Warn("Skipping provenance manifest because --output-path is not set")
	}

	if dispositionFailure != nil {
		return dispositionFailure
	}
	if opts.ContinueOnError && failureManifest.Failed > 0 {
		return fmt.Errorf("partial extraction failure: applied=%d failed=%d failures=%s", failureManifest.Applied, failureManifest.Failed, filepath.Join(opts.OutputPath, "failures.json"))
	}
	return nil
}

type extractFailureManifestFile struct {
	SchemaVersion string                      `json:"schema_version"`
	CohortSize    int                         `json:"cohort_size"`
	Applied       int                         `json:"applied"`
	Failed        int                         `json:"failed"`
	Failures      []extractFailureManifestRow `json:"failures"`
}

type extractFailureManifestRow struct {
	File        string `json:"file"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Detail      string `json:"detail"`
}

func newExtractFailureManifest(cohortSize int) *extractFailureManifestFile {
	return &extractFailureManifestFile{
		SchemaVersion: "extract-failures/v0.1.0",
		CohortSize:    cohortSize,
	}
}

func (m *extractFailureManifestFile) addApplied() {
	if m == nil {
		return
	}
	m.Applied++
}

func (m *extractFailureManifestFile) add(file string, reason extract.DispositionReason, detail string, roots []string) {
	if m == nil {
		return
	}
	if reason == "" {
		reason = extract.DispositionReasonInternalError
	}
	m.Failed++
	m.Failures = append(m.Failures, extractFailureManifestRow{
		File:        provenance.SanitizePath(file, roots...),
		Disposition: string(extract.DispositionFailed),
		Reason:      string(reason),
		Detail:      sanitizeDispositionText(detail, roots),
	})
}

func writeExtractFailureManifest(path string, manifest *extractFailureManifestFile) error {
	if manifest == nil || manifest.Failed == 0 {
		return nil
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal extraction failures: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create extraction failure manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write extraction failure manifest %s: %w", path, err)
	}
	return nil
}

func recoverableFailureResult(file string, err error, reason extract.DispositionReason) extract.ExtractResult {
	return extract.ExtractResult{
		File:                 file,
		Error:                err,
		SignatureMatchStatus: extract.SignatureMatchUnknown,
		Disposition:          extract.DispositionFailed,
		DispositionReason:    reason,
		DispositionDetail:    err.Error(),
	}
}

func failureErrorForResult(result extract.ExtractResult, roots []string) error {
	file := provenance.SanitizePath(result.File, roots...)
	if result.Error != nil {
		return fmt.Errorf("failed to process file %s: %w", file, result.Error)
	}
	if strings.TrimSpace(result.DispositionDetail) != "" {
		if result.DispositionReason != "" {
			return fmt.Errorf("failed to process file %s: %s: %s", file, result.DispositionReason, sanitizeDispositionText(result.DispositionDetail, roots))
		}
		return fmt.Errorf("failed to process file %s: %s", file, sanitizeDispositionText(result.DispositionDetail, roots))
	}
	return fmt.Errorf("failed to process file %s: %s", file, result.DispositionReason)
}

type dispositionSummaryFile struct {
	SchemaVersion string                  `json:"schema_version"`
	CohortSize    int                     `json:"cohort_size"`
	Applied       int                     `json:"applied"`
	NotApplicable int                     `json:"not_applicable"`
	Failed        int                     `json:"failed"`
	Files         []dispositionSummaryRow `json:"files"`
}

type dispositionSummaryRow struct {
	File              string `json:"file"`
	Disposition       string `json:"disposition"`
	DispositionReason string `json:"disposition_reason,omitempty"`
	DispositionDetail string `json:"disposition_detail,omitempty"`
}

func newDispositionSummary(cohortSize int) *dispositionSummaryFile {
	return &dispositionSummaryFile{
		SchemaVersion: "extract-dispositions/v0.1.0",
		CohortSize:    cohortSize,
	}
}

func (s *dispositionSummaryFile) add(result extract.ExtractResult, roots []string) {
	if s == nil || result.Disposition == "" {
		return
	}
	switch result.Disposition {
	case extract.DispositionApplied:
		s.Applied++
	case extract.DispositionNotApplicable:
		s.NotApplicable++
	case extract.DispositionFailed:
		s.Failed++
	}
	s.Files = append(s.Files, dispositionSummaryRow{
		File:              provenance.SanitizePath(result.File, roots...),
		Disposition:       string(result.Disposition),
		DispositionReason: string(result.DispositionReason),
		DispositionDetail: sanitizeDispositionText(result.DispositionDetail, roots),
	})
}

func applyInputDisposition(input *provenance.Input, result extract.ExtractResult, roots []string) {
	if input == nil || result.Disposition == "" {
		return
	}
	input.Disposition = string(result.Disposition)
	input.DispositionReason = string(result.DispositionReason)
	input.DispositionDetail = sanitizeDispositionText(result.DispositionDetail, roots)
}

func sanitizeDispositionText(text string, roots []string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		clean := filepath.Clean(abs)
		text = strings.ReplaceAll(text, clean, provenance.SanitizePath(clean, roots...))
	}
	return text
}

func writeDispositionSummary(path string, summary *dispositionSummaryFile) error {
	if summary == nil || len(summary.Files) == 0 {
		return nil
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dispositions summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create dispositions directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write dispositions summary %s: %w", path, err)
	}
	return nil
}

func validateApplicabilityMode(opts *ExtractOptions, files []string) error {
	if opts == nil || opts.ApplicabilityConfig == nil || !opts.AllowLargeFiles {
		return nil
	}
	const streamingThreshold = 100 * 1024 * 1024
	for _, file := range files {
		info, err := os.Stat(file)
		if err != nil {
			continue
		}
		estimatedSize := info.Size()
		if strings.HasSuffix(strings.ToLower(filepath.Ext(file)), ".gz") {
			estimatedSize *= 10
		}
		if estimatedSize > streamingThreshold {
			return fmt.Errorf("applicability declared but not supported in streaming mode")
		}
	}
	return nil
}

func shouldUseSequentialJSONStreaming(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, outputFormats []string) bool {
	if opts == nil || extCfg == nil {
		return false
	}
	if opts.RecordIndex != "" || len(outputFormats) != 1 || outputFormats[0] != recipesmanifest.OutputFormatJSON {
		return false
	}
	for _, selector := range extCfg.MatchSelectors {
		if selector.MinOccurrences > 0 {
			return false
		}
	}
	return true
}

func shouldUseParallelJSONStreaming(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, outputFormats []string) bool {
	if opts == nil || extCfg == nil {
		return false
	}
	if opts.RecordIndex == "" || len(outputFormats) != 1 || outputFormats[0] != recipesmanifest.OutputFormatJSON {
		return false
	}
	for _, selector := range extCfg.MatchSelectors {
		if selector.MinOccurrences > 0 {
			return false
		}
	}
	return true
}

func isJSONOutputFailure(err error) bool {
	return errors.Is(err, errJSONOutput)
}

func runSequentialJSONStreamingExtraction(opts *ExtractOptions, sigCfg *extract.FileSignature, extCfg *extract.ExtractRecordMatch, files []string, fieldPlan *externalFieldPlan, warnLimiter *sourceExtractionWarnLimiter, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time) error {
	logger := logging.GetLogger()
	ctx := context.Background()
	manifestEnabled := shouldWriteManifest(opts)
	manifestInputs := make([]provenance.Input, 0, len(files))
	manifestOutputs := make([]provenance.Output, 0, len(files))
	countsByRecordType := make(map[string]int)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = filepath.Join(opts.OutputPath, provenance.ManifestFileName)
	}
	dispositionSummary := newDispositionSummary(len(files))
	failureManifest := newExtractFailureManifest(len(files))
	var dispositionFailure error

	for _, file := range files {
		externalFields, err := buildExternalFieldsForFile(file, opts, fieldPlan, warnLimiter)
		if err != nil {
			if !opts.ContinueOnError {
				return fmt.Errorf("failed to build external fields for file %s: %w", file, err)
			}
			result := recoverableFailureResult(file, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
			if err := recordFailedSequentialResult(result, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots, manifestEnabled, logger); err != nil {
				return err
			}
			continue
		}

		target, err := newJSONOutputTarget(opts, file)
		if err != nil {
			return fmt.Errorf("failed to write output %s: %w", outputFileForFormat(opts, recipesmanifest.OutputFormatJSON, file), err)
		}
		result := extract.ProcessFileWithApplicabilityToSink(ctx, file, sigCfg, extCfg, opts.ApplicabilityConfig, externalFields, opts.AllowLargeFiles, runtimeProvenance, target)
		closeErr := target.Close(ctx)

		if result.Error != nil || result.Disposition == extract.DispositionFailed {
			originalDisposition := result.Disposition
			target.Abort()
			if closeErr != nil && result.Error == nil {
				result.Error = closeErr
				result.Disposition = extract.DispositionFailed
				result.DispositionReason = extract.DispositionReasonInternalError
				result.DispositionDetail = closeErr.Error()
			}
			if isJSONOutputFailure(result.Error) {
				return fmt.Errorf("failed to write output %s: %w", target.outputFile, result.Error)
			}
			if result.Error != nil {
				logger.Error("Failed to process file",
					zap.String("file", result.File),
					zap.Error(result.Error))
			}
			if !opts.ContinueOnError && originalDisposition != extract.DispositionFailed && result.Error != nil {
				return fmt.Errorf("failed to process file %s: %w", result.File, result.Error)
			}
			failureErr := recordFailedSequentialResult(result, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots, manifestEnabled, logger)
			if !opts.ContinueOnError && dispositionFailure == nil {
				dispositionFailure = failureErr
				break
			}
			continue
		}
		if closeErr != nil {
			target.Abort()
			return fmt.Errorf("failed to close output %s: %w", target.outputFile, closeErr)
		}
		if err := target.Commit(); err != nil {
			return err
		}

		if result.Disposition != "" {
			dispositionSummary.add(result, sanitizeRoots)
		}
		if manifestEnabled {
			input, err := provenance.BuildInputLedger(result.File, sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			applyInputDisposition(&input, result, sanitizeRoots)
			manifestInputs = append(manifestInputs, input)
		}

		recordCount := target.Count()
		if recordCount == 0 {
			if opts.Progress {
				logger.Info("No matching records found; emitting empty output",
					zap.String("file", result.File))
			}
		} else if opts.Progress {
			logger.Info("Extracted records",
				zap.String("file", result.File),
				zap.Int("record_count", recordCount))
		}
		countsByRecordType[extCfg.RecordType] += recordCount
		if manifestEnabled {
			manifestOutputs = append(manifestOutputs, provenanceOutput(target.outputFile, recipesmanifest.OutputFormatJSON, recordCount, opts, sanitizeRoots...))
		}
		failureManifest.addApplied()
	}

	if opts.ContinueOnError && failureManifest.Failed > 0 {
		failuresPath := filepath.Join(opts.OutputPath, "failures.json")
		if err := writeExtractFailureManifest(failuresPath, failureManifest); err != nil {
			return err
		}
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(filepath.Join(opts.OutputPath, "dispositions.json"), dispositionSummary); err != nil {
			return err
		}
	}

	if manifestEnabled {
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := provenance.WriteManifest(manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written", zap.String("file", manifestPath))
	} else if opts.OutputPath == "" && !opts.NoManifest {
		logger.Warn("Skipping provenance manifest because --output-path is not set")
	}

	if dispositionFailure != nil {
		return dispositionFailure
	}
	if opts.ContinueOnError && failureManifest.Failed > 0 {
		return fmt.Errorf("partial extraction failure: applied=%d failed=%d failures=%s", failureManifest.Applied, failureManifest.Failed, filepath.Join(opts.OutputPath, "failures.json"))
	}
	return nil
}

func recordFailedSequentialResult(result extract.ExtractResult, opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, manifestInputs *[]provenance.Input, dispositionSummary *dispositionSummaryFile, failureManifest *extractFailureManifestFile, sanitizeRoots []string, manifestEnabled bool, logger *zap.Logger) error {
	if result.Disposition == "" {
		result.Disposition = extract.DispositionFailed
	}
	if result.DispositionReason == "" {
		result.DispositionReason = failureReasonForError(result.Error)
	}
	if result.DispositionReason == "" {
		result.DispositionReason = extract.DispositionReasonInternalError
	}
	if result.DispositionDetail == "" && result.Error != nil {
		result.DispositionDetail = result.Error.Error()
	}

	dispositionSummary.add(result, sanitizeRoots)
	failureErr := failureErrorForResult(result, sanitizeRoots)
	if opts.ContinueOnError {
		failureManifest.add(result.File, result.DispositionReason, result.DispositionDetail, sanitizeRoots)
	}
	if manifestEnabled {
		input, err := provenance.BuildInputLedger(result.File, sanitizeRoots...)
		if err != nil {
			if opts.ContinueOnError {
				logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.File), zap.Error(err))
			} else {
				return err
			}
		} else {
			input.RecordType = extCfg.RecordType
			applyInputDisposition(&input, result, sanitizeRoots)
			*manifestInputs = append(*manifestInputs, input)
		}
	}
	if opts.ContinueOnError {
		return nil
	}
	return failureErr
}

type jsonOutputTarget struct {
	opts       *ExtractOptions
	inputFile  string
	outputFile string
	tempFile   string
	file       *os.File
	sink       *extract.JSONLRecordSink
	stdout     bool
	closed     bool
}

func newJSONOutputTarget(opts *ExtractOptions, inputFile string) (*jsonOutputTarget, error) {
	if opts.OutputPath == "" {
		return &jsonOutputTarget{
			opts:      opts,
			inputFile: inputFile,
			sink:      extract.NewJSONLRecordSink(os.Stdout),
			stdout:    true,
		}, nil
	}

	outputFile := outputFileForFormat(opts, recipesmanifest.OutputFormatJSON, inputFile)
	return &jsonOutputTarget{
		opts:       opts,
		inputFile:  inputFile,
		outputFile: outputFile,
	}, nil
}

func (t *jsonOutputTarget) OnRecord(ctx context.Context, record extract.EmittedRecord) error {
	if err := t.ensureOpen(); err != nil {
		return wrapJSONOutputError("open output", err)
	}
	if err := t.sink.OnRecord(ctx, record); err != nil {
		return wrapJSONOutputError("write output record", err)
	}
	return nil
}

func (t *jsonOutputTarget) OnFileBoundary(ctx context.Context, summary extract.FileEmissionSummary) error {
	if err := ctx.Err(); err != nil {
		return wrapJSONOutputError("check output boundary context", err)
	}
	if summary.Disposition == extract.DispositionFailed {
		return nil
	}
	if t.stdout {
		return nil
	}
	if err := t.ensureOpen(); err != nil {
		return wrapJSONOutputError("open output at file boundary", err)
	}
	return nil
}

func (t *jsonOutputTarget) ensureOpen() error {
	if t == nil {
		return fmt.Errorf("json output target is nil")
	}
	if t.sink != nil {
		return nil
	}

	outputDir := filepath.Dir(t.outputFile)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("create output directory %s: %w", outputDir, err)
	}
	tempFile, err := os.CreateTemp(outputDir, "."+filepath.Base(t.outputFile)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary output for %s: %w", t.outputFile, err)
	}

	t.tempFile = tempFile.Name()
	t.file = tempFile
	t.sink = extract.NewJSONLRecordSink(tempFile)
	return nil
}

func (t *jsonOutputTarget) Close(ctx context.Context) error {
	if t == nil || t.closed {
		return nil
	}
	t.closed = true
	if t.sink != nil {
		if err := t.sink.Close(ctx); err != nil {
			return wrapJSONOutputError("close output sink", err)
		}
	}
	if t.file != nil {
		if err := t.file.Close(); err != nil {
			return wrapJSONOutputError("close output file", err)
		}
	}
	return nil
}

func (t *jsonOutputTarget) Commit() error {
	if t == nil || t.stdout {
		return nil
	}
	if t.sink == nil {
		if err := t.ensureOpen(); err != nil {
			return wrapJSONOutputError("open output before commit", err)
		}
	}
	if !t.closed {
		if err := t.Close(context.Background()); err != nil {
			_ = os.Remove(t.tempFile)
			return err
		}
	}
	if err := os.Rename(t.tempFile, t.outputFile); err != nil {
		_ = os.Remove(t.tempFile)
		return wrapJSONOutputError(fmt.Sprintf("commit output %s", t.outputFile), err)
	}
	return nil
}

func (t *jsonOutputTarget) Abort() {
	if t == nil || t.stdout {
		return
	}
	_ = t.Close(context.Background())
	if t.tempFile != "" {
		_ = os.Remove(t.tempFile)
	}
}

func (t *jsonOutputTarget) Count() int {
	if t == nil || t.sink == nil {
		return 0
	}
	return t.sink.Count()
}

func wrapJSONOutputError(message string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %s: %w", errJSONOutput, message, err)
}

func buildExtractRuntimeProvenance(opts *ExtractOptions) (provenance.RuntimeOptions, error) {
	runtimeProvenance := opts.RuntimeProvenance

	runID, err := provenance.ResolveRunID(opts.RunID, os.Getenv(runIDEnvVar))
	if err != nil {
		return provenance.RuntimeOptions{}, fmt.Errorf("invalid run id: %w", err)
	}
	runtimeProvenance.RunID = runID
	runtimeProvenance.SumpterVersion = getVersionFromBuild()

	return runtimeProvenance, nil
}

func runParallelExtraction(opts *ExtractOptions, sigCfg *extract.FileSignature, extCfg *extract.ExtractRecordMatch, fieldPlan *externalFieldPlan, warnLimiter *sourceExtractionWarnLimiter, runtimeProvenance provenance.RuntimeOptions) error {
	logger := logging.GetLogger()
	startedAt := time.Now().UTC()
	logger.Info("Starting parallel extraction",
		zap.String("index", opts.RecordIndex),
		zap.Int("workers", opts.Workers))

	// Open index store to get header (avoids loading full records array)
	indexStore, err := store.Open(opts.RecordIndex)
	if err != nil {
		return fmt.Errorf("failed to open record index: %w", err)
	}
	defer func() { _ = indexStore.Close() }()

	// Get header for source path
	header, err := indexStore.Header()
	if err != nil {
		return fmt.Errorf("failed to read index header: %w", err)
	}
	externalFields, err := buildExternalFieldsForFile(header.Source.Path, opts, fieldPlan, warnLimiter)
	if err != nil {
		return fmt.Errorf("failed to build external fields for file %s: %w", header.Source.Path, err)
	}

	// Create parallel extraction options
	// Pass the already-opened store to avoid double-open
	parallelOpts := parallel.ExtractionOptions{
		IndexPath:         opts.RecordIndex,
		SourcePath:        header.Source.Path,
		IndexStore:        indexStore, // Pass pre-opened store to avoid double-open
		Workers:           opts.Workers,
		MaxRecordSizeMB:   opts.MaxRecordSizeMB,
		SkipLargeRecords:  opts.SkipLargeRecords,
		VerifyIndex:       opts.VerifyIndex,
		ExtractConfig:     extCfg,
		SignatureConfig:   sigCfg,
		ExternalFields:    externalFields,
		RuntimeProvenance: runtimeProvenance,
		ShowProgress:      opts.Progress,
	}

	outputFormats, err := effectiveOutputFormats(opts)
	if err != nil {
		return err
	}
	if shouldUseParallelJSONStreaming(opts, extCfg, outputFormats) {
		return runParallelJSONStreamingExtraction(opts, extCfg, parallelOpts, header.Source.Path, runtimeProvenance, startedAt)
	}

	// Create parallel extractor
	extractor := parallel.NewParallelExtractor(parallelOpts)

	// Extract records
	records, err := extractor.Extract()
	if err != nil {
		return fmt.Errorf("parallel extraction failed: %w", err)
	}
	perSelectorCounts, err := perSelectorCountsForIndexedExtraction(header.Selector.XPath, extCfg, header.Summary.TotalRecords)
	if err != nil {
		return err
	}
	if err := enforceMinOccurrences(opts, extCfg, sigCfg, header.Source.Path, perSelectorCounts, true, extract.SignatureMatchUnknown, 0); err != nil {
		return err
	}

	logger.Info("Parallel extraction complete", zap.Int("record_count", len(records)))

	// Output records (same as sequential path)
	manifestEnabled := shouldWriteManifest(opts)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = filepath.Join(opts.OutputPath, provenance.ManifestFileName)
	}
	var manifestInputs []provenance.Input
	var manifestOutputs []provenance.Output
	countsByRecordType := make(map[string]int)

	if opts.OutputPath == "" {
		// Output to stdout
		for _, record := range records {
			if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
				logger.Error("Failed to encode record", zap.Error(err))
			}
		}
	} else {
		countsByRecordType[extCfg.RecordType] = len(records)
		if manifestEnabled {
			input, err := provenance.BuildInputLedger(header.Source.Path, sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			manifestInputs = append(manifestInputs, input)
		}
		for _, format := range outputFormats {
			outputFile := outputFileForFormat(opts, format, "parallel")
			if err := writeRecordsForFormat(outputFile, format, records, extCfg, sigCfg, opts, runtimeProvenance, header.Source.Path, manifestPath); err != nil {
				logger.Error("Failed to write output file",
					zap.String("file", outputFile),
					zap.Error(err))
				return fmt.Errorf("failed to write output: %w", err)
			}
			logger.Info("Output written", zap.String("file", outputFile), zap.Int("records", len(records)))
			if manifestEnabled {
				manifestOutputs = append(manifestOutputs, provenanceOutput(outputFile, format, len(records), opts, sanitizeRoots...))
			}
		}
	}

	if manifestEnabled {
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := provenance.WriteManifest(manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written", zap.String("file", manifestPath))
	} else if opts.OutputPath == "" && !opts.NoManifest {
		logger.Warn("Skipping provenance manifest because --output-path is not set")
	}

	return nil
}

func runParallelJSONStreamingExtraction(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, parallelOpts parallel.ExtractionOptions, sourcePath string, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time) error {
	logger := logging.GetLogger()
	ctx := context.Background()
	manifestEnabled := shouldWriteManifest(opts)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = filepath.Join(opts.OutputPath, provenance.ManifestFileName)
	}

	target, err := newJSONOutputTarget(opts, "parallel")
	if err != nil {
		return fmt.Errorf("failed to write output %s: %w", outputFileForFormat(opts, recipesmanifest.OutputFormatJSON, "parallel"), err)
	}

	extractor := parallel.NewParallelExtractor(parallelOpts)
	summary, extractErr := extractor.ExtractToSink(ctx, target)
	closeErr := target.Close(ctx)
	if extractErr != nil {
		target.Abort()
		if closeErr != nil && !errors.Is(extractErr, errJSONOutput) {
			extractErr = fmt.Errorf("%w; failed to close output: %v", extractErr, closeErr)
		}
		if isJSONOutputFailure(extractErr) {
			return fmt.Errorf("failed to write output %s: %w", target.outputFile, extractErr)
		}
		return fmt.Errorf("parallel extraction failed: %w", extractErr)
	}
	if closeErr != nil {
		target.Abort()
		return fmt.Errorf("failed to close output %s: %w", target.outputFile, closeErr)
	}
	if err := target.Commit(); err != nil {
		return err
	}

	recordCount := target.Count()
	logger.Info("Parallel extraction complete", zap.Int("record_count", recordCount))

	if manifestEnabled {
		input, err := provenance.BuildInputLedger(sourcePath, sanitizeRoots...)
		if err != nil {
			return err
		}
		input.RecordType = extCfg.RecordType
		manifestInputs := []provenance.Input{input}
		manifestOutputs := []provenance.Output{
			provenanceOutput(target.outputFile, recipesmanifest.OutputFormatJSON, recordCount, opts, sanitizeRoots...),
		}
		countsByRecordType := map[string]int{extCfg.RecordType: recordCount}
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := provenance.WriteManifest(manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written", zap.String("file", manifestPath))
	} else if opts.OutputPath == "" && !opts.NoManifest {
		logger.Warn("Skipping provenance manifest because --output-path is not set")
	}

	if summary.RecordCount != recordCount {
		logger.Warn("Parallel sink summary count differed from output target count",
			zap.Int("summary_count", summary.RecordCount),
			zap.Int("target_count", recordCount))
	}

	return nil
}

type routedExtractError struct {
	reason  extract.DispositionReason
	message string
}

func (e routedExtractError) Error() string {
	return e.message
}

func failureReasonForError(err error) extract.DispositionReason {
	var routed routedExtractError
	if errors.As(err, &routed) {
		return routed.reason
	}
	if err == nil {
		return ""
	}
	text := err.Error()
	switch {
	case strings.Contains(text, "failed to parse XML") || strings.Contains(text, "XML syntax error"):
		return extract.DispositionReasonParseError
	case strings.Contains(text, "signature mismatch"):
		return extract.DispositionReasonSignatureMismatch
	case strings.Contains(text, "min_occurrences violation"):
		return extract.DispositionReasonMinOccurrencesViolation
	case strings.Contains(text, "validation") || strings.Contains(text, "required") || strings.Contains(text, "collides"):
		return extract.DispositionReasonValidationError
	default:
		return ""
	}
}

func enforceMinOccurrences(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, sigCfg *extract.FileSignature, sourceFile string, perSelectorCounts map[int]int, countsComplete bool, signatureMatchStatus extract.SignatureMatchStatus, signatureConfidence float64) error {
	if extCfg == nil {
		return nil
	}
	recipeID := recipeIdentifier(opts)
	hasDeclaredFloor := false
	for _, selector := range extCfg.MatchSelectors {
		if selector.MinOccurrences > 0 {
			hasDeclaredFloor = true
			break
		}
	}
	if signatureMatchStatus == extract.SignatureMatchMismatched && hasDeclaredFloor {
		threshold := 0.0
		if sigCfg != nil {
			threshold = sigCfg.ConfidenceThreshold
		}
		return routedExtractError{
			reason: extract.DispositionReasonSignatureMismatch,
			message: fmt.Sprintf("signature mismatch: recipe %q signature predicate did not match source %q (confidence=%.3f below threshold=%.3f)",
				recipeID, sourceFile, signatureConfidence, threshold),
		}
	}
	for i, selector := range extCfg.MatchSelectors {
		if selector.MinOccurrences <= 0 {
			continue
		}
		actual, ok := perSelectorCounts[i]
		if !ok {
			if !countsComplete {
				continue
			}
			actual = 0
		}
		if actual < selector.MinOccurrences {
			return routedExtractError{
				reason: extract.DispositionReasonMinOccurrencesViolation,
				message: fmt.Sprintf("min_occurrences violation: recipe %q selector %d (xpath=%q) declared min_occurrences=%d but extraction yielded %d matches against source %q",
					recipeID, i, selector.XPath, selector.MinOccurrences, actual, sourceFile),
			}
		}
	}
	return nil
}

func recipeIdentifier(opts *ExtractOptions) string {
	if opts == nil {
		return "direct extract"
	}
	if opts.Recipe != nil && strings.TrimSpace(opts.Recipe.ID) != "" {
		return opts.Recipe.ID
	}
	if strings.TrimSpace(opts.ExtractConfig) != "" {
		return opts.ExtractConfig
	}
	return "direct extract"
}

func perSelectorCountsForIndexedExtraction(indexSelector string, extCfg *extract.ExtractRecordMatch, recordCount int) (map[int]int, error) {
	counts := make(map[int]int)
	if extCfg == nil {
		return counts, nil
	}
	matchedIndex := -1
	for i, selector := range extCfg.MatchSelectors {
		if strings.TrimSpace(selector.XPath) != strings.TrimSpace(indexSelector) {
			continue
		}
		if matchedIndex != -1 {
			return nil, fmt.Errorf("min_occurrences ambiguity: record index selector %q matches multiple extract selectors; run without --record-index or build an unambiguous index", indexSelector)
		}
		matchedIndex = i
	}
	if matchedIndex == -1 {
		for _, selector := range extCfg.MatchSelectors {
			if selector.MinOccurrences > 0 {
				return nil, fmt.Errorf("min_occurrences ambiguity: record index selector %q does not match any extract selector with declared floors; run without --record-index or rebuild the index with a matching selector", indexSelector)
			}
		}
		return counts, nil
	}
	counts[matchedIndex] = recordCount
	for i, selector := range extCfg.MatchSelectors {
		if i == matchedIndex || selector.MinOccurrences <= 0 {
			continue
		}
		return nil, fmt.Errorf("min_occurrences ambiguity: record index selector %q only accounts for extract selector %d; selector %d (xpath=%q) also declares min_occurrences=%d",
			indexSelector, matchedIndex, i, selector.XPath, selector.MinOccurrences)
	}
	return counts, nil
}

func shouldWriteManifest(opts *ExtractOptions) bool {
	return opts != nil && !opts.NoManifest && opts.OutputPath != "" && !opts.DryRun
}

func effectiveOutputFormats(opts *ExtractOptions) ([]string, error) {
	if opts == nil {
		return []string{recipesmanifest.OutputFormatJSON}, nil
	}
	rawFormats := opts.Formats
	if len(rawFormats) == 0 {
		rawFormats = []string{opts.Format}
	}
	if len(rawFormats) == 0 || strings.TrimSpace(rawFormats[0]) == "" {
		rawFormats = []string{recipesmanifest.OutputFormatJSON}
	}

	formats := make([]string, 0, len(rawFormats))
	seen := make(map[string]struct{}, len(rawFormats))
	for _, raw := range rawFormats {
		normalized, err := recipesmanifest.NormalizeOutputFormat(raw)
		if err != nil {
			return nil, err
		}
		effective := recipesmanifest.EffectiveOutputFormat(normalized)
		if _, ok := seen[effective]; ok {
			return nil, fmt.Errorf("duplicate effective output format %q", effective)
		}
		seen[effective] = struct{}{}
		formats = append(formats, effective)
	}
	return formats, nil
}

func hasOutputFormat(formats []string, target string) bool {
	for _, format := range formats {
		if format == target {
			return true
		}
	}
	return false
}

func validateParquetWithholdColumns(withholdColumns []string, outputSchema map[string]interface{}) error {
	if len(withholdColumns) == 0 {
		return nil
	}
	properties, _ := outputSchema["properties"].(map[string]interface{})
	missing := make([]string, 0)
	seen := make(map[string]struct{}, len(withholdColumns))
	for _, column := range withholdColumns {
		column = strings.TrimSpace(column)
		if _, ok := seen[column]; ok {
			continue
		}
		seen[column] = struct{}{}
		if _, ok := properties[column]; !ok {
			missing = append(missing, column)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("defaults.output.parquet.withhold_columns must be a subset of output_schema.properties; missing: %s", strings.Join(missing, ", "))
	}
	return nil
}

func provenanceOutput(outputFile, format string, recordCount int, opts *ExtractOptions, sanitizeRoots ...string) provenance.Output {
	output := provenance.Output{
		Path:        provenance.SanitizePath(outputFile, sanitizeRoots...),
		Format:      format,
		RecordCount: recordCount,
	}
	if format == recipesmanifest.OutputFormatParquet && opts != nil && len(opts.ParquetWithholdColumns) > 0 {
		output.WithholdColumns = append([]string(nil), opts.ParquetWithholdColumns...)
	}
	return output
}

func outputFileForFormat(opts *ExtractOptions, format, inputFile string) string {
	base := filepath.Base(inputFile)
	pattern := opts.OutputPattern
	if len(opts.OutputPatterns) > 0 {
		if exact := opts.OutputPatterns[format]; exact != "" {
			pattern = exact
		} else if format == recipesmanifest.OutputFormatJSON {
			if alias := opts.OutputPatterns[recipesmanifest.OutputFormatJSON]; alias != "" {
				pattern = alias
			} else if alias := opts.OutputPatterns[recipesmanifest.OutputFormatNDJSON]; alias != "" {
				pattern = alias
			}
		}
	}
	if pattern == "" {
		pattern = "extract-{}.json"
	}

	filename := strings.ReplaceAll(pattern, "{}", base)
	if format == recipesmanifest.OutputFormatParquet {
		filename = replaceOutputExtension(filename, ".parquet")
	}
	return filepath.Join(opts.OutputPath, filename)
}

func replaceOutputExtension(filename, ext string) string {
	current := filepath.Ext(filename)
	if current == "" {
		return filename + ext
	}
	return strings.TrimSuffix(filename, current) + ext
}

func writeRecordsForFormat(outputFile, format string, records []map[string]interface{}, extCfg *extract.ExtractRecordMatch, sigCfg *extract.FileSignature, opts *ExtractOptions, runtime provenance.RuntimeOptions, sourceFile, manifestPath string) error {
	switch format {
	case recipesmanifest.OutputFormatJSON:
		return writeRecordsToFile(outputFile, records)
	case recipesmanifest.OutputFormatParquet:
		if opts == nil || opts.Recipe == nil {
			logging.GetLogger().Warn("Parquet output without recipe.yaml; recipe provenance metadata will be omitted",
				zap.String("file", outputFile))
		}
		metadata := parquetFileMetadata(sigCfg, opts, runtime, sourceFile, manifestPath)
		return parquetwriter.WriteFile(outputFile, extCfg, records, parquetwriter.Options{
			Compression:     opts.ParquetCompression,
			Metadata:        metadata,
			WithholdColumns: opts.ParquetWithholdColumns,
		})
	default:
		return fmt.Errorf("unsupported output format %q", format)
	}
}

func parquetFileMetadata(sigCfg *extract.FileSignature, opts *ExtractOptions, runtime provenance.RuntimeOptions, sourceFile, manifestPath string) map[string]string {
	metadata := map[string]string{
		"sumpter.run_id":              runtime.RunID,
		"sumpter.sumpter_version":     runtime.SumpterVersion,
		"sumpter.generated_at":        time.Now().UTC().Format(time.RFC3339),
		"sumpter.source_file":         sourceFile,
		"sumpter.recipe_version":      runtime.RecipeVersion,
		"sumpter.recipe_content_hash": runtime.RecipeContentHash,
		"sumpter.manifest_uri":        manifestPath,
	}
	if opts != nil && opts.Recipe != nil {
		metadata["sumpter.recipe_id"] = opts.Recipe.ID
	}
	if sigCfg != nil {
		metadata["sumpter.signature_id"] = sigCfg.SignatureID
	}
	return metadata
}

func buildProvenanceManifest(opts *ExtractOptions, runtimeProvenance provenance.RuntimeOptions, startedAt, completedAt time.Time, inputs []provenance.Input, outputs []provenance.Output, counts map[string]int, roots []string) provenance.Manifest {
	commandName := opts.CommandName
	if commandName == "" {
		commandName = "sumpter extract files"
	}
	argv := opts.Argv
	if len(argv) == 0 {
		argv = buildExtractArgv(opts)
	}
	return provenance.Manifest{
		SchemaVersion:      provenance.ManifestSchemaVersion,
		RunID:              runtimeProvenance.RunID,
		SumpterVersion:     runtimeProvenance.SumpterVersion,
		StartedAt:          startedAt,
		CompletedAt:        completedAt,
		CLI:                provenance.CLI{Command: commandName, ArgvSanitized: provenance.SanitizeArgv(argv, roots...)},
		Recipe:             opts.Recipe,
		Inputs:             inputs,
		Outputs:            outputs,
		CountsByRecordType: counts,
	}
}

func manifestSanitizeRoots(opts *ExtractOptions) []string {
	var roots []string
	if opts == nil {
		return roots
	}
	for _, root := range []string{opts.OutputPath, opts.InputPath} {
		if root != "" {
			roots = append(roots, root)
		}
	}
	if opts.Files != "" {
		for _, file := range strings.Split(opts.Files, ",") {
			file = strings.TrimSpace(file)
			if file == "" {
				continue
			}
			roots = append(roots, filepath.Dir(file))
		}
	}
	return roots
}

func buildExtractArgv(opts *ExtractOptions) []string {
	args := []string{"extract", "files"}
	if opts == nil {
		return args
	}
	appendFlag := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name+"="+value)
		}
	}
	appendFlag("--files", opts.Files)
	appendFlag("--input-path", opts.InputPath)
	appendFlag("--include-pattern", opts.IncludePattern)
	appendFlag("--exclude-pattern", opts.ExcludePattern)
	if len(opts.Formats) > 1 {
		appendFlag("--formats", strings.Join(opts.Formats, ","))
	} else {
		appendFlag("--format", opts.Format)
	}
	appendFlag("--output-path", opts.OutputPath)
	appendFlag("--output-pattern", opts.OutputPattern)
	appendFlag("--signature-config-path", opts.SignatureConfig)
	appendFlag("--extract-config-path", opts.ExtractConfig)
	appendFlag("--record-index", opts.RecordIndex)
	appendFlag("--run-id", opts.RunID)
	for _, parameter := range opts.Parameters {
		appendFlag("--parameter", parameter)
	}
	if opts.ContinueOnError {
		args = append(args, "--continue-on-error")
	}
	if opts.NoManifest {
		args = append(args, "--no-manifest")
	}
	return args
}

type externalFieldPlan struct {
	shimFields         map[string]string
	manifestParameters map[string]string
	cliParameters      map[string]string
	parametersRequired []string
}

func buildExternalFields(opts *ExtractOptions, mappings []extract.FieldMapping) (map[string]interface{}, error) {
	plan, err := buildExternalFieldPlan(opts, mappings)
	if err != nil {
		return nil, err
	}
	return plan.build(nil), nil
}

func buildExternalFieldPlan(opts *ExtractOptions, mappings []extract.FieldMapping) (*externalFieldPlan, error) {
	plan := &externalFieldPlan{
		shimFields:         make(map[string]string),
		manifestParameters: make(map[string]string),
		cliParameters:      make(map[string]string),
	}
	if opts == nil {
		return plan, nil
	}

	if opts.ClientID != "" {
		plan.shimFields["client_id"] = opts.ClientID
	}
	if opts.SiteID != "" {
		plan.shimFields["site_id"] = opts.SiteID
	}

	for key, value := range opts.ManifestParameters {
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("manifest parameter key cannot be empty")
		}
		plan.manifestParameters[key] = value
	}

	for _, raw := range opts.Parameters {
		key, value, ok := strings.Cut(raw, "=")
		if !ok {
			return nil, fmt.Errorf("invalid --parameter %q: expected key=value", raw)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("invalid --parameter %q: key cannot be empty", raw)
		}
		plan.cliParameters[key] = value
	}
	plan.parametersRequired = append(plan.parametersRequired, opts.ParametersRequired...)

	externalFields := plan.build(nil)
	for key := range externalFields {
		if isFieldMappingOutput(key, mappings) {
			return nil, fmt.Errorf(
				"parameter key %q collides with field_mappings output_field; "+
					"rename one of them to keep injection vs content-extraction fidelity explicit",
				key,
			)
		}
	}

	for _, required := range plan.parametersRequired {
		required = strings.TrimSpace(required)
		if required == "" {
			return nil, fmt.Errorf("required parameter key cannot be empty")
		}
		value, ok := externalFields[required]
		if !ok || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return nil, fmt.Errorf("required parameter %q not provided (neither defaults.parameters nor --parameter %s=...)", required, required)
		}
	}

	return plan, nil
}

func (p *externalFieldPlan) build(sourceFields map[string]string) map[string]interface{} {
	externalFields := make(map[string]interface{})
	if p == nil {
		return externalFields
	}
	for key, value := range p.shimFields {
		externalFields[key] = value
	}
	for key, value := range sourceFields {
		externalFields[key] = value
	}
	for key, value := range p.manifestParameters {
		externalFields[key] = value
	}
	for key, value := range p.cliParameters {
		externalFields[key] = value
	}
	return externalFields
}

func validateSourceExtractionDeclarations(opts *ExtractOptions, mappings []extract.FieldMapping) error {
	if opts == nil || len(opts.SourceExtraction) == 0 {
		return nil
	}

	captureNames := make(map[string]struct{})
	for index, pattern := range opts.SourceExtraction {
		if pattern.CompiledPattern == nil {
			return fmt.Errorf("source_extraction pattern at index %d is not compiled", index)
		}
		for _, name := range pattern.CompiledPattern.SubexpNames() {
			if strings.TrimSpace(name) == "" {
				continue
			}
			captureNames[name] = struct{}{}
		}
	}

	for name := range captureNames {
		if isFieldMappingOutput(name, mappings) {
			return fmt.Errorf(
				"source_extraction capture %q collides with field_mappings output_field; "+
					"rename one of them to keep source-derived injection vs content extraction explicit",
				name,
			)
		}
		if _, ok := opts.ManifestParameters[name]; ok {
			return fmt.Errorf(
				"source_extraction capture %q collides with defaults.parameters; "+
					"rename one of them or use CLI --parameter %s=... as the explicit run-time override",
				name,
				name,
			)
		}
	}

	return nil
}

func buildExternalFieldsForFile(filePath string, opts *ExtractOptions, plan *externalFieldPlan, limiter *sourceExtractionWarnLimiter) (map[string]interface{}, error) {
	sourceFields, err := extractSourceFieldsForFile(filePath, opts, limiter)
	if err != nil {
		return nil, err
	}
	return plan.build(sourceFields), nil
}

func extractSourceFieldsForFile(filePath string, opts *ExtractOptions, limiter *sourceExtractionWarnLimiter) (map[string]string, error) {
	sourceFields := make(map[string]string)
	if opts == nil || len(opts.SourceExtraction) == 0 {
		return sourceFields, nil
	}

	input := sourceExtractionInput(opts)
	for index, pattern := range opts.SourceExtraction {
		if pattern.CompiledPattern == nil {
			return nil, fmt.Errorf("source_extraction pattern at index %d is not compiled", index)
		}
		target, err := sourceExtractionTarget(filePath, pattern.Source, input)
		if err != nil {
			return nil, err
		}
		matches := pattern.CompiledPattern.FindStringSubmatch(target)
		if matches == nil {
			limiter.warn(filePath, pattern, index, opts.SourceExtractionRecipeID)
			continue
		}
		for groupIndex, name := range pattern.CompiledPattern.SubexpNames() {
			if name == "" || groupIndex >= len(matches) {
				continue
			}
			sourceFields[name] = matches[groupIndex]
		}
	}

	for _, required := range opts.SourceExtractionRequired {
		required = strings.TrimSpace(required)
		if required == "" {
			return nil, fmt.Errorf("required source_extraction key cannot be empty")
		}
		value, ok := sourceFields[required]
		if !ok || strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("required source_extraction field %q not provided for file %s", required, filePath)
		}
	}

	return sourceFields, nil
}

func sourceExtractionInput(opts *ExtractOptions) recipesmanifest.InputDefaults {
	input := opts.SourceExtractionInput
	if input.Path == "" && opts.InputPath != "" {
		input.Path = opts.InputPath
		input.FollowSymlinks = opts.FollowSymlinks
	}
	return input
}

func sourceExtractionTarget(filePath, sourceType string, input recipesmanifest.InputDefaults) (string, error) {
	switch sourceType {
	case recipesmanifest.SourceExtractionFilename:
		return filepath.Base(filePath), nil
	case recipesmanifest.SourceExtractionAbsolutePath:
		absFile, err := filepath.Abs(filePath)
		if err != nil {
			return "", fmt.Errorf("failed to resolve absolute source path %s: %w", filePath, err)
		}
		return filepath.ToSlash(filepath.Clean(absFile)), nil
	case recipesmanifest.SourceExtractionRelativePath:
		return resolveRelativeSourcePath(input.Path, filePath, input.FollowSymlinks)
	default:
		return "", fmt.Errorf("unsupported source_extraction source %q", sourceType)
	}
}

func resolveRelativeSourcePath(root, filePath string, followSymlinks bool) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("relative_path requires --input-path; got single --files mode; use 'absolute_path' or 'filename' instead")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source_extraction root %s: %w", root, err)
	}
	absFile, err := filepath.Abs(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source_extraction file %s: %w", filePath, err)
	}
	absRoot = filepath.Clean(absRoot)
	absFile = filepath.Clean(absFile)

	if followSymlinks {
		resolvedRoot, err := filepath.EvalSymlinks(absRoot)
		if err != nil {
			return "", fmt.Errorf("failed to resolve source_extraction root symlinks %s: %w", absRoot, err)
		}
		resolvedFile, err := filepath.EvalSymlinks(absFile)
		if err != nil {
			return "", fmt.Errorf("failed to resolve source_extraction file symlinks %s: %w", absFile, err)
		}
		absRoot = filepath.Clean(resolvedRoot)
		absFile = filepath.Clean(resolvedFile)
	}

	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return "", fmt.Errorf("failed to resolve source_extraction relative path from %s to %s: %w", absRoot, absFile, err)
	}
	if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("source_extraction relative_path file %s escapes input root %s", absFile, absRoot)
	}
	return filepath.ToSlash(rel), nil
}

type sourceExtractionWarnLimiter struct {
	limit      int
	emitted    int
	summarized bool
}

func newSourceExtractionWarnLimiter(limit int) *sourceExtractionWarnLimiter {
	return &sourceExtractionWarnLimiter{limit: limit}
}

func (l *sourceExtractionWarnLimiter) warn(filePath string, pattern recipesmanifest.SourceExtractionPattern, index int, recipeID string) {
	if l == nil {
		return
	}
	if l.limit <= 0 || l.emitted < l.limit {
		l.emitted++
		logging.Warn("source_extraction pattern did not match",
			zap.String("file", filePath),
			zap.String("pattern_id", pattern.ID),
			zap.Int("pattern_index", index),
			zap.String("source_type", pattern.Source),
			zap.String("recipe_id", recipeID))
		return
	}
	if !l.summarized {
		l.summarized = true
		logging.Warn("source_extraction non-match warning cap reached",
			zap.Int("limit", l.limit),
			zap.String("recipe_id", recipeID))
	}
}

func isFieldMappingOutput(key string, mappings []extract.FieldMapping) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	for _, mapping := range mappings {
		if strings.TrimSpace(mapping.OutputField) == key && (strings.TrimSpace(mapping.XPath) != "" || strings.TrimSpace(mapping.Expression) != "") {
			return true
		}
	}
	return false
}

func buildFieldProvenance(mappings []extract.FieldMapping) []provenance.FieldProvenance {
	var fields []provenance.FieldProvenance
	var walk func([]extract.FieldMapping)
	walk = func(items []extract.FieldMapping) {
		for _, mapping := range items {
			if strings.TrimSpace(mapping.OutputField) != "" && (strings.TrimSpace(mapping.XPath) != "" || strings.TrimSpace(mapping.Expression) != "") {
				fields = append(fields, provenance.FieldProvenance{
					OutputField: mapping.OutputField,
					XPath:       mapping.XPath,
					Expression:  mapping.Expression,
					Type:        mapping.Type,
					Description: mapping.Description,
					Transform:   mapping.Transform,
				})
			}
			walk(mapping.ItemMapping)
			for _, poly := range mapping.Polymorphic {
				walk(poly.FieldMappings)
			}
		}
	}
	walk(mappings)
	return fields
}

func discoverInputFiles(opts *ExtractOptions) ([]string, error) {
	absInput, err := filepath.Abs(opts.InputPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve input path %s: %w", opts.InputPath, err)
	}

	facade := pathfinder.NewFinderFacade(pathfinder.NewPathFinder(), pathfinder.FinderConfig{
		MaxWorkers: opts.Workers,
	})

	query := pathfinder.FindQuery{
		Root:           absInput,
		MaxDepth:       opts.MaxDepth,
		FollowSymlinks: opts.FollowSymlinks,
		Workers:        opts.Workers,
	}

	if include := strings.TrimSpace(opts.IncludePattern); include != "" {
		query.Include = []string{include}
	}

	if exclude := strings.TrimSpace(opts.ExcludePattern); exclude != "" {
		query.Exclude = []string{exclude}
	}

	results, err := facade.Find(query)
	if err != nil {
		return nil, fmt.Errorf("failed to discover files from %s: %w", absInput, err)
	}

	files := make([]string, 0, len(results))
	for _, result := range results {
		files = append(files, filepath.Clean(filepath.FromSlash(result.SourcePath)))
	}

	return files, nil
}

func writeRecordsToFile(filename string, records []map[string]interface{}) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
		return err
	}
	file, err := os.Create(filename) // #nosec G304 - filename is constructed from user-provided output path and pattern
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close() // Ignore close error in defer
	}()

	for _, record := range records {
		if err := json.NewEncoder(file).Encode(record); err != nil {
			return err
		}
	}

	return nil
}

func runExtractTransformsList() error {
	registry := transforms.NewTransformRegistry()
	categories := registry.ListByCategory()

	for category, transformNames := range categories {
		fmt.Printf("Category: %s\n", category)
		for _, name := range transformNames {
			transform, _ := registry.Get(name)
			fmt.Printf("  %s: %s\n", name, transform.Description)
		}
		fmt.Println()
	}

	return nil
}

func runExtractTransformsDescribe(transformName string) error {
	registry := transforms.NewTransformRegistry()
	transform, err := registry.Describe(transformName)
	if err != nil {
		return fmt.Errorf("transform '%s' not found: %w", transformName, err)
	}

	fmt.Printf("Name: %s\n", transform.Name)
	fmt.Printf("Category: %s\n", transform.Category)
	fmt.Printf("Description: %s\n", transform.Description)

	return nil
}
