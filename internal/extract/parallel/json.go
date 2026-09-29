package parallel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sync"

	"github.com/fulmenhq/sumpter/internal/docnode"
	docjson "github.com/fulmenhq/sumpter/internal/docnode/json"
	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/fulmenhq/sumpter/internal/extract/streaming"
	"github.com/fulmenhq/sumpter/internal/index"
	"github.com/fulmenhq/sumpter/internal/index/store"
)

// testHookJSONSourceOpened, when a test sets it, runs after the source has
// been opened and checked and before any record is read.
var testHookJSONSourceOpened func()

// DefaultJSONMaxRecordBytes is the record size limit for JSON indexed reads
// when MaxRecordSizeMB is zero: 100 MiB.
const DefaultJSONMaxRecordBytes int64 = 100 << 20

// maxReorderWindow bounds how many records may be scheduled ahead of the
// ordered output.
const maxReorderWindow = 1 << 16

// jsonRecordLimit returns the byte limit for a JSON record. Zero means the
// default; a positive value is MiB; anything else is refused.
func jsonRecordLimit(mb int) (int64, error) {
	switch {
	case mb == 0:
		return DefaultJSONMaxRecordBytes, nil
	case mb < 0:
		return 0, fmt.Errorf("max record size %d MB is negative", mb)
	case int64(mb) > math.MaxInt64>>20:
		return 0, fmt.Errorf("max record size %d MB is too large", mb)
	}
	limit := int64(mb) << 20
	if uint64(limit) > uint64(math.MaxInt) {
		return 0, fmt.Errorf("max record size %d MB is too large for this platform", mb)
	}
	return limit, nil
}

// jsonWorkItem is one validated index record. Everything a worker needs is
// carried here and never re-read from the index.
type jsonWorkItem struct {
	num        int
	start, end int64
	size       int64
	sha        string
	name       string
}

type jsonResult struct {
	num     int
	records []map[string]interface{}
	err     error
}

// jsonRun extracts the records of one JSON source through its index.
type jsonRun struct {
	pe       *ParallelExtractor
	store    store.IndexStore
	header   *index.RecordIndex
	extCfg   *extract.ExtractRecordMatch
	sigCfg   *extract.FileSignature
	src      *os.File
	srcInfo  os.FileInfo
	limit    int64
	workers  int
	window   int
	selector streaming.RecordSelector
}

// newJSONRun checks everything that does not depend on reading records and
// opens the source once. The caller closes it.
func (pe *ParallelExtractor) newJSONRun(indexStore store.IndexStore, header *index.RecordIndex) (*jsonRun, error) {
	extCfg, ok := pe.opts.ExtractConfig.(*extract.ExtractRecordMatch)
	if !ok || extCfg == nil {
		return nil, fmt.Errorf("extract config must be *extract.ExtractRecordMatch")
	}
	sigCfg, _ := pe.opts.SignatureConfig.(*extract.FileSignature)
	if !extract.IsRecordScope(sigCfg) {
		return nil, fmt.Errorf(`the indexed route evaluates the signature per record; declare "match_scope: record" in the signature`)
	}
	if len(extCfg.MatchSelectors) != 1 {
		return nil, fmt.Errorf("indexed json extraction needs exactly one match selector; found %d", len(extCfg.MatchSelectors))
	}
	requested, err := streaming.ParseRecordSelector(extCfg.MatchSelectors[0].XPath)
	if err != nil {
		return nil, err
	}
	indexed, err := streaming.ParseRecordSelector(header.Selector.XPath)
	if err != nil {
		return nil, fmt.Errorf("record index selector: %w", err)
	}
	if indexed.ElementName != header.Selector.ElementName {
		return nil, fmt.Errorf("record index selector %q names %q, but the header records element_name %q", header.Selector.XPath, indexed.ElementName, header.Selector.ElementName)
	}
	if requested.Raw != indexed.Raw {
		return nil, fmt.Errorf("the recipe selects %q, but the index was built for %q; rebuild the index with the recipe's selector", requested.Raw, indexed.Raw)
	}
	if err := index.ValidateNamespaceContextShape(header); err != nil {
		return nil, err
	}
	limit, err := jsonRecordLimit(pe.opts.MaxRecordSizeMB)
	if err != nil {
		return nil, err
	}
	workers := capWorkerCount(pe.opts.Workers)
	window := pe.opts.ReorderWindow
	switch {
	case window < 0:
		return nil, fmt.Errorf("reorder window %d is negative", window)
	case window == 0:
		window = workers * 2
	case window > maxReorderWindow:
		return nil, fmt.Errorf("reorder window %d is above the limit %d", window, maxReorderWindow)
	}

	src, err := os.Open(pe.opts.SourcePath) // #nosec G304 - operator-selected source path
	if err != nil {
		return nil, fmt.Errorf("failed to open source: %w", err)
	}
	info, err := src.Stat()
	if err != nil {
		_ = src.Close()
		return nil, fmt.Errorf("failed to stat source: %w", err)
	}
	if !info.Mode().IsRegular() {
		_ = src.Close()
		return nil, fmt.Errorf("source is not a regular file")
	}
	if info.Size() != header.Source.SizeBytes {
		_ = src.Close()
		return nil, fmt.Errorf("source is %d bytes, but the index describes %d bytes", info.Size(), header.Source.SizeBytes)
	}
	return &jsonRun{
		pe: pe, store: indexStore, header: header, extCfg: extCfg, sigCfg: sigCfg,
		src: src, srcInfo: info, limit: limit, workers: workers, window: window, selector: indexed,
	}, nil
}

