package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// resolvedSingleObjectSource is a single source (index build/verify, inspect)
// made available on the local filesystem through the uriio read boundary. For a
// cloud source it is a staged working copy; Identity carries the logical s3://
// URI (never the staging path) and cleanup removes the run's staging directory.
// For a local source LocalPath and Identity coincide and cleanup is a no-op.
type resolvedSingleObjectSource struct {
	LocalPath string // local path to read source bytes from
	Identity  string // logical identity recorded in / compared against the index
	BaseName  string // source basename for default output-path derivation
	cleanup   func() error
}

// resolveSingleObjectSource classifies a single source argument and makes it
// available locally. Cloud sources are single objects only — these commands map
// one source file, so prefixes and globs are rejected. The staged copy is
// byte-identical to the source object, so byte offsets and integrity hashes
// remain valid.
func resolveSingleObjectSource(ctx context.Context, op, inputArg, credentialsPath string, credentialOverrides []string) (*resolvedSingleObjectSource, error) {
	ref, err := uriio.Classify(inputArg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}

	if !ref.IsCloud() {
		absPath, err := filepath.Abs(ref.LocalPath)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve input path: %w", err)
		}
		if _, err := os.Stat(absPath); err != nil {
			return nil, fmt.Errorf("input file not found: %w", err)
		}
		return &resolvedSingleObjectSource{
			LocalPath: absPath,
			Identity:  absPath,
			BaseName:  filepath.Base(absPath),
			cleanup:   func() error { return nil },
		}, nil
	}

	if ref.IsPrefix() || ref.IsPattern() {
		return nil, fmt.Errorf("%s: cloud index source must be a single object, not a prefix or glob: %s", op, inputArg)
	}

	runID, err := provenance.NewRunID()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	session, err := newCloudSessionFromCredentials(credentialsPath, credentialOverrides, runID)
	if err != nil {
		return nil, err
	}
	src, err := session.Acquire(ctx, inputArg, uriio.DefaultHandleName)
	if err != nil {
		_ = session.Close()
		return nil, fmt.Errorf("%s: acquire %s: %w", op, inputArg, err)
	}
	return &resolvedSingleObjectSource{
		LocalPath: src.LocalPath,
		Identity:  src.LogicalURI,
		BaseName:  path.Base(ref.Key),
		cleanup:   session.Close,
	}, nil
}

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
		outputPath          string
		selector            string
		includeP50          bool
		includeP95          bool
		includeP99          bool
		progress            bool
		emitJSON            bool
		emitSzst            bool
		credentialsPath     string
		credentialOverrides []string
	)

	cmd := &cobra.Command{
		Use:   "build <input-xml-file>",
		Short: "Build a record index from an XML file",
		Long: `Build a record index by streaming through an XML file and capturing:
  • Record boundary byte offsets (start/end positions)
  • Record sizes and SHA-256 checksums
  • Aggregate statistics (min/max/avg; exact percentiles are opt-in)
  • Source file integrity metadata (SHA-256 hash)

Output formats:
  • JSON (default): Single *.recordindex.json file
  • Seekable-zstd: *.recordindex.header.json + *.recordindex.records.szst
    (requires CGO build with seekablezstd tag)

Default memory usage is bounded by parser state and writer buffering. Exact
percentile flags retain record sizes to calculate exact percentiles.

The source may be an S3-compatible cloud URI (s3://) using a credential handle
(--credentials/--credential); see docs/extract-workflow.md "Cloud Sources and
Outputs". The index file itself is always written locally.

Example:
  sumpter index build clinvar.xml --selector "//VariationArchive" --output clinvar.recordindex.json
  sumpter index build clinvar.xml --selector "//VariationArchive" --emit-szst --emit-json=false`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logging.Component("index-build")

			// Resolve the source through the uriio read boundary. A cloud (s3://)
			// source is staged to a local working copy that the builder reads
			// byte-for-byte; the index records the logical URI (src.Identity), and
			// the staging path never leaks into the index, output, or logs.
			src, err := resolveSingleObjectSource(cmd.Context(), "index build", args[0], credentialsPath, credentialOverrides)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := src.cleanup(); cerr != nil {
					logger.Warn("Failed to clean up cloud staging directory", zap.Error(cerr))
				}
			}()
			absPath := src.LocalPath

			// Validate selector
			if selector == "" {
				return fmt.Errorf("--selector is required (e.g., --selector '//Record')")
			}
			if err := streaming.ValidateRecordSelector(selector); err != nil {
				return err
			}

			// Validate at least one output format is enabled
			if !emitJSON && !emitSzst {
				return fmt.Errorf("at least one output format must be enabled (--emit-json or --emit-szst)")
			}

			// Determine output base path
			var basePath string
			if outputPath == "" {
				// Default: ${SUMPTER_HOME}/indexes/<basename>
				paths, err := config.ResolvePaths("", "")
				if err != nil {
					return fmt.Errorf("failed to resolve application paths: %w", err)
				}

				indexesDir := filepath.Join(paths.Home, "indexes")
				if err := os.MkdirAll(indexesDir, 0o750); err != nil {
					return fmt.Errorf("failed to create indexes directory: %w", err)
				}

				inputBaseName := src.BaseName
				ext := filepath.Ext(inputBaseName)
				nameWithoutExt := inputBaseName[:len(inputBaseName)-len(ext)]
				basePath = filepath.Join(indexesDir, nameWithoutExt)
			} else {
				// Strip common suffixes to get base path
				basePath = outputPath
				basePath = strings.TrimSuffix(basePath, ".recordindex.json")
				basePath = strings.TrimSuffix(basePath, ".recordindex.header.json")
			}

			jsonOutputPath := basePath + ".recordindex.json"

			logger.Info("Building record index",
				zap.String("input", src.Identity),
				zap.String("base_path", basePath),
				zap.String("selector", selector),
				zap.Bool("emit_json", emitJSON),
				zap.Bool("emit_szst", emitSzst),
			)

			// Get version for index metadata. Use the build-time injected version
			// (not the CWD-relative VERSION file) so a built binary stamps the correct
			// version regardless of the working directory it is run from.
			version := getVersionFromBuild()

			// Build index
			buildOpts := index.BuildOptions{
				InputPath:      absPath,
				SourceIdentity: src.Identity,
				OutputPath:     jsonOutputPath,
				Selector:       selector,
				IncludeP50:     includeP50,
				IncludeP95:     includeP95,
				IncludeP99:     includeP99,
				SumpterVersion: version,
				EmitJSON:       emitJSON,
				EmitSzst:       emitSzst,
			}

			builder := index.NewBuilder(buildOpts)

			writers := make([]index.IndexWriter, 0, 2)
			if emitJSON {
				writers = append(writers, index.NewJSONIndexWriter(jsonOutputPath))
			}
			if emitSzst {
				writers = append(writers, store.NewSeekableIndexWriter(basePath))
			}

			if progress {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Building index for %s...\n", src.BaseName)
			}

			startTime := time.Now()
			idx, err := builder.BuildTo(writers...)
			if err != nil {
				return fmt.Errorf("failed to build index: %w", err)
			}
			buildDuration := time.Since(startTime)

			if emitJSON {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ JSON index created: %s\n", jsonOutputPath)
			}

			if emitSzst {
				headerPath, recordsPath := store.DeriveSeekablePaths(basePath)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Seekable-zstd index created:\n")
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Header:  %s\n", headerPath)
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  Records: %s\n", recordsPath)
			}

			// Output summary
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
				zap.Bool("emit_json", emitJSON),
				zap.Bool("emit_szst", emitSzst),
			)

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "", "Output base path for index files (default: $SUMPTER_HOME/indexes/<input-basename>)")
	cmd.Flags().StringVarP(&selector, "selector", "s", "", "XPath selector for record boundaries (required, e.g., '//Record')")
	cmd.Flags().BoolVar(&includeP50, "p50", false, "Include exact p50 (median) in summary statistics (retains record sizes)")
	cmd.Flags().BoolVar(&includeP95, "p95", false, "Include exact p95 in summary statistics (retains record sizes)")
	cmd.Flags().BoolVar(&includeP99, "p99", false, "Include exact p99 in summary statistics (retains record sizes)")
	cmd.Flags().BoolVarP(&progress, "progress", "p", true, "Show progress messages")
	cmd.Flags().BoolVar(&emitJSON, "emit-json", true, "Emit JSON format (*.recordindex.json)")
	cmd.Flags().BoolVar(&emitSzst, "emit-szst", false, "Emit seekable-zstd format (requires CGO build)")
	cmd.Flags().StringVar(&credentialsPath, "credentials", "", "Path to a cloud credentials config (named handles; no secrets in recipe YAML) for s3:// sources")
	cmd.Flags().StringArrayVar(&credentialOverrides, "credential", nil, "Override a handle's AWS profile: handle=profile (repeatable; references only, never a raw key)")

	return cmd
}

