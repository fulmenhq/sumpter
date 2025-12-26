package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
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
	cmd.AddCommand(newIndexStreamCommand())

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

func newIndexStreamCommand() *cobra.Command {
	var (
		progressEvery int
		limit         int
		verifySummary bool
	)

	cmd := &cobra.Command{
		Use:   "stream <index-json-file>",
		Short: "Stream-walk a record index without loading it fully",
		Long: `Stream-walk a record index by iterating records one at a time.

This command is intended for dogfooding at extreme scale (multi-GB JSON indexes),
where loading the full records array into memory is undesirable.

For RSS-level memory measurements, run with your OS time tool, e.g.:
  /usr/bin/time -l sumpter index stream path/to/index.recordindex.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logging.Component("index-stream")
			indexPath := args[0]

			absPath, err := filepath.Abs(indexPath)
			if err != nil {
				return fmt.Errorf("failed to resolve index path: %w", err)
			}

			if _, err := os.Stat(absPath); err != nil {
				return fmt.Errorf("index file not found: %w", err)
			}

			stream, err := index.OpenRecordIndexStream(absPath)
			if err != nil {
				return err
			}
			defer func() { _ = stream.Close() }()

			var memStart runtime.MemStats
			runtime.ReadMemStats(&memStart)

			start := time.Now()
			header, err := stream.Header()
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", absPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Schema: %s\n", header.Version)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\n", header.Source.Path)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Selector: %s\n", header.Selector.XPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\n")

			var (
				count      int
				totalBytes int64
				minSize    int64
				maxSize    int64
				lastNum    int
			)

			for limit == 0 || count < limit {
				rec, err := stream.NextRecord()
				if err == io.EOF {
					break
				}
				if err != nil {
					return err
				}

				count++
				totalBytes += rec.SizeBytes

				if minSize == 0 || rec.SizeBytes < minSize {
					minSize = rec.SizeBytes
				}
				if rec.SizeBytes > maxSize {
					maxSize = rec.SizeBytes
				}

				if rec.RecordNum != 0 {
					if lastNum != 0 && rec.RecordNum != lastNum+1 {
						return fmt.Errorf("non-contiguous record numbers: got %d after %d", rec.RecordNum, lastNum)
					}
					lastNum = rec.RecordNum
				}

				if progressEvery > 0 && count%progressEvery == 0 {
					elapsed := time.Since(start)
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "... scanned %d records in %s\n", count, elapsed.Round(time.Second))
				}
			}

			elapsed := time.Since(start)
			var memEnd runtime.MemStats
			runtime.ReadMemStats(&memEnd)

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Scanned records: %d\n", count)
			if count > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Computed sizes: min=%0.2f KB max=%0.2f KB avg=%0.2f KB\n",
					float64(minSize)/1024,
					float64(maxSize)/1024,
					float64(totalBytes)/float64(count)/1024,
				)
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Elapsed: %s\n", elapsed.Round(time.Millisecond))
			if elapsed > 0 {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Throughput: %0.0f records/sec\n", float64(count)/elapsed.Seconds())
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nHeap (start): alloc=%0.2f MB sys=%0.2f MB num_gc=%d\n",
				float64(memStart.HeapAlloc)/(1024*1024),
				float64(memStart.HeapSys)/(1024*1024),
				memStart.NumGC,
			)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Heap (end):   alloc=%0.2f MB sys=%0.2f MB num_gc=%d\n",
				float64(memEnd.HeapAlloc)/(1024*1024),
				float64(memEnd.HeapSys)/(1024*1024),
				memEnd.NumGC,
			)

			if verifySummary {
				if limit > 0 {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSummary verification: skipped (limit enabled)\n")
				} else {
					if header.Summary.TotalRecords != count {
						return fmt.Errorf("summary mismatch: header total_records=%d, scanned=%d", header.Summary.TotalRecords, count)
					}
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\nSummary verification: ok\n")
				}
			}

			logger.Info("Index stream scan completed",
				zap.String("index", absPath),
				zap.Int("records_scanned", count),
				zap.Duration("duration", elapsed),
				zap.Int("limit", limit),
			)

			return nil
		},
	}

	cmd.Flags().IntVar(&progressEvery, "progress-every", 250000, "Print progress every N records (0 disables)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Stop after scanning N records (0 scans all)")
	cmd.Flags().BoolVar(&verifySummary, "verify-summary", true, "Verify scanned record count matches index summary (only when --limit=0)")

	return cmd
}
