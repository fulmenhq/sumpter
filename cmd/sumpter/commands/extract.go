package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fulmenhq/goneat/pkg/pathfinder"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/extract/parallel"
	"github.com/fulmenhq/sumpter/internal/extract/transforms"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const runIDEnvVar = "SUMPTER_RUN_ID"

type ExtractOptions struct {
	Files           string
	InputPath       string
	IncludePattern  string
	ExcludePattern  string
	MaxDepth        int
	FollowSymlinks  bool
	Workers         int
	DryRun          bool
	Progress        bool
	Format          string
	OutputPath      string
	OutputPattern   string
	SignatureConfig string
	ExtractConfig   string
	ClientID        string
	SiteID          string
	RunID           string
	NoManifest      bool
	AllowLargeFiles bool
	CommandName     string
	Argv            []string
	Recipe          *provenance.Recipe
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
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "json", "Output format")
	cmd.Flags().StringVarP(&opts.OutputPath, "output-path", "o", "", "Output destination path")
	cmd.Flags().StringVar(&opts.OutputPattern, "output-pattern", "extract-{}.json", "Output filename pattern for files mode (use {} for input filename)")
	cmd.Flags().StringVar(&opts.SignatureConfig, "signature-config-path", "", "Path to signature configuration YAML file")
	cmd.Flags().StringVar(&opts.ExtractConfig, "extract-config-path", "", "Path to extract configuration YAML file")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "Client ID to blend into extracted records")
	cmd.Flags().StringVar(&opts.SiteID, "site-id", "", "Site ID to blend into extracted records")
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
	logger.Debug("Extract config loaded", zap.String("record_type", extCfg.RecordType))
	if opts.Recipe != nil && len(opts.Recipe.FieldProvenance) == 0 {
		opts.Recipe.FieldProvenance = buildFieldProvenance(extCfg.FieldMappings)
	}

	runtimeProvenance, err := buildExtractRuntimeProvenance(opts)
	if err != nil {
		return err
	}

	// Prepare external fields
	externalFields := make(map[string]interface{})
	if opts.ClientID != "" {
		externalFields["client_id"] = opts.ClientID
	}
	if opts.SiteID != "" {
		externalFields["site_id"] = opts.SiteID
	}

	// Route to parallel extraction if --record-index is provided
	if opts.RecordIndex != "" {
		logger.Info("Parallel extraction mode enabled", zap.String("record_index", opts.RecordIndex))
		return runParallelExtraction(opts, sigCfg, extCfg, externalFields, runtimeProvenance)
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

	// Process files
	results := make(chan extract.ExtractResult, len(files))
	manifestEnabled := shouldWriteManifest(opts)
	manifestInputs := make([]provenance.Input, 0, len(files))
	manifestOutputs := make([]provenance.Output, 0, len(files))
	countsByRecordType := make(map[string]int)
	sanitizeRoots := manifestSanitizeRoots(opts)

	// For now, serial processing
	for _, file := range files {
		result := extract.ProcessFileWithProvenance(file, sigCfg, extCfg, externalFields, opts.AllowLargeFiles, runtimeProvenance)
		results <- result
	}
	close(results)

	// Collect and output results
	for result := range results {
		if result.Error != nil {
			logger.Error("Failed to process file",
				zap.String("file", result.File),
				zap.Error(result.Error))
			return fmt.Errorf("failed to process file %s: %w", result.File, result.Error)
		}

		if manifestEnabled {
			input, err := provenance.BuildInputLedger(result.File, sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			manifestInputs = append(manifestInputs, input)
		}

		if len(result.Records) == 0 {
			if opts.Progress {
				logger.Info("No matching records found",
					zap.String("file", result.File))
			}
			continue
		}

		if opts.Progress {
			logger.Info("Extracted records",
				zap.String("file", result.File),
				zap.Int("record_count", len(result.Records)))
		}

		// Output records
		if opts.OutputPath == "" {
			// Output to stdout
			for _, record := range result.Records {
				if opts.Format == "json" {
					if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
						logger.Error("Failed to encode record", zap.Error(err))
					}
				} else {
					// For now, only JSON supported
					logger.Warn("Only JSON format supported, outputting JSON")
					if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
						logger.Error("Failed to encode record", zap.Error(err))
					}
				}
			}
		} else {
			// Write to file
			outputFile := filepath.Join(opts.OutputPath, strings.ReplaceAll(opts.OutputPattern, "{}", filepath.Base(result.File)))
			if err := writeRecordsToFile(outputFile, result.Records); err != nil {
				logger.Error("Failed to write output file",
					zap.String("file", outputFile),
					zap.Error(err))
				return fmt.Errorf("failed to write output %s: %w", outputFile, err)
			}
			if manifestEnabled {
				manifestOutputs = append(manifestOutputs, provenance.Output{
					Path:        provenance.SanitizePath(outputFile, sanitizeRoots...),
					Format:      effectiveOutputFormat(opts.Format),
					RecordCount: len(result.Records),
				})
				countsByRecordType[extCfg.RecordType] += len(result.Records)
			}
		}
	}

	if manifestEnabled {
		manifestPath := filepath.Join(opts.OutputPath, provenance.ManifestFileName)
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

func runParallelExtraction(opts *ExtractOptions, sigCfg *extract.FileSignature, extCfg *extract.ExtractRecordMatch, externalFields map[string]interface{}, runtimeProvenance provenance.RuntimeOptions) error {
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

	// Create parallel extractor
	extractor := parallel.NewParallelExtractor(parallelOpts)

	// Extract records
	records, err := extractor.Extract()
	if err != nil {
		return fmt.Errorf("parallel extraction failed: %w", err)
	}

	logger.Info("Parallel extraction complete", zap.Int("record_count", len(records)))

	// Output records (same as sequential path)
	manifestEnabled := shouldWriteManifest(opts)
	sanitizeRoots := manifestSanitizeRoots(opts)
	var manifestInputs []provenance.Input
	var manifestOutputs []provenance.Output
	countsByRecordType := make(map[string]int)

	if opts.OutputPath == "" {
		// Output to stdout
		for _, record := range records {
			if opts.Format == "json" {
				if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
					logger.Error("Failed to encode record", zap.Error(err))
				}
			} else {
				// For now, only JSON supported
				logger.Warn("Only JSON format supported, outputting JSON")
				if err := json.NewEncoder(os.Stdout).Encode(record); err != nil {
					logger.Error("Failed to encode record", zap.Error(err))
				}
			}
		}
	} else {
		// Write to file
		outputFile := filepath.Join(opts.OutputPath, "extract-parallel.json")
		if err := writeRecordsToFile(outputFile, records); err != nil {
			logger.Error("Failed to write output file",
				zap.String("file", outputFile),
				zap.Error(err))
			return fmt.Errorf("failed to write output: %w", err)
		}
		logger.Info("Output written", zap.String("file", outputFile), zap.Int("records", len(records)))
		if manifestEnabled {
			input, err := provenance.BuildInputLedger(header.Source.Path, sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			manifestInputs = append(manifestInputs, input)
			manifestOutputs = append(manifestOutputs, provenance.Output{
				Path:        provenance.SanitizePath(outputFile, sanitizeRoots...),
				Format:      effectiveOutputFormat(opts.Format),
				RecordCount: len(records),
			})
			countsByRecordType[extCfg.RecordType] = len(records)
		}
	}

	if manifestEnabled {
		manifestPath := filepath.Join(opts.OutputPath, provenance.ManifestFileName)
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

func shouldWriteManifest(opts *ExtractOptions) bool {
	return opts != nil && !opts.NoManifest && opts.OutputPath != "" && !opts.DryRun
}

func effectiveOutputFormat(_ string) string {
	return "json"
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
	appendFlag("--format", opts.Format)
	appendFlag("--output-path", opts.OutputPath)
	appendFlag("--output-pattern", opts.OutputPattern)
	appendFlag("--signature-config-path", opts.SignatureConfig)
	appendFlag("--extract-config-path", opts.ExtractConfig)
	appendFlag("--record-index", opts.RecordIndex)
	appendFlag("--run-id", opts.RunID)
	if opts.NoManifest {
		args = append(args, "--no-manifest")
	}
	return args
}

func buildFieldProvenance(mappings []extract.FieldMapping) []provenance.FieldProvenance {
	var fields []provenance.FieldProvenance
	var walk func([]extract.FieldMapping)
	walk = func(items []extract.FieldMapping) {
		for _, mapping := range items {
			if strings.TrimSpace(mapping.OutputField) != "" && strings.TrimSpace(mapping.XPath) != "" {
				fields = append(fields, provenance.FieldProvenance{
					OutputField: mapping.OutputField,
					XPath:       mapping.XPath,
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
