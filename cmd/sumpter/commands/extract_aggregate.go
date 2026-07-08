package commands

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/fulmenhq/sumpter/internal/provenance"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/uriio"
)

// Output modes for --output-mode.
const (
	outputModePerInput  = "per-input"
	outputModeAggregate = "aggregate"
)

// isAggregateMode reports whether the run should stream all inputs' records to one
// NDJSON writer per invocation instead of one file per input.
func isAggregateMode(opts *ExtractOptions) bool {
	return opts != nil && strings.TrimSpace(opts.OutputMode) == outputModeAggregate
}

// validateAggregateOptions enforces the aggregate-mode plan-time contract before
// any input is read. Per-input mode (default) is a no-op so existing callers are
// untouched. Aggregate requires --output-path and a manifest (input-set provenance
// is half the feature), accepts JSON/NDJSON only, is serial in v0 (rejects the
// record-index parallel path), and — until the cloud slice lands — refuses a cloud
// destination. Caps must be non-negative.
func validateAggregateOptions(opts *ExtractOptions, outputFormats []string) error {
	if opts == nil {
		return nil
	}
	mode := strings.TrimSpace(opts.OutputMode)
	switch mode {
	case "", outputModePerInput:
		// Per-input caps are meaningless; flag them rather than ignore silently.
		if opts.AggregateMaxRecords != 0 || opts.AggregateMaxBytes != 0 {
			return fmt.Errorf("--aggregate-max-records/--aggregate-max-bytes require --output-mode aggregate")
		}
		return nil
	case outputModeAggregate:
		// fall through to aggregate validation
	default:
		return fmt.Errorf("invalid --output-mode %q: expected %q or %q", mode, outputModePerInput, outputModeAggregate)
	}

	if strings.TrimSpace(opts.OutputPath) == "" {
		return fmt.Errorf("--output-mode aggregate requires --output-path (the aggregate stream and its input-set provenance cannot be written to stdout)")
	}
	if opts.NoManifest {
		return fmt.Errorf("--output-mode aggregate cannot be combined with --no-manifest: the input-set provenance manifest is required to keep consolidated output traceable")
	}
	for _, f := range outputFormats {
		if f != recipesmanifest.OutputFormatJSON && f != recipesmanifest.OutputFormatNDJSON {
			return fmt.Errorf("--output-mode aggregate supports only json/ndjson output, not %q; aggregate emits NDJSON (one record envelope per line)", f)
		}
	}
	if opts.RecordIndex != "" {
		return fmt.Errorf("--output-mode aggregate does not support --record-index (parallel record-index extraction) in this version; aggregate is a serial streamed writer")
	}
	if opts.AggregateMaxRecords < 0 {
		return fmt.Errorf("--aggregate-max-records must be >= 0 (0 = uncapped)")
	}
	if opts.AggregateMaxBytes < 0 {
		return fmt.Errorf("--aggregate-max-bytes must be >= 0 (0 = uncapped)")
	}
	// Cloud aggregate: each shard is one published object subject to the single-PUT
	// limit. Enforce the byte cap PROACTIVELY at plan time (R7) — a cloud run must be
	// capped at or below the limit so the streamed byte count rolls a shard BEFORE it
	// could exceed it, never discovering an over-limit object only at publish after
	// gigabytes were staged.
	if referenceIsCloud(opts.OutputPath) {
		if opts.AggregateMaxBytes <= 0 {
			return fmt.Errorf("--output-mode aggregate to a cloud (s3://) destination requires --aggregate-max-bytes: each shard is one object subject to the %d-byte (5 GiB) single-PUT limit, so the stream must be capped to roll shards proactively", uriio.MaxSinglePutBytes)
		}
		if opts.AggregateMaxBytes > uriio.MaxSinglePutBytes {
			return fmt.Errorf("--aggregate-max-bytes %d exceeds the cloud single-PUT limit of %d bytes (5 GiB); each aggregate shard is one object and must stay at or below it", opts.AggregateMaxBytes, uriio.MaxSinglePutBytes)
		}
	}
	return nil
}

