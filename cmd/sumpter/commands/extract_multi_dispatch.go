package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/antchfx/xmlquery"

	"go.uber.org/zap"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

// dispatchLogger returns the process logger, or a no-op logger when none is
// configured (e.g. in tests), so logging never panics on a nil logger.
func dispatchLogger() *zap.Logger {
	if l := logging.GetLogger(); l != nil {
		return l
	}
	return zap.NewNop()
}

// multiDispatcher runs the parse-once, multi-recipe extraction. parseFile is
// injectable so tests can prove each input file is parsed exactly once for M
// matching recipes (a counting seam rather than timing).
type multiDispatcher struct {
	shared    *multiSharedOptions
	warnOut   io.Writer
	parseFile func(filePath string, allowLargeFiles bool) (*xmlquery.Node, error)
}

func newMultiDispatcher(shared *multiSharedOptions, warnOut io.Writer) *multiDispatcher {
	if warnOut == nil {
		warnOut = io.Discard
	}
	return &multiDispatcher{
		shared:    shared,
		warnOut:   warnOut,
		parseFile: extract.ParseFileForDOMDispatch,
	}
}

// runExtractMulti applies every recipe in workspaces to one shared input set,
// parsing each input file ONCE and dispatching the parsed document to each
// recipe. Output, reference tables, source-extraction, and provenance stay
// isolated per recipe; the read+parse is the only shared work.
func runExtractMulti(shared *multiSharedOptions, workspaces []string, warnOut io.Writer, startedAt time.Time) error {
	return newMultiDispatcher(shared, warnOut).run(workspaces, startedAt)
}

func (d *multiDispatcher) run(workspaces []string, startedAt time.Time) error {
	shared := d.shared
	if shared == nil {
		return fmt.Errorf("extract-multi: shared options are required")
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("extract-multi requires at least one recipe workspace")
	}
	if err := validateSharedInputMode(shared); err != nil {
		return err
	}
	outputRoot := shared.OutputPath
	if err := requireOutputRoot(outputRoot); err != nil {
		return err
	}
	// Normalize the shared output root before deriving per-recipe dirs, matching
	// the single-recipe runner: a local file:// root resolves to its filesystem
	// path; a cloud (s3://) root keeps its URI; an unsupported scheme (e.g. gs://)
	// is rejected here. resolveRecipeOutputDirs then sees a clean local path or
	// an s3:// URI, never a half-resolved file:// root.
	if ref, cerr := uriio.Classify(outputRoot); cerr != nil {
		return cerr
	} else if !ref.IsCloud() {
		local, lerr := uriio.LocalPath("output root", outputRoot)
		if lerr != nil {
			return lerr
		}
		outputRoot = local
	}
	shared.OutputPath = outputRoot

	// Resolve ONE run id for the whole invocation so every recipe's provenance
	// ties back to a single extract-multi run.
	if shared.RunID == "" {
		runID, err := provenance.NewRunID()
		if err != nil {
			return fmt.Errorf("extract-multi: failed to resolve run id: %w", err)
		}
		shared.RunID = runID
	}

	// Read each recipe id and run the output-slug/collision preflight BEFORE
	// loading any plan, opening any output session, or writing anything — the
	// load-bearing handoff from the containment primitive.
	ids := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		manifest, err := recipesmanifest.LoadManifest(recipeManifestPath(ws))
		if err != nil {
			return fmt.Errorf("extract-multi: %w", err)
		}
		ids = append(ids, manifest.ID)
	}
	dirs, err := resolveRecipeOutputDirs(outputRoot, ids)
	if err != nil {
		return err
	}

	// Load every plan (isolated per-recipe state) against the shared input and
	// the recipe's validated output directory.
	plans := make([]*RecipePlan, 0, len(workspaces))
	for i, ws := range workspaces {
		plan, err := loadRecipePlan(ws, shared, dirs[i].Dir, d.warnOut)
		if err != nil {
			return err
		}
		plans = append(plans, plan)
	}

	// Now (after the preflight) set up each recipe's output: create its
	// validated directory and, for cloud destinations, its write-boundary
	// session. Deferred from the loader so nothing is created before validation.
	for _, plan := range plans {
		// Only create a local directory for a local destination; a cloud (s3://)
		// output dir is published through the output session/target, not MkdirAll.
		if !referenceIsCloud(plan.OutputDir) {
			if err := os.MkdirAll(plan.OutputDir, 0o750); err != nil {
				return fmt.Errorf("recipe %q: failed to create output directory: %w", plan.RecipeID, err)
			}
		}
		if err := setupOutputSession(plan.opts, shared.RunID); err != nil {
			return fmt.Errorf("recipe %q: %w", plan.RecipeID, err)
		}
		defer closeOutputSession(plan.opts)
	}

	// Resolve the shared input set ONCE (the whole point: one discovery + one
	// read/parse per file, shared across recipes).
	inputOpts := sharedInputOptions(shared)
	if err := resolveLocalReferences(inputOpts); err != nil {
		return err
	}
	if err := validateCredentialOptions(inputOpts); err != nil {
		return err
	}
	files, logicalByLocal, inputSession, err := resolveInputSources(context.Background(), inputOpts, shared.RunID)
	if err != nil {
		return err
	}
	if inputSession != nil {
		defer func() {
			if cerr := inputSession.Close(); cerr != nil {
				_, _ = fmt.Fprintf(d.warnOut, "warning: failed to clean up input staging directory: %v\n", cerr)
			}
		}()
	}

	states := make([]*recipeRunState, 0, len(plans))
	for _, plan := range plans {
		states = append(states, newRecipeRunState(plan, len(files)))
	}

	ctx := context.Background()
	for _, file := range files {
		logical := logicalIdentity(file, logicalByLocal)
		doc, perr := d.parseFile(file, shared.AllowLargeFiles)
		if perr != nil {
			// Read/parse failure is INPUT-level: it affects every recipe's view
			// of this file. Record it per recipe; honor continue-on-error.
			for _, st := range states {
				st.recordInputFailure(file, logical, perr)
			}
			if !shared.ContinueOnError {
				return fmt.Errorf("failed to process file %s: %w", logical, perr)
			}
			continue
		}
		for _, st := range states {
			// Extraction/applicability/signature/output failure is RECIPE-level:
			// isolated to that recipe, never aborting the others for this file.
			if rerr := st.dispatchParsedFile(ctx, file, logical, doc); rerr != nil && !shared.ContinueOnError {
				return rerr
			}
		}
	}

	var firstErr error
	for _, st := range states {
		if ferr := st.finalize(startedAt); ferr != nil && firstErr == nil {
			firstErr = ferr
		}
	}
	return firstErr
}

