package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/goneat/pkg/pathfinder"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

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
}

func NewExtractCommand() *cobra.Command {
	opts := &ExtractOptions{}

	cmd := &cobra.Command{
		Use:   "extract",
		Short: "Extract structured data from XML files using signature and extract configurations",
		Long: `Extract structured records from XML files based on user-provided signature and extract configurations.

The command supports both direct file specification and directory scanning with glob patterns.
Files are matched against the signature configuration, and matching records are extracted
according to the extract configuration, producing structured output.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExtract(opts)
		},
	}

	cmd.Flags().StringVar(&opts.Files, "files", "", "Comma-separated list of file paths to process")
	cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Input path for processing (directory or file)")
	cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "*.xml", "File inclusion pattern")
	cmd.Flags().StringVar(&opts.ExcludePattern, "exclude-pattern", "", "File exclusion pattern")
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

	_ = cmd.MarkFlagRequired("signature-config-path")
	_ = cmd.MarkFlagRequired("extract-config-path")

	return cmd
}

func runExtract(opts *ExtractOptions) error {
	logger := logging.GetLogger()

	// Validate options
	if opts.Files == "" && opts.InputPath == "" {
		return fmt.Errorf("must specify either --files or --input-path")
	}
	if opts.Files != "" && opts.InputPath != "" {
		return fmt.Errorf("cannot specify both --files and --input-path")
	}

	// Load configurations
	sigCfg, err := extract.LoadSignatureConfig(opts.SignatureConfig)
	if err != nil {
		return fmt.Errorf("failed to load signature config: %w", err)
	}

	extCfg, err := extract.LoadExtractConfig(opts.ExtractConfig)
	if err != nil {
		return fmt.Errorf("failed to load extract config: %w", err)
	}

	// Get file list
	var files []string
	if opts.Files != "" {
		files = strings.Split(opts.Files, ",")
		for i, f := range files {
			files[i] = strings.TrimSpace(f)
		}
	} else {
		files, err = discoverInputFiles(opts)
		if err != nil {
			return fmt.Errorf("failed to find files: %w", err)
		}
	}

	// Dry run: just list files
	if opts.DryRun {
		for _, file := range files {
			fmt.Println(file)
		}
		return nil
	}

	if opts.Progress {
		logger.Info("Starting extraction",
			zap.Int("file_count", len(files)),
			zap.String("signature", sigCfg.SignatureID),
			zap.String("record_type", extCfg.RecordType))
	}

	// Prepare external fields
	externalFields := make(map[string]interface{})
	if opts.ClientID != "" {
		externalFields["client_id"] = opts.ClientID
	}
	if opts.SiteID != "" {
		externalFields["site_id"] = opts.SiteID
	}

	// Process files
	results := make(chan extract.ExtractResult, len(files))

	// For now, serial processing
	for _, file := range files {
		result := extract.ProcessFile(file, sigCfg, extCfg, externalFields)
		results <- result
	}
	close(results)

	// Collect and output results
	for result := range results {
		if result.Error != nil {
			logger.Error("Failed to process file",
				zap.String("file", result.File),
				zap.Error(result.Error))
			continue
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
			}
		}
	}

	return nil
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
