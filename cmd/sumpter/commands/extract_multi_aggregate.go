package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/antchfx/xmlquery"

	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
)

// validateAggregateMulti enforces the aggregate-mode contract for every recipe in
// an extract-multi pass, before any output session opens. Each recipe inherits the
// same single-recipe plan-time guardrails (json/ndjson + --output-path + manifest,
// no record-index, non-negative caps; cloud requires a byte cap). --continue-on-error
// and match_selectors[].min_occurrences floors are supported on both local and cloud
// destinations via the writer's per-input spool barrier (a failed or floor-missing input
// is discarded at input completion, before its rows are flushed/published to the shared
// shard).
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
	// SUM-068 slice 1: split into a worker-safe build stage (no shared writer/ledger
	// state) and a single-owner commit stage. Here both run on the ordered drain in
	// immediate succession, so behavior is byte-identical to the pre-split path; a later
	// slice hoists the build onto a worker. See extract_multi_bundle.go.
	app := st.buildAggregateApplication(ctx, file, logical, ordinal, doc)
	return st.commitAggregateApplication(ctx, app)
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
		// Per-recipe input accounting from this recipe's own gap-free inputs[]
		// inventory; counts stay isolated per recipe, like the rest of the manifest.
		if err := manifest.SetInputAccounting(); err != nil {
			return fmt.Errorf("recipe %q: compute input accounting for aggregate manifest: %w", st.plan.RecipeID, err)
		}
		manifestPath := outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
		if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
			return err
		}
		logger.Info("Provenance manifest written (aggregate)")
		// After the normal manifest is durable, the run-level incomplete:true failure
		// handler must not overwrite it if a later optional sidecar write fails.
		st.finalized = true
		if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
			return err
		}
		if err := maybeValidateExtractOutput(opts, manifest); err != nil {
			return err
		}
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
		return recipePartialFailure(st.plan.RecipeID, st.failures.Applied, st.failures.Failed)
	}
	return nil
}
