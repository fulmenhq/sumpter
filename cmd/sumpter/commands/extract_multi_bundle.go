package commands

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/antchfx/xmlquery"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// SUM-068 (tiny-file-parallelism) — per-input bundle/commit split for both output modes.
//
// The high-count tiny-file regime spends most of its time AFTER parse, in per-input recipe
// application (external-field building, signature/applicability/extraction, min_occurrences
// enforcement). SUM-066's parse workers parallelized only read+parse, so that work stayed
// serial on the ordered drain and worker concurrency plateaued. This file splits per-input
// application into two stages so the expensive build can run on a worker:
//
//   - BUILD (build{Aggregate,PerInput}Application): builds external fields, runs the
//     extraction core into a worker-LOCAL record buffer (collectingRecordSink, which also
//     enforces the per-input bundle budget, G4), and computes the min_occurrences verdict.
//     It touches only the recipe's read-only plan and a per-file config clone — never the
//     shared aggregate writer, per-input output target, ledgers, dispositions, failures, or
//     manifest. Keeping it free of shared mutable state is what makes worker-side execution
//     safe (G1).
//   - COMMIT (commit{Aggregate,PerInput}Application): consumes the built bundle in
//     input-ordinal order and performs every durable/shared mutation — replaying the records
//     into the aggregate shard (through the per-input spool barrier) or the per-input output
//     file, and updating counts, dispositions, failures, and the provenance manifest. It is
//     the sole owner of that state, single-threaded on the ordered committer.
//
// Both modes implement builtApplication so one worker-build skeleton
// (processInputsConcurrentWorkers) drives them; the serial path (InputWorkers <= 1) runs
// build then commit on one goroutine. Output, provenance, failure, and disposition
// semantics are byte-identical across worker counts — the determinism/correctness/moto
// suites are the gate.

// builtApplication is one recipe's worker-built outcome for one input, committed in input
// order by the ordered committer. Both output modes implement it (aggregateApplication,
// perInputApplication), so the worker-build concurrency skeleton is mode-agnostic: workers
// build the mode-appropriate value, the committer calls commit() in ordinal order, and
// recordCount() feeds the in-flight record bound (G4). The adapters are thin — the real
// commit logic stays in commit{Aggregate,PerInput}Application (single-owner on the drain).
type builtApplication interface {
	commit(ctx context.Context, st *recipeRunState) error
	recordCount() int
}

// aggregateApplication is one recipe's worker-built outcome for one input in aggregate
// output mode: the records it would emit plus the verdict the ordered committer must act
// on. It carries no references to shared writer/ledger state, so it can be produced off
// the ordered committer (slice 2) and applied in input order on it.
//
// The committer evaluates the verdict fields in a fixed priority that mirrors the
// pre-split dispatch order: externalFieldsErr, then the extraction result
// (result.Error / DispositionFailed), then floorErr, then success (replay records).
type aggregateApplication struct {
	file    string
	logical string
	ordinal int

	// records are the enriched envelopes produced by the extraction core, MARSHALED on the
	// worker (each entry is one JSON object plus a trailing newline) and retained in a
	// worker-local buffer instead of being streamed straight to the shared shard. Marshaling
	// on the worker parallelizes that CPU and lets the per-input bundle budget (G4) be
	// counted in exact bytes; the committer replays the bytes into the shard verbatim on the
	// success path, so the shard is byte-identical to streaming the records.
	records [][]byte
	// result is the extraction outcome (disposition, error, per-selector counts, identity)
	// the committer needs to record failures, dispositions, and the input ledger.
	result extract.ExtractResult

	// externalFieldsErr is set when building external fields failed (recipe-level): the
	// extraction core never ran, so records/result are empty.
	externalFieldsErr error
	// floorErr is set when a clean (no error, applicable) input missed a declared
	// min_occurrences floor: a recipe-level failure that discards the input's rows.
	floorErr error
}

