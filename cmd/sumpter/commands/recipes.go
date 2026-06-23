package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
	"time"

	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	regulatory "github.com/fulmenhq/sumpter/internal/retrieve/recipe/finance/regulatory"
	"github.com/fulmenhq/sumpter/internal/utils"
	"github.com/spf13/cobra"
)

type templateData struct {
	RecipeID  string
	CreatedAt string
}

func NewRecipesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipes",
		Short: "Manage recipes for acquisition and extraction workflows",
	}

	cmd.AddCommand(newRecipeInitCommand())
	cmd.AddCommand(newRecipeRetrieveCommand())
	cmd.AddCommand(newRecipeRunCommand())
	cmd.AddCommand(newRecipeMigrateCommand())

	return cmd
}

func newRecipeMigrateCommand() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:   "migrate <path>",
		Short: "Stamp content_version on legacy recipe manifests",
		Long: `Stamp a starter content_version on recipe manifests that predate
ADR-0006. Per the migration policy, manifests missing content_version receive
` + fmt.Sprintf("content_version: %q", recipesmanifest.StarterContentVersion) + ` and authors are expected to manage the
value going forward. v0.1.4 will treat missing content_version as a hard error.

When <path> is a file, that single manifest is migrated. When <path> is a
directory, the command walks the tree and migrates every file named
` + fmt.Sprintf("%q", recipesmanifest.ManifestFileName) + `. The operation is idempotent — manifests that already
declare content_version are reported and left untouched.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRecipeMigrate(cmd, args[0], dryRun)
		},
	}

	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Report what would change without modifying any files")

	return cmd
}

func runRecipeMigrate(cmd *cobra.Command, path string, dryRun bool) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", absPath, err)
	}

	var targets []string
	if info.IsDir() {
		walkErr := filepath.WalkDir(absPath, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Base(p) == recipesmanifest.ManifestFileName {
				targets = append(targets, p)
			}
			return nil
		})
		if walkErr != nil {
			return fmt.Errorf("failed to walk %s: %w", absPath, walkErr)
		}
		if len(targets) == 0 {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "No %s files found under %s\n", recipesmanifest.ManifestFileName, absPath); err != nil {
				return fmt.Errorf("failed to write summary: %w", err)
			}
			return nil
		}
	} else {
		targets = []string{absPath}
	}

	var stamped, alreadyStamped int
	for _, target := range targets {
		action, err := recipesmanifest.MigrateFile(target, dryRun)
		if err != nil {
			return err
		}
		prefix := "stamped"
		if action == recipesmanifest.MigrationAlreadyStamped {
			prefix = "already-stamped"
			alreadyStamped++
		} else {
			stamped++
		}
		suffix := ""
		if dryRun && action == recipesmanifest.MigrationStamped {
			suffix = " (dry-run)"
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s: %s%s\n", prefix, target, suffix); err != nil {
			return fmt.Errorf("failed to write status: %w", err)
		}
	}

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Summary: %d stamped, %d already-stamped (dry-run=%t)\n", stamped, alreadyStamped, dryRun); err != nil {
		return fmt.Errorf("failed to write summary: %w", err)
	}
	return nil
}

func newRecipeInitCommand() *cobra.Command {
	var (
		basePath string
		recipeID string
		gitInit  bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold a new recipe workspace",
		RunE: func(cmd *cobra.Command, args []string) error {
			if basePath == "" {
				return errors.New("--path is required")
			}

			absPath, err := filepath.Abs(basePath)
			if err != nil {
				return fmt.Errorf("failed to resolve path: %w", err)
			}

			if err := ensureEmptyOrMissing(absPath); err != nil {
				return err
			}

			data := templateData{
				RecipeID:  recipeID,
				CreatedAt: time.Now().UTC().Format(time.RFC3339),
			}

			dirs := []string{
				filepath.Join(absPath, "signature"),
				filepath.Join(absPath, "extract"),
				filepath.Join(absPath, "validation"),
				filepath.Join(absPath, "testdata"),
				filepath.Join(absPath, "outputs"),
			}
			for _, dir := range dirs {
				if err := os.MkdirAll(dir, 0o750); err != nil {
					return fmt.Errorf("failed to create directory %s: %w", dir, err)
				}
			}

			templateFS, err := assets.GetTemplatesFS()
			if err != nil {
				return fmt.Errorf("failed to access embedded templates: %w", err)
			}

			files := map[string]string{
				filepath.Join(absPath, "README.md"):   "commands/recipe/README.md.tmpl",
				filepath.Join(absPath, "recipe.yaml"): "commands/recipe/recipe.yaml.tmpl",
			}

			for target, source := range files {
				if err := renderTemplate(templateFS, source, target, data); err != nil {
					return err
				}
			}

			if gitInit {
				if err := runGitInit(absPath); err != nil {
					return err
				}
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Recipe scaffold created at %s\n", absPath); err != nil {
				return fmt.Errorf("failed to write scaffold confirmation: %w", err)
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&basePath, "path", "", "Target directory for the new recipe (required)")
	cmd.Flags().StringVar(&recipeID, "id", "", "Recipe identifier used in templates")
	cmd.Flags().BoolVar(&gitInit, "git-init", false, "Initialize a git repository in the recipe directory")

	return cmd
}

func newRecipeRetrieveCommand() *cobra.Command {
	defaultOutput := filepath.Join(".scratchpad", "work")
	if paths, err := config.ResolvePaths("", ""); err == nil {
		defaultOutput = paths.WorkDir
	}

	cmd := &cobra.Command{
		Use:   "retrieve <realm> <domain-tag>",
		Short: "Run a data acquisition recipe for a specific realm and domain",
		Long: `Execute data acquisition recipes for specific realms and domains.