func newIndexVerifyCommand() *cobra.Command {
	var (
		indexPath           string
		verifyRecords       bool
		failFast            bool
		progress            bool
		credentialsPath     string
		credentialOverrides []string
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

Supports both JSON and seekable-zstd index formats:
  • *.recordindex.json - JSON format
  • *.recordindex.header.json - Seekable-zstd format (with companion .records.szst)

Example:
  sumpter index verify clinvar.xml --index clinvar.recordindex.json
  sumpter index verify clinvar.xml --index clinvar.recordindex.header.json
  sumpter index verify clinvar.xml --index clinvar.recordindex.json --verify-records`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := logging.Component("index-verify")

			// Resolve the source through the uriio read boundary. A cloud (s3://)
			// source is staged to a local working copy; verification then compares
			// the staged bytes' size + SHA-256 against the index's recorded values,
			// detecting drift between the remote object and the index just as it
			// does for local sources.
			src, err := resolveSingleObjectSource(cmd.Context(), "index verify", args[0], credentialsPath, credentialOverrides)
			if err != nil {
				return err
			}
			defer func() {
				if cerr := src.cleanup(); cerr != nil {
					logger.Warn("Failed to clean up cloud staging directory", zap.Error(cerr))
				}
			}()
			absPath := src.LocalPath

			// Determine index path. Cloud sources have no local directory to derive
			// a default companion index from, so --index is required for them.
			finalIndexPath := indexPath
			if finalIndexPath == "" {
				if src.Identity != absPath {
					return fmt.Errorf("--index is required when verifying a cloud source (%s)", src.Identity)
				}
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
				zap.String("input", src.Identity),
				zap.String("index", finalIndexPath),
				zap.Bool("verify_records", verifyRecords),
			)

			if progress {
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Verifying index for %s...\n", src.BaseName)
				if verifyRecords {
					_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  (with per-record verification - this may take a while)\n")
				}
			}

			// Open index using format-agnostic store abstraction
			indexStore, err := store.Open(finalIndexPath)
			if err != nil {
				return fmt.Errorf("failed to open index: %w", err)
			}
			defer func() { _ = indexStore.Close() }()

			// Verify index using the store provider
			verifyOpts := index.VerifyOptions{
				InputPath:     absPath,
				IndexPath:     finalIndexPath,
				VerifyRecords: verifyRecords,
				FailFast:      failFast,
			}

			verifier := index.NewVerifier(verifyOpts)
			result, err := verifier.VerifyWithProvider(&storeAdapter{indexStore})
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
	cmd.Flags().StringVar(&credentialsPath, "credentials", "", "Path to a cloud credentials config (named handles; no secrets in recipe YAML) for s3:// sources")
	cmd.Flags().StringArrayVar(&credentialOverrides, "credential", nil, "Override a handle's AWS profile: handle=profile (repeatable; references only, never a raw key)")

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
		Use:   "stream <index-file>",
		Short: "Stream-walk a record index without loading it fully",
		Long: `Stream-walk a record index by iterating records one at a time.

This command is intended for dogfooding at extreme scale (multi-GB JSON indexes),
where loading the full records array into memory is undesirable.

Supports both JSON and seekable-zstd index formats:
  • *.recordindex.json - JSON format
  • *.recordindex.header.json - Seekable-zstd format (with companion .records.szst)

For RSS-level memory measurements, run with your OS time tool, e.g.:
  /usr/bin/time -l sumpter index stream path/to/index.recordindex.json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			logger := logging.Component("index-stream")
			indexPath := args[0]

			absPath, err := filepath.Abs(indexPath)
			if err != nil {
				return fmt.Errorf("failed to resolve index path: %w", err)
			}

			if _, err := os.Stat(absPath); err != nil {
				return fmt.Errorf("index file not found: %w", err)
			}

			// Use store.Open() for format-agnostic access (JSON or seekable-zstd)
			indexStore, err := store.Open(absPath)
			if err != nil {
				return fmt.Errorf("failed to open index store: %w", err)
			}
			defer func() { _ = indexStore.Close() }()

			var memStart runtime.MemStats
			runtime.ReadMemStats(&memStart)

			start := time.Now()
			header, err := indexStore.Header()
			if err != nil {
				return fmt.Errorf("failed to read index header: %w", err)
			}

			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Index: %s\n", absPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Schema: %s\n", header.Version)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Source: %s\n", header.Source.Path)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Selector: %s\n", header.Selector.XPath)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "\n")

			// Get record iterator
			iter, err := indexStore.Records(ctx)
			if err != nil {
				return fmt.Errorf("failed to create record iterator: %w", err)
			}
			defer func() { _ = iter.Close() }()

			var (
				count      int
				totalBytes int64
				minSize    int64
				maxSize    int64
				lastNum    int
			)

			for limit == 0 || count < limit {
				rec, err := iter.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return fmt.Errorf("failed to read record: %w", err)
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

// storeAdapter wraps store.IndexStore to satisfy index.RecordProvider interface.
// This adapter bridges the store package types to the index package interface.
type storeAdapter struct {
	store store.IndexStore
}

func (a *storeAdapter) Header() (*index.RecordIndex, error) {
	return a.store.Header()
}

func (a *storeAdapter) Records(ctx context.Context) (index.RecordIterator, error) {
	iter, err := a.store.Records(ctx)
	if err != nil {
		return nil, err
	}
	return &iteratorAdapter{iter: iter}, nil
}

func (a *storeAdapter) Close() error {
	return a.store.Close()
}

// iteratorAdapter wraps store.RecordIterator to satisfy index.RecordIterator interface.
type iteratorAdapter struct {
	iter store.RecordIterator
}

func (a *iteratorAdapter) Next() (*index.RecordMetadata, error) {
	return a.iter.Next()
}

func (a *iteratorAdapter) Close() error {
	return a.iter.Close()
}
