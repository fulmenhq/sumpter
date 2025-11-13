package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// NewIndexCommand creates the parent 'index' command with build and verify subcommands
func NewIndexCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Build and verify XML record indexes for seekable parallel extraction",
		Long: `Build and verify XML record indexes that enable seekable parallel extraction.

The index command creates metadata files containing record boundaries, byte offsets,
and integrity checksums (SHA-256) for large XML files. These indexes enable:

  • Seekable random access to individual records
  • Parallel extraction with worker pools
  • Integrity verification and tamper detection
  • Performance optimization for multi-GB XML files

Use 'sumpter index build' to create an index and 'sumpter index verify' to validate it.`,
	}

	cmd.AddCommand(newIndexBuildCommand())
	cmd.AddCommand(newIndexVerifyCommand())

	return cmd
}

func newIndexBuildCommand() *cobra.Command {
	var (
		outputPath string
		selector   string
		includeP50 bool
		includeP95 bool
		includeP99 bool
		progress   bool
	)

	cmd := &cobra.Command{
		Use:   "build <input-xml-file>",
		Short: "Build a record index from an XML file",
		Long: `Build a record index by streaming through an XML file and capturing:
  • Record boundary byte offsets (start/end positions)
  • Record sizes and SHA-256 checksums
  • Aggregate statistics (min/max/avg/percentiles)
  • Source file integrity metadata (SHA-256 hash)

The index is written to a JSON file conforming to record-index/v0.1.0 schema.

Memory usage: <100MB regardless of source file size (streaming architecture).

Example:
  sumpter index build clinvar.xml --selector "//VariationArchive" --output clinvar.recordindex.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logging.Component("index-build")
			inputPath := args[0]

			// Resolve absolute path
			absPath, err := filepath.Abs(inputPath)
			if err != nil {
				return fmt.Errorf("failed to resolve input path: %w", err)
			}

			// Validate input file exists
			if _, err := os.Stat(absPath); err != nil {
				return fmt.Errorf("input file not found: %w", err)
			}

			// Validate selector
			if selector == "" {
				return fmt.Errorf("--selector is required (e.g., --selector '//Record')")
			}

			// Determine output path
			finalOutputPath := outputPath
			if finalOutputPath == "" {
				// Default: ${SUMPTER_HOME}/indexes/<basename>.recordindex.json
				paths, err := config.ResolvePaths("", "")
				if err != nil {
					return fmt.Errorf("failed to resolve application paths: %w", err)
				}

				indexesDir := filepath.Join(paths.Home, "indexes")
				if err := os.MkdirAll(indexesDir, 0755); err != nil {
					return fmt.Errorf("failed to create indexes directory: %w", err)
				}

				baseName := filepath.Base(absPath)
				ext := filepath.Ext(baseName)
				nameWithoutExt := baseName[:len(baseName)-len(ext)]
				finalOutputPath = filepath.Join(indexesDir, nameWithoutExt+".recordindex.json")
			}

			logger.Info("Building record index",
				zap.String("input", absPath),
				zap.String("output", finalOutputPath),
				zap.String("selector", selector),
			)

			// Get version for metadata
			version := getVersion()

			// Build index
			buildOpts := index.BuildOptions{
				InputPath:      absPath,
				OutputPath:     finalOutputPath,
				Selector:       selector,
				IncludeP50:     includeP50,
				IncludeP95:     includeP95,
				IncludeP99:     includeP99,
				SumpterVersion: version,
			}

			builder := index.NewBuilder(buildOpts)

			if progress {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building index for %s...\n", filepath.Base(absPath))
			}

			startTime := time.Now()
			idx, err := builder.Build()
			if err != nil {
				return fmt.Errorf("failed to build index: %w", err)
			}
			buildDuration := time.Since(startTime)

			// Write index to file
			if err := builder.WriteToFile(idx, finalOutputPath); err != nil {
				return fmt.Errorf("failed to write index: %w", err)
			}

			// Output summary
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Index created: %s\n", finalOutputPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Records: %d\n", idx.Summary.TotalRecords)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Total size: %.2f MB\n", float64(idx.Summary.TotalBytes)/(1024*1024))
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Avg record: %.2f KB\n", idx.Summary.AvgRecordSizeBytes/1024)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Min/Max: %.2f KB / %.2f KB\n",
				float64(idx.Summary.MinRecordSizeBytes)/1024,
				float64(idx.Summary.MaxRecordSizeBytes)/1024,
			)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Build time: %s\n", buildDuration.Round(time.Millisecond))

			logger.Info("Index build completed",
				zap.Int("records", idx.Summary.TotalRecords),
				zap.Int64("total_bytes", idx.Summary.TotalBytes),
				zap.Duration("duration", buildDuration),
			)

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output path for index file (default: $SUMPTER_HOME/indexes/<input-basename>.recordindex.json)")
	cmd.Flags().StringVarP(&selector, "selector", "s", "", "XPath selector for record boundaries (required, e.g., '//Record')")
	cmd.Flags().BoolVar(&includeP50, "p50", true, "Include p50 (median) in summary statistics")
	cmd.Flags().BoolVar(&includeP95, "p95", true, "Include p95 in summary statistics")
	cmd.Flags().BoolVar(&includeP99, "p99", true, "Include p99 in summary statistics")
	cmd.Flags().BoolVarP(&progress, "progress", "p", true, "Show progress messages")

	return cmd
}

func newIndexVerifyCommand() *cobra.Command {
	var (
		indexPath     string
		verifyRecords bool
		failFast      bool
		progress      bool
	)

	cmd := &cobra.Command{
		Use:   "verify <input-xml-file>",
		Short: "Verify a record index against its source XML file",
		Long: `Verify index integrity by checking:
  • Source file size matches index metadata
  • Source file SHA-256 matches index metadata
  • (Optional) Individual record SHA-256 hashes match

Verification detects:
  • Source file tampering or modification
  • Index file corruption
  • Mismatched index/source pairs

Example:
  sumpter index verify clinvar.xml --index clinvar.recordindex.json
  sumpter index verify clinvar.xml --index clinvar.recordindex.json --verify-records`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logging.Component("index-verify")
			inputPath := args[0]

			// Resolve absolute path
			absPath, err := filepath.Abs(inputPath)
			if err != nil {
				return fmt.Errorf("failed to resolve input path: %w", err)
			}

			// Validate input file exists
			if _, err := os.Stat(absPath); err != nil {
				return fmt.Errorf("input file not found: %w", err)
			}

			// Determine index path
			finalIndexPath := indexPath
			if finalIndexPath == "" {
				// Default: same directory as input, .recordindex.json extension
				baseName := filepath.Base(absPath)
				ext := filepath.Ext(baseName)
				nameWithoutExt := baseName[:len(baseName)-len(ext)]
				finalIndexPath = filepath.Join(filepath.Dir(absPath), nameWithoutExt+".recordindex.json")
			}

			// Validate index file exists
			if _, err := os.Stat(finalIndexPath); err != nil {
				return fmt.Errorf("index file not found: %w (use --index to specify location)", err)
			}

			logger.Info("Verifying record index",
				zap.String("input", absPath),
				zap.String("index", finalIndexPath),
				zap.Bool("verify_records", verifyRecords),
			)

			if progress {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Verifying index for %s...\n", filepath.Base(absPath))
				if verifyRecords {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  (with per-record verification - this may take a while)\n")
				}
			}

			// Verify index
			verifyOpts := index.VerifyOptions{
				InputPath:     absPath,
				IndexPath:     finalIndexPath,
				VerifyRecords: verifyRecords,
				FailFast:      failFast,
			}

			verifier := index.NewVerifier(verifyOpts)
			result, err := verifier.Verify()
			if err != nil {
				return fmt.Errorf("verification error: %w", err)
			}

			// Output results
			if result.Valid {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Index verification passed\n")
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Source size: match\n")
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Source hash: match\n")
				if verifyRecords {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Records verified: %d\n", result.RecordsVerified)
				}

				logger.Info("Index verification passed",
					zap.Bool("source_size_match", result.SourceSizeMatch),
					zap.Bool("source_hash_match", result.SourceHashMatch),
					zap.Int("records_verified", result.RecordsVerified),
				)
			} else {
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "✗ Index verification failed\n")
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  Source size: %v\n", formatBool(result.SourceSizeMatch))
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  Source hash: %v\n", formatBool(result.SourceHashMatch))
				if verifyRecords {
					_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  Records verified: %d\n", result.RecordsVerified)
					if len(result.RecordErrors) > 0 {
						_, _ = fmt.Fprintf(cmd.OutOrStderr(), "  Record errors: %d\n", len(result.RecordErrors))
						// Show first few errors
						maxErrors := 5
						for i, errMsg := range result.RecordErrors {
							if i >= maxErrors {
								_, _ = fmt.Fprintf(cmd.OutOrStderr(), "    ... and %d more errors\n", len(result.RecordErrors)-maxErrors)
								break
							}
							_, _ = fmt.Fprintf(cmd.OutOrStderr(), "    - %s\n", errMsg)
						}
					}
				}
				_, _ = fmt.Fprintf(cmd.OutOrStderr(), "\n  Error: %s\n", result.ErrorMessage)

				logger.Error("Index verification failed",
					zap.String("error", result.ErrorMessage),
					zap.Bool("source_size_match", result.SourceSizeMatch),
					zap.Bool("source_hash_match", result.SourceHashMatch),
					zap.Int("record_errors", len(result.RecordErrors)),
				)

				return fmt.Errorf("index verification failed: %s", result.ErrorMessage)
			}

			return nil
		},
	}

	cmd.Flags().StringVarP(&indexPath, "index", "i", "", "Path to index file (default: <input-basename>.recordindex.json in same directory)")
	cmd.Flags().BoolVar(&verifyRecords, "verify-records", false, "Verify individual record checksums (slower, more thorough)")
	cmd.Flags().BoolVar(&failFast, "fail-fast", false, "Stop on first verification error (only with --verify-records)")
	cmd.Flags().BoolVarP(&progress, "progress", "p", true, "Show progress messages")

	return cmd
}

// formatBool formats a boolean as "match" or "MISMATCH"
func formatBool(b bool) string {
	if b {
		return "match"
	}
	return "MISMATCH"
}