Supported realms: finance
Supported domain-tags: sec-edgar

Configuration is loaded from retrieve.yaml (use --config-path to override).
For finance realm, configure user_agent for SEC compliance.

Example: sumpter recipes retrieve finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			realm := args[0]
			domainTag := args[1]

			if !config.IsValidRealm(realm) {
				return fmt.Errorf("unsupported realm: %s (supported realms: %v)", realm, config.ValidRealms())
			}

			outputBase, err := cmd.Flags().GetString("output-base")
			if err != nil {
				return err
			}
			configPath, err := cmd.Flags().GetString("config-path")
			if err != nil {
				return err
			}
			ticker, err := cmd.Flags().GetString("ticker")
			if err != nil {
				return err
			}
			filingType, err := cmd.Flags().GetString("filing-type")
			if err != nil {
				return err
			}
			year, err := cmd.Flags().GetString("year")
			if err != nil {
				return err
			}

			return runRecipeRetrieve(realm, domainTag, outputBase, configPath, ticker, filingType, year)
		},
	}

	cmd.Flags().String("output-base", defaultOutput, "Base output directory for recipe artifacts")
	cmd.Flags().String("config-path", "", "Path to retrieve configuration file (default: SUMPTER_HOME/configs/retrieve.yaml)")
	cmd.Flags().String("ticker", "", "Stock ticker symbol (for finance/sec-edgar)")
	cmd.Flags().String("filing-type", "", "Filing type (e.g., 10-K, 10-Q) (for finance/sec-edgar)")
	cmd.Flags().String("year", "", "Filing year (for finance/sec-edgar)")

	_ = cmd.MarkFlagRequired("ticker")
	_ = cmd.MarkFlagRequired("filing-type")
	_ = cmd.MarkFlagRequired("year")

	return cmd
}

func newRecipeRunCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Execute a recipe using its manifest defaults",
	}

	cmd.AddCommand(newRecipeRunExtractCommand())
	cmd.AddCommand(newRecipeRunExtractMultiCommand())

	return cmd
}