// resolvedAggregateInputOrder returns the resolved input list in the deterministic
// order aggregate ordinals are assigned from. For --file-list and --files the
// caller-provided order is authoritative; for --input-path directory discovery the
// walk order is not guaranteed stable, so it is sorted before ordinals are assigned.
func resolvedAggregateInputOrder(opts *ExtractOptions, files []string) []string {
	if opts != nil && strings.TrimSpace(opts.InputPath) != "" {
		ordered := append([]string(nil), files...)
		sort.Strings(ordered)
		return ordered
	}
	return files
}

// aggregateWriter is the invocation-local streamed NDJSON sink for aggregate mode.
// It implements extract.RecordSink, so each input's records are appended to the
// single open shard as they are extracted (no per-input file, no buffer-all). When
// a record/byte cap would be exceeded it rolls to the next lexically ordered shard
// BEFORE writing the record. Each shard streams to a local ".partial" staging file
// (sha256 + byte/record counters maintained incrementally); on a successful run all
// shards are committed (renamed) atomically, and on failure the staging files are
// removed so a failed run never leaves successful-looking output.
type aggregateWriter struct {
	mu         sync.Mutex
	opts       *ExtractOptions
	outputPath string
	maxRecords int
	maxBytes   int64
	// sharded is true when any cap is set: output is numbered records-NNNNN.jsonl;
	// otherwise a single records.jsonl that never rolls.
	sharded bool
	// cloud routes each shard through the output session (openOutputTarget/Publish,
	// R1) and publishes shards INCREMENTALLY (each shard is one object — they cannot
	// be renamed all-at-once like local). Local stays all-or-nothing (.partial +
	// rename at commit). Cloud is always sharded (R7 requires a byte cap).
	cloud bool

	// buffering engages the per-input transactional barrier (set when --continue-on-error
	// is used OR the recipe declares min_occurrences floors — see aggregateBuffering):
	// records accumulate in pending instead of streaming straight to the shard, so a
	// failed/floor-missing input's rows are discarded (discardInput) before they ever
	// touch the shared shard, and a successful input is flushed atomically (commitInput).
	// The shared shard therefore only ever receives whole, qualifying inputs — no
	// committed bytes/digest/shards ever need rolling back. For CLOUD this is the
	// mechanism that makes continue-on-error and floors safe: publish happens only at the
	// per-input flush on success, so a discarded input is never PUT. Bounded by one
	// input's records (independent of input count). Off (direct streaming) by default,
	// keeping the audited fail-fast path byte-identical.
	buffering bool
	pending   [][]byte

	currentInputOrdinal int

	// open shard state
	open        bool
	shardOrd    int
	file        *os.File
	hasher      hash.Hash
	byteCount   int64
	recordCount int
	inputStart  int
	inputEnd    int
	curTgt      *uriio.OutputTarget // cloud only: the open shard's publish target

	shards       []provenance.AggregateOutput
	stagePaths   []string // local: ".partial" staging paths, in shard order
	finalPaths   []string // local: final paths, parallel to stagePaths
	totalRecords int
}

// newAggregateWriter builds the writer. buffering engages the per-input spool barrier
// (a failed/floor-missing input is discarded before it touches the shared shard) — the
// caller passes true when --continue-on-error is set OR the recipe declares
// min_occurrences floors, so both work locally AND for cloud (a failed input is never
// published, since publish happens only at the per-input flush on success).
func newAggregateWriter(opts *ExtractOptions, buffering bool) *aggregateWriter {
	return &aggregateWriter{
		opts:       opts,
		outputPath: opts.OutputPath,
		maxRecords: opts.AggregateMaxRecords,
		maxBytes:   opts.AggregateMaxBytes,
		sharded:    opts.AggregateMaxRecords > 0 || opts.AggregateMaxBytes > 0,
		cloud:      referenceIsCloud(opts.OutputPath),
		buffering:  buffering,
	}
}

