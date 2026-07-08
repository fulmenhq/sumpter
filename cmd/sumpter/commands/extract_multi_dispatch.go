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
	"sync/atomic"
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

	// onBuildApplication is a test-only seam invoked inside the worker's per-input
	// application stage (within the panic-recovery region of buildApplicationContained, for
	// both aggregate and per-input modes), keyed by input ordinal. Tests use it to inject
	// latency/blocking (to prove application-stage overlap and record backpressure) or a
	// panic (to prove G3 containment). nil in production.
	onBuildApplication func(ordinal int)
	// maxInFlightRecords overrides the aggregate input-worker in-flight record ceiling
	// (default inFlightRecordsPerSlot × window) for tests that need a small, deterministic
	// bound. 0 means use the default.
	maxInFlightRecords int64
	// bundleMaxRecords/bundleMaxBytes are the per-input bundle budget (SUM-068 G4), defaulted
	// in newMultiDispatcher and overridable by tests. 0 disables that dimension.
	bundleMaxRecords int
	bundleMaxBytes   int64
}

func newMultiDispatcher(shared *multiSharedOptions, warnOut io.Writer) *multiDispatcher {
	if warnOut == nil {
		warnOut = io.Discard
	}
	return &multiDispatcher{
		shared:           shared,
		warnOut:          warnOut,
		parseFile:        extract.ParseFileForDOMDispatch,
		bundleMaxRecords: defaultBundleMaxRecords,
		bundleMaxBytes:   defaultBundleMaxBytes,
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
		statsCollector = runstats.Start(shared.InputWorkers)
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

	// Validate input-worker concurrency BEFORE any output session opens. A zero value
	// (internal callers / unset) means the serial default; a negative value is a
	// programming error. Cloud (s3://) destinations are supported under concurrency:
	// workers only BUILD (parse + per-input application into worker-local bundles) and
	// never touch the aggregate writer — durable shard publishes stay on the single ordered
	// committer, in deterministic shard order, and cloud input staging is resolved
	// upstream-serial before any worker starts, so concurrency multiplies neither cloud PUTs
	// nor concurrent GETs (the R1/R7/R8/S9 invariants are unchanged).
	if shared.InputWorkers < 0 {
		return fmt.Errorf("extract-multi: --input-workers must be >= 1 (got %d)", shared.InputWorkers)
	}
	if err := validateExtractMultiInternalParameters(shared); err != nil {
		return err
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
		states = append(states, newRecipeRunState(plan, len(files), d.bundleMaxRecords, d.bundleMaxBytes))
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

// processInputs walks the resolved input set and applies each input to every recipe state
// in input-ordinal order. With InputWorkers <= 1 it is the audited serial loop (build then
// commit on one goroutine), byte-identical to single-worker behavior. With InputWorkers > 1
// the workers run parse + the full per-input recipe application into worker-local bundles,
// and a single ordered committer performs only the durable commit — so per-recipe isolation,
// the per-input spool barrier, deterministic emit order, and the per-invocation manifest are
// unchanged from serial (the committer owns all writing, ledgers, and finalization).
func (d *multiDispatcher) processInputs(ctx context.Context, files []string, logicalByLocal map[string]string, states []*recipeRunState, shared *multiSharedOptions) error {
	if shared.InputWorkers <= 1 {
		return d.processInputsSerial(ctx, files, logicalByLocal, states, shared)
	}
	// SUM-068 input-workers: workers run parse + the full per-input recipe application into
	// bundles and the ordered committer performs only the durable commit, for BOTH output
	// modes (aggregate and per-input). Concurrency now covers the per-input work that
	// dominates the high-count tiny-file regime — not just parse. (This supersedes the
	// SUM-066 parse-only concurrent path.)
	return d.processInputsConcurrentWorkers(ctx, files, logicalByLocal, states, shared)
}

// processInputsSerial is the original serial dispatch loop: parse each input in order
// and apply it before parsing the next. This is the default (InputWorkers <= 1) path
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

// applyOutcome applies one input's parse outcome to every recipe state on the SERIAL path
// (InputWorkers <= 1): it builds and commits each recipe in turn. A non-nil return aborts
// the run: either a fail-fast input/recipe failure, or a terminal output/sink/ledger
// failure that ADR-0009 says must abort even under --continue-on-error. Always runs on a
// single goroutine, so it retains sole ownership of every recipe's writers, ledgers, and
// manifests. (The InputWorkers > 1 path uses commitBuiltOutcome, which mirrors this
// branching but commits worker-built bundles.)
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

// inFlightRecordsPerSlot, multiplied by the look-ahead window, is the in-flight emitted-
// record ceiling for the aggregate input-worker path (SUM-068 G4). The window bounds
// in-flight INPUTS; this bounds the RECORDS those inputs hold in memory ahead of the
// ordered committer, so a few high-yield inputs cannot accumulate unbounded record memory
// within the window before the committer drains them. It is a backpressure throttle only —
// it paces scheduling, never drops or fails records, so output stays byte-identical across
// worker counts. Per-input byte size is independently bounded by the extract-multi DOM
// input limit (ParseFileForDOMDispatch rejects >100MB).
const inFlightRecordsPerSlot = 25_000

// defaultBundleMaxRecords / defaultBundleMaxBytes bound ONE input's in-flight aggregate
// bundle (SUM-068 G4), enforced inside the worker-local collecting sink as records are
// appended — so a single high-output input trips a bounded failure during construction
// rather than after a huge bundle is already retained (or, worse, an OOM). They are the
// per-input half of the memory bound; inFlightRecordsPerSlot×window is the scheduling
// throttle across inputs. Total worst-case in-flight bundle memory is therefore bounded by
// roughly workers × defaultBundleMaxBytes (concurrent builds) plus the in-flight throttle —
// an explicit bound, not "process every input's full output in memory". Spill (deferred)
// is the future path for inputs that legitimately exceed this; until then an over-budget
// input fails with an actionable message (split the input / reduce per-input output; spill
// is the planned path for very large per-input outputs). Defaults are
// generous so the tiny-file regime and normal inputs never trip them.
const (
	defaultBundleMaxRecords = 2_000_000
	defaultBundleMaxBytes   = 256 << 20 // 256 MiB of marshaled records per input
)

// builtInputOutcome carries one input's worker-BUILT result to the ordered committer:
// either an input-level parse failure, or one fully-built aggregateApplication per recipe
// (in states order). records is the total emitted-record count across all recipe bundles,
// computed once at build time and reused at commit to decrement the in-flight bound exactly.
type builtInputOutcome struct {
	idx      int
	ordinal  int
	file     string
	logical  string
	parseErr error
	apps     []builtApplication
	records  int
}

// commitBuiltOutcome applies one worker-built outcome to every recipe state on the ordered
// committer. It mirrors applyOutcome's branching exactly — an input-level parse failure is
// recorded per recipe (honoring --continue-on-error); a per-recipe commit failure aborts
// on fail-fast or on an ADR-0009 terminal error, otherwise is recorded and skipped — but
// sources records from the pre-built bundles instead of extracting inline. It always runs
// on the single committer goroutine, so it retains sole ownership of every recipe's writer,
// ledgers, and manifests.
func (d *multiDispatcher) commitBuiltOutcome(ctx context.Context, o builtInputOutcome, states []*recipeRunState, shared *multiSharedOptions) error {
	if o.parseErr != nil {
		for _, st := range states {
			st.recordInputFailure(o.file, o.logical, o.parseErr)
		}
		if !shared.ContinueOnError {
			return fmt.Errorf("failed to process file %s: %w", o.logical, o.parseErr)
		}
		return nil
	}
	for i, st := range states {
		if rerr := o.apps[i].commit(ctx, st); rerr != nil {
			if !shared.ContinueOnError || isTerminalDispatchError(rerr) {
				return rerr
			}
		}
	}
	return nil
}

// processInputsConcurrentWorkers is the SUM-068 input-worker path for BOTH output modes.
// Each worker parses AND runs the full per-input recipe application into worker-local
// bundles (parse + external fields + signature/applicability/extraction/min_occurrences),
// then the single ordered committer performs only the durable, in-order commit. The
// per-recipe build and commit are mode-specific (aggregate writer vs per-input file) behind
// the builtApplication interface; this skeleton is mode-agnostic. This moves the extract CPU
// that dominates the high-count tiny-file regime off the serial drain, and supersedes the
// SUM-066 parse-only concurrent path.
//
// Determinism, provenance, and failure semantics are preserved exactly: bundles are
// committed in strict input-ordinal order (not worker-completion order); the committer is
// the sole owner of every writer, output target, ledger, counter, failure, disposition, and
// manifest mutation; per-input record order is the extraction order captured in the bundle.
// Two bounds keep memory in check: a look-ahead window caps in-flight INPUTS (as in
// SUM-066), and an in-flight RECORD ceiling (inFlightRecordsPerSlot × window) throttles
// scheduling so high-yield inputs cannot accumulate unbounded record memory (G4). Worker
// application panics are contained per recipe (G3). Worker-shared recipe plan state is
// read-only or per-file cloned; the only shared-mutable touch (the warn limiter) is locked
// (G1).
func (d *multiDispatcher) processInputsConcurrentWorkers(parent context.Context, files []string, logicalByLocal map[string]string, states []*recipeRunState, shared *multiSharedOptions) error {
	workers := shared.InputWorkers
	window := workers * 2
	maxInFlightRecords := int64(window) * inFlightRecordsPerSlot
	if d.maxInFlightRecords > 0 {
		maxInFlightRecords = d.maxInFlightRecords
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	workChan := make(chan int)
	resultChan := make(chan builtInputOutcome, window)
	slots := make(chan struct{}, window)
	// freed coalesces "in-flight records decreased" signals so the feeder can wake from the
	// record-ceiling wait without busy-looping. Buffered/non-blocking send: a missed signal
	// is fine because the feeder re-checks the atomic on every wake.
	freed := make(chan struct{}, 1)
	var inFlightRecords int64

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range workChan {
				o := builtInputOutcome{idx: idx, ordinal: idx + 1, file: files[idx], logical: logicalIdentity(files[idx], logicalByLocal)}
				doc, perr := d.parseInputContained(files[idx], shared.AllowLargeFiles)
				if perr != nil {
					o.parseErr = perr
				} else {
					o.apps = make([]builtApplication, len(states))
					for i, st := range states {
						o.apps[i] = st.buildApplicationContained(ctx, o.file, o.logical, o.ordinal, doc, d.onBuildApplication)
						o.records += o.apps[i].recordCount()
					}
				}
				atomic.AddInt64(&inFlightRecords, int64(o.records))
				select {
				case resultChan <- o:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Feeder: wait out the in-flight record ceiling, then acquire a window slot, then
	// schedule. Every wait honors cancellation so a fail-fast drain never strands it.
	go func() {
		defer close(workChan)
		for idx := range files {
			for atomic.LoadInt64(&inFlightRecords) >= maxInFlightRecords {
				select {
				case <-freed:
				case <-ctx.Done():
					return
				}
			}
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

	// Drain: reorder outcomes into strict ordinal order and commit each exactly once in
	// order. Out-of-order outcomes wait in pending (bounded by window).
	pending := make(map[int]builtInputOutcome, window)
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
			if err := d.commitBuiltOutcome(ctx, cur, states, shared); err != nil {
				runErr = err
				break
			}
			// Release this input's in-flight records and window slot so the feeder advances.
			if cur.records > 0 {
				atomic.AddInt64(&inFlightRecords, -int64(cur.records))
				select {
				case freed <- struct{}{}:
				default:
				}
			}
			next++
			<-slots
		}
		if runErr != nil {
			break
		}
	}

	// Stop the world: cancel unblocks the feeder and any worker blocked on send, then wait
	// for every goroutine to exit before returning (no leaked goroutines, no further work).
	cancel()
	wg.Wait()
	return runErr
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

	// bundleMaxRecords/bundleMaxBytes bound one input's in-flight aggregate bundle during
	// construction (SUM-068 G4). Threaded from the dispatcher so they apply uniformly on the
	// serial and worker build paths (an over-budget input fails identically at every worker
	// count). 0 disables that dimension.
	bundleMaxRecords int
	bundleMaxBytes   int64
}

func newRecipeRunState(plan *RecipePlan, fileCount, bundleMaxRecords int, bundleMaxBytes int64) *recipeRunState {
	st := &recipeRunState{
		plan:             plan,
		manifestEnabled:  shouldWriteManifest(plan.opts),
		manifestInputs:   make([]provenance.Input, 0, fileCount),
		manifestOutputs:  make([]provenance.Output, 0, fileCount),
		counts:           make(map[string]int),
		dispositions:     newDispositionSummary(fileCount),
		failures:         newExtractFailureManifest(fileCount),
		sanitizeRoots:    manifestSanitizeRoots(plan.opts),
		bundleMaxRecords: bundleMaxRecords,
		bundleMaxBytes:   bundleMaxBytes,
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
	// SUM-068 slice 3a: split into a worker-safe build (extraction into a worker-local
	// buffer, no durable target) and a single-owner commit (create the per-input output
	// target, replay, write ledgers). Here both run on the ordered drain in immediate
	// succession, so behavior is byte-identical to the pre-split path; slice 3b hoists the
	// build onto a worker. See extract_multi_bundle.go.
	app := st.buildPerInputApplication(ctx, file, logical, ordinal, doc)
	return st.commitPerInputApplication(ctx, app)
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
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
			return err
		}
	}

	if st.dispositionErr != nil {
		return st.dispositionErr
	}
	if opts.ContinueOnError && st.failures.Failed > 0 {
		return fmt.Errorf("recipe %q partial failure: applied=%d failed=%d", st.plan.RecipeID, st.failures.Applied, st.failures.Failed)
	}
	return nil
}
