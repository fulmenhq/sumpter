package commands

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/fulmenhq/sumpter/internal/provenance"
)

// recipeRunExtractMultiOptions holds the shared, run-level flags for an
// extract-multi run. Output destination, formats, reference tables, and
// credential handles stay per recipe (from each recipe's manifest); the input
// set, the output ROOT, run-level controls, and the shared --parameter override
// layer are shared here. Per-recipe defaults.parameters stay authoritative for
// recipe config; --parameter carries only the genuinely run-level keys every
// recipe shares, layered over each recipe's defaults exactly as single-recipe
// `recipes run extract --parameter` is.
type recipeRunExtractMultiOptions struct {
	Files                  string
	FileList               string
	InputPath              string
	IncludePattern         string
	ExcludePattern         string
	MaxDepth               int
	FollowSymlinks         bool
	OutputPath             string
	ContinueOnError        bool
	Progress               bool
	RunID                  string
	NoManifest             bool
	Parameters             []string
	OutputMode             string
	AggregateMaxRecords    int
	AggregateMaxBytes      int64
	CredentialsPath        string
	CredentialOverrides    []string
	InputCredentialsHandle string
}

func newRecipeRunExtractMultiCommand() *cobra.Command {
	opts := &recipeRunExtractMultiOptions{}

	cmd := &cobra.Command{
		Use:   "extract-multi <workspace>...",
		Short: "Apply multiple extract recipes to one input set in a single parse-once pass",
		Long: `Apply multiple extract recipes to one shared input set, reading and parsing
each input file exactly once and dispatching the parsed document to every
recipe. At high file counts this amortizes the per-recipe re-parse — the
dominant cost — instead of opening and parsing each file once per recipe.

Each recipe writes to its own subdirectory under --output-path
(<output-path>/<recipe-id>/): records, the provenance manifest, and (when
applicable) dispositions.json / failures.json. Output, formats, reference
tables, and credential handles are per recipe (from each recipe's manifest);
the input set, the output root, and run-level controls are shared.

Each recipe's defaults.parameters stay authoritative for per-recipe config. A
shared run-level --parameter key=value (repeatable) is layered over every
recipe's defaults.parameters with the same override/collision/typed-value
semantics as single-recipe "recipes run extract", carrying the genuinely
per-run keys every recipe shares (e.g. a per-run provenance stamp).

Scope (v0): JSON/NDJSON output only — a recipe declaring another format is
rejected; run it with single-recipe "recipes run extract" instead. The
streaming/large-file path is not supported: a file large enough to route to
streaming is rejected (--allow-large-files does not relax this), since
extract-multi parses each file once into memory.

Input and output may be S3-compatible cloud URIs (s3://) using credential
handles. See docs/extract-workflow.md "Cloud Sources and Outputs".`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			allowLargeFiles, err := cmd.InheritedFlags().GetBool("allow-large-files")
			if err != nil {
				return fmt.Errorf("failed to get allow-large-files flag: %w", err)
			}
			// Resolve one run id (flag > SUMPTER_RUN_ID env > generated) shared by
			// every recipe so all per-recipe provenance ties to one invocation.
			runID, err := provenance.ResolveRunID(opts.RunID, os.Getenv("SUMPTER_RUN_ID"))
			if err != nil {
				return err
			}
			shared := &multiSharedOptions{
				Argv:                   buildExtractMultiArgv(args, opts),
				Files:                  opts.Files,
				FileList:               opts.FileList,
				InputPath:              opts.InputPath,
				IncludePattern:         opts.IncludePattern,
				ExcludePattern:         opts.ExcludePattern,
				MaxDepth:               opts.MaxDepth,
				FollowSymlinks:         opts.FollowSymlinks,
				OutputPath:             opts.OutputPath,
				ContinueOnError:        opts.ContinueOnError,
				Progress:               opts.Progress,
				RunID:                  runID,
				NoManifest:             opts.NoManifest,
				Parameters:             opts.Parameters,
				OutputMode:             opts.OutputMode,
				AggregateMaxRecords:    opts.AggregateMaxRecords,
				AggregateMaxBytes:      opts.AggregateMaxBytes,
				AllowLargeFiles:        allowLargeFiles,
				CredentialsPath:        opts.CredentialsPath,
				CredentialOverrides:    opts.CredentialOverrides,
				InputCredentialsHandle: opts.InputCredentialsHandle,
			}
			return runExtractMulti(shared, args, cmd.ErrOrStderr(), time.Now().UTC())
		},
	}

	cmd.Flags().StringVar(&opts.FileList, "file-list", "", "Path to a newline-delimited file listing input references (local or s3://), one per line; # comments ignored. No walk, no argv limit. Mutually exclusive with --files/--input-path")
	cmd.Flags().StringVar(&opts.Files, "files", "", "Comma-separated list of files to process (short ad hoc sets — use --file-list for large batches)")
	cmd.Flags().StringVar(&opts.InputPath, "input-path", "", "Directory of XML files to process; walks and filters by include/exclude patterns")
	cmd.Flags().StringVar(&opts.IncludePattern, "include-pattern", "", "Include pattern for --input-path discovery")
	cmd.Flags().StringVar(&opts.ExcludePattern, "exclude-pattern", "", "Exclude pattern for --input-path discovery")
	cmd.Flags().IntVar(&opts.MaxDepth, "max-depth", 0, "Max directory depth for --input-path discovery (0 = unlimited)")
	cmd.Flags().BoolVar(&opts.FollowSymlinks, "follow-symlinks", false, "Follow symlinks during --input-path discovery")
	cmd.Flags().StringVar(&opts.OutputPath, "output-path", "", "Required output root; each recipe writes to <output-path>/<recipe-id>/")
	cmd.Flags().BoolVar(&opts.ContinueOnError, "continue-on-error", false, "Continue after recoverable per-file/per-recipe failures; each recipe records its own failures.json")
	cmd.Flags().BoolVarP(&opts.Progress, "progress", "p", false, "Show progress indicators")
	cmd.Flags().StringVar(&opts.RunID, "run-id", "", "UUIDv7 run identifier for deterministic replay (overrides SUMPTER_RUN_ID); shared by every recipe")
	cmd.Flags().BoolVar(&opts.NoManifest, "no-manifest", false, "Disable provenance sidecar manifest output")
	cmd.Flags().StringArrayVar(&opts.Parameters, "parameter", nil, "Inject a key=value pair into every record (repeatable, overrides manifest defaults.parameters). Value is a literal string unless it is a JSON array of strings, e.g. --parameter prefixes='[\"NM_\",\"NR_\"]', which becomes a list parameter")
	cmd.Flags().StringVar(&opts.OutputMode, "output-mode", outputModePerInput, "Record-file fan-out applied to every recipe: per-input (one file per input) or aggregate (each recipe streams to one NDJSON writer per invocation under its <recipe-id>/ dir, rolling to numbered shards). Aggregate is NDJSON only and requires a manifest")
	cmd.Flags().IntVar(&opts.AggregateMaxRecords, "aggregate-max-records", 0, "Aggregate mode: roll each recipe's shards before exceeding this record count per shard (0 = uncapped)")
	cmd.Flags().Int64Var(&opts.AggregateMaxBytes, "aggregate-max-bytes", 0, "Aggregate mode: roll each recipe's shards before exceeding this uncompressed byte count per shard (0 = uncapped)")
	cmd.Flags().StringVar(&opts.CredentialsPath, "credentials", "", "Path to a cloud credentials config (named handles; no secrets in recipe YAML)")
	cmd.Flags().StringArrayVar(&opts.CredentialOverrides, "credential", nil, "Override a handle's AWS profile: handle=profile (repeatable; references only)")
	cmd.Flags().StringVar(&opts.InputCredentialsHandle, "input-credentials-handle", "", "Credential handle name for cloud (s3://) source input")

	return cmd
}