// aggregateBuffering reports whether the per-input spool barrier must engage: under
// --continue-on-error (a failed input is discarded + recorded) or whenever the recipe
// declares min_occurrences floors (a floor miss must discard the input's rows before
// they are flushed/published). For cloud this is the mechanism that makes both safe.
func aggregateBuffering(opts *ExtractOptions, extCfg *extract.ExtractRecordMatch) bool {
	return opts.ContinueOnError || hasDeclaredMinOccurrences(extCfg)
}

// shardFileName returns the lexically ordered shard name for the given 1-based
// ordinal: records.jsonl when uncapped, records-00001.jsonl when sharded.
func (w *aggregateWriter) shardFileName(ordinal int) string {
	if !w.sharded {
		return "records.jsonl"
	}
	return fmt.Sprintf("records-%05d.jsonl", ordinal)
}

func (w *aggregateWriter) setCurrentInput(ordinal int) {
	w.mu.Lock()
	w.currentInputOrdinal = ordinal
	w.mu.Unlock()
}

func (w *aggregateWriter) openShard() error {
	w.shardOrd++
	name := w.shardFileName(w.shardOrd)
	var f *os.File
	if w.cloud {
		// Route the shard object through the output session (R1): a cloud target
		// stages to a session-managed local file that Publish (in finalizeShard)
		// uploads through the SUM-005 write boundary; the staging file is the byte
		// source we hash and size-cap.
		tgt, err := openOutputTarget(context.Background(), w.opts, outputRefJoin(w.outputPath, name))
		if err != nil {
			return fmt.Errorf("open aggregate cloud shard %s: %w", name, err)
		}
		if err := os.MkdirAll(filepath.Dir(tgt.LocalPath), 0o750); err != nil {
			return fmt.Errorf("create aggregate staging directory: %w", err)
		}
		f, err = os.OpenFile(tgt.LocalPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 - session-managed staging path
		if err != nil {
			return fmt.Errorf("open aggregate cloud shard staging %s: %w", name, err)
		}
		w.curTgt = tgt
	} else {
		finalPath := outputRefJoin(w.outputPath, name)
		stagePath := finalPath + ".partial"
		if err := os.MkdirAll(w.outputPath, 0o750); err != nil {
			return fmt.Errorf("create aggregate output directory: %w", err)
		}
		var err error
		f, err = os.OpenFile(stagePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) // #nosec G304 - tool-generated shard name under the validated output dir
		if err != nil {
			return fmt.Errorf("open aggregate shard %s: %w", name, err)
		}
		w.stagePaths = append(w.stagePaths, stagePath)
		w.finalPaths = append(w.finalPaths, finalPath)
	}
	w.open = true
	w.file = f
	w.hasher = sha256.New()
	w.byteCount = 0
	w.recordCount = 0
	w.inputStart = 0
	w.inputEnd = 0
	return nil
}

// finalizeShard closes the open shard and records its provenance summary (path,
// record count, content digest, covered input-ordinal range).
func (w *aggregateWriter) finalizeShard() error {
	if !w.open {
		return nil
	}
	if err := w.file.Close(); err != nil {
		return fmt.Errorf("close aggregate shard: %w", err)
	}
	shard := provenance.AggregateOutput{
		Path:              w.shardFileName(w.shardOrd),
		Format:            recipesmanifest.OutputFormatNDJSON,
		RecordCount:       w.recordCount,
		SHA256:            "sha256:" + hex.EncodeToString(w.hasher.Sum(nil)),
		InputOrdinalStart: w.inputStart,
		InputOrdinalEnd:   w.inputEnd,
	}
	if w.cloud {
		// Publish the completed shard NOW (incremental, R1): each shard is one object.
		// Record the credential-handle NAME (S8) so the committed object is auditable.
		shard.CredentialsHandle = w.opts.outputHandle
		if err := w.curTgt.Publish(context.Background()); err != nil {
			return fmt.Errorf("publish aggregate shard %s: %w", shard.Path, err)
		}
		w.curTgt = nil
	}
	// A finalized shard is a committed shard: local will be renamed at commit, cloud
	// is already published. w.shards is therefore the committed-shard ledger an
	// incomplete (R8) manifest reports on failure.
	w.shards = append(w.shards, shard)
	w.open = false
	w.file = nil
	return nil
}

// OnRecord appends one extracted record to the current shard, rolling first if the
// record would push the shard past a configured cap. Locally, a single record larger
// than the byte cap is written to its own shard rather than dropped or split. For
// CLOUD output that record could never publish (each shard is one object subject to
// the single-PUT limit), so it is rejected here — before any staging work — rather
// than discovered at Publish (R7).
func (w *aggregateWriter) OnRecord(ctx context.Context, record extract.EmittedRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal aggregate record: %w", err)
	}
	data = append(data, '\n')
	return w.writeMarshaled(data)
}