func newRecipeRunExtractCommand() *cobra.Command {
	opts := &recipeRunExtractOptions{}

	cmd := &cobra.Command{
		Use:   "extract <workspace>",
		Short: "Execute an extract recipe defined in recipe.yaml",
		Long: `Execute an extract recipe defined in recipe.yaml.

A recipe's input and output may be S3-compatible cloud URIs (s3://) using
credential handles — declared inline as defaults.{input,output}.credentials_handle
and resolved from --credentials at run time, or selected with
--input-credentials-handle / --output-credentials-handle. See
docs/extract-workflow.md "Cloud Sources and Outputs".`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("format") && cmd.Flags().Changed("formats") {
				return fmt.Errorf("--format and --formats are mutually exclusive")
			}
			workspace := args[0]
			return executeExtractRecipe(cmd, workspace, opts)
		},
	}

	cmd.Flags().StringVar(&opts.ManifestPath, "manifest", "recipe.yaml", "Path to recipe manifest relative to workspace")
	cmd.Flags().StringVar(&opts.Files, "files", "", "Comma-separated list of files to process (overrides manifest; short ad hoc sets — use --file-list for large batches)")
	cmd.Flags().StringVar(&opts.FileList, "file-list", "", "Path to a newline-delimited file listing input references (local or s3://), one per line; # comments ignored. No walk, no argv limit (overrides manifest). Mutually exclusive with --files/--input-path")
	cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Directory of XML files to process; walks and filters by include/exclude patterns — for large or precisely-scoped sets prefer --file-list (overrides manifest)")
	cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "", "Override manifest include pattern")
	cmd.Flags().StringVar(&opts.ExcludePattern, "exclude-pattern", "", "Override manifest exclude pattern")
	cmd.Flags().IntVar(&opts.MaxDepth, "max-depth", -1, "Override manifest max depth")
	cmd.Flags().BoolVar(&opts.FollowSymlinks, "follow-symlinks", false, "Follow symlinks (overrides manifest)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "Preview files without processing")
	cmd.Flags().BoolVar(&opts.ContinueOnError, "continue-on-error", false, "Continue processing sibling files after recoverable per-file failures; requires --output-path")
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
	cmd.Flags().IntVar(&opts.Workers, "workers", 0, "Number of parallel workers (overrides manifest)")
	cmd.Flags().StringVar(&opts.Format, "format", "", "Override output format")
	cmd.Flags().StringSliceVar(&opts.Formats, "formats", nil, "Override output formats (comma-separated or repeatable; json/ndjson/parquet)")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", "", "Override output path")
	cmd.Flags().StringVar(&opts.OutputPattern, "output-pattern", "", "Override output filename pattern")
	cmd.Flags().StringVar(&opts.OutputMode, "output-mode", outputModePerInput, "Record-file fan-out: per-input (one file per input) or aggregate (stream all inputs to one NDJSON writer per invocation, rolling to numbered shards). Aggregate requires --output-path + a manifest and is JSON/NDJSON only")
	cmd.Flags().IntVar(&opts.AggregateMaxRecords, "aggregate-max-records", 0, "Aggregate mode: roll to the next shard before exceeding this record count per shard (0 = uncapped)")
	cmd.Flags().Int64Var(&opts.AggregateMaxBytes, "aggregate-max-bytes", 0, "Aggregate mode: roll to the next shard before exceeding this uncompressed byte count per shard (0 = uncapped)")
	cmd.Flags().StringVar(&opts.ClientID, "client-id", "", "Blend client identifier into extracted records")
	cmd.Flags().StringVar(&opts.SiteID, "site-id", "", "Blend site identifier into extracted records")
	cmd.Flags().StringArrayVar(&opts.Parameters, "parameter", nil, "Inject a key=value pair into every record (repeatable, overrides manifest defaults.parameters). Value is a literal string unless it is a JSON array of strings, e.g. --parameter prefixes='[\"NM_\",\"NR_\"]', which becomes a list parameter")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "UUIDv7 run identifier for deterministic replay (overrides SUMPTER_RUN_ID)")
	cmd.Flags().BoolVar(&opts.NoManifest, "no-manifest", false, "Disable provenance sidecar manifest output")
	cmd.Flags().StringVar(&opts.SignatureOverride, "signature", "", "Override manifest signature config path")
	cmd.Flags().StringVar(&opts.ExtractOverride, "extract", "", "Override manifest extract config path")
	cmd.Flags().StringArrayVar(&opts.ReferenceTableOverrides, "reference-table", nil, "Override a declared reference table's source: name=source (repeatable). Source is a contained workspace-relative path (no absolute, \"..\", or symlinks) or an s3:// URI reusing the table's declared credentials_handle. Format, columns, and caps stay recipe-declared")
	cmd.Flags().StringVar(&opts.CredentialsPath, "credentials", "", "Path to a cloud credentials config (named handles; no secrets in recipe YAML)")
	cmd.Flags().StringArrayVar(&opts.CredentialOverrides, "credential", nil, "Override a handle's AWS profile: handle=profile (repeatable; references only, never a raw key)")
	cmd.Flags().StringVar(&opts.InputCredentialsHandle, "input-credentials-handle", "", "Credential handle name for cloud (s3://) source input; overrides the recipe's defaults.input.credentials_handle")
	cmd.Flags().StringVar(&opts.OutputCredentialsHandle, "output-credentials-handle", "", "Credential handle name for cloud (s3://) output; overrides the recipe's defaults.output.credentials_handle")

	return cmd
}