func recipeManifestPath(workspace string) string {
	return recipesmanifest.ResolvePath(workspace, "recipe.yaml")
}

func validateSharedInputMode(shared *multiSharedOptions) error {
	modes := 0
	if shared.Files != "" {
		modes++
	}
	if shared.FileList != "" {
		modes++
	}
	if shared.InputPath != "" {
		modes++
	}
	if modes == 0 {
		return fmt.Errorf("extract-multi requires one of --files, --file-list, or --input-path")
	}
	if modes > 1 {
		return fmt.Errorf("extract-multi accepts only one of --files, --file-list, or --input-path")
	}
	return nil
}

func requireOutputRoot(outputRoot string) error {
	if outputRoot == "" {
		return fmt.Errorf("extract-multi requires --output-path (each recipe writes to <output-root>/<recipe-id>/)")
	}
	return nil
}

// sharedInputOptions builds the ExtractOptions used solely to discover/acquire
// the shared input set once. Only input + input-credential fields matter here.
func sharedInputOptions(shared *multiSharedOptions) *ExtractOptions {
	return &ExtractOptions{
		Files:                  shared.Files,
		FileList:               shared.FileList,
		InputPath:              shared.InputPath,
		IncludePattern:         shared.IncludePattern,
		ExcludePattern:         shared.ExcludePattern,
		MaxDepth:               shared.MaxDepth,
		FollowSymlinks:         shared.FollowSymlinks,
		AllowLargeFiles:        shared.AllowLargeFiles,
		CredentialsPath:        shared.CredentialsPath,
		CredentialOverrides:    shared.CredentialOverrides,
		InputCredentialsHandle: shared.InputCredentialsHandle,
	}
}

// recipeRunState accumulates one recipe's per-run output ledgers and dispositions
// while the dispatcher walks the shared input set. Each recipe owns its own
// state — there is no shared output/manifest accumulator across recipes.
type recipeRunState struct {
	plan            *RecipePlan
	manifestEnabled bool
	manifestInputs  []provenance.Input
	manifestOutputs []provenance.Output
	counts          map[string]int
	dispositions    *dispositionSummaryFile
	failures        *extractFailureManifestFile
	sanitizeRoots   []string
	dispositionErr  error
}

func newRecipeRunState(plan *RecipePlan, fileCount int) *recipeRunState {
	return &recipeRunState{
		plan:            plan,
		manifestEnabled: shouldWriteManifest(plan.opts),
		manifestInputs:  make([]provenance.Input, 0, fileCount),
		manifestOutputs: make([]provenance.Output, 0, fileCount),
		counts:          make(map[string]int),
		dispositions:    newDispositionSummary(fileCount),
		failures:        newExtractFailureManifest(fileCount),
		sanitizeRoots:   manifestSanitizeRoots(plan.opts),
	}
}

// recordInputFailure records an input-level (read/parse) failure for this recipe.
func (st *recipeRunState) recordInputFailure(file, logical string, cause error) {
	result := recoverableFailureResult(file, logical, fmt.Errorf("failed to read/parse input: %w", cause), extract.DispositionReasonParseError)
	_ = recordFailedSequentialResult(result, st.plan.opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, dispatchLogger())
}

