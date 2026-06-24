package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// validateAggregateMulti enforces the aggregate-mode contract for every recipe in
// an extract-multi pass, before any output session opens. Each recipe inherits the
// same single-recipe plan-time guardrails (json/ndjson + --output-path + manifest,
// no record-index, non-negative caps; cloud requires a byte cap and rejects
// --continue-on-error). Local --continue-on-error and match_selectors[].min_occurrences
// floors are both supported via the writer's per-input spool barrier (a floor miss is
// enforced at input completion and the buffered rows are discarded before the shared
// shard changes).
func validateAggregateMulti(shared *multiSharedOptions, plans []*RecipePlan) error {
	if shared == nil {
		return nil
	}
	for _, plan := range plans {
		formats, err := effectiveOutputFormats(plan.opts)
		if err != nil {
			return err
		}
		// Always validate the mode/cap combination — even in per-input or an invalid
		// mode — so a typo (`--output-mode bogus`) or a cap set without aggregate is
		// rejected rather than silently running per-input (the exact high-file-count
		// fan-out aggregate exists to avoid). validateAggregateOptions is a no-op for
		// the real per-input/empty mode with no caps.
		if verr := validateAggregateOptions(plan.opts, formats); verr != nil {
			return fmt.Errorf("recipe %q: %w", plan.RecipeID, verr)
		}
	}
	return nil
}

// writeIncompleteAggregateManifestOnFailure records this recipe's already-published
// cloud shards (R8) in an incomplete manifest when the pass fails, so the orphaned
// objects are discoverable. No-op for local or when no shard was committed.
func (st *recipeRunState) writeIncompleteAggregateManifestOnFailure(startedAt time.Time) {
	// A recipe that finalized cleanly already wrote its own SUCCESSFUL manifest; a
	// sibling recipe failing later must never overwrite it with incomplete:true
	// (per-recipe isolation). Local writers never publish orphans, so they are skipped
	// too — only committed cloud shards need an incomplete record.
	if st.aggWriter == nil || st.finalized || !st.aggWriter.cloud {
		return
	}
	committed := st.aggWriter.committedShards()
	if len(committed) == 0 {
		return
	}
	writeIncompleteAggregateManifest(st.plan.opts, st.plan.runtimeProvenance, startedAt, st.manifestInputs, committed, st.counts, st.sanitizeRoots)
}