type recipeRunExtractOptions struct {
	ManifestPath            string
	Files                   string
	FileList                string
	InputPath               string
	IncludePattern          string
	ExcludePattern          string
	MaxDepth                int
	FollowSymlinks          bool
	DryRun                  bool
	ContinueOnError         bool
	Progress                bool
	Workers                 int
	Format                  string
	Formats                 []string
	OutputPath              string
	OutputPattern           string
	OutputMode              string
	AggregateMaxRecords     int
	AggregateMaxBytes       int64
	ClientID                string
	SiteID                  string
	Parameters              []string
	ReferenceTableOverrides []string
	RunID                   string
	NoManifest              bool
	SignatureOverride       string
	ExtractOverride         string
	// Cloud credential options (handle references — no secrets in recipe YAML or
	// on the CLI). They mirror the extract command and let a recipe run read
	// from / write to s3:// destinations.
	CredentialsPath         string
	CredentialOverrides     []string
	InputCredentialsHandle  string
	OutputCredentialsHandle string
}

func executeExtractRecipe(cmd *cobra.Command, workspace string, opts *recipeRunExtractOptions) error {
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("failed to resolve workspace: %w", err)
	}

	manifestPath := opts.ManifestPath
	if !filepath.IsAbs(manifestPath) {
		manifestPath = filepath.Join(absWorkspace, manifestPath)
	}

	manifest, err := recipesmanifest.LoadManifest(manifestPath)
	if err != nil {
		return err
	}
	manifestBytes, err := os.ReadFile(manifestPath) // #nosec G304 - path resolved from explicit recipe workspace/manifest flag
	if err != nil {
		return fmt.Errorf("failed to read recipe manifest for provenance: %w", err)
	}

	// Emit ADR-0006 deprecation warnings (e.g. missing content_version)
	// exactly once per load. DrainWarnings clears the slice so the same
	// messages do not surface if the manifest is reloaded mid-session.
	for _, warning := range manifest.DrainWarnings() {
		if _, werr := fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", warning); werr != nil {
			return fmt.Errorf("failed to emit manifest warning: %w", werr)
		}
	}

	if manifest.Kind != recipesmanifest.KindExtract {
		return fmt.Errorf("manifest kind %s is not supported by run extract", manifest.Kind)
	}

	signaturePath := manifest.Assets.Signature
	extractPath := manifest.Assets.Extract
	applicabilityPath := manifest.Assets.Applicability

	if opts.SignatureOverride != "" {
		signaturePath = opts.SignatureOverride
	}
	if opts.ExtractOverride != "" {
		extractPath = opts.ExtractOverride
	}

	var applicabilityCfg *extract.ApplicabilityConfig
	var applicabilityBytes []byte
	if strings.TrimSpace(applicabilityPath) != "" {
		asset, err := recipesmanifest.OpenRelativeFile(absWorkspace, applicabilityPath)
		if err != nil {
			return fmt.Errorf("failed to open applicability asset: %w", err)
		}
		if err := asset.Close(); err != nil {
			return fmt.Errorf("failed to close applicability asset: %w", err)
		}
		applicabilityPath = recipesmanifest.ResolvePath(absWorkspace, applicabilityPath)
		applicabilityBytes, err = os.ReadFile(applicabilityPath) // #nosec G304 - path resolved from validated recipe asset path
		if err != nil {
			return fmt.Errorf("failed to read applicability asset for provenance: %w", err)
		}
		applicabilityCfg, err = extract.LoadApplicabilityConfig(applicabilityPath)
		if err != nil {
			return err
		}
	}

	signaturePath = recipesmanifest.ResolvePath(absWorkspace, signaturePath)
	extractPath = recipesmanifest.ResolvePath(absWorkspace, extractPath)

	signatureBytes, err := os.ReadFile(signaturePath) // #nosec G304 - path resolved from recipe manifest or explicit CLI override
	if err != nil {
		return fmt.Errorf("failed to read signature asset for provenance: %w", err)
	}
	extractBytes, err := os.ReadFile(extractPath) // #nosec G304 - path resolved from recipe manifest or explicit CLI override
	if err != nil {
		return fmt.Errorf("failed to read extract asset for provenance: %w", err)
	}
	recipeContentHash, err := provenance.RecipeContentHash(signatureBytes, extractBytes, applicabilityBytes)
	if err != nil {
		return fmt.Errorf("failed to compute recipe content hash: %w", err)
	}

	// Retrieve the allow-large-files flag from the persistent flags
	allowLargeFiles, err := cmd.InheritedFlags().GetBool("allow-large-files")
	if err != nil {
		return fmt.Errorf("failed to get allow-large-files flag: %w", err)
	}

	extractOpts := &ExtractOptions{
		SignatureConfig:     signaturePath,
		ExtractConfig:       extractPath,
		ApplicabilityConfig: applicabilityCfg,
		ContinueOnError:     opts.ContinueOnError,
		AllowLargeFiles:     allowLargeFiles,
		RunID:               opts.RunID,
		NoManifest:          opts.NoManifest,
		CommandName:         "sumpter recipes run extract",
		RuntimeProvenance: provenance.RuntimeOptions{
			RecipeVersion:     manifest.ContentVersion,
			RecipeContentHash: recipeContentHash,
		},
		Recipe: buildRecipeProvenance(manifest, manifestBytes, signatureBytes, extractBytes, applicabilityBytes, recipeContentHash),
	}

	defaults := manifest.Defaults
	sourceExtractionInput := defaults.Input
	if sourceExtractionInput.Path != "" {
		sourceExtractionInput.Path = resolveMaybeRelative(absWorkspace, sourceExtractionInput.Path)
	}

	// Input resolution. A CLI input flag (--files / --file-list / --input-path)
	// overrides the manifest input; each provided flag is set so a conflicting CLI
	// combination is caught by runExtract's single-mode check. Only when no CLI input
	// flag is given does the manifest's input mode apply (files / files_from / path).
	cliInput := opts.FileList != "" || opts.Files != "" || opts.InputPath != ""
	if opts.FileList != "" {
		extractOpts.FileList = resolveMaybeRelative(absWorkspace, opts.FileList)
	}
	if opts.Files != "" {
		extractOpts.Files = opts.Files
	}
	if opts.InputPath != "" {
		extractOpts.InputPath = resolveMaybeRelative(absWorkspace, opts.InputPath)
		sourceExtractionInput.Path = extractOpts.InputPath
	}
	if !cliInput {
		switch {
		case defaults.Input.Mode == "files" && len(defaults.Input.Files) > 0:
			var resolved []string
			for _, file := range defaults.Input.Files {
				resolved = append(resolved, resolveMaybeRelative(absWorkspace, file))
			}
			extractOpts.Files = strings.Join(resolved, ",")
		case strings.TrimSpace(defaults.Input.FilesFrom) != "":
			extractOpts.FileList = resolveMaybeRelative(absWorkspace, defaults.Input.FilesFrom)
		case defaults.Input.Path != "":
			extractOpts.InputPath = resolveMaybeRelative(absWorkspace, defaults.Input.Path)
			sourceExtractionInput.Path = extractOpts.InputPath
		}
	}

	// Include/exclude patterns
	if opts.IncludePattern != "" {
		extractOpts.IncludePattern = opts.IncludePattern
	} else {
		extractOpts.IncludePattern = defaults.Input.IncludePattern
	}

	if opts.ExcludePattern != "" {
		extractOpts.ExcludePattern = opts.ExcludePattern
	} else {
		extractOpts.ExcludePattern = defaults.Input.ExcludePattern
	}

	if opts.MaxDepth >= 0 {
		extractOpts.MaxDepth = opts.MaxDepth
	} else {
		extractOpts.MaxDepth = defaults.Input.MaxDepth
	}

	if cmd.Flags().Changed("follow-symlinks") {
		extractOpts.FollowSymlinks = opts.FollowSymlinks
	} else {
		extractOpts.FollowSymlinks = defaults.Input.FollowSymlinks
	}
	sourceExtractionInput.FollowSymlinks = extractOpts.FollowSymlinks

	// Output controls
	if len(opts.Formats) > 0 {
		extractOpts.Formats = opts.Formats
		extractOpts.Format = ""
	} else if opts.Format != "" {
		extractOpts.Format = opts.Format
	} else {
		formats, err := defaults.Output.FormatsOrDefault()
		if err != nil {
			return err
		}
		extractOpts.Formats = formats
		if len(formats) > 0 {
			extractOpts.Format = formats[0]
		}
	}

	if opts.OutputPath != "" {
		extractOpts.OutputPath = resolveMaybeRelative(absWorkspace, opts.OutputPath)
	} else if defaults.Output.Path != "" {
		extractOpts.OutputPath = resolveMaybeRelative(absWorkspace, defaults.Output.Path)
	}

	if opts.OutputPattern != "" {
		extractOpts.OutputPattern = opts.OutputPattern
	} else {
		extractOpts.OutputPattern = defaults.Output.Pattern
		extractOpts.OutputPatterns = defaults.Output.Patterns
	}
	extractOpts.OutputMode = opts.OutputMode
	extractOpts.AggregateMaxRecords = opts.AggregateMaxRecords
	extractOpts.AggregateMaxBytes = opts.AggregateMaxBytes

	// Cloud credentials are handle references — no secrets in recipe YAML or on the
	// CLI. The credentials config + handle selectors flow through to the extract
	// read/write boundaries; CLI selectors override the recipe's declared handles
	// (precedence: CLI > recipe > the default handle).
	extractOpts.CredentialsPath = opts.CredentialsPath
	extractOpts.CredentialOverrides = opts.CredentialOverrides
	if strings.TrimSpace(opts.InputCredentialsHandle) != "" {
		extractOpts.InputCredentialsHandle = opts.InputCredentialsHandle
	} else {
		extractOpts.InputCredentialsHandle = defaults.Input.CredentialsHandle
	}
	if strings.TrimSpace(opts.OutputCredentialsHandle) != "" {
		extractOpts.OutputCredentialsHandle = opts.OutputCredentialsHandle
	} else {
		extractOpts.OutputCredentialsHandle = defaults.Output.CredentialsHandle
	}
	parquetCompression, err := defaults.Output.ParquetCompression()
	if err != nil {
		return err
	}
	extractOpts.ParquetCompression = parquetCompression
	extractOpts.UniformSchema = defaults.Output.UniformSchema
	if defaults.Output.Parquet != nil && len(defaults.Output.Parquet.WithholdColumns) > 0 {
		extractOpts.ParquetWithholdColumns = append([]string(nil), defaults.Output.Parquet.WithholdColumns...)
	}

	if cmd.Flags().Changed("workers") {
		extractOpts.Workers = opts.Workers
	} else {
		extractOpts.Workers = defaults.Workers
	}

	if cmd.Flags().Changed("progress") {
		extractOpts.Progress = opts.Progress
	} else {
		extractOpts.Progress = defaults.Progress
	}

	if cmd.Flags().Changed("dry-run") {
		extractOpts.DryRun = opts.DryRun
	}

	if opts.ClientID != "" {
		extractOpts.ClientID = opts.ClientID
	} else {
		extractOpts.ClientID = defaults.ClientID
	}

	if opts.SiteID != "" {
		extractOpts.SiteID = opts.SiteID
	} else {
		extractOpts.SiteID = defaults.SiteID
	}
	extractOpts.ManifestParameters = defaults.Parameters
	extractOpts.ParametersRequired = defaults.ParametersRequired
	extractOpts.Parameters = opts.Parameters
	// Reference tables: declarations come from the recipe; absWorkspace is the C1
	// containment root for their local sources; CLI overrides replace a source only.
	extractOpts.ReferenceTableDecls = defaults.ReferenceTables
	extractOpts.ReferenceTableRoot = absWorkspace
	extractOpts.ReferenceTableOverrides = opts.ReferenceTableOverrides
	extractOpts.SourceExtraction = defaults.SourceExtraction
	extractOpts.SourceExtractionRequired = defaults.SourceExtractionRequired
	extractOpts.SourceExtractionInput = sourceExtractionInput
	extractOpts.SourceExtractionRecipeID = manifest.ID

	if extractOpts.Files == "" && extractOpts.FileList == "" && extractOpts.InputPath == "" {
		return errors.New("no input source resolved: provide --files, --file-list, or --input-path, or define defaults.input in recipe.yaml")
	}
	extractOpts.Argv = buildRecipeExtractArgv(workspace, opts, extractOpts)

	return runExtract(extractOpts)
}