// writeMarshaled appends one already-marshaled record (one JSON object + trailing newline)
// following the same per-input buffering and shard cap/roll rules as OnRecord. OnRecord
// marshals then calls it (single-recipe streaming path); the extract-multi input-worker
// path marshals on the worker and replays the bytes here, so marshaling is parallelized and
// the per-input bundle budget is exact — and because the bytes are identical to what
// OnRecord would produce, the committed shard is byte-identical to streaming the records.
func (w *aggregateWriter) writeMarshaled(data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.buffering {
		// Per-input barrier: hold the record until the input commits (flush) or fails
		// (discard). data is a fresh allocation per call, so retaining it is safe.
		w.pending = append(w.pending, data)
		return nil
	}
	return w.writeRecordLocked(data)
}

// writeRecordLocked appends one fully-marshaled record (with trailing newline) to the
// current shard, rolling to a new shard first if the record would push it past a
// configured cap. The caller holds w.mu. It is the single point where bytes, digest,
// and counters are committed to a shard — both the direct (fail-fast) path and the
// per-input flush (commitInput) route through it.
func (w *aggregateWriter) writeRecordLocked(data []byte) error {
	if w.cloud && w.maxBytes > 0 && int64(len(data)) > w.maxBytes {
		return fmt.Errorf("aggregate cloud record is %d bytes, larger than --aggregate-max-bytes %d: a single record cannot fit in a shard within the cloud single-PUT limit, so it can never be published; raise --aggregate-max-bytes (up to %d) or split the source", len(data), w.maxBytes, uriio.MaxSinglePutBytes)
	}

	if !w.open {
		if err := w.openShard(); err != nil {
			return err
		}
	} else if w.recordCount > 0 {
		overBytes := w.maxBytes > 0 && w.byteCount+int64(len(data)) > w.maxBytes
		overRecords := w.maxRecords > 0 && w.recordCount >= w.maxRecords
		if overBytes || overRecords {
			if err := w.finalizeShard(); err != nil {
				return err
			}
			if err := w.openShard(); err != nil {
				return err
			}
		}
	}

	n, err := w.file.Write(data)
	if err != nil {
		return fmt.Errorf("write aggregate record: %w", err)
	}
	_, _ = w.hasher.Write(data[:n])
	w.byteCount += int64(n)
	w.recordCount++
	w.totalRecords++
	if w.inputStart == 0 {
		w.inputStart = w.currentInputOrdinal
	}
	w.inputEnd = w.currentInputOrdinal
	return nil
}

// beginInput starts a fresh per-input buffer. No-op when not buffering (pending stays
// nil and commitInput/discardInput are no-ops), so the fail-fast path is unchanged.
func (w *aggregateWriter) beginInput() {
	w.mu.Lock()
	w.pending = w.pending[:0]
	w.mu.Unlock()
}