// buildExtractMultiArgv reconstructs the sanitized `recipes run extract-multi`
// invocation for provenance. As with the single-recipe argv, operator/
// environment-specific credential flags are intentionally omitted — they are not
// part of the portable, replayable invocation.
func buildExtractMultiArgv(workspaces []string, opts *recipeRunExtractMultiOptions) []string {
	args := append([]string{"recipes", "run", "extract-multi"}, workspaces...)
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
	appendFlag("--output-path", opts.OutputPath)
	appendFlag("--run-id", opts.RunID)
	// Shared run-level parameters are part of the portable, replayable invocation
	// (unlike the operator/environment-specific credential flags above). The common
	// provenance sanitizer redacts secret-shaped --parameter values by inner key.
	for _, parameter := range opts.Parameters {
		appendFlag("--parameter", parameter)
	}
	if opts.OutputMode != "" && opts.OutputMode != outputModePerInput {
		appendFlag("--output-mode", opts.OutputMode)
	}
	if opts.AggregateMaxRecords > 0 {
		appendFlag("--aggregate-max-records", fmt.Sprintf("%d", opts.AggregateMaxRecords))
	}
	if opts.AggregateMaxBytes > 0 {
		appendFlag("--aggregate-max-bytes", fmt.Sprintf("%d", opts.AggregateMaxBytes))
	}
	if opts.MaxDepth > 0 {
		appendFlag("--max-depth", fmt.Sprintf("%d", opts.MaxDepth))
	}
	if opts.FollowSymlinks {
		args = append(args, "--follow-symlinks")
	}
	if opts.ContinueOnError {
		args = append(args, "--continue-on-error")
	}
	if opts.NoManifest {
		args = append(args, "--no-manifest")
	}
	return args
}
