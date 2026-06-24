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
// no record-index, no cloud destination, non-negative caps, no --continue-on-error)
// and additionally must not declare match_selectors[].min_occurrences floors — the
// streamed writer cannot retract an input's already-emitted rows when a floor fails.
func validateAggregateMulti(shared *multiSharedOptions, plans []*RecipePlan) error {
	if shared == nil {
		return nil
	}
	aggregate := shared.OutputMode == outputModeAggregate
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
		// Aggregate-only: a min_occurrences floor must be enforced before output is
		// published, which the streamed writer cannot retract (per-input mode keeps
		// floors).
		if aggregate && hasDeclaredMinOccurrences(plan.extCfg) {
			return fmt.Errorf("recipe %q: --output-mode aggregate does not support match_selectors[].min_occurrences floors in this version: a floor must be enforced before output is published, which the streamed aggregate writer cannot retract; run without aggregate mode", plan.RecipeID)
		}
	}
	return nil
}

// dispatchParsedFileAggregate streams one already-parsed input's records into this
// recipe's invocation-local aggregate writer (rolling shards), recording the
// per-input inventory entry. Aggregate mode rejects --continue-on-error and floors,
// so any extraction failure aborts the whole run (the deferred writer.abort in the
// dispatcher discards every recipe's uncommitted staging) — no failed-input rows are
// ever committed.
func (st *recipeRunState) dispatchParsedFileAggregate(ctx context.Context, file, logical string, ordinal int, doc *xmlquery.Node) error {
	opts := st.plan.opts

	externalFields, err := buildExternalFieldsForFile(logical, opts, st.plan.fieldPlan, st.plan.warnLimiter)
	if err != nil {
		return fmt.Errorf("recipe %q: failed to build external fields for %s: %w", st.plan.RecipeID, logical, err)
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
	recordCount := st.aggWriter.totalRecords - before

	if result.Error != nil || result.Disposition == extract.DispositionFailed {
		if result.Error != nil {
			return fmt.Errorf("recipe %q: failed to process %s: %w", st.plan.RecipeID, logical, result.Error)
		}
		st.dispositionErr = failureErrorForResult(result, st.sanitizeRoots)
		return st.dispositionErr
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

	if st.dispositionErr != nil {
		return st.dispositionErr
	}
	return nil
}