func buildRecipeProvenance(manifest *recipesmanifest.Manifest, manifestBytes, signatureBytes, extractBytes, applicabilityBytes []byte, contentHash string) *provenance.Recipe {
	if manifest == nil {
		return nil
	}
	owners := make([]provenance.Owner, 0, len(manifest.Owners))
	for _, owner := range manifest.Owners {
		owners = append(owners, provenance.Owner{
			Name:    owner.Name,
			Contact: owner.Contact,
			Role:    owner.Role,
		})
	}
	return &provenance.Recipe{
		ID:                    manifest.ID,
		ManifestSchemaVersion: manifest.Version,
		ContentVersion:        manifest.ContentVersion,
		ContentHash:           contentHash,
		Owners:                owners,
		Cadence:               manifest.Defaults.Cadence,
		ManifestYAML:          string(manifestBytes),
		SignatureYAML:         string(signatureBytes),
		ExtractYAML:           string(extractBytes),
		ApplicabilityYAML:     string(applicabilityBytes),
	}
}

func buildRecipeExtractArgv(workspace string, opts *recipeRunExtractOptions, extractOpts *ExtractOptions) []string {
	args := []string{"recipes", "run", "extract", workspace}
	if opts == nil || extractOpts == nil {
		return args
	}
	// The cloud credential flags (--credentials, --credential, --input/output-
	// credentials-handle) are intentionally omitted from the recorded argv: they
	// are operator/environment-specific (a local config path, a profile reference)
	// rather than part of the portable recipe-run invocation, and the recipe's
	// declared handle names already capture the intent. The credentials config
	// itself never contains run-portable state worth replaying here.
	appendFlag := func(name, value string) {
		if strings.TrimSpace(value) != "" {
			args = append(args, name+"="+value)
		}
	}
	appendFlag("--manifest", opts.ManifestPath)
	appendFlag("--files", extractOpts.Files)
	appendFlag("--file-list", extractOpts.FileList)
	appendFlag("--input-path", extractOpts.InputPath)
	appendFlag("--include-pattern", extractOpts.IncludePattern)
	appendFlag("--exclude-pattern", extractOpts.ExcludePattern)
	if len(extractOpts.Formats) > 1 {
		appendFlag("--formats", strings.Join(extractOpts.Formats, ","))
	} else {
		appendFlag("--format", extractOpts.Format)
	}
	appendFlag("--output-path", extractOpts.OutputPath)
	appendFlag("--output-pattern", extractOpts.OutputPattern)
	if extractOpts.OutputMode != "" && extractOpts.OutputMode != outputModePerInput {
		appendFlag("--output-mode", extractOpts.OutputMode)
	}
	if extractOpts.AggregateMaxRecords > 0 {
		appendFlag("--aggregate-max-records", fmt.Sprintf("%d", extractOpts.AggregateMaxRecords))
	}
	if extractOpts.AggregateMaxBytes > 0 {
		appendFlag("--aggregate-max-bytes", fmt.Sprintf("%d", extractOpts.AggregateMaxBytes))
	}
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
	return args
}