// errBundleOverBudget marks a per-input bundle that exceeded its in-memory record/byte
// budget during construction. It is a bounded failure (not an OOM): ProcessParsedDocument
// surfaces it as the input's extraction error, which the committer records as a per-input
// failure (--continue-on-error) or aborts on (fail-fast).
type errBundleOverBudget struct{ msg string }

func (e *errBundleOverBudget) Error() string { return e.msg }

// collectingRecordSink is a worker-local extract.RecordSink that MARSHALS each emitted
// record and retains the bytes in memory instead of writing them to a durable target. It
// is how the build stage runs the extraction core without touching the shared aggregate
// writer, and it enforces the per-input bundle budget (SUM-068 G4) as records are appended
// — so a pathological high-output input trips a bounded failure BEFORE its bundle can grow
// unbounded, rather than after the whole bundle is already built. Marshaling here (json,
// deterministic key order) matches aggregateWriter.OnRecord exactly, so committing these
// bytes is byte-identical to streaming the records. maxRecords/maxBytes <= 0 disable that
// dimension.
type collectingRecordSink struct {
	records    [][]byte
	bytes      int64
	maxRecords int
	maxBytes   int64
	// summary is the file-emission boundary the extraction core reports once per input. The
	// aggregate path ignores it (its writer's OnFileBoundary is a no-op), but the per-input
	// path replays it onto the durable target so a successful zero-record input still opens
	// (and closes) its output file before commit — preserving the pre-split lifecycle.
	summary extract.FileEmissionSummary
}

func (s *collectingRecordSink) OnRecord(_ context.Context, record extract.EmittedRecord) error {
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal aggregate record: %w", err)
	}
	data = append(data, '\n')
	if s.maxRecords > 0 && len(s.records) >= s.maxRecords {
		return &errBundleOverBudget{msg: fmt.Sprintf("input produced more than the %d-record in-flight bundle limit; split the input into smaller files or reduce its per-input output (streaming/spill support for very large per-input outputs is planned)", s.maxRecords)}
	}
	if s.maxBytes > 0 && s.bytes+int64(len(data)) > s.maxBytes {
		return &errBundleOverBudget{msg: fmt.Sprintf("input produced more than the %d-byte in-flight bundle limit; split the input into smaller files or reduce its per-input output (streaming/spill support for very large per-input outputs is planned)", s.maxBytes)}
	}
	s.records = append(s.records, data)
	s.bytes += int64(len(data))
	return nil
}

// OnFileBoundary captures the file-emission summary the extraction core reports for this
// input. The aggregate committer ignores it; the per-input committer replays it onto the
// durable target so the zero-record open/close lifecycle matches the pre-split path.
func (s *collectingRecordSink) OnFileBoundary(_ context.Context, summary extract.FileEmissionSummary) error {
	s.summary = summary
	return nil
}

func (s *collectingRecordSink) Close(context.Context) error { return nil }

