package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// multiSharedOptions carries the run-level inputs that are shared across every
// recipe in an extract-multi run. Input discovery, run-level flags, and the
// cloud credentials config are shared (one input set is read and parsed once);
// output destination, formats, parameters, reference tables, and
// source-extraction stay per recipe and are loaded from each recipe's manifest.
type multiSharedOptions struct {
	// Shared input — exactly one of Files / FileList / InputPath, validated by
	// the caller before any plan is built.
	Files          string
	FileList       string
	InputPath      string
	IncludePattern string
	ExcludePattern string
	MaxDepth       int
	FollowSymlinks bool

	// Run level.
	ContinueOnError bool
	Workers         int
	Progress        bool
	DryRun          bool
	// RunID is generated once for the whole multi run and shared by every plan
	// so provenance ties the per-recipe outputs to a single invocation.
	RunID           string
	NoManifest      bool
	AllowLargeFiles bool

	// Shared cloud credentials (handle references only — never secrets). The
	// input handle is shared because the input set is shared; each recipe's
	// output handle still comes from its own manifest unless overridden here.
	CredentialsPath        string
	CredentialOverrides    []string
	InputCredentialsHandle string
}

// RecipePlan is the fully-loaded, isolated per-recipe execution state for one
// recipe in an extract-multi run. Every field is owned by exactly one plan:
// nothing here is shared across recipes, so a recipe can never read another's
// reference tables, credentials, output destination, or extract config. The
// dispatcher clones extCfg per concurrent holder (compiled XPath is mutable
// state) before extraction.
type RecipePlan struct {
	RecipeID  string
	Workspace string
	// OutputDir is the validated, contained <output-root>/<slug> directory from
	// the output-slug/collision preflight; the dispatcher writes only here.
	OutputDir string

	opts              *ExtractOptions
	sigCfg            *extract.FileSignature
	extCfg            *extract.ExtractRecordMatch
	appCfg            *extract.ApplicabilityConfig
	fieldPlan         *externalFieldPlan
	runtimeProvenance provenance.RuntimeOptions
	warnLimiter       *sourceExtractionWarnLimiter
}