func resolveMaybeRelative(base, candidate string) string {
	if candidate == "" {
		return ""
	}
	// A scheme-qualified reference (s3://, file://, or an as-yet-unsupported
	// scheme) is an absolute URI, never a workspace-relative path. Return it
	// verbatim so uriio classifies it downstream: cloud refs route through the
	// read/write boundary, file:// normalizes to its local path, and an
	// unsupported scheme gets the actionable uriio.ErrUnsupportedScheme rejection
	// in resolveLocalReferences / resolveInputSources — rather than being mangled
	// into a workspace-local path like "<workspace>/gs:/bucket/key".
	if strings.Contains(candidate, "://") {
		return candidate
	}
	if filepath.IsAbs(candidate) {
		return candidate
	}
	return filepath.Join(base, candidate)
}

func runRecipeRetrieve(realm, domainTag, outputBase, configPath, ticker, filingType, year string) error {
	switch realm {
	case "finance":
		return runFinanceRecipe(domainTag, outputBase, configPath, ticker, filingType, year)
	default:
		return fmt.Errorf("unsupported realm: %s", realm)
	}
}

func runFinanceRecipe(domainTag, outputBase, configPath, ticker, filingType, year string) error {
	switch domainTag {
	case "sec-edgar":
		return runSecEdgarRecipe(outputBase, configPath, ticker, filingType, year)
	default:
		return fmt.Errorf("unsupported finance domain-tag: %s", domainTag)
	}
}