// buildAggregateApplication runs one recipe's per-input application into a worker-local
// bundle WITHOUT touching the shared aggregate writer, ledgers, or manifest. It reads
// only the recipe's read-only plan and a per-file config clone, so it is safe to run off
// the ordered committer in a later slice. The returned bundle's verdict fields tell the
// committer which path to take.
func (st *recipeRunState) buildAggregateApplication(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node) aggregateApplication {
	app := aggregateApplication{file: file, logical: logical, ordinal: ordinal}
	opts := st.plan.opts

	externalFields, err := buildExternalFieldsForFile(logical, opts, st.plan.fieldPlan, st.plan.warnLimiter)
	if err != nil {
		app.externalFieldsErr = err
		return app
	}

	rp := st.plan.runtimeProvenance
	if file != logical {
		rp.SourceURI = logical
	}
	// Clone the extract config per recipe per file so compiled XPath state is never shared
	// while M recipes read the one shared, read-only document.
	cloned := extract.CloneRecordMatch(st.plan.extCfg)
	// G4: bound this one input's in-flight bundle as records are appended (bounded failure
	// on exceed), so a high-output input cannot accumulate unbounded memory before the
	// committer drains it. Enforced uniformly on the serial and worker paths, so an
	// over-budget input fails identically at every worker count (determinism preserved).
	sink := &collectingRecordSink{maxRecords: st.bundleMaxRecords, maxBytes: st.bundleMaxBytes}
	app.result = extract.ProcessParsedDocument(ctx, doc, file, st.plan.sigCfg, cloned, st.plan.appCfg, externalFields, rp, sink)
	app.records = sink.records

	// Enforce match_selectors[].min_occurrences floors for a clean, applicable input —
	// the same gate the pre-split path ran after a successful extraction and before the
	// per-input flush. A floor miss becomes a recipe-level failure handled by the
	// committer's per-input spool barrier. Skipped when the extraction errored, the
	// disposition is already Failed, or the input was not applicable (matching the
	// pre-split guard exactly).
	if app.result.Error == nil &&
		app.result.Disposition != extract.DispositionFailed &&
		app.result.Disposition != extract.DispositionNotApplicable {
		if floorErr := enforceMinOccurrences(opts, cloned, st.plan.sigCfg, app.result.LogicalURI, app.result.PerSelectorCounts, app.result.PerSelectorCountsComplete, app.result.SignatureMatchStatus, app.result.SignatureConfidence); floorErr != nil {
			app.floorErr = floorErr
		}
	}
	return app
}

// perInputApplication is one recipe's worker-built outcome for one input in per-input
// output mode (one output file per input): the marshaled records to write plus the verdict
// the ordered committer acts on. Like aggregateApplication it carries no durable/shared
// state, so the build can run on a worker (slice 3b) while the per-input file write, ledger,
// and manifest updates stay single-owner on the ordered committer. The committer evaluates
// externalFieldsErr, then the extraction result, then floorErr, then success (write+commit),
// mirroring the pre-split dispatchParsedFile order.
type perInputApplication struct {
	file    string
	logical string
	ordinal int

	records [][]byte
	result  extract.ExtractResult
	// summary is the extraction core's file-emission boundary, replayed onto the durable
	// target at commit so a successful zero-record input opens (and closes) its output file
	// before Commit — matching the pre-split lifecycle (no rename-of-open-file / FD leak).
	summary extract.FileEmissionSummary

	externalFieldsErr error
	floorErr          error
}

// buildPerInputApplication runs one recipe's per-input application into a worker-local
// bundle WITHOUT creating the durable per-input output target (that is a commit-stage
// concern). It reads only the read-only plan + a per-file config clone and enforces the
// per-input bundle budget (G4) as records are appended, exactly like the aggregate build.
func (st *recipeRunState) buildPerInputApplication(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node) perInputApplication {
	app := perInputApplication{file: file, logical: logical, ordinal: ordinal}
	opts := st.plan.opts

	externalFields, err := buildExternalFieldsForFile(logical, opts, st.plan.fieldPlan, st.plan.warnLimiter)
	if err != nil {
		app.externalFieldsErr = err
		return app
	}

	rp := st.plan.runtimeProvenance
	if file != logical {
		rp.SourceURI = logical
	}
	cloned := extract.CloneRecordMatch(st.plan.extCfg)
	sink := &collectingRecordSink{maxRecords: st.bundleMaxRecords, maxBytes: st.bundleMaxBytes}
	app.result = extract.ProcessParsedDocument(ctx, doc, file, st.plan.sigCfg, cloned, st.plan.appCfg, externalFields, rp, sink)
	app.records = sink.records
	app.summary = sink.summary

	if app.result.Error == nil &&
		app.result.Disposition != extract.DispositionFailed &&
		app.result.Disposition != extract.DispositionNotApplicable {
		if floorErr := enforceMinOccurrences(opts, cloned, st.plan.sigCfg, app.result.LogicalURI, app.result.PerSelectorCounts, app.result.PerSelectorCountsComplete, app.result.SignatureMatchStatus, app.result.SignatureConfidence); floorErr != nil {
			app.floorErr = floorErr
		}
	}
	return app
}