// commitInput flushes the current input's buffered records into the shared shard
// (rolling/publishing as needed), making them part of the committed output. Called
// only after the input extracted successfully, so the shard never receives a failed
// input's rows. No-op when not buffering.
func (w *aggregateWriter) commitInput() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, data := range w.pending {
		if err := w.writeRecordLocked(data); err != nil {
			return err
		}
	}
	w.pending = w.pending[:0]
	return nil
}

// discardInput drops the current input's buffered records so a failed input (or a
// min_occurrences floor miss) never contributes a row to the shared shard. No-op when
// not buffering.
func (w *aggregateWriter) discardInput() {
	w.mu.Lock()
	w.pending = w.pending[:0]
	w.mu.Unlock()
}

// OnFileBoundary is a no-op for the aggregate sink: aggregate output deliberately
// spans input boundaries (the whole point), so there is no per-input flush.
func (w *aggregateWriter) OnFileBoundary(ctx context.Context, _ extract.FileEmissionSummary) error {
	return ctx.Err()
}

// Close satisfies extract.RecordSink but is intentionally a no-op: the aggregate
// writer spans every input in the invocation, so the per-input extraction core must
// not close it. The run lifecycle is owned by commit()/abort().
func (w *aggregateWriter) Close(ctx context.Context) error {
	return ctx.Err()
}

// commit finalizes the open shard and atomically renames every staged shard to its
// final path. On an empty run (no records) it still emits one empty records.jsonl
// covering the resolved input set, so downstream always has a file.
func (w *aggregateWriter) commit(totalInputs int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.open && len(w.shards) == 0 {
		// Zero-record successful run: emit a single empty file covering all inputs.
		if err := w.openShard(); err != nil {
			return err
		}
		w.inputStart = 1
		w.inputEnd = totalInputs
	}
	if err := w.finalizeShard(); err != nil {
		return err
	}
	// Cloud shards were each published in finalizeShard (incremental, R1); only local
	// defers to an all-or-nothing rename here so a failed local run leaves nothing.
	if !w.cloud {
		for i, stage := range w.stagePaths {
			if err := os.Rename(stage, w.finalPaths[i]); err != nil {
				return fmt.Errorf("commit aggregate shard %s: %w", w.finalPaths[i], err)
			}
		}
	}
	return nil
}

// committedShards returns the shards already finalized (local: staged+renamed-pending;
// cloud: PUBLISHED). On a failed cloud run these are the orphaned objects an incomplete
// manifest (R8) must record. Caller holds no lock; used after the run loop returns.
func (w *aggregateWriter) committedShards() []provenance.AggregateOutput {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]provenance.AggregateOutput(nil), w.shards...)
}

// abort discards un-published staging so a failed run leaves no successful-looking
// output. Already-PUBLISHED cloud shards cannot be un-published — they are recorded by
// an incomplete (R8) manifest instead; only the open shard's un-published staging is
// dropped.
func (w *aggregateWriter) abort() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.open && w.file != nil {
		_ = w.file.Close()
		w.open = false
	}
	if w.cloud {
		if w.curTgt != nil {
			_ = os.Remove(w.curTgt.LocalPath)
			w.curTgt = nil
		}
		return
	}
	for _, stage := range w.stagePaths {
		_ = os.Remove(stage)
	}
}

