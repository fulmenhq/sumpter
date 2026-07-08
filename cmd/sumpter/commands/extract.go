package commands

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fulmenhq/goneat/pkg/pathfinder"
	"github.com/fulmenhq/sumpter/internal/artifactcontract"
	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/dataartifact"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/extract/parallel"
	"github.com/fulmenhq/sumpter/internal/extract/parquetwriter"
	"github.com/fulmenhq/sumpter/internal/extract/transforms"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/uriio"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

const runIDEnvVar = "SUMPTER_RUN_ID"

var errJSONOutput = errors.New("json output failure")

type ExtractOptions struct {
	Files           string
	FileList        string
	InputPath       string
	IncludePattern  string
	ExcludePattern  string
	MaxDepth        int
	FollowSymlinks  bool
	Workers         int
	DryRun          bool
	ContinueOnError bool
	Progress        bool
	Format          string
	Formats         []string
	OutputPath      string
	OutputPattern   string
	OutputPatterns  map[string]string
	// OutputMode selects record-file fan-out: "per-input" (default — one output
	// file per input) or "aggregate" (stream all inputs' records to one NDJSON
	// writer per invocation, rolling to numbered shards when a cap is hit). Empty
	// means per-input. Aggregate is NDJSON/JSON only and requires --output-path
	// and a manifest.
	OutputMode string
	// AggregateMaxRecords / AggregateMaxBytes roll the aggregate output to the next
	// numbered shard before a cap would be exceeded (0 = uncapped). Bytes are
	// measured on the uncompressed NDJSON stream. Cloud aggregate requires a byte
	// cap <= the single-PUT limit, enforced at plan time.
	AggregateMaxRecords      int
	AggregateMaxBytes        int64
	UniformSchema            bool
	ParquetCompression       string
	ParquetWithholdColumns   []string
	SignatureConfig          string
	ExtractConfig            string
	ApplicabilityConfig      *extract.ApplicabilityConfig
	ClientID                 string
	SiteID                   string
	ManifestParameters       map[string]recipesmanifest.ParamValue
	Parameters               []string
	ParametersRequired       []string
	ParametersInternal       []string
	SourceExtraction         []recipesmanifest.SourceExtractionPattern
	SourceExtractionRequired []string
	SourceExtractionInput    recipesmanifest.InputDefaults
	SourceExtractionRecipeID string
	RunID                    string
	NoManifest               bool
	ArtifactDescriptor       bool
	ArtifactContractBase     string
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
	// Cloud credentials options. They declare credential handles indirectly — no
	// secrets on the CLI or in recipe YAML. The cloud read/write boundaries that
	// act on these land in later deliveries; this delivery validates them when
	// supplied.
	CredentialsPath     string   // Path to the credentials config (handles)
	CredentialOverrides []string // Repeatable handle=profile CLI overrides
	// InputCredentialsHandle selects the credential handle for the cloud read
	// boundary (a handle name, never a secret). Precedence: this CLI value >
	// recipe defaults.input.credentials_handle > the default handle.
	InputCredentialsHandle string
	// OutputCredentialsHandle selects the credential handle for the cloud write
	// boundary (a handle name, never a secret). Precedence: this CLI value >
	// recipe defaults.output.credentials_handle > the default handle. Independent
	// of the input handle so a run can read from one account and write to another.
	OutputCredentialsHandle string

	// Reference tables (in_reference / lookup_reference). Declarations come from the
	// recipe (defaults.reference_tables); ReferenceTableRoot is the recipe workspace
	// directory used as the C1 containment root for local sources; overrides replace a
	// declared table's source only (format/columns/caps stay recipe-declared).
	ReferenceTableDecls     []recipesmanifest.ReferenceTableDecl
	ReferenceTableRoot      string
	ReferenceTableOverrides []string
	// referenceTableProv carries the loaded-table provenance entries from the registry
	// build to the sidecar manifest (sidecar-only, no row values).
	referenceTableProv []provenance.ReferenceTable

	// outputSession publishes cloud (s3://) output for the run; nil for local
	// output. outputHandle is the resolved handle it publishes under. Both are set
	// once at run start and consumed by every output writer.
	outputSession *uriio.Session
	outputHandle  string
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

Choose exactly one input mode: --files for a short ad hoc set, --input-path to
walk and pattern-filter a directory tree, or --file-list for a newline-delimited
batch of references (no directory walk, no argv limit — the input for large or
precisely-scoped sets). Processing many files in one invocation is the supported,
faster path for large sets. Matching records are extracted according to the
extract configuration, producing structured output.

Source input and result output may be S3-compatible cloud URIs (s3://) using
credential handles. See docs/extract-workflow.md "Cloud Sources and Outputs".`,
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

	cmd.Flags().StringVar(&opts.Files, "files", "", "Comma-separated list of file paths to process (short ad hoc sets; subject to the shell argv limit — use --file-list for large batches)")
	cmd.Flags().StringVar(&opts.FileList, "file-list", "", "Path to a newline-delimited file listing input references (local paths or s3:// URIs), one per line; blank lines and # comments ignored. No directory walk, no argv limit — the batch input for large/precise sets. Mutually exclusive with --files and --input-path")
	cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Directory (or single file) to process; walks the tree and filters by --include-pattern/--exclude-pattern. The walk enumerates the whole tree before filtering and announces progress on large trees — for large or precisely-scoped sets prefer --file-list. Mutually exclusive with --files and --file-list")
	cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "*.xml", "File inclusion pattern (use quotes for globs: \"*.xml\")")
	cmd.Flags().StringVar(&opts.ExcludePattern, "exclude-pattern", "", "File exclusion pattern (use quotes for globs: \"temp/*\")")
	cmd.Flags().IntVar(&opts.MaxDepth, "max-depth", 0, "Maximum directory depth to scan (0 = unlimited)")
	cmd.Flags().BoolVar(&opts.FollowSymlinks, "follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().IntVar(&opts.Workers, "workers", 1, "Number of parallel workers")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview operation without execution")
	cmd.Flags().BoolVar(&opts.ContinueOnError, "continue-on-error", false, "Continue after recoverable per-file failures instead of aborting; the run still exits non-zero and lists every dropped input in failures.json — reconcile it so inputs are not silently dropped. Requires --output-path")
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
	cmd.Flags().StringVarP(&opts.Format, "format", "f", "json", "Output format")
	cmd.Flags().StringSliceVar(&opts.Formats, "formats", nil, "Output formats (comma-separated or repeatable; json/ndjson/parquet)")
	cmd.Flags().StringVarP(&opts.OutputPath, "output-path", "o", "", "Output destination path")
	cmd.Flags().StringVar(&opts.OutputPattern, "output-pattern", "extract-{}.json", "Output filename pattern for files mode (use {} for input filename)")
	cmd.Flags().StringVar(&opts.OutputMode, "output-mode", outputModePerInput, "Record-file fan-out: per-input (one file per input) or aggregate (stream all inputs to one NDJSON writer per invocation, rolling to numbered shards). Aggregate requires --output-path + a manifest and is JSON/NDJSON only")
	cmd.Flags().IntVar(&opts.AggregateMaxRecords, "aggregate-max-records", 0, "Aggregate mode: roll to the next shard before exceeding this record count per shard (0 = uncapped)")
	cmd.Flags().Int64Var(&opts.AggregateMaxBytes, "aggregate-max-bytes", 0, "Aggregate mode: roll to the next shard before exceeding this uncompressed byte count per shard (0 = uncapped)")
	cmd.Flags().StringVar(&opts.SignatureConfig, "signature-config-path", "", "Path to signature configuration YAML file")
	cmd.Flags().StringVar(&opts.ExtractConfig, "extract-config-path", "", "Path to extract configuration YAML file")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "Client ID to blend into extracted records")
	cmd.Flags().StringVar(&opts.SiteID, "site-id", "", "Site ID to blend into extracted records")
	cmd.Flags().StringArrayVar(&opts.Parameters, "parameter", nil, "Inject a key=value pair into every record (repeatable). Value is a literal string unless it is a JSON array of strings, e.g. --parameter prefixes='[\"NM_\",\"NR_\"]', which becomes a list parameter")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "UUIDv7 run identifier for deterministic replay (overrides SUMPTER_RUN_ID)")
	cmd.Flags().BoolVar(&opts.NoManifest, "no-manifest", false, "Disable provenance sidecar manifest output")
	cmd.Flags().BoolVar(&opts.ArtifactDescriptor, "artifact-descriptor", false, "Write a portable data artifact descriptor sidecar for the record-stream output")
	cmd.Flags().StringVar(&opts.ArtifactContractBase, "contract-base", "", "Local data-artifact/v0 contract base used to validate --artifact-descriptor output")

	// Parallel extraction flags
	cmd.Flags().StringVar(&opts.RecordIndex, "record-index", "", "Path to record index file (enables parallel extraction)")
	cmd.Flags().IntVar(&opts.MaxRecordSizeMB, "max-record-size-mb", 0, "Maximum record size in MB (0 = no limit)")
	cmd.Flags().BoolVar(&opts.SkipLargeRecords, "skip-large-records", false, "Skip oversized records instead of failing")
	cmd.Flags().BoolVar(&opts.VerifyIndex, "verify-index", false, "Verify index integrity with SHA-256 before extraction")
	cmd.Flags().StringVar(&opts.CredentialsPath, "credentials", "", "Path to a cloud credentials config (named handles; no secrets in recipe YAML)")
	cmd.Flags().StringArrayVar(&opts.CredentialOverrides, "credential", nil, "Override a handle's AWS profile: handle=profile (repeatable; references only, never a raw key)")
	cmd.Flags().StringVar(&opts.InputCredentialsHandle, "input-credentials-handle", "", "Credential handle name for cloud (s3://) source input (a handle reference, not a secret; defaults to the default handle)")
	cmd.Flags().StringVar(&opts.OutputCredentialsHandle, "output-credentials-handle", "", "Credential handle name for cloud (s3://) output (a handle reference, not a secret; defaults to the default handle)")

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

// resolveLocalReferences validates and normalizes the extract command's storage
// references through the uriio seam, at the edge before any work begins.
//
// Cloud (s3://) references are rejected with an actionable, role-specific error
// until the cloud boundaries land; genuinely unsupported schemes (e.g. gs://)
// surface a parse error. Root references that are joined to child artifact names
// or walked for discovery before per-source acquisition — the input path, the
// output path, and the record index — are rewritten in place from file:// URIs
// to their local filesystem path, so downstream filepath.Join/filepath.Abs and
// directory traversal operate on a clean local path (file:// is a verbose alias
// for that path; a path join would otherwise mangle the scheme). Per-file
// --files entries are validated here and resolved individually at the read-
// boundary acquire loop. Bare local paths pass through unchanged.
func resolveLocalReferences(opts *ExtractOptions) error {
	if opts.InputPath != "" {
		ref, err := uriio.Classify(opts.InputPath)
		if err != nil {
			return err
		}
		if ref.IsCloud() {
			// Cloud source inputs are listed/staged through the run session; leave
			// the logical URI intact for that boundary. Only local roots are
			// rewritten here (file:// -> path) before any join/walk.
		} else {
			opts.InputPath = ref.LocalPath
		}
	}
	if opts.Files != "" {
		for _, f := range strings.Split(opts.Files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				// Source entries may be local or s3://; both are acquired at the read
				// boundary. Classify here only to reject genuinely unsupported
				// schemes (e.g. gs://) early, before any work begins.
				if _, err := uriio.Classify(f); err != nil {
					return err
				}
			}
		}
	}
	if opts.OutputPath != "" {
		ref, err := uriio.Classify(opts.OutputPath)
		if err != nil {
			return err
		}
		if !ref.IsCloud() {
			// Local output resolves to its filesystem path exactly as before. A
			// cloud (s3://) output destination is kept as its logical URI; output
			// keys are composed in URI space and published through the output
			// session at the write boundary.
			local, err := uriio.LocalPath("result output", opts.OutputPath)
			if err != nil {
				return err
			}
			opts.OutputPath = local
		}
	}
	if opts.RecordIndex != "" {
		local, err := uriio.LocalPath("record index", opts.RecordIndex)
		if err != nil {
			return err
		}
		opts.RecordIndex = local
	}
	return nil
}

// validateCredentialOptions validates the cloud credential options up front so a
// malformed CLI override or a bad credentials config fails fast, before any
// extraction work. The cloud read/write boundaries that consume these land in
// later deliveries; this delivery proves the options are well-formed:
//   - --credential handle=profile values parse and never carry a raw key (the
//     profile is a reference, not a secret on the command line);
//   - a supplied --credentials config loads under the fail-closed parser and the
//     owner-only permission rule for literal keys.
//
// A run that passes no credential options does no credential work at all, so
// zero-config local runs are unchanged.
func validateCredentialOptions(opts *ExtractOptions) error {
	if _, err := uriio.ParseCredentialOverrides(opts.CredentialOverrides); err != nil {
		return err
	}
	if strings.TrimSpace(opts.CredentialsPath) != "" {
		if _, err := uriio.LoadCredentialsConfig(opts.CredentialsPath); err != nil {
			return err
		}
	}
	// Validate the handle-name selectors with the same slug rule as the
	// credentials config and --credential overrides, so a malformed selector
	// (e.g. "bad handle") fails fast with an invalid-handle-name error rather than
	// surfacing later as an undefined-handle resolution failure. Empty selectors
	// fall back to the default handle and need no validation.
	for _, sel := range []struct{ flag, value string }{
		{"--input-credentials-handle", opts.InputCredentialsHandle},
		{"--output-credentials-handle", opts.OutputCredentialsHandle},
	} {
		if v := strings.TrimSpace(sel.value); v != "" {
			if err := uriio.ValidateHandleName(v); err != nil {
				return fmt.Errorf("%s: %w", sel.flag, err)
			}
		}
	}
	return nil
}

func runExtract(opts *ExtractOptions) error {
	logger := logging.GetLogger()
	logger.Debug("Starting extract command")
	startedAt := time.Now().UTC()

	// Validate options: exactly one input mode (--files, --file-list, --input-path).
	inputModes := 0
	if opts.Files != "" {
		inputModes++
	}
	if opts.FileList != "" {
		inputModes++
	}
	if opts.InputPath != "" {
		inputModes++
	}
	if inputModes == 0 {
		return fmt.Errorf("must specify one of --files, --file-list, or --input-path")
	}
	if inputModes > 1 {
		return fmt.Errorf("specify only one of --files, --file-list, or --input-path")
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
	if err := validateArtifactDescriptorOptions(opts); err != nil {
		return err
	}
	if err := validateAggregateOptions(opts, outputFormats); err != nil {
		return err
	}
	if err := resolveLocalReferences(opts); err != nil {
		return err
	}
	if err := validateCredentialOptions(opts); err != nil {
		return err
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

	// Set up the cloud write boundary: when output is an s3:// destination this
	// creates the run's output session (publishing under the resolved output
	// handle) and validates the handle up front so an unknown handle fails before
	// any extraction work. Both extraction routes (sequential + record-index) read
	// the session off opts. Local output leaves the session nil (zero-drift).
	if err := setupOutputSession(opts, runtimeProvenance.RunID); err != nil {
		return err
	}
	defer closeOutputSession(opts)

	fieldPlan, err := buildExternalFieldPlan(opts, extCfg.FieldMappings)
	if err != nil {
		return err
	}
	if err := validateSourceExtractionDeclarations(opts, extCfg.FieldMappings); err != nil {
		return err
	}
	// Reference-table pre-flight (C3): every in_reference / lookup_reference table name
	// must be a string literal declared in defaults.reference_tables. Runs before the
	// registry build (and before the dry-run return) so unknown/dynamic names fail
	// pre-flight, not per-record. Then build the run-scoped, immutable registry once
	// and thread it into the extract config for both the sequential and parallel paths.
	// On a dry run the registry is not loaded (the build still validates containment).
	if err := validateReferenceTableDeclarations(opts, extCfg.FieldMappings); err != nil {
		return err
	}
	referenceRegistry, referenceProv, err := buildReferenceRegistry(context.Background(), opts, runtimeProvenance.RunID, !opts.DryRun)
	if err != nil {
		return err
	}
	if referenceRegistry != nil {
		extCfg.ReferenceTables = referenceRegistry
	}
	opts.referenceTableProv = referenceProv
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

	// Dry run: preview the files that would be processed, by logical identity,
	// WITHOUT acquiring (downloading/staging) cloud objects. A cloud prefix is
	// still listed — you must list to know what would match — but no object bytes
	// are read and nothing is staged, so a dry run is truly dry: it touches no
	// staging directory and downloads nothing. Output is byte-identical to the
	// previous (stage-then-list) behavior for both local and cloud sources.
	if opts.DryRun {
		return runDryRunPreview(context.Background(), opts, runtimeProvenance.RunID)
	}

	// Discover and acquire the run's input files through the uriio read boundary.
	// Local references (bare paths and file:// URIs) resolve to their own local
	// path — byte-for-byte the historical behavior; s3:// references are listed
	// (prefixes) and staged to a run-scoped working copy. A run with no cloud
	// reference builds no session and stays entirely on the local path. files
	// carries local read paths; logicalByLocal maps each staged path back to its
	// logical URI so provenance, manifests, and output naming record the logical
	// source identity, never the staged working path.
	files, logicalByLocal, session, err := resolveInputSources(context.Background(), opts, runtimeProvenance.RunID)
	if err != nil {
		return err
	}
	if session != nil {
		// The session owns every staged file; Close removes the run's staging
		// directory on all exit paths (success, handled failure, early error).
		defer func() {
			if cerr := session.Close(); cerr != nil {
				logger.Warn("Failed to clean up cloud staging directory", zap.Error(cerr))
			}
		}()
	}
	logger.Debug("File discovery complete", zap.Int("file_count", len(files)))

	if opts.ApplicabilityConfig != nil {
		if strings.TrimSpace(opts.OutputPath) == "" {
			return fmt.Errorf("applicability declared but requires --output-path for dispositions output")
		}
		if err := validateApplicabilityMode(opts, files); err != nil {
			return err
		}
	}

	if opts.Progress {
		logger.Info("Starting extraction",
			zap.Int("file_count", len(files)),
			zap.String("signature", sigCfg.SignatureID),
			zap.String("record_type", extCfg.RecordType))
	}

	// Aggregate output mode streams every input's records to one NDJSON writer
	// (rolling to numbered shards) instead of one file per input. It is validated
	// (validateAggregateOptions) to JSON/NDJSON + --output-path + manifest, and is
	// serial in v0 (the win is eliminating file creation, not write concurrency).
	if isAggregateMode(opts) {
		// match_selectors[].min_occurrences floors are enforced per input at input
		// completion (ADR-0007), before the input's buffered rows are flushed into the
		// shared shard, so a floor miss discards the input's rows rather than publishing
		// output that should have failed. When floors are declared (or --continue-on-error
		// is set) the writer buffers per input, so this holds for cloud too — a discarded
		// input is never published. See runAggregateJSONStreamingExtraction.
		return runAggregateJSONStreamingExtraction(opts, sigCfg, extCfg, files, logicalByLocal, fieldPlan, warnLimiter, runtimeProvenance, startedAt)
	}

	if shouldUseSequentialJSONStreaming(opts, extCfg, outputFormats) {
		return runSequentialJSONStreamingExtraction(opts, sigCfg, extCfg, files, logicalByLocal, fieldPlan, warnLimiter, runtimeProvenance, startedAt)
	}
	warnSequentialMinOccurrencesBufferedFallback(logger, opts, extCfg, outputFormats)

	// Process files
	results := make(chan extract.ExtractResult, len(files))
	manifestEnabled := shouldWriteManifest(opts)
	manifestInputs := make([]provenance.Input, 0, len(files))
	manifestOutputs := make([]provenance.Output, 0, len(files))
	countsByRecordType := make(map[string]int)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
	}
	dispositionSummary := newDispositionSummary(len(files))
	failureManifest := newExtractFailureManifest(len(files))
	var dispositionFailure error

	// For now, serial processing
	for _, file := range files {
		logical := logicalIdentity(file, logicalByLocal)
		// source_extraction derives record fields from the source identity (its
		// filename/absolute/relative path), so it must see the logical URI — never
		// the staged local path — for cloud inputs. It reads no file bytes, so the
		// logical identity is the correct input. For local sources this is the same
		// string as the read path.
		externalFields, err := buildExternalFieldsForFile(logical, opts, fieldPlan, warnLimiter)
		if err != nil {
			if opts.ContinueOnError {
				result := recoverableFailureResult(file, logical, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
				results <- result
				continue
			}
			return fmt.Errorf("failed to build external fields for file %s: %w", logical, err)
		}
		// rp carries the per-file logical source identity into the extraction core
		// so in-core surfaces (_runtime.source_file, file-boundary summaries,
		// ExtractResult.LogicalURI) record the logical URI; bytes are still read
		// from the local staged path. For local sources rp is unchanged.
		rp := runtimeProvenance
		if file != logical {
			rp.SourceURI = logical
		}
		result := extract.ProcessFileWithApplicability(file, sigCfg, extCfg, opts.ApplicabilityConfig, externalFields, opts.AllowLargeFiles, rp)
		results <- result
	}
	close(results)

	// Collect and output results
	for result := range results {
		if result.Disposition == extract.DispositionFailed {
			if result.Error != nil {
				logger.Error("Failed to process file",
					zap.String("file", result.LogicalURI),
					zap.Error(result.Error))
			}
			dispositionSummary.add(result, sanitizeRoots)
			failureErr := failureErrorForResult(result, sanitizeRoots)
			if opts.ContinueOnError {
				detail := result.DispositionDetail
				if detail == "" && result.Error != nil {
					detail = result.Error.Error()
				}
				failureManifest.add(result.LogicalURI, result.DispositionReason, detail, sanitizeRoots)
			}
			if manifestEnabled {
				input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
				if err != nil {
					if opts.ContinueOnError {
						logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.LogicalURI), zap.Error(err))
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
				zap.String("file", result.LogicalURI),
				zap.Error(result.Error))
			if opts.ContinueOnError {
				reason := failureReasonForError(result.Error)
				if reason == "" {
					reason = extract.DispositionReasonInternalError
				}
				result.Disposition = extract.DispositionFailed
				result.DispositionReason = reason
				result.DispositionDetail = result.Error.Error()
				failureManifest.add(result.LogicalURI, reason, result.Error.Error(), sanitizeRoots)
				if manifestEnabled {
					input, ledgerErr := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
					if ledgerErr != nil {
						logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.LogicalURI), zap.Error(ledgerErr))
					} else {
						input.RecordType = extCfg.RecordType
						applyInputDisposition(&input, result, sanitizeRoots)
						manifestInputs = append(manifestInputs, input)
					}
				}
				continue
			}
			return fmt.Errorf("failed to process file %s: %w", result.LogicalURI, result.Error)
		}

		if result.Disposition != extract.DispositionNotApplicable {
			if err := enforceMinOccurrences(opts, extCfg, sigCfg, result.LogicalURI, result.PerSelectorCounts, result.PerSelectorCountsComplete, result.SignatureMatchStatus, result.SignatureConfidence); err != nil {
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
						failureManifest.add(result.LogicalURI, reason, err.Error(), sanitizeRoots)
					}
					if manifestEnabled {
						input, ledgerErr := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
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
			input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
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
					zap.String("file", result.LogicalURI))
			}
		} else if opts.Progress {
			logger.Info("Extracted records",
				zap.String("file", result.LogicalURI),
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
				outputFile := outputFileForFormat(opts, format, result.LogicalURI)
				if err := writeRecordsForFormat(outputFile, format, result.Records, extCfg, sigCfg, opts, runtimeProvenance, result.LogicalURI, manifestPath); err != nil {
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
		failuresPath := outputRefJoin(opts.OutputPath, "failures.json")
		if err := writeExtractFailureManifest(opts, failuresPath, failureManifest); err != nil {
			return err
		}
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(opts, outputRefJoin(opts.OutputPath, "dispositions.json"), dispositionSummary); err != nil {
			return err
		}
	}

	if manifestEnabled {
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
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
		return fmt.Errorf("partial extraction failure: applied=%d failed=%d failures=%s", failureManifest.Applied, failureManifest.Failed, outputRefJoin(opts.OutputPath, "failures.json"))
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

func writeExtractFailureManifest(opts *ExtractOptions, path string, manifest *extractFailureManifestFile) error {
	if manifest == nil || manifest.Failed == 0 {
		return nil
	}
	tgt, err := openOutputTarget(context.Background(), opts, path)
	if err != nil {
		return err
	}
	localPath := tgt.LocalPath
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal extraction failures: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return fmt.Errorf("create extraction failure manifest directory: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return fmt.Errorf("write extraction failure manifest %s: %w", tgt.LogicalURI, err)
	}
	return tgt.Publish(context.Background())
}

func recoverableFailureResult(file, logicalURI string, err error, reason extract.DispositionReason) extract.ExtractResult {
	if logicalURI == "" {
		logicalURI = file
	}
	return extract.ExtractResult{
		File:                 file,
		LogicalURI:           logicalURI,
		Error:                err,
		SignatureMatchStatus: extract.SignatureMatchUnknown,
		Disposition:          extract.DispositionFailed,
		DispositionReason:    reason,
		DispositionDetail:    err.Error(),
	}
}

// logicalIdentity returns the logical source URI for a staged/local read path,
// falling back to the read path itself when the source is local (where the
// logical identity and the read path coincide). Provenance, manifests, disposition
// records, and output naming use this so a staged cloud working path never leaks.
func logicalIdentity(localPath string, logicalByLocal map[string]string) string {
	if lu, ok := logicalByLocal[localPath]; ok && lu != "" {
		return lu
	}
	return localPath
}

func failureErrorForResult(result extract.ExtractResult, roots []string) error {
	file := provenance.SanitizePath(result.LogicalURI, roots...)
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
		File:              provenance.SanitizePath(result.LogicalURI, roots...),
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

func writeDispositionSummary(opts *ExtractOptions, path string, summary *dispositionSummaryFile) error {
	if summary == nil || len(summary.Files) == 0 {
		return nil
	}
	tgt, err := openOutputTarget(context.Background(), opts, path)
	if err != nil {
		return err
	}
	localPath := tgt.LocalPath
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal dispositions summary: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return fmt.Errorf("create dispositions directory: %w", err)
	}
	if err := os.WriteFile(localPath, data, 0o600); err != nil {
		return fmt.Errorf("write dispositions summary %s: %w", tgt.LogicalURI, err)
	}
	return tgt.Publish(context.Background())
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
	return true
}

func warnSequentialMinOccurrencesBufferedFallback(logger *zap.Logger, opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, outputFormats []string) {
	if logger == nil || opts == nil || extCfg == nil {
		return
	}
	if opts.RecordIndex != "" || len(outputFormats) != 1 || outputFormats[0] != recipesmanifest.OutputFormatJSON {
		return
	}
	if !hasDeclaredMinOccurrences(extCfg) {
		return
	}
	logger.Warn("Sequential JSON/NDJSON min_occurrences uses buffered extraction path",
		zap.String("record_type", extCfg.RecordType),
		zap.String("recipe", recipeIdentifier(opts)),
		zap.String("reason", "min_occurrences floors must be enforced before publishing sequential output"),
		zap.String("bounded_alternative", "build a record index and use --record-index for bounded JSON/NDJSON streaming with unambiguous floors"))
}

func isJSONOutputFailure(err error) bool {
	return errors.Is(err, errJSONOutput)
}

func runSequentialJSONStreamingExtraction(opts *ExtractOptions, sigCfg *extract.FileSignature, extCfg *extract.ExtractRecordMatch, files []string, logicalByLocal map[string]string, fieldPlan *externalFieldPlan, warnLimiter *sourceExtractionWarnLimiter, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time) error {
	logger := logging.GetLogger()
	ctx := context.Background()
	manifestEnabled := shouldWriteManifest(opts)
	manifestInputs := make([]provenance.Input, 0, len(files))
	manifestOutputs := make([]provenance.Output, 0, len(files))
	countsByRecordType := make(map[string]int)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
	}
	dispositionSummary := newDispositionSummary(len(files))
	failureManifest := newExtractFailureManifest(len(files))
	var dispositionFailure error

	for _, file := range files {
		logical := logicalIdentity(file, logicalByLocal)
		// source_extraction derives record fields from the source identity (its
		// filename/absolute/relative path), so it must see the logical URI — never
		// the staged local path — for cloud inputs. It reads no file bytes, so the
		// logical identity is the correct input. For local sources this is the same
		// string as the read path.
		externalFields, err := buildExternalFieldsForFile(logical, opts, fieldPlan, warnLimiter)
		if err != nil {
			if !opts.ContinueOnError {
				return fmt.Errorf("failed to build external fields for file %s: %w", logical, err)
			}
			result := recoverableFailureResult(file, logical, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
			if err := recordFailedSequentialResult(result, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots, manifestEnabled, logger); err != nil {
				return err
			}
			continue
		}

		// Output naming derives from the logical source identity (so a staged cloud
		// path never names an output file); bytes are still read from the local
		// path passed to the extraction core below.
		target, err := newJSONOutputTarget(opts, logical)
		if err != nil {
			return fmt.Errorf("failed to write output %s: %w", outputFileForFormat(opts, recipesmanifest.OutputFormatJSON, logical), err)
		}
		rp := runtimeProvenance
		if file != logical {
			rp.SourceURI = logical
		}
		result := extract.ProcessFileWithApplicabilityToSink(ctx, file, sigCfg, extCfg, opts.ApplicabilityConfig, externalFields, opts.AllowLargeFiles, rp, target)
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
				return fmt.Errorf("failed to write output %s: %w", target.logicalName(), result.Error)
			}
			if result.Error != nil {
				logger.Error("Failed to process file",
					zap.String("file", result.LogicalURI),
					zap.Error(result.Error))
			}
			if !opts.ContinueOnError && originalDisposition != extract.DispositionFailed && result.Error != nil {
				return fmt.Errorf("failed to process file %s: %w", result.LogicalURI, result.Error)
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
			return fmt.Errorf("failed to close output %s: %w", target.logicalName(), closeErr)
		}
		if err := target.Commit(); err != nil {
			return err
		}

		if result.Disposition != "" {
			dispositionSummary.add(result, sanitizeRoots)
		}
		if manifestEnabled {
			input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
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
					zap.String("file", result.LogicalURI))
			}
		} else if opts.Progress {
			logger.Info("Extracted records",
				zap.String("file", result.LogicalURI),
				zap.Int("record_count", recordCount))
		}
		countsByRecordType[extCfg.RecordType] += recordCount
		if manifestEnabled {
			manifestOutputs = append(manifestOutputs, provenanceOutput(target.logicalName(), recipesmanifest.OutputFormatJSON, recordCount, opts, sanitizeRoots...))
		}
		failureManifest.addApplied()
	}

	if opts.ContinueOnError && failureManifest.Failed > 0 {
		failuresPath := outputRefJoin(opts.OutputPath, "failures.json")
		if err := writeExtractFailureManifest(opts, failuresPath, failureManifest); err != nil {
			return err
		}
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(opts, outputRefJoin(opts.OutputPath, "dispositions.json"), dispositionSummary); err != nil {
			return err
		}
	}

	if manifestEnabled {
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
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
		return fmt.Errorf("partial extraction failure: applied=%d failed=%d failures=%s", failureManifest.Applied, failureManifest.Failed, outputRefJoin(opts.OutputPath, "failures.json"))
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
		failureManifest.add(result.LogicalURI, result.DispositionReason, result.DispositionDetail, sanitizeRoots)
	}
	if manifestEnabled {
		input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
		if err != nil {
			if opts.ContinueOnError {
				logger.Warn("Skipping provenance input ledger for failed file", zap.String("file", result.LogicalURI), zap.Error(err))
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
	outputFile string // local filesystem path the sink writes/commits to (a staging path for cloud)
	logicalURI string // canonical destination identity for manifests, errors, and logs (never the staging path)
	tempFile   string
	file       *os.File
	sink       *extract.JSONLRecordSink
	output     *uriio.OutputTarget
	stdout     bool
	closed     bool
}

// logicalName returns the destination identity for manifests and user-facing
// errors: the logical URI when set (cloud or local file://), else the local path.
func (t *jsonOutputTarget) logicalName() string {
	if t.logicalURI != "" {
		return t.logicalURI
	}
	return t.outputFile
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
	// Route the destination through the output seam up front so a cloud target
	// fails fast (before any records are streamed). Local targets resolve to their
	// own path and Publish (in Commit) is a no-op; for cloud, LocalPath is a
	// staging file the streaming sink writes (temp + rename), and Commit's Publish
	// uploads the completed artifact. The existing atomic temp-file + rename commit
	// is unchanged either way.
	tgt, err := openOutputTarget(context.Background(), opts, outputFile)
	if err != nil {
		return nil, err
	}
	return &jsonOutputTarget{
		opts:       opts,
		inputFile:  inputFile,
		outputFile: tgt.LocalPath,
		logicalURI: tgt.LogicalURI,
		output:     tgt,
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

// writeMarshaled writes one already-marshaled record (json object + trailing newline)
// verbatim, the byte-for-byte equivalent of OnRecord for the extract-multi input-worker
// path, which marshals each record on its worker and replays the bytes here on the ordered
// committer. Lazily opens the per-input output the same way OnRecord does.
func (t *jsonOutputTarget) writeMarshaled(data []byte) error {
	if err := t.ensureOpen(); err != nil {
		return wrapJSONOutputError("open output", err)
	}
	if err := t.sink.WriteMarshaled(data); err != nil {
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
		return fmt.Errorf("create temporary output for %s: %w", t.logicalName(), err)
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
		return wrapJSONOutputError(fmt.Sprintf("commit output %s", t.logicalName()), err)
	}
	// Publish makes the committed artifact durable at the destination. No-op for
	// local targets (the rename already finalized it); the cloud upload lands here
	// in a later delivery.
	if t.output != nil {
		if err := t.output.Publish(context.Background()); err != nil {
			return wrapJSONOutputError(fmt.Sprintf("publish output %s", t.logicalName()), err)
		}
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
	// Make the index's source available locally. A local header path is used as-is
	// (unchanged); a cloud (s3://) header path is staged to a run-scoped working
	// copy and verified against the index header before any byte-range read. The
	// staged localReadPath is internal (byte reads only); logicalURI drives
	// provenance, output naming, and source_extraction — never the staging path.
	localReadPath, logicalURI, cleanupSource, err := acquireRecordIndexSource(context.Background(), opts, runtimeProvenance.RunID, header)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := cleanupSource(); cerr != nil {
			logger.Warn("Failed to clean up cloud staging directory", zap.Error(cerr))
		}
	}()
	if logicalURI != localReadPath {
		// Cloud source: published artifacts and source_extraction use the logical
		// s3:// identity. SourceIdentity() returns SourceURI when set, so enrichment
		// and provenance record the URI while byte reads use the staged path.
		runtimeProvenance.SourceURI = logicalURI
	}

	externalFields, err := buildExternalFieldsForFile(logicalURI, opts, fieldPlan, warnLimiter)
	if err != nil {
		return fmt.Errorf("failed to build external fields for file %s: %w", logicalURI, err)
	}

	// Create parallel extraction options
	// Pass the already-opened store to avoid double-open
	parallelOpts := parallel.ExtractionOptions{
		IndexPath:         opts.RecordIndex,
		SourcePath:        localReadPath, // staged working copy for cloud sources; the header path for local
		IndexStore:        indexStore,    // Pass pre-opened store to avoid double-open
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
		if hasDeclaredMinOccurrences(extCfg) {
			indexedCount, err := countIndexedRecords(opts.RecordIndex)
			if err != nil {
				return err
			}
			perSelectorCounts, err := perSelectorCountsForIndexedExtraction(header.Selector.XPath, extCfg, indexedCount)
			if err != nil {
				return err
			}
			if err := enforceMinOccurrences(opts, extCfg, sigCfg, logicalURI, perSelectorCounts, true, extract.SignatureMatchUnknown, 0); err != nil {
				return err
			}
		}
		return runParallelJSONStreamingExtraction(opts, extCfg, parallelOpts, localReadPath, logicalURI, runtimeProvenance, startedAt)
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
	if err := enforceMinOccurrences(opts, extCfg, sigCfg, logicalURI, perSelectorCounts, true, extract.SignatureMatchUnknown, 0); err != nil {
		return err
	}

	logger.Info("Parallel extraction complete", zap.Int("record_count", len(records)))

	// Output records (same as sequential path)
	manifestEnabled := shouldWriteManifest(opts)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
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
			// The input ledger hashes the local bytes (localReadPath — a staged
			// working copy for cloud sources) but records the logical identity
			// (logicalURI), so the staging path never reaches the manifest.
			input, err := provenance.BuildInputLedger(localReadPath, logicalURI, resolvedInputHandle(opts), sanitizeRoots...)
			if err != nil {
				return err
			}
			input.RecordType = extCfg.RecordType
			manifestInputs = append(manifestInputs, input)
		}
		for _, format := range outputFormats {
			outputFile := outputFileForFormat(opts, format, "parallel")
			if err := writeRecordsForFormat(outputFile, format, records, extCfg, sigCfg, opts, runtimeProvenance, logicalURI, manifestPath); err != nil {
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
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written", zap.String("file", manifestPath))
	} else if opts.OutputPath == "" && !opts.NoManifest {
		logger.Warn("Skipping provenance manifest because --output-path is not set")
	}

	return nil
}

func runParallelJSONStreamingExtraction(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, parallelOpts parallel.ExtractionOptions, localReadPath, logicalURI string, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time) error {
	logger := logging.GetLogger()
	ctx := context.Background()
	manifestEnabled := shouldWriteManifest(opts)
	sanitizeRoots := manifestSanitizeRoots(opts)
	manifestPath := ""
	if manifestEnabled {
		manifestPath = outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
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
			return fmt.Errorf("failed to write output %s: %w", target.logicalName(), extractErr)
		}
		return fmt.Errorf("parallel extraction failed: %w", extractErr)
	}
	if closeErr != nil {
		target.Abort()
		return fmt.Errorf("failed to close output %s: %w", target.logicalName(), closeErr)
	}
	if err := target.Commit(); err != nil {
		return err
	}

	recordCount := target.Count()
	logger.Info("Parallel extraction complete", zap.Int("record_count", recordCount))

	if manifestEnabled {
		// Hash the local bytes (staged working copy for cloud sources) but record
		// the logical identity, so the staging path never reaches the manifest.
		input, err := provenance.BuildInputLedger(localReadPath, logicalURI, resolvedInputHandle(opts), sanitizeRoots...)
		if err != nil {
			return err
		}
		input.RecordType = extCfg.RecordType
		manifestInputs := []provenance.Input{input}
		manifestOutputs := []provenance.Output{
			provenanceOutput(target.logicalName(), recipesmanifest.OutputFormatJSON, recordCount, opts, sanitizeRoots...),
		}
		countsByRecordType := map[string]int{extCfg.RecordType: recordCount}
		manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
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
	if signatureMatchStatus == extract.SignatureMatchMismatched && hasDeclaredMinOccurrences(extCfg) {
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

func hasDeclaredMinOccurrences(extCfg *extract.ExtractRecordMatch) bool {
	if extCfg == nil {
		return false
	}
	for _, selector := range extCfg.MatchSelectors {
		if selector.MinOccurrences > 0 {
			return true
		}
	}
	return false
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

func countIndexedRecords(indexPath string) (int, error) {
	indexStore, err := store.Open(indexPath)
	if err != nil {
		return 0, fmt.Errorf("failed to open record index for min_occurrences preflight: %w", err)
	}
	defer func() { _ = indexStore.Close() }()

	iter, err := indexStore.Records(context.Background())
	if err != nil {
		return 0, fmt.Errorf("failed to read record index for min_occurrences preflight: %w", err)
	}
	defer func() { _ = iter.Close() }()

	count := 0
	for {
		if _, err := iter.Next(); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return 0, fmt.Errorf("failed to count record index entries for min_occurrences preflight: %w", err)
		}
		count++
	}
	return count, nil
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

func validateArtifactDescriptorOptions(opts *ExtractOptions) error {
	if opts == nil {
		return nil
	}
	if strings.TrimSpace(opts.ArtifactContractBase) != "" && !opts.ArtifactDescriptor {
		return fmt.Errorf("--contract-base requires --artifact-descriptor")
	}
	if !opts.ArtifactDescriptor {
		return nil
	}
	if strings.TrimSpace(opts.OutputPath) == "" {
		return fmt.Errorf("--artifact-descriptor requires --output-path")
	}
	if opts.NoManifest {
		return fmt.Errorf("--artifact-descriptor cannot be combined with --no-manifest because the descriptor links to the provenance manifest")
	}
	if opts.DryRun {
		return fmt.Errorf("--artifact-descriptor cannot be combined with --dry-run")
	}
	if strings.TrimSpace(opts.ArtifactContractBase) == "" {
		return fmt.Errorf("--artifact-descriptor requires --contract-base")
	}
	local, err := uriio.LocalPath("data artifact contract base", opts.ArtifactContractBase)
	if err != nil {
		return err
	}
	opts.ArtifactContractBase = local
	return nil
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
	// FU-2: record the resolved logical output handle NAME for cloud (s3://)
	// destinations only. opts.outputHandle is set exactly when a cloud output
	// session exists (setupOutputSession), so local outputs stay byte-identical.
	// Name only — never the resolved profile/endpoint/region/secret (S8).
	if opts != nil && opts.outputHandle != "" && referenceIsCloud(outputFile) {
		output.CredentialsHandle = opts.outputHandle
	}
	if format == recipesmanifest.OutputFormatParquet && opts != nil && len(opts.ParquetWithholdColumns) > 0 {
		output.WithholdColumns = append([]string(nil), opts.ParquetWithholdColumns...)
	}
	return output
}

// referenceIsCloud reports whether a logical reference targets cloud object
// storage (s3://). Classification failures are treated as non-cloud — the caller
// gates emitting cloud-only provenance, so a parse failure conservatively omits
// the handle rather than recording it on a local entry.
func referenceIsCloud(reference string) bool {
	ref, err := uriio.Classify(reference)
	return err == nil && ref.IsCloud()
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
	return outputRefJoin(opts.OutputPath, filename)
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
		return writeRecordsToFile(opts, outputFile, records)
	case recipesmanifest.OutputFormatParquet:
		if opts == nil || opts.Recipe == nil {
			logging.Warn("Parquet output without recipe.yaml; recipe provenance metadata will be omitted",
				zap.String("file", outputFile))
		}
		// The parquet writer needs a local seekable file. Resolve the destination
		// through the output seam: local writes go to the final path (no-op
		// Publish); a cloud destination writes the complete parquet to a staging
		// file, then Publish uploads it.
		tgt, err := openOutputTarget(context.Background(), opts, outputFile)
		if err != nil {
			return err
		}
		metadata := parquetFileMetadata(sigCfg, opts, runtime, sourceFile, manifestPath)
		if err := parquetwriter.WriteFile(tgt.LocalPath, extCfg, records, parquetwriter.Options{
			Compression:     opts.ParquetCompression,
			Metadata:        metadata,
			WithholdColumns: opts.ParquetWithholdColumns,
		}); err != nil {
			return err
		}
		return tgt.Publish(context.Background())
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
		CLI:                provenance.CLI{Command: commandName, ArgvSanitized: provenance.SanitizeArgvWithInternalParameters(argv, opts.ParametersInternal, roots...)},
		Recipe:             opts.Recipe,
		Inputs:             inputs,
		Outputs:            outputs,
		CountsByRecordType: counts,
		ReferenceTables:    opts.referenceTableProv,
	}
}

func manifestSanitizeRoots(opts *ExtractOptions) []string {
	var roots []string
	if opts == nil {
		return roots
	}
	for _, root := range []string{opts.OutputPath, opts.InputPath, opts.ArtifactContractBase} {
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
	if opts.FileList != "" {
		// The list file's directory is the resolution base for its relative entries,
		// so it covers the input refs the list contributes.
		roots = append(roots, filepath.Dir(opts.FileList))
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
	appendFlag("--file-list", opts.FileList)
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
	if opts.OutputMode != "" && opts.OutputMode != outputModePerInput {
		appendFlag("--output-mode", opts.OutputMode)
	}
	if opts.AggregateMaxRecords > 0 {
		appendFlag("--aggregate-max-records", fmt.Sprintf("%d", opts.AggregateMaxRecords))
	}
	if opts.AggregateMaxBytes > 0 {
		appendFlag("--aggregate-max-bytes", fmt.Sprintf("%d", opts.AggregateMaxBytes))
	}
	appendFlag("--signature-config-path", opts.SignatureConfig)
	appendFlag("--extract-config-path", opts.ExtractConfig)
	appendFlag("--record-index", opts.RecordIndex)
	appendFlag("--run-id", opts.RunID)
	for _, parameter := range opts.Parameters {
		appendFlag("--parameter", parameter)
	}
	for _, override := range opts.ReferenceTableOverrides {
		appendFlag("--reference-table", override)
	}
	if opts.ContinueOnError {
		args = append(args, "--continue-on-error")
	}
	if opts.NoManifest {
		args = append(args, "--no-manifest")
	}
	if opts.ArtifactDescriptor {
		args = append(args, "--artifact-descriptor")
	}
	appendFlag("--contract-base", opts.ArtifactContractBase)
	return args
}

type externalFieldPlan struct {
	shimFields         map[string]string
	manifestParameters map[string]recipesmanifest.ParamValue
	cliParameters      map[string]recipesmanifest.ParamValue
	parametersRequired []string
	// internalParameters is the set of defaults.parameters keys declared
	// derive-only via defaults.parameters_internal. build() wraps matching
	// resolved parameter values so they stay in expression scope but are never
	// emitted to extract.data.
	internalParameters map[string]struct{}
	// internalCaptures is the set of source_extraction capture names declared
	// `internal: true`. build() wraps these in extract.InternalField so they reach
	// expression scope but are not emitted into the record body.
	internalCaptures map[string]struct{}
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
		manifestParameters: make(map[string]recipesmanifest.ParamValue),
		cliParameters:      make(map[string]recipesmanifest.ParamValue),
		internalParameters: make(map[string]struct{}),
		internalCaptures:   make(map[string]struct{}),
	}
	if opts == nil {
		return plan, nil
	}

	// Record which source_extraction capture names are derive-only (internal:true)
	// so build() can wrap them. Collected per pattern from its named groups; a
	// pattern's internal flag covers all of its captures.
	for i := range opts.SourceExtraction {
		pattern := &opts.SourceExtraction[i]
		if !pattern.Internal || pattern.CompiledPattern == nil {
			continue
		}
		for _, name := range pattern.CompiledPattern.SubexpNames() {
			if strings.TrimSpace(name) == "" {
				continue
			}
			plan.internalCaptures[name] = struct{}{}
		}
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
		pv, perr := parseCLIParameterValue(key, value)
		if perr != nil {
			return nil, perr
		}
		plan.cliParameters[key] = pv
	}
	plan.parametersRequired = append(plan.parametersRequired, opts.ParametersRequired...)
	for _, key := range opts.ParametersInternal {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("internal parameter key cannot be empty")
		}
		plan.internalParameters[key] = struct{}{}
	}

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
		if !ok || externalFieldUnprovided(value) {
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
		if _, internal := p.internalCaptures[key]; internal {
			// Derive-only: visible in expression scope (unwrapped there), skipped
			// by the record-emission merge so it never reaches extract.data.
			externalFields[key] = extract.InternalField{Value: value}
			continue
		}
		externalFields[key] = value
	}
	for key, value := range p.manifestParameters {
		scopeValue := value.AsScope()
		if _, internal := p.internalParameters[key]; internal {
			externalFields[key] = extract.InternalField{Value: scopeValue}
			continue
		}
		externalFields[key] = scopeValue
	}
	for key, value := range p.cliParameters {
		scopeValue := value.AsScope()
		if _, internal := p.internalParameters[key]; internal {
			externalFields[key] = extract.InternalField{Value: scopeValue}
			continue
		}
		externalFields[key] = scopeValue
	}
	return externalFields
}

// parseCLIParameterValue parses a --parameter key=<value> value into a typed
// parameter. A value is taken as a JSON array of strings ONLY when it begins with
// "[" (after trimming): then it is strictly unmarshalled into []string, so a
// number/boolean/object/nested/mixed array fails loudly rather than being
// stringified, and empty members are rejected. Any other value — including one
// that merely contains commas — stays the literal string it has always been.
func parseCLIParameterValue(key, value string) (recipesmanifest.ParamValue, error) {
	if !strings.HasPrefix(strings.TrimSpace(value), "[") {
		return recipesmanifest.ScalarParam(value), nil
	}
	var list []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &list); err != nil {
		return recipesmanifest.ParamValue{}, fmt.Errorf(
			"invalid --parameter %s: value looks like a JSON array but is not a valid array of strings "+
				"(only string members are allowed — no numbers, booleans, objects, or nested arrays): %w", key, err)
	}
	for i, member := range list {
		if member == "" {
			return recipesmanifest.ParamValue{}, fmt.Errorf(
				"invalid --parameter %s: list member %d is an empty string; empty members are not allowed "+
					"(an empty prefix would match everything)", key, i)
		}
	}
	return recipesmanifest.ListParam(list), nil
}

// externalFieldUnprovided reports whether a resolved external-field value counts
// as "not provided" for parameters_required. A list value — even an empty list —
// counts as provided (an explicit empty set is a meaningful run input); a string
// counts as provided only when it is non-blank.
func externalFieldUnprovided(value interface{}) bool {
	if internal, ok := value.(extract.InternalField); ok {
		value = internal.Value
	}
	switch v := value.(type) {
	case []string:
		return false
	case string:
		return strings.TrimSpace(v) == ""
	default:
		return strings.TrimSpace(fmt.Sprint(v)) == ""
	}
}

func validateSourceExtractionDeclarations(opts *ExtractOptions, mappings []extract.FieldMapping) error {
	if opts == nil || len(opts.SourceExtraction) == 0 {
		return nil
	}

	captureNames := make(map[string]struct{})
	// A capture name's emit visibility must be unambiguous. Source captures with a
	// repeated name across patterns keep last-match-wins value semantics, but the
	// internal (derive-only) flag is recorded by name in the field plan — so a name
	// declared on both an internal and a non-internal pattern would make emission
	// depend on declaration rather than on which pattern actually matched. Reject
	// that mix up front (fail loud); same-name duplicates that agree on visibility
	// stay allowed.
	internalNames := make(map[string]struct{})
	nonInternalNames := make(map[string]struct{})
	for index, pattern := range opts.SourceExtraction {
		if pattern.CompiledPattern == nil {
			return fmt.Errorf("source_extraction pattern at index %d is not compiled", index)
		}
		for _, name := range pattern.CompiledPattern.SubexpNames() {
			if strings.TrimSpace(name) == "" {
				continue
			}
			captureNames[name] = struct{}{}
			if pattern.Internal {
				internalNames[name] = struct{}{}
			} else {
				nonInternalNames[name] = struct{}{}
			}
		}
	}

	for name := range internalNames {
		if _, both := nonInternalNames[name]; both {
			return fmt.Errorf(
				"source_extraction capture %q is declared on both an internal:true and a "+
					"non-internal pattern; a capture name must have one emit visibility — mark all "+
					"of its patterns internal or none of them",
				name,
			)
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
	// A cloud source identity (s3://...) is matched in URI space, not as a
	// filesystem path: filepath.Abs/Rel would mangle the scheme or treat the
	// object key as escaping the root, and would also stamp the local staging
	// directory into extracted fields. Handle it explicitly.
	if ref, err := uriio.Classify(filePath); err == nil && ref.IsCloud() {
		return cloudSourceExtractionTarget(ref, sourceType, input)
	}
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

// cloudSourceExtractionTarget derives a source_extraction match target from a
// cloud reference in URI space, so the logical s3:// identity (never a staged
// local path) is what record fields are extracted from.
func cloudSourceExtractionTarget(ref uriio.Ref, sourceType string, input recipesmanifest.InputDefaults) (string, error) {
	switch sourceType {
	case recipesmanifest.SourceExtractionFilename:
		key := ref.Key
		if i := strings.LastIndex(key, "/"); i >= 0 {
			key = key[i+1:]
		}
		return key, nil
	case recipesmanifest.SourceExtractionAbsolutePath:
		// The canonical logical URI is the absolute identity for a cloud object.
		return ref.LogicalURI, nil
	case recipesmanifest.SourceExtractionRelativePath:
		return cloudRelativeSourcePath(input.Path, ref)
	default:
		return "", fmt.Errorf("unsupported source_extraction source %q", sourceType)
	}
}

// cloudRelativeSourcePath returns the object key relative to a matching cloud
// input prefix (same bucket), mirroring relative_path for local roots. A file
// outside the root's bucket/prefix is reported as escaping the root.
func cloudRelativeSourcePath(root string, fileRef uriio.Ref) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("relative_path requires --input-path; got single --files mode; use 'absolute_path' or 'filename' instead")
	}
	rootRef, err := uriio.Classify(root)
	if err != nil {
		return "", fmt.Errorf("failed to classify source_extraction root %s: %w", root, err)
	}
	if !rootRef.IsCloud() || rootRef.Bucket != fileRef.Bucket {
		return "", fmt.Errorf("source_extraction relative_path file %s escapes input root %s", fileRef.LogicalURI, root)
	}
	// A single-object root (not a prefix or glob) is the file itself: relative_path
	// of a file against itself is ".", mirroring local filepath.Rel(file, file).
	// Any other key under that root escapes.
	if !rootRef.IsPrefix() && !rootRef.IsPattern() {
		if fileRef.Key == rootRef.Key {
			return ".", nil
		}
		return "", fmt.Errorf("source_extraction relative_path file %s escapes input root %s", fileRef.LogicalURI, root)
	}
	// Prefix/pattern root: return the object key relative to the prefix, with a
	// path-component boundary so prefix/ does not match prefixsuffix/.
	prefix := rootRef.Key
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	if !strings.HasPrefix(fileRef.Key, prefix) {
		return "", fmt.Errorf("source_extraction relative_path file %s escapes input root %s", fileRef.LogicalURI, root)
	}
	return strings.TrimPrefix(fileRef.Key, prefix), nil
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
	// mu guards emitted/summarized. SUM-068: a single per-recipe limiter is shared across
	// inputs, and from the input-workers slice the per-input application (which reaches
	// warn() via buildExternalFieldsForFile) runs concurrently across workers. The lock
	// keeps the global non-match warning cap correct under that concurrency; it is
	// uncontended on the serial path. Emission stays on the build stage (not deferred to
	// the ordered committer) so the cap counts attempts, not commits.
	mu         sync.Mutex
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
	l.mu.Lock()
	defer l.mu.Unlock()
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

// staleStagingMaxAge bounds how long an orphaned cloud-staging run directory may
// survive a crash (a SIGKILL/OOM/panic that skipped Session.Close) before a
// later run's startup sweep removes it. It is deliberately conservative: the
// sweep runs only at startup and removes only directories older than this, so it
// cannot disturb a concurrent run's live staging — no real run stages for a day.
const staleStagingMaxAge = 24 * time.Hour

// referencesIncludeCloud reports whether the run's source inputs include any
// s3:// reference. A run with only local inputs needs no cloud session and stays
// on the pure-local discovery/acquire path, unchanged from prior releases.
func referencesIncludeCloud(opts *ExtractOptions) (bool, error) {
	isCloud := func(ref string) (bool, error) {
		r, err := uriio.Classify(ref)
		if err != nil {
			return false, err
		}
		return r.IsCloud(), nil
	}
	if opts.FileList != "" {
		// A file-list can carry s3:// refs, so the session-need check must read it (the
		// same refs discoverInputReferences will use). readFileListRefs validates each
		// entry's scheme with line context.
		refs, err := readFileListRefs(opts.FileList)
		if err != nil {
			return false, err
		}
		for _, ref := range refs {
			cloud, cerr := isCloud(ref)
			if cerr != nil {
				return false, cerr
			}
			if cloud {
				return true, nil
			}
		}
		return false, nil
	}
	if opts.Files != "" {
		for _, f := range strings.Split(opts.Files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				cloud, err := isCloud(f)
				if err != nil {
					return false, err
				}
				if cloud {
					return true, nil
				}
			}
		}
		return false, nil
	}
	if strings.TrimSpace(opts.InputPath) != "" {
		return isCloud(opts.InputPath)
	}
	return false, nil
}

// newCloudSession builds the run-scoped cloud session: a credential resolver
// layered from the credentials config and CLI handle overrides, over the resolved
// Sumpter work directory. It also runs a startup orphan sweep of stale staging
// directories left by prior crashed runs before any new staging begins.
func newCloudSession(opts *ExtractOptions, runID string) (*uriio.Session, error) {
	return newCloudSessionFromCredentials(opts.CredentialsPath, opts.CredentialOverrides, runID)
}

// resolvedOutputHandle returns the credential handle for cloud output. Precedence
// is CLI selector > recipe defaults.output.credentials_handle (already mapped
// onto opts.OutputCredentialsHandle by the recipe runner) > the default handle.
func resolvedOutputHandle(opts *ExtractOptions) string {
	if h := strings.TrimSpace(opts.OutputCredentialsHandle); h != "" {
		return h
	}
	return uriio.DefaultHandleName
}

// resolvedInputHandle returns the credential handle for cloud source acquisition.
// Precedence is CLI selector > recipe defaults.input.credentials_handle (mapped
// onto opts.InputCredentialsHandle by the recipe runner) > the default handle.
func resolvedInputHandle(opts *ExtractOptions) string {
	if opts != nil {
		if h := strings.TrimSpace(opts.InputCredentialsHandle); h != "" {
			return h
		}
	}
	return uriio.DefaultHandleName
}

// setupOutputSession creates the run's cloud output session when the output
// destination is an s3:// URI. It resolves and validates the output handle up
// front (an unknown handle fails before any extraction work) and emits a loud,
// redacted run-start confirmation of the destination — a misrouted write is
// materially more dangerous than a misrouted read, so the operator sees the
// resolved bucket/endpoint/handle (never credentials) before any bytes leave.
// Local output leaves opts.outputSession nil (byte-for-byte unchanged).
func setupOutputSession(opts *ExtractOptions, runID string) error {
	if strings.TrimSpace(opts.OutputPath) == "" {
		return nil
	}
	ref, err := uriio.Classify(opts.OutputPath)
	if err != nil {
		return err
	}
	if !ref.IsCloud() {
		return nil
	}

	handle := resolvedOutputHandle(opts)
	session, err := newCloudSession(opts, runID)
	if err != nil {
		return err
	}
	// Fail fast on an unknown/invalid output handle, before any work or staging.
	confirmation, err := session.DescribeOutputHandle(handle, ref.Bucket)
	if err != nil {
		_ = session.Close()
		return err
	}
	opts.outputSession = session
	opts.outputHandle = handle

	logging.Info("Publishing extraction output to cloud destination",
		zap.String("destination", ref.LogicalURI),
		zap.String("handle", handle),
		zap.String("resolved", confirmation))
	return nil
}

// closeOutputSession removes the output session's staging directory and releases
// its providers on every run exit path. Safe when no session was created.
func closeOutputSession(opts *ExtractOptions) {
	if opts == nil || opts.outputSession == nil {
		return
	}
	if err := opts.outputSession.Close(); err != nil {
		logging.Warn("Failed to clean up cloud output staging directory", zap.Error(err))
	}
	opts.outputSession = nil
}

// openOutputTarget resolves a destination through the run's output session when
// one exists (cloud output), otherwise through the local-only free resolver. It
// is the single seam every output writer uses so cloud publishing and local
// writes share one path.
func openOutputTarget(ctx context.Context, opts *ExtractOptions, reference string) (*uriio.OutputTarget, error) {
	if opts != nil && opts.outputSession != nil {
		return opts.outputSession.OpenOutput(ctx, reference, opts.outputHandle)
	}
	return uriio.OpenOutput(ctx, uriio.OutputRequest{Reference: reference})
}

// writeProvenanceManifest publishes the provenance sidecar through the run's
// output seam, so a cloud manifest publishes alongside its output under the
// output handle (local stays a no-op-publish write).
//
// Publish-ordering contract (S9): every primary output is published BEFORE this
// sidecar — all call sites invoke this only after the output loop completes. A
// cloud PutObject is atomic (single PUT, no partial object), so if the sidecar
// publish fails the primary objects are already durable while this returns a
// fatal error: the run fails with the output object present but no manifest. That
// state is intentional and must be read as a failed run ("an output object
// present without its manifest means the run failed; do not treat it as
// success") — a published object can no longer be un-published.
func writeProvenanceManifest(opts *ExtractOptions, path string, manifest provenance.Manifest) error {
	tgt, err := openOutputTarget(context.Background(), opts, path)
	if err != nil {
		return err
	}
	return provenance.WriteManifestVia(context.Background(), tgt, manifest)
}

func writeDataArtifactDescriptor(opts *ExtractOptions, manifest provenance.Manifest) error {
	if opts == nil || !opts.ArtifactDescriptor {
		return nil
	}
	artifactUUID, err := provenance.NewRunID()
	if err != nil {
		return fmt.Errorf("generate artifact id: %w", err)
	}
	descriptor, err := dataartifact.BuildRecordStreamDescriptor(manifest, artifactUUID)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(descriptor, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal data artifact descriptor: %w", err)
	}
	data = append(data, '\n')

	resolved, err := artifactcontract.ResolveBaseline(opts.ArtifactContractBase)
	if err != nil {
		return err
	}
	result, err := artifactcontract.ValidateDescriptorBytes(resolved, data, dataartifact.DescriptorFileName)
	if err != nil {
		return err
	}
	if !result.Valid {
		return fmt.Errorf("generated data artifact descriptor failed validation: %s", artifactValidationSummary(result))
	}

	path := outputRefJoin(opts.OutputPath, dataartifact.DescriptorFileName)
	tgt, err := openOutputTarget(context.Background(), opts, path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tgt.LocalPath), 0o750); err != nil {
		return fmt.Errorf("create data artifact descriptor directory: %w", err)
	}
	if err := os.WriteFile(tgt.LocalPath, data, 0o600); err != nil {
		return fmt.Errorf("write data artifact descriptor %s: %w", tgt.LogicalURI, err)
	}
	return tgt.Publish(context.Background())
}

func artifactValidationSummary(result *artifactcontract.ValidationResult) string {
	if result == nil || len(result.Errors) == 0 {
		return "unknown validation error"
	}
	parts := make([]string, 0, len(result.Errors))
	for _, validationErr := range result.Errors {
		if validationErr.Path == "" {
			parts = append(parts, validationErr.Message)
		} else {
			parts = append(parts, validationErr.Path+": "+validationErr.Message)
		}
	}
	return strings.Join(parts, "; ")
}

// outputRefJoin composes an output destination from a base path/URI and a file
// name. For a cloud (s3://) base it joins in URI space (filepath.Join would
// collapse the "//"); for a local base it is filepath.Join.
func outputRefJoin(base, name string) string {
	if ref, err := uriio.Classify(base); err == nil && ref.IsCloud() {
		return strings.TrimRight(base, "/") + "/" + name
	}
	return filepath.Join(base, name)
}

// acquireRecordIndexSource makes a record index's source available locally for
// seekable/parallel extraction. A local header path is returned as-is with a
// no-op cleanup (byte-for-byte the historical behavior). A cloud (s3://) header
// path is staged to a run-scoped working copy and verified against the index
// header (size + SHA-256) before any byte-range read — the remote object is
// mutable, so a mismatch means the index is stale and its offsets cannot be
// trusted. localReadPath is the staged path the seekable extractor reads ranges
// from; logicalURI is the logical identity recorded in provenance, output
// naming, and source_extraction (never the staging path). The caller MUST invoke
// cleanup on every exit path.
func acquireRecordIndexSource(ctx context.Context, opts *ExtractOptions, runID string, header *index.RecordIndex) (localReadPath, logicalURI string, cleanup func() error, err error) {
	noop := func() error { return nil }
	ref, cerr := uriio.Classify(header.Source.Path)
	if cerr != nil || !ref.IsCloud() {
		// Local source (or an unclassifiable path treated as local): unchanged.
		return header.Source.Path, header.Source.Path, noop, nil
	}
	session, serr := newCloudSession(opts, runID)
	if serr != nil {
		return "", "", noop, serr
	}
	src, aerr := session.Acquire(ctx, header.Source.Path, resolvedInputHandle(opts))
	if aerr != nil {
		_ = session.Close()
		return "", "", noop, fmt.Errorf("acquire record-index source %s: %w", header.Source.Path, aerr)
	}
	if verr := index.VerifySourceIntegrity(src.LocalPath, header.Source); verr != nil {
		_ = session.Close()
		return "", "", noop, fmt.Errorf("record-index source %s: %w", header.Source.Path, verr)
	}
	return src.LocalPath, src.LogicalURI, session.Close, nil
}

// newCloudSessionFromCredentials builds a uriio cloud session from the same
// credential inputs the extract command exposes (a credentials-config path and
// repeatable handle=profile overrides). It is shared by extract and the index
// command so cloud source acquisition behaves identically at every read-boundary
// entry point. No secrets are accepted here — only a config path and handle
// references (see uriio.ParseCredentialOverrides / LoadCredentialsConfig).
func newCloudSessionFromCredentials(credentialsPath string, credentialOverrides []string, runID string) (*uriio.Session, error) {
	cliProfiles, err := uriio.ParseCredentialOverrides(credentialOverrides)
	if err != nil {
		return nil, err
	}
	var credCfg *uriio.CredentialsConfig
	if strings.TrimSpace(credentialsPath) != "" {
		credCfg, err = uriio.LoadCredentialsConfig(credentialsPath)
		if err != nil {
			return nil, err
		}
	}
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return nil, fmt.Errorf("resolve sumpter work directory: %w", err)
	}
	uriio.SweepStaleStaging(paths.WorkDir, staleStagingMaxAge, time.Now())
	resolver := uriio.NewResolver(credCfg, cliProfiles)
	return uriio.NewSession(resolver, paths.WorkDir, runID), nil
}

// discoverInputReferences resolves the run's source inputs to a list of logical
// references to acquire. --files entries are taken verbatim (local or s3://). A
// local --input-path uses the existing filesystem discovery; a cloud --input-path
// prefix/glob is enumerated through the session (include/exclude globs apply), and
// a single cloud object is returned as one reference.
func discoverInputReferences(ctx context.Context, session *uriio.Session, opts *ExtractOptions) ([]string, error) {
	if opts.FileList != "" {
		// Batch file-list input: the orchestrator supplies the exact set of
		// references (local or s3://) — no directory walk, no argv ceiling. Refs are
		// acquired through the same read boundary as --files entries.
		return readFileListRefs(opts.FileList)
	}
	if opts.Files != "" {
		refs := make([]string, 0)
		for _, f := range strings.Split(opts.Files, ",") {
			if f = strings.TrimSpace(f); f != "" {
				refs = append(refs, f)
			}
		}
		return refs, nil
	}

	ref, err := uriio.Classify(opts.InputPath)
	if err != nil {
		return nil, err
	}
	if !ref.IsCloud() {
		files, derr := discoverInputFiles(opts)
		if derr != nil {
			return nil, fmt.Errorf("failed to find files: %w", derr)
		}
		return files, nil
	}
	if !ref.IsPrefix() && !ref.IsPattern() {
		// A single cloud object addressed directly — acquire it, no listing.
		return []string{opts.InputPath}, nil
	}
	listing, err := session.List(ctx, opts.InputPath, resolvedInputHandle(opts), opts.IncludePattern, opts.ExcludePattern)
	if err != nil {
		return nil, err
	}
	if listing.FullBucketScan {
		return nil, fmt.Errorf("refusing a full-bucket scan: %q names no key prefix; narrow it to s3://bucket/prefix/", opts.InputPath)
	}
	refs := make([]string, 0, len(listing.Entries))
	for _, e := range listing.Entries {
		refs = append(refs, e.LogicalURI)
	}
	return refs, nil
}

// runDryRunPreview prints the input references a run would process, by logical
// identity, without acquiring (downloading/staging) any cloud object. It is the
// truly-dry path for --dry-run: cloud prefixes are listed (no download), single
// cloud objects are echoed (no network at all), and local globs are walked
// exactly as before. No staging directory is created and no object bytes are
// read.
func runDryRunPreview(ctx context.Context, opts *ExtractOptions, runID string) error {
	logger := logging.GetLogger()
	logger.Debug("Starting dry run")
	refs, session, err := discoverInputReferencesForPreview(ctx, opts, runID)
	if session != nil {
		defer func() {
			if cerr := session.Close(); cerr != nil {
				logger.Warn("Failed to clean up cloud session", zap.Error(cerr))
			}
		}()
	}
	if err != nil {
		return err
	}
	for _, ref := range refs {
		fmt.Println(ref)
	}
	logger.Debug("Dry run complete, exiting")
	return nil
}

// discoverInputReferencesForPreview lists the input references a run would
// process, by logical identity, WITHOUT acquiring any of them. It shares the
// cloud-session setup and reference discovery with resolveInputSources but stops
// before the acquire/stage step, so a cloud dry run downloads nothing. Returns
// the run session (nil for an all-local run) for the caller to Close.
func discoverInputReferencesForPreview(ctx context.Context, opts *ExtractOptions, runID string) ([]string, *uriio.Session, error) {
	cloud, err := referencesIncludeCloud(opts)
	if err != nil {
		return nil, nil, err
	}
	var session *uriio.Session
	if cloud {
		session, err = newCloudSession(opts, runID)
		if err != nil {
			return nil, nil, err
		}
	}
	refs, err := discoverInputReferences(ctx, session, opts)
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		return nil, nil, err
	}
	return refs, session, nil
}

// resolveInputSources discovers and acquires the run's input files through the
// uriio read boundary. It returns the local read paths (in discovery order), a
// localPath->logicalURI map for the staged cloud sources, and the run session
// (nil for an all-local run). The caller owns Close on the returned session.
func resolveInputSources(ctx context.Context, opts *ExtractOptions, runID string) ([]string, map[string]string, *uriio.Session, error) {
	cloud, err := referencesIncludeCloud(opts)
	if err != nil {
		return nil, nil, nil, err
	}
	var session *uriio.Session
	if cloud {
		session, err = newCloudSession(opts, runID)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	refs, err := discoverInputReferences(ctx, session, opts)
	if err != nil {
		if session != nil {
			_ = session.Close()
		}
		return nil, nil, nil, err
	}

	files := make([]string, 0, len(refs))
	logicalByLocal := make(map[string]string, len(refs))
	for _, ref := range refs {
		var src *uriio.AcquiredSource
		if session != nil {
			src, err = session.Acquire(ctx, ref, resolvedInputHandle(opts))
		} else {
			src, err = uriio.Acquire(ctx, uriio.AcquireRequest{Reference: ref})
		}
		if err != nil {
			if session != nil {
				_ = session.Close()
			}
			return nil, nil, nil, fmt.Errorf("resolve input %s: %w", ref, err)
		}
		files = append(files, src.LocalPath)
		// Only cloud sources carry a distinct logical identity. file:// stays a
		// verbose alias for its local path (byte-identical provenance/output to the
		// bare path), so it is intentionally not recorded here.
		if src.Scheme != uriio.SchemeLocal {
			logicalByLocal[src.LocalPath] = src.LogicalURI
		}
	}
	return files, logicalByLocal, session, nil
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

	// Discovery visibility: a local --input-path run walks the ENTIRE tree before the
	// include pattern filters files (filename-only patterns like *.xml can't prune
	// directories), so on a large mixed-grain corpus there is a multi-minute stall
	// before any record is produced — and an accidental over-scope is invisible.
	// Surface the enumeration as a legible phase: announce it, report the matched
	// count + elapsed time, and warn loudly when the walk is slow.
	logger := logging.GetLogger()
	logger.Info("Enumerating input files",
		zap.String("root", absInput),
		zap.String("include", opts.IncludePattern),
		zap.String("exclude", opts.ExcludePattern),
		zap.Int("max_depth", opts.MaxDepth))

	start := time.Now()
	results, err := facade.Find(query)
	if err != nil {
		return nil, fmt.Errorf("failed to discover files from %s: %w", absInput, err)
	}
	elapsed := time.Since(start)

	files := make([]string, 0, len(results))
	for _, result := range results {
		files = append(files, filepath.Clean(filepath.FromSlash(result.SourcePath)))
	}

	logger.Info("Input discovery complete",
		zap.String("root", absInput),
		zap.Int("matched", len(files)),
		zap.Duration("elapsed", elapsed))
	if elapsed >= slowInputDiscoveryThreshold {
		logger.Warn("Input enumeration was slow: sumpter walked the whole tree under --input-path before --include-pattern filtered it (filename-only patterns cannot prune directories). For large or precisely-scoped sets, prefer --file-list (an explicit list, no walk), a narrower --input-path, or --exclude-pattern to skip known-large subtrees",
			zap.String("root", absInput),
			zap.Duration("elapsed", elapsed),
			zap.Int("matched", len(files)),
			zap.String("include", opts.IncludePattern))
	}

	return files, nil
}

// slowInputDiscoveryThreshold is the elapsed-time bound past which a local
// --input-path enumeration is flagged as slow, so an over-broad walk is loud rather
// than an unexplained stall.
const slowInputDiscoveryThreshold = 5 * time.Second

func writeRecordsToFile(opts *ExtractOptions, filename string, records []map[string]interface{}) error {
	// Route the destination through the uriio seam. Local destinations resolve to
	// their own path and Publish is a no-op; a cloud destination resolves to a
	// staging file that Publish uploads only after every record is written, so a
	// mid-write failure returns before Publish and leaves no object.
	tgt, err := openOutputTarget(context.Background(), opts, filename)
	if err != nil {
		return err
	}
	localPath := tgt.LocalPath

	if err := os.MkdirAll(filepath.Dir(localPath), 0o750); err != nil {
		return err
	}
	file, err := os.Create(localPath) // #nosec G304 - localPath is a staging path or a user-provided output path
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
	// Close before publishing so the staged artifact is fully flushed; only then
	// upload. A close error must not be masked.
	if err := file.Close(); err != nil {
		return fmt.Errorf("close output %s: %w", tgt.LogicalURI, err)
	}
	return tgt.Publish(context.Background())
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