func runSecEdgarRecipe(outputBase, configPath, ticker, filingType, year string) error {
	if err := ensureWritableDir(outputBase); err != nil {
		return fmt.Errorf("output directory validation failed: %w", err)
	}

	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return fmt.Errorf("failed to resolve paths: %w", err)
	}

	loader := config.NewLoader(paths)
	retrieveConfig, err := loader.LoadRetrieveConfig(configPath)
	if err != nil {
		return fmt.Errorf("failed to load retrieve config: %w", err)
	}

	realmConfig, exists := retrieveConfig.Realms["finance"]
	if !exists {
		return fmt.Errorf("finance realm not configured in retrieve config")
	}

	if realmConfig.Client.UserAgent == "" {
		return fmt.Errorf("user_agent is required in finance realm config for SEC compliance (set in retrieve.yaml)")
	}

	client := regulatory.NewSecEdgarClient(realmConfig.Client.UserAgent, realmConfig.RateLimits.RequestsPerSecond)
	defer client.Close()

	return client.DownloadFiling(ticker, filingType, year, outputBase)
}

func ensureEmptyOrMissing(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("path %s exists and is not a directory", path)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Errorf("failed to inspect existing directory: %w", err)
		}
		if len(entries) > 0 {
			return fmt.Errorf("path %s already exists and is not empty", path)
		}
		return nil
	}

	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(path, 0o750); mkErr != nil {
			return fmt.Errorf("failed to create directory %s: %w", path, mkErr)
		}
		return nil
	}

	return fmt.Errorf("failed to stat path %s: %w", path, err)
}