// runAggregateJSONStreamingExtraction streams every input's records to one NDJSON
// writer (rolling shards) in deterministic resolved-input order, recording the
// per-input inventory and per-shard integrity digests in the provenance manifest.
func runAggregateJSONStreamingExtraction(opts *ExtractOptions, sigCfg *extract.FileSignature, extCfg *extract.ExtractRecordMatch, files []string, logicalByLocal map[string]string, fieldPlan *externalFieldPlan, warnLimiter *sourceExtractionWarnLimiter, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time) (err error) {
	logger := logging.GetLogger()
	ctx := context.Background()
	sanitizeRoots := manifestSanitizeRoots(opts)
	ordered := resolvedAggregateInputOrder(opts, files)

	writer := newAggregateWriter(opts, aggregateBuffering(opts, extCfg))
	manifestInputs := make([]provenance.Input, 0, len(ordered))
	countsByRecordType := make(map[string]int)
	dispositionSummary := newDispositionSummary(len(ordered))
	failureManifest := newExtractFailureManifest(len(ordered))
	// manifestFinalized is set only after the run's shards AND its normal manifest are
	// durably written (see end of function). Until then, any terminal output
	// failure — including one AFTER shards are committed but BEFORE the manifest is written
	// — must fall through to the incomplete:true (R8) path below, so committed cloud
	// objects are never left undiscoverable.
	manifestFinalized := false

	// On a genuine failure: drop un-published staging, and — if a cloud run already
	// published shards — write an incomplete (R8) manifest recording those committed
	// objects so the orphans are discoverable. A fully-finalized run (success, or a
	// continue-on-error partial that wrote its normal manifest) keeps its output.
	defer func() {
		if err == nil || manifestFinalized {
			return
		}
		if c := writer.committedShards(); writer.cloud && len(c) > 0 {
			writeIncompleteAggregateManifest(opts, runtimeProvenance, startedAt, manifestInputs, c, countsByRecordType, sanitizeRoots)
		}
		writer.abort()
	}()

	for i, file := range ordered {
		ordinal := i + 1
		logical := logicalIdentity(file, logicalByLocal)
		writer.setCurrentInput(ordinal)
		// Start this input's per-input buffer (no-op unless --continue-on-error). Its
		// records stay buffered until the input commits (flush) or fails (discard), so a
		// failed input never reaches the shared shard.
		writer.beginInput()

		externalFields, ferr := buildExternalFieldsForFile(logical, opts, fieldPlan, warnLimiter)
		if ferr != nil {
			if opts.ContinueOnError {
				writer.discardInput()
				failResult := recoverableFailureResult(file, logical, fmt.Errorf("failed to build external fields: %w", ferr), extract.DispositionReasonValidationError)
				recordFailedAggregateInput(failResult, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots)
				continue
			}
			return fmt.Errorf("failed to build external fields for file %s: %w", logical, ferr)
		}

		rp := runtimeProvenance
		if file != logical {
			rp.SourceURI = logical
		}
		before := writer.totalRecords
		result := extract.ProcessFileWithApplicabilityToSink(ctx, file, sigCfg, extCfg, opts.ApplicabilityConfig, externalFields, opts.AllowLargeFiles, rp, writer)

		if result.Error != nil || result.Disposition == extract.DispositionFailed {
			// Drop the failed input's buffered rows so they never reach the shared shard.
			// Fail-fast (default): return and let the deferred abort drop all staging.
			// Continue-on-error: record the failure and move on; earlier inputs' committed
			// rows are untouched because the failed input's rows were only ever buffered.
			writer.discardInput()
			if !opts.ContinueOnError {
				if result.Error != nil {
					logger.Error("Failed to process file", zap.String("file", result.LogicalURI), zap.Error(result.Error))
					return fmt.Errorf("failed to process file %s: %w", result.LogicalURI, result.Error)
				}
				return fmt.Errorf("failed to process file %s", result.LogicalURI)
			}
			recordFailedAggregateInput(result, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots)
			continue
		}

		// Enforce match_selectors[].min_occurrences floors at input completion (before the
		// buffered rows are flushed): a floor miss is an input-level failure handled by the
		// same per-input barrier — discard the buffered rows and record the input as failed,
		// or (fail-fast) abort the run. The streamed shard never receives a floor-failing
		// input's rows.
		if result.Disposition != extract.DispositionNotApplicable {
			if floorErr := enforceMinOccurrences(opts, extCfg, sigCfg, result.LogicalURI, result.PerSelectorCounts, result.PerSelectorCountsComplete, result.SignatureMatchStatus, result.SignatureConfidence); floorErr != nil {
				writer.discardInput()
				if !opts.ContinueOnError {
					return floorErr
				}
				reason := failureReasonForError(floorErr)
				if reason == "" {
					reason = extract.DispositionReasonMinOccurrencesViolation
				}
				result.Disposition = extract.DispositionFailed
				result.DispositionReason = reason
				result.DispositionDetail = floorErr.Error()
				recordFailedAggregateInput(result, opts, extCfg, &manifestInputs, dispositionSummary, failureManifest, sanitizeRoots)
				continue
			}
		}

		// The input extracted cleanly: flush its buffered records into the shared shard
		// (rolling/publishing as needed). recordCount is measured after the flush.
		if cerr := writer.commitInput(); cerr != nil {
			return cerr
		}
		recordCount := writer.totalRecords - before
		failureManifest.addApplied()

		if result.Disposition != "" {
			dispositionSummary.add(result, sanitizeRoots)
		}
		input, lerr := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
		if lerr != nil {
			return lerr
		}
		input.RecordType = extCfg.RecordType
		rc := recordCount
		input.RecordCount = &rc
		applyInputDisposition(&input, result, sanitizeRoots)
		if input.Disposition == "" {
			// Inventory completeness (R5): a successful input that carried no explicit
			// disposition (no applicability config) was applied — including the
			// zero-record case, distinguished by record_count == 0.
			input.Disposition = string(extract.DispositionApplied)
		}
		manifestInputs = append(manifestInputs, input)
		countsByRecordType[extCfg.RecordType] += recordCount
		if opts.Progress {
			logger.Info("Extracted records (aggregate)", zap.String("file", result.LogicalURI), zap.Int("record_count", recordCount))
		}
	}

	if cerr := writer.commit(len(ordered)); cerr != nil {
		return cerr
	}

	// Under --continue-on-error, record which inputs failed so the partial run is
	// auditable (mirrors per-input mode's failures.json + non-zero exit).
	if opts.ContinueOnError && failureManifest.Failed > 0 {
		if werr := writeExtractFailureManifest(opts, outputRefJoin(opts.OutputPath, "failures.json"), failureManifest); werr != nil {
			return werr
		}
	}

	// Per-shard provenance Output entries (one Output per aggregate shard).
	manifestOutputs := make([]provenance.Output, 0, len(writer.shards))
	for _, shard := range writer.shards {
		manifestOutputs = append(manifestOutputs, provenanceOutput(shard.Path, recipesmanifest.OutputFormatNDJSON, shard.RecordCount, opts, sanitizeRoots...))
	}

	if opts.ApplicabilityConfig != nil && opts.OutputPath != "" {
		if err := writeDispositionSummary(opts, outputRefJoin(opts.OutputPath, "dispositions.json"), dispositionSummary); err != nil {
			return err
		}
	}

	manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), manifestInputs, manifestOutputs, countsByRecordType, sanitizeRoots)
	manifest.OutputMode = outputModeAggregate
	manifest.AggregateOutputs = writer.shards
	// Emit the input-accounting integers from the gap-free inputs[] inventory this
	// completed aggregate run holds (R5). An unaccounted disposition would be a
	// producer bug, so fail rather than emit unsubstantiated counts.
	if err := manifest.SetInputAccounting(); err != nil {
		return fmt.Errorf("compute input accounting for aggregate manifest: %w", err)
	}
	manifestPath := outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
	if err := writeProvenanceManifest(opts, manifestPath, manifest); err != nil {
		return err
	}
	logger.Info("Provenance manifest written", zap.String("file", manifestPath))

	// Once the normal manifest is durable, the incomplete:true guard must stay off:
	// later sidecar failures (including the optional descriptor) should fail the run
	// without overwriting a complete manifest.
	manifestFinalized = true
	if err := writeDataArtifactDescriptor(opts, manifest); err != nil {
		return err
	}

	// A continue-on-error run that committed its successful inputs still signals partial
	// failure (non-zero exit) so callers do not treat it as a clean run.
	if opts.ContinueOnError && failureManifest.Failed > 0 {
		return fmt.Errorf("partial extraction failure: applied=%d failed=%d failures=%s", failureManifest.Applied, failureManifest.Failed, outputRefJoin(opts.OutputPath, "failures.json"))
	}
	return nil
}

