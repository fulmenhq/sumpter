package index

import (
	"context"
	"fmt"
	"io"
)

// verifyJSON verifies a JSON-source index semantically: it holds the source
// open, checks its size and hash, scans it again with the index's selector,
// and requires the fresh record sequence (ordinal, range, size, name, depth
// and count) and every record hash to equal the index. Records are read in
// step with the scan, so an index declaring more or fewer records than the
// source holds fails without walking its declared count.
func (v *Verifier) verifyJSON(header *RecordIndex, provider RecordProvider, result *VerifyResult) (*VerifyResult, error) {
	fail := func(msg string, args ...any) (*VerifyResult, error) {
		result.Valid = false
		result.ErrorMessage = fmt.Sprintf(msg, args...)
		return result, nil
	}
	selector, err := jsonElementSelector(header.Selector.XPath)
	if err != nil {
		return fail("index selector %q is not a record selector: %v", header.Selector.XPath, err)
	}
	if selector.ElementName != header.Selector.ElementName {
		return fail("index selector %q names %q, but the header records element_name %q", header.Selector.XPath, selector.ElementName, header.Selector.ElementName)
	}
	if header.Summary.TotalRecords < 0 {
		return fail("index declares a negative record count %d", header.Summary.TotalRecords)
	}

	src, err := openJSONSource(v.opts.InputPath)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = src.Close() }()
	if src.size != header.Source.SizeBytes {
		result.SourceSizeMatch = false
		return fail("source file size mismatch: expected %d bytes, got %d bytes", header.Source.SizeBytes, src.size)
	}
	result.SourceSizeMatch = true
	sourceHash, err := src.hash()
	if err != nil {
		return nil, fmt.Errorf("failed to compute source file hash: %w", err)
	}
	if sourceHash != header.Source.SHA256 {
		result.SourceHashMatch = false
		return fail("source file hash mismatch: expected %s, got %s", header.Source.SHA256, sourceHash)
	}
	result.SourceHashMatch = true

	scanner, err := src.records(selector.Raw)
	if err != nil {
		return fail("%v", err)
	}
	defer func() { _ = scanner.Close() }()
	iter, err := provider.Records(context.Background())
	if err != nil {
		return nil, fmt.Errorf("failed to create record iterator: %w", err)
	}
	defer func() { _ = iter.Close() }()

	count := 0
	for {
		fresh, scanErr := scanner.Next()
		indexed, iterErr := iter.Next()
		if scanErr != nil && scanErr != io.EOF {
			return fail("source does not scan as JSON with selector %q: %v", selector.Raw, scanErr)
		}
		if iterErr != nil && iterErr != io.EOF {
			return nil, fmt.Errorf("failed to read record %d: %w", count+1, iterErr)
		}
		switch {
		case scanErr == io.EOF && iterErr == io.EOF:
			declared := header.Summary.TotalRecords
			if r, ok := provider.(interface{ FinalSummary() (SummaryStats, error) }); ok {
				summary, err := r.FinalSummary()
				if err != nil {
					return fail("%v", err)
				}
				declared = summary.TotalRecords
			}
			if count != declared {
				return fail("index declares %d records, but it lists and the source holds %d", declared, count)
			}
			result.RecordsVerified = count
			return result, nil
		case scanErr == io.EOF:
			return fail("index lists record %d, but the source holds only %d records for selector %q", count+1, count, selector.Raw)
		case iterErr == io.EOF:
			return fail("source holds record %d for selector %q, but the index lists only %d", count+1, selector.Raw, count)
		}
		count++
		name := indexed.ElementName
		if name == "" {
			name = header.Selector.ElementName // the seekable store keeps no per-record name
		}
		if indexed.RecordNum != fresh.Num || indexed.StartOffset != fresh.StartOffset || indexed.EndOffset != fresh.EndOffset ||
			indexed.SizeBytes != fresh.SizeBytes || name != fresh.Name || indexed.Depth != fresh.Depth || indexed.NamespaceContextRef != 0 {
			return fail("record %d does not match the source: index has #%d [%d,%d) size %d name %q depth %d ref %d; source has #%d [%d,%d) size %d name %q depth %d",
				count, indexed.RecordNum, indexed.StartOffset, indexed.EndOffset, indexed.SizeBytes, name, indexed.Depth, indexed.NamespaceContextRef,
				fresh.Num, fresh.StartOffset, fresh.EndOffset, fresh.SizeBytes, fresh.Name, fresh.Depth)
		}
		recordHash, err := src.rangeHash(fresh.StartOffset, fresh.EndOffset)
		if err != nil {
			return nil, fmt.Errorf("failed to compute hash for record %d: %w", count, err)
		}
		if recordHash != indexed.SHA256 {
			return fail("record %d hash mismatch: expected %s, got %s", count, indexed.SHA256, recordHash)
		}
	}
}