// hashSource hashes the whole held source.
func (r *jsonRun) hashSource() (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, io.NewSectionReader(r.src, 0, r.srcInfo.Size())); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// validate checks one index record before it is scheduled.
func (r *jsonRun) validate(rec *index.RecordMetadata, expected int) (jsonWorkItem, error) {
	switch {
	case rec.RecordNum != expected:
		return jsonWorkItem{}, fmt.Errorf("record index lists record %d where record %d was expected", rec.RecordNum, expected)
	case rec.StartOffset < 0 || rec.EndOffset <= rec.StartOffset || rec.EndOffset > r.srcInfo.Size():
		return jsonWorkItem{}, fmt.Errorf("record %d range [%d,%d) is outside the %d-byte source", expected, rec.StartOffset, rec.EndOffset, r.srcInfo.Size())
	case rec.SizeBytes != rec.EndOffset-rec.StartOffset:
		return jsonWorkItem{}, fmt.Errorf("record %d size %d does not match its range [%d,%d)", expected, rec.SizeBytes, rec.StartOffset, rec.EndOffset)
	case rec.SizeBytes > r.limit:
		return jsonWorkItem{}, fmt.Errorf("record %d is %d bytes, above the %d-byte record limit", expected, rec.SizeBytes, r.limit)
	case rec.NamespaceContextRef != 0:
		return jsonWorkItem{}, fmt.Errorf("record %d refers to namespace context %d; json records have none", expected, rec.NamespaceContextRef)
	case rec.Depth < 1:
		return jsonWorkItem{}, fmt.Errorf("record %d depth %d is not positive", expected, rec.Depth)
	case len(rec.SHA256) != sha256.Size*2:
		return jsonWorkItem{}, fmt.Errorf("record %d hash is not a SHA-256 hex digest", expected)
	}
	if _, err := hex.DecodeString(rec.SHA256); err != nil {
		return jsonWorkItem{}, fmt.Errorf("record %d hash is not a SHA-256 hex digest", expected)
	}
	name := rec.ElementName
	if name == "" {
		name = r.header.Selector.ElementName // the seekable store keeps no per-record name
	}
	if name != r.header.Selector.ElementName {
		return jsonWorkItem{}, fmt.Errorf("record %d is named %q, but the index selects %q", expected, name, r.header.Selector.ElementName)
	}
	return jsonWorkItem{num: expected, start: rec.StartOffset, end: rec.EndOffset, size: rec.SizeBytes, sha: rec.SHA256, name: name}, nil
}

// process reads one record's bytes from the held source, checks them against
// the index hash before parsing, scores the record-scoped signature on the
// record's document, and extracts it.
func (r *jsonRun) process(item jsonWorkItem, cfg *extract.ExtractRecordMatch) jsonResult {
	res := jsonResult{num: item.num}
	buf := make([]byte, item.size)
	n, err := r.src.ReadAt(buf, item.start)
	if n != len(buf) {
		if err == nil || errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		res.err = fmt.Errorf("record %d: source ends inside its range: %w", item.num, err)
		return res
	}
	sum := sha256.Sum256(buf)
	if hex.EncodeToString(sum[:]) != item.sha {
		res.err = fmt.Errorf("record %d bytes do not match the index hash; the source changed or the index is not for it", item.num)
		return res
	}
	doc, err := docjson.Format{}.ParseRecord(&docnode.Record{Raw: buf, Num: item.num, Name: item.name, StartOffset: item.start})
	if err != nil {
		res.err = fmt.Errorf("failed to parse record %d (json): %w", item.num, err)
		return res
	}
	matched, confidence, err := extract.ScoreRecordSignature(doc, r.sigCfg)
	if err != nil {
		res.err = fmt.Errorf("failed to check signature for record %d: %w", item.num, err)
		return res
	}
	if !matched {
		res.err = extract.RecordSignatureMismatch(item.num, confidence, r.sigCfg.ConfidenceThreshold)
		return res
	}
	records, err := extract.ExtractRecordsFromDocument(doc, cfg, r.pe.opts.ExternalFields)
	if err != nil {
		res.err = fmt.Errorf("failed to extract fields for record %d: %w", item.num, err)
		return res
	}
	for _, rec := range records {
		if err := extract.EnrichRecordWithRecordNum(rec, r.pe.opts.SourcePath, r.sigCfg, cfg, r.pe.opts.RuntimeProvenance, item.num); err != nil {
			res.err = fmt.Errorf("failed to enrich record %d: %w", item.num, err)
			return res
		}
	}
	res.records = records
	return res
}