// dispatchParsedFileAggregate streams one already-parsed input's records into this
// recipe's invocation-local aggregate writer (rolling shards), recording the
// per-input inventory entry. Under --continue-on-error the writer buffers each input
// (beginInput) and the records are only flushed into the shared shard on success
// (commitInput); a failed input is discarded (discardInput) so its rows never reach
// the shard, and is recorded as a failure. In fail-fast mode any extraction failure
// aborts the whole run (the deferred writer.abort in the dispatcher discards every
// recipe's uncommitted staging) — either way, no failed-input rows are committed.
func (st *recipeRunState) dispatchParsedFileAggregate(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node) error {
	opts := st.plan.opts

	// Start this input's per-input buffer (no-op unless --continue-on-error).
	st.aggWriter.beginInput()

	externalFields, err := buildExternalFieldsForFile(logical, opts, st.plan.fieldPlan, st.plan.warnLimiter)
	if err != nil {
		st.aggWriter.discardInput()
		if !opts.ContinueOnError {
			return fmt.Errorf("recipe %q: failed to build external fields for %s: %w", st.plan.RecipeID, logical, err)
		}
		result := recoverableFailureResult(file, logical, fmt.Errorf("failed to build external fields: %w", err), extract.DispositionReasonValidationError)
		recordFailedAggregateInput(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return nil
	}

	st.aggWriter.setCurrentInput(ordinal)
	rp := st.plan.runtimeProvenance
	if file != logical {
		rp.SourceURI = logical
	}
	// Clone the extract config per recipe per file so compiled XPath state is never
	// shared while M recipes read the one shared, read-only document.
	cloned := extract.CloneRecordMatch(st.plan.extCfg)
	before := st.aggWriter.totalRecords
	result := extract.ProcessParsedDocument(ctx, doc, file, st.plan.sigCfg, cloned, st.plan.appCfg, externalFields, rp, st.aggWriter)

	if result.Error != nil || result.Disposition == extract.DispositionFailed {
		// Drop the failed input's buffered rows. Fail-fast: return; the deferred abort
		// drops all staging. Continue-on-error: record the failure and move on — earlier
		// inputs' committed rows are untouched because the failed input only ever buffered.
		st.aggWriter.discardInput()
		if !opts.ContinueOnError {
			if result.Error != nil {
				return fmt.Errorf("recipe %q: failed to process %s: %w", st.plan.RecipeID, logical, result.Error)
			}
			st.dispositionErr = failureErrorForResult(result, st.sanitizeRoots)
			return st.dispositionErr
		}
		recordFailedAggregateInput(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
		return nil
	}

	// Enforce match_selectors[].min_occurrences floors at input completion (before the
	// buffered rows are flushed): a floor miss is an input-level failure handled by the
	// same per-input barrier — discard the buffered rows and record the input as failed,
	// or (fail-fast) abort the run. The streamed shard never receives a floor-failing
	// input's rows.
	if result.Disposition != extract.DispositionNotApplicable {
		if floorErr := enforceMinOccurrences(opts, cloned, st.plan.sigCfg, result.LogicalURI, result.PerSelectorCounts, result.PerSelectorCountsComplete, result.SignatureMatchStatus, result.SignatureConfidence); floorErr != nil {
			st.aggWriter.discardInput()
			if !opts.ContinueOnError {
				st.dispositionErr = floorErr
				return floorErr
			}
			reason := failureReasonForError(floorErr)
			if reason == "" {
				reason = extract.DispositionReasonMinOccurrencesViolation
			}
			result.Disposition = extract.DispositionFailed
			result.DispositionReason = reason
			result.DispositionDetail = floorErr.Error()
			recordFailedAggregateInput(result, opts, st.plan.extCfg, &st.manifestInputs, st.dispositions, st.failures, st.sanitizeRoots)
			return nil
		}
	}

	// The input extracted cleanly: flush its buffered records into the shared shard. A
	// commit failure is a terminal output/sink error (ADR-0009): it must abort the whole
	// run even under --continue-on-error, never be recorded as a recoverable input failure.
	if cerr := st.aggWriter.commitInput(); cerr != nil {
		return terminalDispatch(fmt.Errorf("recipe %q: failed to commit aggregate output for %s: %w", st.plan.RecipeID, logical, cerr))
	}
	recordCount := st.aggWriter.totalRecords - before
	st.failures.addApplied()

	if result.Disposition != "" {
		st.dispositions.add(result, st.sanitizeRoots)
	}
	if st.manifestEnabled {
		input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), st.sanitizeRoots...)
		if err != nil {
			return terminalDispatch(fmt.Errorf("recipe %q: failed to build input ledger for %s: %w", st.plan.RecipeID, logical, err))
		}
		input.RecordType = st.plan.extCfg.RecordType
		rc := recordCount
		input.RecordCount = &rc
		applyInputDisposition(&input, result, st.sanitizeRoots)
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

// finalizeAggregate commits this recipe's aggregate shards (atomic rename) and writes
// its provenance manifest with the aggregate output_mode + per-shard summaries, under
// the recipe's own <output-root>/<recipe-id>/ directory.
func (st *recipeRunState) finalizeAggregate(startedAt time.Time) error {
	opts := st.plan.opts
	logger := dispatchLogger()

	if err := st.aggWriter.commit(st.inputCount); err != nil {
		st.aggWriter.abort()
		return err
	}

	// Under --continue-on-error, record which inputs failed so the partial run is
	// auditable, mirroring the per-input recipe finalize.
	if opts.ContinueOnError && st.failures.Failed > 0 {
		if err := writeExtractFailureManifest(opts, outputRefJoin(opts.OutputPath, "failures.json"), st.failures); err != nil {
			return err
		}
	}

	manifestOutputs := make([]provenance.Output, 0, len(st.aggWriter.shards))
	for _, shard := range st.aggWriter.shards {
		manifestOutputs = append(manifestOutputs, provenanceOutput(shard.Path, recipesmanifest.OutputFormatNDJSON, shard.RecordCount, opts, st.sanitizeRoots...))
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(opts, outputRefJoin(opts.OutputPath, "dispositions.json"), st.dispositions); err != nil {
			return err
		}
	}
	if st.manifestEnabled {
		manifest := buildProvenanceManifest(opts, st.plan.runtimeProvenance, startedAt, time.Now().UTC(), st.manifestInputs, manifestOutputs, st.counts, st.sanitizeRoots)
		manifest.OutputMode = outputModeAggregate
		manifest.AggregateOutputs = st.aggWriter.shards
		manifestPath := outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written (aggregate)")
	}

	// This recipe committed its shards and wrote its own successful manifest, so the
	// run-level failure handler must not later overwrite it with incomplete:true — set
	// before any partial-failure return below.
	st.finalized = true

	if st.dispositionErr != nil {
		return st.dispositionErr
	}
	// A continue-on-error recipe that committed its successful inputs still signals
	// partial failure so the pass exits non-zero, mirroring per-input finalize.
	if opts.ContinueOnError && st.failures.Failed > 0 {
		return fmt.Errorf("recipe %q partial failure: applied=%d failed=%d", st.plan.RecipeID, st.failures.Applied, st.failures.Failed)
	}
	return nil
}