// recordFailedAggregateInput records a failed input under --continue-on-error. Its
// buffered rows were already discarded (discardInput), so it contributes zero rows to
// the shared shard. It is added to the disposition summary, the failures manifest, and
// the per-input inventory (record_count 0, disposition failed) so aggregate provenance
// stays gap-free (R5) and the shard == Σ per-input invariant holds (R4).
func recordFailedAggregateInput(result extract.ExtractResult, opts *ExtractOptions, extCfg *extract.ExtractRecordMatch, manifestInputs *[]provenance.Input, dispositionSummary *dispositionSummaryFile, failureManifest *extractFailureManifestFile, sanitizeRoots []string) {
	if result.Disposition == "" {
		result.Disposition = extract.DispositionFailed
	}
	if result.DispositionReason == "" {
		result.DispositionReason = failureReasonForError(result.Error)
	}
	if result.DispositionReason == "" {
		result.DispositionReason = extract.DispositionReasonInternalError
	}
	if result.DispositionDetail == "" && result.Error != nil {
		result.DispositionDetail = result.Error.Error()
	}
	dispositionSummary.add(result, sanitizeRoots)
	failureManifest.add(result.LogicalURI, result.DispositionReason, result.DispositionDetail, sanitizeRoots)
	input, err := provenance.BuildInputLedger(result.File, result.LogicalURI, resolvedInputHandle(opts), sanitizeRoots...)
	if err != nil {
		logging.Warn("Skipping provenance input ledger for failed aggregate input",
			zap.String("file", result.LogicalURI), zap.Error(err))
		return
	}
	input.RecordType = extCfg.RecordType
	zero := 0
	input.RecordCount = &zero
	applyInputDisposition(&input, result, sanitizeRoots)
	if input.Disposition == "" {
		input.Disposition = string(extract.DispositionFailed)
	}
	*manifestInputs = append(*manifestInputs, input)
}