// dispatchParsedFile runs this recipe against an already-parsed document, writing
// to the recipe's own output target. A recipe-level failure is recorded (and,
// without continue-on-error, returned) but never affects the other recipes.
func (st *recipeRunState) dispatchParsedFile(ctx context.Context, file, logical string, doc *xmlquery.Node) error {
	opts := st.plan.opts
	logger := dispatchLogger()

	externalFields, err := buildExternalFieldsForFile(logical, opts, st.plan.fieldPlan, st.plan.warnLimiter)
	if err != nil {
		if !opts.ContinueOnError {
			return fmt.Errorf("recipe %q: failed to build external fields for %s: %w", st.plan.RecipeID, logical, err)
		}
		result := recoverableFailureResult(file, logical, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
		return recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
	}

	target, err := newJSONOutputTarget(opts, logical)
	if err != nil {
		return fmt.Errorf("recipe %q: failed to open output for %s: %w", st.plan.RecipeID, logical, err)
	}

	rp := st.plan.runtimeProvenance
	if file != logical {
		rp.SourceURI = logical
	}
	// Clone the extract config per recipe per file so compiled XPath state is
	// never shared while M recipes read the one shared, read-only document.
	cloned := extract.CloneRecordMatch(st.plan.extCfg)
	result := extract.ProcessParsedDocument(ctx, doc, file, st.plan.sigCfg, cloned, st.plan.appCfg, externalFields, rp, target)
	closeErr := target.Close(ctx)

	if result.Error != nil || result.Disposition == extract.DispositionFailed {
		target.Abort()
		if closeErr != nil && result.Error == nil {
			result.Error = closeErr
			result.Disposition = extract.DispositionFailed
			result.DispositionReason = extract.DispositionReasonInternalError
			result.DispositionDetail = closeErr.Error()
		}
		if !opts.ContinueOnError {
			if result.Error != nil {
				return fmt.Errorf("recipe %q: failed to process %s: %w", st.plan.RecipeID, logical, result.Error)
			}
			st.dispositionErr = failureErrorForResult(result, st.sanitizeRoots)
			return st.dispositionErr
		}
		_ = recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
		return nil
	}
	if closeErr != nil {
		target.Abort()
		return fmt.Errorf("recipe %q: failed to close output for %s: %w", st.plan.RecipeID, logical, closeErr)
	}

	// Enforce match_selectors[].min_occurrences floors BEFORE publishing the
	// output/manifest (ADR-0007), matching the single-recipe path. A violation is
	// a recipe-level failure: abort this recipe's output for the file, never
	// commit a successful zero/short payload, and never abort the other recipes.
	if result.Disposition != extract.DispositionNotApplicable {
		if mErr := enforceMinOccurrences(opts, cloned, st.plan.sigCfg, result.LogicalURI, result.PerSelectorCounts, result.PerSelectorCountsComplete, result.SignatureMatchStatus, result.SignatureConfidence); mErr != nil {
			target.Abort()
			if !opts.ContinueOnError {
				st.dispositionErr = mErr
				return mErr
			}
			reason := failureReasonForError(mErr)
			if reason == "" {
				reason = extract.DispositionReasonMinOccurrencesViolation
			}
			result.Disposition = extract.DispositionFailed
			result.DispositionReason = reason
			result.DispositionDetail = mErr.Error()
			_ = recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
			return nil
		}
	}

	if err := target.Commit(); err != nil {
		return err
	}

	if result.Disposition != "" {
		st.dispositions.add(result, st.sanitizeRoots)
	}
	if st.manifestEnabled {
		input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), st.sanitizeRoots...)
		if err != nil {
			return err
		}
		input.RecordType = st.plan.extCfg.RecordType
		applyInputDisposition(&input, result, st.sanitizeRoots)
		st.manifestInputs = append(st.manifestInputs, input)
	}
	recordCount := target.Count()
	st.counts[st.plan.extCfg.RecordType] += recordCount
	if st.manifestEnabled {
		st.manifestOutputs = append(st.manifestOutputs, provenanceOutput(target.logicalName(), recipesmanifest.OutputFormatJSON, recordCount, opts, st.sanitizeRoots...))
	}
	st.failures.addApplied()
	return nil
}

// finalize writes this recipe's failures, dispositions, and provenance manifest
// to its own output directory.
func (st *recipeRunState) finalize(startedAt time.Time) error {
	opts := st.plan.opts
	logger := dispatchLogger()

	if opts.ContinueOnError && st.failures.Failed > 0 {
		if err := writeExtractFailureManifest(opts, outputRefJoin(opts.OutputPath, "failures.json"), st.failures); err != nil {
			return err
		}
	}
	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(opts, outputRefJoin(opts.OutputPath, "dispositions.json"), st.dispositions); err != nil {
			return err
		}
	}
	if st.manifestEnabled {
		manifestPath := outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
		manifest := buildProvenanceManifest(opts, st.plan.runtimeProvenance, startedAt, time.Now().UTC(), st.manifestInputs, st.manifestOutputs, st.counts, st.sanitizeRoots)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written")
	}

	if st.dispositionErr != nil {
		return st.dispositionErr
	}
	if opts.ContinueOnError && st.failures.Failed > 0 {
		return fmt.Errorf("recipe %q partial failure: applied=%d failed=%d", st.plan.RecipeID, st.failures.Applied, st.failures.Failed)
	}
	return nil
}