// commitPerInputApplication writes one built bundle to this recipe's own per-input output
// file and updates its ledgers, in input-ordinal order. It is the sole owner of output
// targets, counts, dispositions, failures, and the manifest. Behavior mirrors the pre-split
// dispatchParsedFile branch-for-branch: external-fields failure, extraction/write/close
// failure, floor miss, or success. A replay write error is folded into result.Error exactly
// as the pre-split path surfaced a sink-write failure during extraction.
func (st *recipeRunState) commitPerInputApplication(ctx context.Context, app perInputApplication) error {
	opts := st.plan.opts
	logger := dispatchLogger()

	if app.externalFieldsErr != nil {
		if !opts.ContinueOnError {
			return fmt.Errorf("recipe %q: failed to build external fields for %s: %w", st.plan.RecipeID, app.logical, app.externalFieldsErr)
		}
		result := recoverableFailureResult(app.file, app.logical, fmt.Errorf("failed to build external fields: %w", app.externalFieldsErr), extract.DispositionReasonValidationError)
		return recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
	}

	target, err := newJSONOutputTarget(opts, app.logical)
	if err != nil {
		return fmt.Errorf("recipe %q: failed to open output for %s: %w", st.plan.RecipeID, app.logical, err)
	}

	result := app.result
	var writeErr error
	for _, data := range app.records {
		if werr := target.writeMarshaled(data); werr != nil {
			writeErr = werr
			break
		}
	}
	// Replay the extraction core's file-boundary call onto the durable target, exactly as
	// ProcessParsedDocument did pre-split. For a non-failed input this opens the output (a
	// no-op when records already opened it; the file CREATE for a zero-record applied /
	// not-applicable input) so the Close below closes it and Commit renames a CLOSED file —
	// rather than Commit ensureOpen()ing after Close() and renaming a still-open file (an FD
	// leak, non-portable). A boundary error is a durable-output failure, folded into
	// result.Error like a write error.
	if writeErr == nil {
		if berr := target.OnFileBoundary(ctx, app.summary); berr != nil {
			writeErr = fmt.Errorf("failed to emit file boundary: %w", berr)
		}
	}
	closeErr := target.Close(ctx)

	// The pre-split path surfaced a sink-write failure as result.Error during extraction;
	// here extraction wrote to a worker-local buffer, so the durable write (and any failure)
	// happens now. Fold it in so the existing failure handling applies unchanged.
	if writeErr != nil && result.Error == nil {
		result.Error = writeErr
		result.Disposition = extract.DispositionFailed
		result.DispositionReason = extract.DispositionReasonInternalError
		result.DispositionDetail = writeErr.Error()
	}

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
				return fmt.Errorf("recipe %q: failed to process %s: %w", st.plan.RecipeID, app.logical, result.Error)
			}
			st.dispositionErr = failureErrorForResult(result, st.sanitizeRoots)
			return st.dispositionErr
		}
		_ = recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
		return nil
	}
	if closeErr != nil {
		target.Abort()
		return fmt.Errorf("recipe %q: failed to close output for %s: %w", st.plan.RecipeID, app.logical, closeErr)
	}

	if result.Disposition != extract.DispositionNotApplicable && app.floorErr != nil {
		target.Abort()
		if !opts.ContinueOnError {
			st.dispositionErr = app.floorErr
			return app.floorErr
		}
		reason := failureReasonForError(app.floorErr)
		if reason == "" {
			reason = extract.DispositionReasonMinOccurrencesViolation
		}
		result.Disposition = extract.DispositionFailed
		result.DispositionReason = reason
		result.DispositionDetail = app.floorErr.Error()
		_ = recordFailedSequentialResult(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots, st.manifestEnabled, logger)
		return nil
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

// buildAggregateApplicationContained runs buildAggregateApplication with panic recovery,
// converting a panic in the per-input application surface (signature/applicability/
// extraction/min_occurrences/external-field building over adversarial or malformed XML)
// into a recipe-level failure verdict rather than crashing the process.
//
// SUM-068 G3: from the input-workers slice the application runs on a worker, where an
// unrecovered panic would take down the whole invocation and could deadlock the ordered
// committer. Recovering it into a Failed result routes the input through the committer's
// existing extraction-failure branch — recorded as a per-input failure under
// --continue-on-error, or fatal (aborting the run, with the cloud incomplete:true
// manifest still written by the run-level defer) in fail-fast mode — so failure
// accounting and ADR-0009 semantics are preserved. The serial path stays uncontained at
// the application stage (byte-identical to pre-SUM-068), matching how parse-panic
// containment is concurrency-only.
// hook, when non-nil, is a test-only seam invoked inside the recovery region before the
// application runs, so injected panics exercise containment and injected blocking exercises
// concurrency/backpressure. It is always nil in production.
func (st *recipeRunState) buildAggregateApplicationContained(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node, hook func(ordinal int)) (app aggregateApplication) {
	defer func() {
		if r := recover(); r != nil {
			app = aggregateApplication{file: file, logical: logical, ordinal: ordinal}
			app.result = recoverableFailureResult(file, logical, fmt.Errorf("panic during recipe application: %v", r), extract.DispositionReasonInternalError)
		}
	}()
	if hook != nil {
		hook(ordinal)
	}
	return st.buildAggregateApplication(ctx, file, logical, ordinal, doc)
}

// commitAggregateApplication applies one built bundle to this recipe's durable aggregate
// output and ledgers, in input-ordinal order. It is the sole owner of the aggregate
// writer, counts, dispositions, failures, and manifest — every shared mutation lives
// here, never in the build stage. Behavior mirrors the pre-split dispatchParsedFileAggregate
// branch-for-branch: external-fields failure, extraction failure, floor miss, or success.
func (st *recipeRunState) commitAggregateApplication(ctx context.Context, app aggregateApplication) error {
	opts := st.plan.opts

	// Start this input's per-input buffer (no-op unless buffering is engaged).
	st.aggWriter.beginInput()

	if app.externalFieldsErr != nil {
		st.aggWriter.discardInput()
		if !opts.ContinueOnError {
			return fmt.Errorf("recipe %q: failed to build external fields for %s: %w", st.plan.RecipeID, app.logical, app.externalFieldsErr)
		}
		result := recoverableFailureResult(app.file, app.logical, fmt.Errorf("failed to build external fields: %w", app.externalFieldsErr), extract.DispositionReasonValidationError)
		recordFailedAggregateInput(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return nil
	}

	st.aggWriter.setCurrentInput(app.ordinal)
	before := st.aggWriter.totalRecords

	if app.result.Error != nil || app.result.Disposition == extract.DispositionFailed {
		// Failed input: never flush its rows. (We never replayed them into the writer, so
		// discardInput just clears any prior buffer; earlier inputs' committed rows are
		// untouched.)
		st.aggWriter.discardInput()
		if !opts.ContinueOnError {
			if app.result.Error != nil {
				return fmt.Errorf("recipe %q: failed to process %s: %w", st.plan.RecipeID, app.logical, app.result.Error)
			}
			st.dispositionErr = failureErrorForResult(app.result, st.sanitizeRoots)
			return st.dispositionErr
		}
		recordFailedAggregateInput(app.result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return nil
	}

	if app.floorErr != nil {
		st.aggWriter.discardInput()
		if !opts.ContinueOnError {
			st.dispositionErr = app.floorErr
			return app.floorErr
		}
		reason := failureReasonForError(app.floorErr)
		if reason == "" {
			reason = extract.DispositionReasonMinOccurrencesViolation
		}
		app.result.Disposition = extract.DispositionFailed
		app.result.DispositionReason = reason
		app.result.DispositionDetail = app.floorErr.Error()
		recordFailedAggregateInput(app.result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return nil
	}

	// The input extracted cleanly: replay its buffered records into the shared shard. With
	// buffering engaged, OnRecord buffers and commitInput flushes; without buffering,
	// OnRecord writes straight through. Either way the writeRecordLocked sequence (and so
	// the shard rolling, digests, and ordinal ranges) is identical to the pre-split stream.
	// A write/commit failure is a terminal output/sink error (ADR-0009): it aborts the
	// whole run even under --continue-on-error, never recorded as a recoverable failure.
	for _, rec := range app.records {
		if err := st.aggWriter.writeMarshaled(rec); err != nil {
			return terminalDispatch(fmt.Errorf("recipe %q: failed to commit aggregate output for %s: %w", st.plan.RecipeID, app.logical, err))
		}
	}
	if cerr := st.aggWriter.commitInput(); cerr != nil {
		return terminalDispatch(fmt.Errorf("recipe %q: failed to commit aggregate output for %s: %w", st.plan.RecipeID, app.logical, cerr))
	}
	recordCount := st.aggWriter.totalRecords - before
	st.failures.addApplied()

	if app.result.Disposition != "" {
		st.dispositions.add(app.result, st.sanitizeRoots)
	}
	if st.manifestEnabled {
		input, err := provenance.BuildInputLedger(app.result.File, app.result.LogicalURI, resolvedInputHandle(opts), st.sanitizeRoots...)
		if err != nil {
			return terminalDispatch(fmt.Errorf("recipe %q: failed to build input ledger for %s: %w", st.plan.RecipeID, app.logical, err))
		}
		input.RecordType = st.plan.extCfg.RecordType
		rc := recordCount
		input.RecordCount = &rc
		applyInputDisposition(&input, app.result, st.sanitizeRoots)
		if input.Disposition == "" {
			// Inventory completeness (R5): an applied input without an explicit
			// disposition (no applicability config), including zero-record.
			input.Disposition = string(extract.DispositionApplied)
		}
		st.manifestInputs = append(st.manifestInputs, input)
	}
	st.counts[st.plan.extCfg.RecordType] += recordCount
	return nil
}

// --- builtApplication adapters -------------------------------------------------------
//
// Thin adapters so each application value satisfies builtApplication; the substantive
// commit logic stays in commit{Aggregate,PerInput}Application. recordCount feeds the G4
// in-flight bound (emitted records held in this bundle).

func (a aggregateApplication) commit(ctx context.Context, st *recipeRunState) error {
	return st.commitAggregateApplication(ctx, a)
}

func (a aggregateApplication) recordCount() int { return len(a.records) }

func (a perInputApplication) commit(ctx context.Context, st *recipeRunState) error {
	return st.commitPerInputApplication(ctx, a)
}

func (a perInputApplication) recordCount() int { return len(a.records) }

// buildPerInputApplicationContained is the G3 panic-contained wrapper for the per-input
// build, mirroring buildAggregateApplicationContained: a panic in the application surface
// becomes a recipe-level failure verdict the committer records (continue-on-error) or
// aborts on (fail-fast), rather than crashing the worker. hook is a test-only seam (nil in
// production).
func (st *recipeRunState) buildPerInputApplicationContained(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node, hook func(ordinal int)) (app perInputApplication) {
	defer func() {
		if r := recover(); r != nil {
			app = perInputApplication{file: file, logical: logical, ordinal: ordinal}
			app.result = recoverableFailureResult(file, logical, fmt.Errorf("panic during recipe application: %v", r), extract.DispositionReasonInternalError)
		}
	}()
	if hook != nil {
		hook(ordinal)
	}
	return st.buildPerInputApplication(ctx, file, logical, ordinal, doc)
}

// buildApplicationContained builds one recipe's worker-safe bundle for the input, routing by
// output mode. Both branches are panic-contained (G3) and return a builtApplication the
// ordered committer applies in input order.
func (st *recipeRunState) buildApplicationContained(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node, hook func(ordinal int)) builtApplication {
	if st.aggWriter != nil {
		return st.buildAggregateApplicationContained(ctx, file, logical, ordinal, doc, hook)
	}
	return st.buildPerInputApplicationContained(ctx, file, logical, ordinal, doc, hook)
}
