package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/antchfx/xmlquery"

	"go.uber.org/zap"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/runstats"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

// dispatchLogger returns the process logger. logging.GetLogger() is nil-safe (it
// returns a no-op logger when none is configured, e.g. in tests), so this is a thin
// alias kept for call-site readability.
func dispatchLogger() *zap.Logger {
	return logging.GetLogger()
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

// sumLocalFileSizes totals the on-disk size of the resolved input read paths
// (local sources or staged-cloud copies) using cheap stat calls only — no file
// reads and no cloud/object-store requests. allKnown is false if any size could
// not be determined, so the caller reports input bytes as unavailable rather than
// a misleading partial total. It never fails the run.
func sumLocalFileSizes(files []string) (total int64, allKnown bool) {
	allKnown = true
	for _, f := range files {
		info, statErr := os.Stat(f)
		if statErr != nil || info.IsDir() {
			allKnown = false
			continue
		}
		total += info.Size()
	}
	return total, allKnown
}

func (d *multiDispatcher) run(workspaces []string, startedAt time.Time) (err error) {
	shared := d.shared
	if shared == nil {
		return fmt.Errorf("extract-multi: shared options are required")
	}
	if len(workspaces) == 0 {
		return fmt.Errorf("extract-multi requires at least one recipe workspace")
	}

	// Opt-in run stats: capture the wall/CPU baseline now and emit a diagnostic
	// summary to stderr on the way out (success or failure, once the input set has
	// been resolved). Purely observational — it never touches records, output, or
	// the manifest, and is deferred FIRST so it prints after all other teardown.
	var (
		statsCollector  *runstats.Collector
		statsInputs     int
		statsInputBytes int64
		statsBytesKnown bool
		statsReady      bool
	)
	if shared.Stats {
		statsCollector = runstats.Start(shared.ParseWorkers)
		defer func() {
			if !statsReady {
				return
			}
			_, _ = io.WriteString(d.warnOut, runstats.Format(statsCollector.Sample(statsInputs, statsInputBytes, statsBytesKnown)))
		}()
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

	// Validate parse concurrency BEFORE any output session opens. A zero value
	// (internal callers / unset) means the serial default; a negative value is a
	// programming error. Cloud (s3://) destinations are supported under concurrency:
	// parse workers never touch the aggregate writer — durable shard publishes stay on
	// the single ordered drain, in deterministic shard order, and cloud input staging
	// is resolved upstream-serial before any worker starts, so concurrency multiplies
	// neither cloud PUTs nor concurrent GETs (the R1/R7/R8/S9 invariants are unchanged).
	if shared.ParseWorkers < 0 {
		return fmt.Errorf("extract-multi: --parse-workers must be >= 1 (got %d)", shared.ParseWorkers)
	}
	// Normalize the output mode ONCE so every downstream check agrees on the canonical
	// value: plan opts (threaded from shared), isAggregateMode (writer selection),
	// validateAggregateMulti's aggregate-only gate, and the --input-path determinism
	// sort. The shared single-recipe validator and isAggregateMode already trim; without
	// this, a padded "aggregate" would run the aggregate writer while the multi-only
	// guards (min_occurrences rejection, input ordering) compared the raw string and
	// silently skipped.
	shared.OutputMode = strings.TrimSpace(shared.OutputMode)

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

	// Validate aggregate output mode for the whole pass BEFORE any output session is
	// opened: each recipe must satisfy the same fail-fast contract as single-recipe
	// (json/ndjson + manifest, no cloud, no min_occurrences floors), and aggregate
	// rejects --continue-on-error.
	if err := validateAggregateMulti(shared, plans); err != nil {
		return err
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
	if shared.Stats {
		// Counters are taken from the resolved read paths (local or staged-cloud
		// copies) using cheap stat calls only — no extra file reads, no cloud calls.
		statsInputs = len(files)
		statsInputBytes, statsBytesKnown = sumLocalFileSizes(files)
		statsReady = true
	}
	if inputSession != nil {
		defer func() {
			if cerr := inputSession.Close(); cerr != nil {
				_, _ = fmt.Fprintf(d.warnOut, "warning: failed to clean up input staging directory: %v\n", cerr)
			}
		}()
	}

	// Aggregate determinism: assign input ordinals in a stable order. For --file-list
	// / --files the resolved order is authoritative; --input-path discovery order is
	// not guaranteed stable, so sort before assigning ordinals (and the shared parse
	// order follows, identical for every recipe).
	if shared.OutputMode == outputModeAggregate && strings.TrimSpace(shared.InputPath) != "" {
		sort.Strings(files)
	}

	states := make([]*recipeRunState, 0, len(plans))
	for _, plan := range plans {
		states = append(states, newRecipeRunState(plan, len(files)))
	}
	// On an early return (parse/recipe failure): for a cloud recipe that already
	// published shards, record them in an incomplete (R8) manifest so the orphaned
	// objects are discoverable; then discard any un-published staging (idempotent and a
	// no-op once finalize has committed+renamed).
	defer func() {
		for _, st := range states {
			if st.aggWriter == nil {
				continue
			}
			// Only a recipe that did NOT finalize cleanly gets an incomplete manifest:
			// a sibling recipe failing later must never overwrite a recipe that already
			// committed and wrote its own successful manifest (per-recipe isolation).
			if err != nil && !st.finalized {
				st.writeIncompleteAggregateManifestOnFailure(startedAt)
			}
			st.aggWriter.abort()
		}
	}()

	ctx := context.Background()
	if perr := d.processInputs(ctx, files, logicalByLocal, states, shared); perr != nil {
		return perr
	}

	var firstErr error
	for _, st := range states {
		if ferr := st.finalize(startedAt); ferr != nil && firstErr == nil {
			firstErr = ferr
		}
	}
	return firstErr
}

// parsedInputOutcome carries one input's read+parse result from a parse worker to
// the ordered drain. Exactly one of doc/err is meaningful: err set means an
// input-level read/parse failure (recorded against every recipe), doc set means a
// parsed document to dispatch to every recipe. idx is the 0-based resolved-input
// position; ordinal is idx+1, the authoritative emit order.
type parsedInputOutcome struct {
	idx     int
	ordinal int
	file    string
	logical string
	doc     *xmlquery.Node
	err     error
}

// processInputs walks the resolved input set, parsing each input and dispatching the
// parsed document to every recipe state in input-ordinal order. With ParseWorkers <= 1
// it is the audited serial loop, byte-identical to single-worker behavior. With
// ParseWorkers > 1 it parses inputs concurrently across workers and fans them into a
// single ordered drain — the drain owns ALL extraction, writing, manifest accounting,
// and finalization, so per-recipe isolation, the per-input spool barrier, deterministic
// emit order, and the per-invocation manifest are unchanged from serial.
func (d *multiDispatcher) processInputs(ctx context.Context, files []string, logicalByLocal map[string]string, states []*recipeRunState, shared *multiSharedOptions) error {
	if shared.ParseWorkers <= 1 {
		return d.processInputsSerial(ctx, files, logicalByLocal, states, shared)
	}
	return d.processInputsConcurrent(ctx, files, logicalByLocal, states, shared)
}

// processInputsSerial is the original serial dispatch loop: parse each input in order
// and apply it before parsing the next. This is the default (ParseWorkers <= 1) path
// and stays byte-identical to pre-SUM-066 behavior.
func (d *multiDispatcher) processInputsSerial(ctx context.Context, files []string, logicalByLocal map[string]string, states []*recipeRunState, shared *multiSharedOptions) error {
	for fileIdx, file := range files {
		o := parsedInputOutcome{idx: fileIdx, ordinal: fileIdx + 1, file: file, logical: logicalIdentity(file, logicalByLocal)}
		o.doc, o.err = d.parseFile(file, shared.AllowLargeFiles)
		if err := d.applyOutcome(ctx, o, states, shared); err != nil {
			return err
		}
	}
	return nil
}

// applyOutcome applies one input's parse outcome to every recipe state, exactly as the
// serial loop did. It is the single shared dispatch body for both the serial and the
// concurrent (ordered-drain) paths, so they cannot diverge in behavior. A non-nil
// return aborts the run: either a fail-fast input/recipe failure, or a terminal
// output/sink/ledger failure that ADR-0009 says must abort even under
// --continue-on-error. Always runs on a single goroutine (serial loop or drain), so it
// retains sole ownership of every recipe's writers, ledgers, and manifests.
func (d *multiDispatcher) applyOutcome(ctx context.Context, o parsedInputOutcome, states []*recipeRunState, shared *multiSharedOptions) error {
	if o.err != nil {
		// Read/parse failure is INPUT-level: it affects every recipe's view of this
		// file. Record it per recipe; honor continue-on-error.
		for _, st := range states {
			st.recordInputFailure(o.file, o.logical, o.err)
		}
		if !shared.ContinueOnError {
			return fmt.Errorf("failed to process file %s: %w", o.logical, o.err)
		}
		return nil
	}
	for _, st := range states {
		// Extraction/applicability/signature/output failure is RECIPE-level: isolated
		// to that recipe, never aborting the others for this file. dispatchParsedFile
		// records recoverable per-input failures internally and returns nil under
		// --continue-on-error; a non-nil error is therefore either a fail-fast failure
		// OR a terminal output/sink/ledger failure that ADR-0009 says must abort even
		// under --continue-on-error (never silently suppressed).
		if rerr := st.dispatchParsedFile(ctx, o.file, o.logical, o.ordinal, o.doc); rerr != nil {
			if !shared.ContinueOnError || isTerminalDispatchError(rerr) {
				return rerr
			}
		}
	}
	return nil
}

// processInputsConcurrent parses the input set across ParseWorkers workers and drains
// the outcomes in strict input-ordinal order. Workers ONLY read+parse (no shared
// recipe/writer state); the single drain runs applyOutcome, so all the SUM-063
// integrity invariants (per-input barrier, deterministic emit order, per-shard digests,
// manifest accounting) are preserved unchanged. A bounded look-ahead window caps
// scheduled-but-not-drained inputs so a slow early input cannot let later workers buffer
// unboundedly (head-of-line) — backpressure blocks the feeder until the drain advances.
func (d *multiDispatcher) processInputsConcurrent(parent context.Context, files []string, logicalByLocal map[string]string, states []*recipeRunState, shared *multiSharedOptions) error {
	workers := shared.ParseWorkers
	// Scheduled-but-not-drained inputs are bounded to window: one slot is acquired
	// per scheduled input and released only after the drain has consumed (and freed)
	// that input's parsed document. The in-flight memory bound is therefore
	// window × (largest single input's parsed document) — independent of input count.
	window := workers * 2

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	workChan := make(chan int)
	resultChan := make(chan parsedInputOutcome, window)
	slots := make(chan struct{}, window)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range workChan {
				o := parsedInputOutcome{idx: idx, ordinal: idx + 1, file: files[idx], logical: logicalIdentity(files[idx], logicalByLocal)}
				o.doc, o.err = d.parseInputContained(files[idx], shared.AllowLargeFiles)
				select {
				case resultChan <- o:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Feeder: acquire a window slot then schedule the next input. Both selects honor
	// cancellation so a fail-fast drain never leaves the feeder blocked.
	go func() {
		defer close(workChan)
		for idx := range files {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case workChan <- idx:
			case <-ctx.Done():
				return
			}
		}
	}()

	// Drain: consume outcomes, reorder into strict ordinal order, and apply each input
	// exactly once in order. Out-of-order outcomes wait in pending (bounded by window).
	pending := make(map[int]parsedInputOutcome, window)
	next := 0
	var runErr error
	for next < len(files) {
		o, ok := <-resultChan
		if !ok {
			break
		}
		pending[o.idx] = o
		for {
			cur, ready := pending[next]
			if !ready {
				break
			}
			delete(pending, next)
			if err := d.applyOutcome(ctx, cur, states, shared); err != nil {
				runErr = err
				break
			}
			next++
			<-slots // release the drained input's window slot so the feeder advances
		}
		if runErr != nil {
			break
		}
	}

	// Stop the world: cancel unblocks the feeder and any worker blocked on send, then
	// wait for every goroutine to exit before returning (no leaked goroutines, no
	// further parsing). Workers select on ctx.Done(), so they need no further drain.
	cancel()
	wg.Wait()
	return runErr
}

// parseInputContained parses one input for a concurrent worker, recovering a panic into
// an input-level error so a single malformed/pathological input cannot crash the whole
// invocation or corrupt the shared drain. The serial path intentionally does NOT recover
// (default behavior is byte-identical to pre-SUM-066); containment is a concurrency
// concern only.
func (d *multiDispatcher) parseInputContained(filePath string, allowLargeFiles bool) (node *xmlquery.Node, err error) {
	defer func() {
		if r := recover(); r != nil {
			node = nil
			err = fmt.Errorf("panic while parsing input: %v", r)
		}
	}()
	return d.parseFile(filePath, allowLargeFiles)
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
	// aggWriter is this recipe's invocation-local aggregate NDJSON writer when the
	// run is in aggregate output mode (nil in per-input mode). It spans every input
	// in the pass under the recipe's own <output-root>/<recipe-id>/ dir, so per-recipe
	// isolation holds (one writer per recipe, never shared).
	aggWriter  *aggregateWriter
	inputCount int  // resolved input count, for the aggregate zero-record shard range
	finalized  bool // aggregate: this recipe's finalize committed + wrote its manifest
}

func newRecipeRunState(plan *RecipePlan, fileCount int) *recipeRunState {
	st := &recipeRunState{
		plan:            plan,
		manifestEnabled: shouldWriteManifest(plan.opts),
		manifestInputs:  make([]provenance.Input, 0, fileCount),
		manifestOutputs: make([]provenance.Output, 0, fileCount),
		counts:          make(map[string]int),
		dispositions:    newDispositionSummary(fileCount),
		failures:        newExtractFailureManifest(fileCount),
		sanitizeRoots:   manifestSanitizeRoots(plan.opts),
	}
	if isAggregateMode(plan.opts) {
		st.aggWriter = newAggregateWriter(plan.opts, aggregateBuffering(plan.opts, plan.extCfg))
		st.inputCount = fileCount
	}
	return st
}

// terminalDispatchError marks a dispatch failure that must abort the whole run even
// under --continue-on-error (ADR-0009: write/finalize/output/ledger failures are fatal
// and are never suppressed by continue-on-error, unlike recoverable per-input
// extraction failures which are recorded and skipped).
type terminalDispatchError struct{ err error }

func (e *terminalDispatchError) Error() string { return e.err.Error() }
func (e *terminalDispatchError) Unwrap() error { return e.err }

// terminalDispatch wraps err so the dispatcher aborts on it regardless of
// --continue-on-error. nil stays nil.
func terminalDispatch(err error) error {
	if err == nil {
		return nil
	}
	return &terminalDispatchError{err: err}
}

func isTerminalDispatchError(err error) bool {
	var t *terminalDispatchError
	return errors.As(err, &t)
}

// recordInputFailure records an input-level (read/parse) failure for this recipe. In
// aggregate mode it routes through recordFailedAggregateInput so the failed input
// carries record_count 0 (part of the aggregate input-set provenance contract, R4/R5).
func (st *recipeRunState) recordInputFailure(file, logical string, cause error) {
	result := recoverableFailureResult(file, logical, fmt.Errorf("failed to read/parse input: %w", cause), extract.DispositionReasonParseError)
	if st.aggWriter != nil {
		recordFailedAggregateInput(result, st.plan.opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return
	}
	_ = recordFailedSequentialResult(result, st.plan.opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, dispatchLogger())
}

// dispatchParsedFile runs this recipe against an already-parsed document, writing
// to the recipe's own output target. A recipe-level failure is recorded (and,
// without continue-on-error, returned) but never affects the other recipes.
func (st *recipeRunState) dispatchParsedFile(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node) error {
	if st.aggWriter != nil {
		return st.dispatchParsedFileAggregate(ctx, file, logical, ordinal, doc)
	}
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
	if st.aggWriter != nil {
		return st.finalizeAggregate(startedAt)
	}
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