func renderTemplate(fsys fs.FS, source, target string, data templateData) (err error) {
	tmplBytes, err := fs.ReadFile(fsys, source)
	if err != nil {
		return fmt.Errorf("failed to read template %s: %w", source, err)
	}

	tmpl, err := template.New(filepath.Base(source)).Parse(string(tmplBytes))
	if err != nil {
		return fmt.Errorf("failed to parse template %s: %w", source, err)
	}

	// Get current working directory for path validation
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Validate user-provided target path
	if err := utils.ValidateUserPathForCreate(target, utils.RootCwd, cwd); err != nil {
		return fmt.Errorf("invalid target path: %w", err)
	}

	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 - Path validated by ValidateUserPathForCreate
	if err != nil {
		return fmt.Errorf("failed to create file %s: %w", target, err)
	}
	defer func() {
		if cerr := file.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("failed to close file %s: %w", target, cerr)
		}
	}()

	if err := tmpl.Execute(file, data); err != nil {
		return fmt.Errorf("failed to render template %s: %w", source, err)
	}

	return nil
}

func runGitInit(path string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = path
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git init failed: %w", err)
	}
	return nil
}

func ensureWritableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("directory path cannot be empty")
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("cannot create directory: %s: %w", dir, err)
	}

	probe := filepath.Join(dir, ".sumpter-recipe-write-test")
	if err := os.WriteFile(probe, []byte("ok"), 0o600); err != nil {
		return fmt.Errorf("directory is not writable: %s: %w", dir, err)
	}

	_ = os.Remove(probe)
	return nil
}