// run schedules, extracts and emits the records in order. Records reach sink
// only in order and only while no earlier record has failed; the first
// failure in record order ends the run, so the lowest failing record is the
// one reported whichever worker finishes first. On failure the caller must
// treat every row already given to sink as provisional.
func (r *jsonRun) run(ctx context.Context, sink extract.RecordSink) (int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if r.pe.opts.VerifyIndex {
		sum, err := r.hashSource()
		if err != nil {
			return 0, fmt.Errorf("failed to hash source: %w", err)
		}
		if sum != r.header.Source.SHA256 {
			return 0, fmt.Errorf("source hash does not match the index")
		}
	}

	if testHookJSONSourceOpened != nil {
		testHookJSONSourceOpened()
	}

	workCh := make(chan jsonWorkItem)
	resCh := make(chan jsonResult, r.workers)
	slots := make(chan struct{}, r.window)
	release := func() {
		select {
		case <-slots:
		default:
		}
	}
	var wg sync.WaitGroup

	// Scheduler: bounded iteration over the index, contiguous ordinals, every
	// record validated before it is queued. The declared count is compared
	// only after the records end, and it never sizes anything.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(workCh)
		terminal := func(num int, err error) {
			select {
			case resCh <- jsonResult{num: num, err: err}:
			case <-ctx.Done():
			}
		}
		iter, err := r.store.Records(ctx)
		if err != nil {
			terminal(1, fmt.Errorf("failed to read record index: %w", err))
			return
		}
		defer func() { _ = iter.Close() }()
		expected := 1
		for {
			rec, err := iter.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				terminal(expected, fmt.Errorf("failed to read record %d from index: %w", expected, err))
				return
			}
			item, err := r.validate(rec, expected)
			if err != nil {
				terminal(expected, err)
				return
			}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case workCh <- item:
			case <-ctx.Done():
				return
			}
			if expected == math.MaxInt {
				terminal(expected, fmt.Errorf("record index lists more records than can be numbered"))
				return
			}
			expected++
		}
		summary, err := store.FinalSummary(r.store)
		if err != nil {
			terminal(expected, fmt.Errorf("record index summary: %w", err))
			return
		}
		if summary.TotalRecords != expected-1 {
			terminal(expected, fmt.Errorf("record index declares %d records but lists %d", summary.TotalRecords, expected-1))
		}
	}()

	for i := 0; i < r.workers; i++ {
		cfg := extract.CloneRecordMatchForRecordDocument(r.extCfg)
		if err := extract.PrepareRecordMatch(cfg); err != nil {
			cancel()
			wg.Wait()
			return 0, fmt.Errorf("failed to prepare worker extract config: %w", err)
		}
		wg.Add(1)
		go func(cfg *extract.ExtractRecordMatch) {
			defer wg.Done()
			for item := range workCh {
				res := r.process(item, cfg)
				select {
				case resCh <- res:
				case <-ctx.Done():
					return
				}
			}
		}(cfg)
	}
	go func() {
		wg.Wait()
		close(resCh)
	}()

	pending := make(map[int]jsonResult)
	next := 1
	emitted := 0
	var failure error
	for res := range resCh {
		if failure != nil {
			continue // drain until every goroutine has returned
		}
		pending[res.num] = res
		for failure == nil {
			p, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			release()
			if p.err != nil {
				failure = p.err
				cancel()
				break
			}
			for _, rec := range p.records {
				if err := sink.OnRecord(ctx, extract.NewEmittedRecord(rec)); err != nil {
					failure = fmt.Errorf("failed to emit record %d: %w", p.num, err)
					cancel()
					break
				}
				emitted++
			}
			next++
		}
	}
	if failure != nil {
		return emitted, failure
	}
	// A canceled run may have dropped its only error or result on the way
	// here; it is a failed input, never a short success.
	if err := ctx.Err(); err != nil {
		return emitted, fmt.Errorf("extraction canceled: %w", err)
	}
	if len(pending) > 0 {
		return emitted, fmt.Errorf("internal error: %d records were extracted but not emitted in order", len(pending))
	}

	// The records came from the file held open since the start; the path
	// must still name that file, and with full verification the whole file
	// must still hash to the index.
	now, err := os.Stat(r.pe.opts.SourcePath)
	if err != nil {
		return emitted, fmt.Errorf("source is no longer readable: %w", err)
	}
	if !os.SameFile(now, r.srcInfo) || now.Size() != r.srcInfo.Size() {
		return emitted, fmt.Errorf("source was replaced during extraction")
	}
	if r.pe.opts.VerifyIndex {
		sum, err := r.hashSource()
		if err != nil {
			return emitted, fmt.Errorf("failed to hash source: %w", err)
		}
		if sum != r.header.Source.SHA256 {
			return emitted, fmt.Errorf("source changed during extraction")
		}
	}
	return emitted, nil
}