// writeIncompleteAggregateManifest records the shards a failed aggregate run had
// already published (R8) in an incomplete manifest, so the orphaned cloud objects are
// discoverable for cleanup / idempotent rerun. Best-effort on the failure path: a
// write failure is logged, never masking the original run error.
func writeIncompleteAggregateManifest(opts *ExtractOptions, runtimeProvenance provenance.RuntimeOptions, startedAt time.Time, inputs []provenance.Input, committed []provenance.AggregateOutput, counts map[string]int, sanitizeRoots []string) {
	if !shouldWriteManifest(opts) {
		return
	}
	outputs := make([]provenance.Output, 0, len(committed))
	for _, shard := range committed {
		outputs = append(outputs, provenanceOutput(shard.Path, recipesmanifest.OutputFormatNDJSON, shard.RecordCount, opts, sanitizeRoots...))
	}
	manifest := buildProvenanceManifest(opts, runtimeProvenance, startedAt, time.Now().UTC(), inputs, outputs, counts, sanitizeRoots)
	manifest.OutputMode = outputModeAggregate
	manifest.AggregateOutputs = committed
	manifest.Incomplete = true
	manifestPath := outputRefJoin(opts.OutputPath, provenance.ManifestFileName)
	if werr := writeProvenanceManifest(opts, manifestPath, manifest); werr != nil {
		logging.Error("Failed to write incomplete aggregate manifest; committed cloud shards may be orphaned",
			zap.String("path", manifestPath), zap.Error(werr))
	}
}