// loadRecipePlan builds the isolated execution state for a single recipe
// workspace, taking the shared run-level input and the recipe's validated output
// directory. It reuses the same low-level loaders/assemblers as the single-recipe
// runExtract path (LoadSignatureConfig, LoadExtractConfig, buildExternalFieldPlan,
// buildReferenceRegistry, ...) so per-recipe semantics match exactly, but it does
// NOT touch executeExtractRecipe — the single-recipe command stays byte-identical.
//
// setupOutputSession (the cloud write boundary) is intentionally deferred to the
// dispatcher so the output-slug/collision preflight runs before any session or
// writer initialization.
func loadRecipePlan(workspace string, shared *multiSharedOptions, outputDir string, warnOut io.Writer) (*RecipePlan, error) {
	if shared == nil {
		return nil, fmt.Errorf("loadRecipePlan: shared options are required")
	}
	if warnOut == nil {
		warnOut = io.Discard
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve workspace: %w", err)
	}

	manifestPath := filepath.Join(absWorkspace, "recipe.yaml")
	manifest, err := recipesmanifest.LoadManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := os.ReadFile(manifestPath) // #nosec G304 - path resolved from explicit recipe workspace
	if err != nil {
		return nil, fmt.Errorf("failed to read recipe manifest for provenance: %w", err)
	}
	for _, warning := range manifest.DrainWarnings() {
		if _, werr := fmt.Fprintf(warnOut, "warning: %s\n", warning); werr != nil {
			return nil, fmt.Errorf("failed to emit manifest warning: %w", werr)
		}
	}
	if manifest.Kind != recipesmanifest.KindExtract {
		return nil, fmt.Errorf("recipe %q manifest kind %s is not supported by run extract-multi", manifest.ID, manifest.Kind)
	}

	// Resolve recipe asset paths (signature / extract / applicability) and read
	// their bytes for provenance + the recipe content hash.
	signaturePath := recipesmanifest.ResolvePath(absWorkspace, manifest.Assets.Signature)
	extractPath := recipesmanifest.ResolvePath(absWorkspace, manifest.Assets.Extract)

	var applicabilityCfg *extract.ApplicabilityConfig
	var applicabilityBytes []byte
	if strings.TrimSpace(manifest.Assets.Applicability) != "" {
		// Validate workspace containment with OpenRelativeFile (rejects ".."
		// escapes) BEFORE reading — same guard as the single-recipe path, so a
		// multi recipe cannot point applicability outside its workspace.
		asset, oerr := recipesmanifest.OpenRelativeFile(absWorkspace, manifest.Assets.Applicability)
		if oerr != nil {
			return nil, fmt.Errorf("failed to open applicability asset: %w", oerr)
		}
		if cerr := asset.Close(); cerr != nil {
			return nil, fmt.Errorf("failed to close applicability asset: %w", cerr)
		}
		applicabilityPath := recipesmanifest.ResolvePath(absWorkspace, manifest.Assets.Applicability)
		applicabilityBytes, err = os.ReadFile(applicabilityPath) // #nosec G304 - path validated by OpenRelativeFile containment check
		if err != nil {
			return nil, fmt.Errorf("failed to read applicability asset for provenance: %w", err)
		}
		applicabilityCfg, err = extract.LoadApplicabilityConfig(applicabilityPath)
		if err != nil {
			return nil, err
		}
	}

	signatureBytes, err := os.ReadFile(signaturePath) // #nosec G304 - path resolved from recipe manifest
	if err != nil {
		return nil, fmt.Errorf("failed to read signature asset for provenance: %w", err)
	}
	extractBytes, err := os.ReadFile(extractPath) // #nosec G304 - path resolved from recipe manifest
	if err != nil {
		return nil, fmt.Errorf("failed to read extract asset for provenance: %w", err)
	}
	recipeContentHash, err := provenance.RecipeContentHash(signatureBytes, extractBytes, applicabilityBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compute recipe content hash: %w", err)
	}

	defaults := manifest.Defaults
	opts := &ExtractOptions{
		SignatureConfig:     signaturePath,
		ExtractConfig:       extractPath,
		ApplicabilityConfig: applicabilityCfg,
		ContinueOnError:     shared.ContinueOnError,
		AllowLargeFiles:     shared.AllowLargeFiles,
		RunID:               shared.RunID,
		NoManifest:          shared.NoManifest,
		Workers:             shared.Workers,
		Progress:            shared.Progress,
		DryRun:              shared.DryRun,
		CommandName:         "sumpter recipes run extract-multi",
		RuntimeProvenance: provenance.RuntimeOptions{
			RecipeVersion:     manifest.ContentVersion,
			RecipeContentHash: recipeContentHash,
		},
		Recipe: buildRecipeProvenance(manifest, manifestBytes, signatureBytes, extractBytes, applicabilityBytes, recipeContentHash),
	}

	// Shared input — identical for every recipe so each file is discovered once.
	// The dispatcher acquires/parses; the plan only records how input maps.
	sourceExtractionInput := defaults.Input
	// Resolve a manifest-declared input path against the recipe workspace BEFORE
	// any shared-input override, so source_extraction relative_path keeps a stable
	// workspace-rooted root (matching the single-recipe runner) rather than one
	// resolved against the process cwd.
	if sourceExtractionInput.Path != "" {
		sourceExtractionInput.Path = resolveMaybeRelative(absWorkspace, sourceExtractionInput.Path)
	}
	switch {
	case shared.Files != "":
		opts.Files = shared.Files
	case shared.FileList != "":
		opts.FileList = shared.FileList
	case shared.InputPath != "":
		opts.InputPath = shared.InputPath
		sourceExtractionInput.Path = shared.InputPath
	}
	opts.IncludePattern = shared.IncludePattern
	opts.ExcludePattern = shared.ExcludePattern
	opts.MaxDepth = shared.MaxDepth
	opts.FollowSymlinks = shared.FollowSymlinks
	sourceExtractionInput.FollowSymlinks = shared.FollowSymlinks

	// Output is owned by the dispatcher: every recipe writes to its validated,
	// contained <output-root>/<slug> directory. The manifest's own output.path is
	// deliberately ignored so per-recipe isolation is structural, not advisory.
	opts.OutputPath = outputDir
	formats, err := defaults.Output.FormatsOrDefault()
	if err != nil {
		return nil, err
	}
	opts.Formats = formats
	if len(formats) > 0 {
		opts.Format = formats[0]
	}
	opts.OutputPattern = defaults.Output.Pattern
	opts.OutputPatterns = defaults.Output.Patterns
	parquetCompression, err := defaults.Output.ParquetCompression()
	if err != nil {
		return nil, err
	}
	opts.ParquetCompression = parquetCompression
	opts.UniformSchema = defaults.Output.UniformSchema
	if defaults.Output.Parquet != nil && len(defaults.Output.Parquet.WithholdColumns) > 0 {
		opts.ParquetWithholdColumns = append([]string(nil), defaults.Output.Parquet.WithholdColumns...)
	}

	// Cloud credentials: the input handle is shared (shared input set); the
	// output handle is the recipe's own (its manifest), so each plan resolves an
	// isolated handle set — recipe A's handle name is not reachable by recipe B.
	opts.CredentialsPath = shared.CredentialsPath
	opts.CredentialOverrides = shared.CredentialOverrides
	if strings.TrimSpace(shared.InputCredentialsHandle) != "" {
		opts.InputCredentialsHandle = shared.InputCredentialsHandle
	} else {
		opts.InputCredentialsHandle = defaults.Input.CredentialsHandle
	}
	opts.OutputCredentialsHandle = defaults.Output.CredentialsHandle

	opts.ClientID = defaults.ClientID
	opts.SiteID = defaults.SiteID
	opts.ManifestParameters = defaults.Parameters
	opts.ParametersRequired = defaults.ParametersRequired
	// Reference tables resolve against THIS recipe's workspace (per-recipe
	// containment root); no cross-recipe root, no CLI override surface in v0.
	opts.ReferenceTableDecls = defaults.ReferenceTables
	opts.ReferenceTableRoot = absWorkspace
	opts.SourceExtraction = defaults.SourceExtraction
	opts.SourceExtractionRequired = defaults.SourceExtractionRequired
	opts.SourceExtractionInput = sourceExtractionInput
	opts.SourceExtractionRecipeID = manifest.ID

	return assembleRecipePlan(manifest.ID, absWorkspace, outputDir, opts)
}

// assembleRecipePlan runs the same configuration assembly as the single-recipe
// runExtract path (load + validate the signature/extract configs, build the
// external-field plan, validate + load the run-scoped reference registry) and
// captures the result as an isolated RecipePlan. It stops short of input
// acquisition and output-session setup, both of which the dispatcher performs.
func assembleRecipePlan(recipeID, absWorkspace, outputDir string, opts *ExtractOptions) (*RecipePlan, error) {
	if err := resolveLocalReferences(opts); err != nil {
		return nil, err
	}
	if err := validateCredentialOptions(opts); err != nil {
		return nil, err
	}

	sigCfg, err := extract.LoadSignatureConfig(opts.SignatureConfig)
	if err != nil {
		return nil, fmt.Errorf("recipe %q: failed to load signature config: %w", recipeID, err)
	}
	extCfg, err := extract.LoadExtractConfig(opts.ExtractConfig)
	if err != nil {
		return nil, fmt.Errorf("recipe %q: failed to load extract config: %w", recipeID, err)
	}
	if err := extract.SetUniformSchema(extCfg, opts.UniformSchema); err != nil {
		return nil, err
	}
	if err := validateParquetWithholdColumns(opts.ParquetWithholdColumns, extCfg.OutputSchema); err != nil {
		return nil, err
	}
	if opts.Recipe != nil && len(opts.Recipe.FieldProvenance) == 0 {
		opts.Recipe.FieldProvenance = buildFieldProvenance(extCfg.FieldMappings)
	}

	runtimeProvenance, err := buildExtractRuntimeProvenance(opts)
	if err != nil {
		return nil, err
	}

	fieldPlan, err := buildExternalFieldPlan(opts, extCfg.FieldMappings)
	if err != nil {
		return nil, err
	}
	if err := validateSourceExtractionDeclarations(opts, extCfg.FieldMappings); err != nil {
		return nil, err
	}
	if err := validateReferenceTableDeclarations(opts, extCfg.FieldMappings); err != nil {
		return nil, err
	}
	referenceRegistry, referenceProv, err := buildReferenceRegistry(context.Background(), opts, runtimeProvenance.RunID, !opts.DryRun)
	if err != nil {
		return nil, err
	}
	if referenceRegistry != nil {
		extCfg.ReferenceTables = referenceRegistry
	}
	opts.referenceTableProv = referenceProv

	return &RecipePlan{
		RecipeID:          recipeID,
		Workspace:         absWorkspace,
		OutputDir:         outputDir,
		opts:              opts,
		sigCfg:            sigCfg,
		extCfg:            extCfg,
		appCfg:            opts.ApplicabilityConfig,
		fieldPlan:         fieldPlan,
		runtimeProvenance: runtimeProvenance,
		warnLimiter:       newSourceExtractionWarnLimiter(1000),
	}, nil
}
