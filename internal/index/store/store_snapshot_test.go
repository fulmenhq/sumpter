package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// indexSummaryAfterRecords is a JSON index whose "summary" follows "records" — the
// streaming-writer default and the original race failure mode: Header() parses to the
// records array before the summary is seen, then draining records decodes the trailing
// summary into the stream's header.
const indexSummaryAfterRecords = `{
  "version": "record-index/v0.1.1",
  "source": {"path": "/test/data.xml", "size_bytes": 3000, "sha256": "abc"},
  "selector": {"xpath": "//Record", "element_name": "Record"},
  "records": [
    {"record_num": 1, "start_offset": 0,   "end_offset": 100, "size_bytes": 100, "sha256": "a", "element_name": "Record", "depth": 1},
    {"record_num": 2, "start_offset": 100, "end_offset": 200, "size_bytes": 100, "sha256": "b", "element_name": "Record", "depth": 1},
    {"record_num": 3, "start_offset": 200, "end_offset": 300, "size_bytes": 100, "sha256": "c", "element_name": "Record", "depth": 1}
  ],
  "summary": {"total_records": 3, "total_bytes": 300},
  "metadata": {"generator": "test"}
}`

// TestJSONStoreHeaderSnapshotStableWhileRecordsDrain pins the race-fix site-A
// contract: a caller may retain the value returned by Header() and use it while a
// Records() iterator drains, without racing and without observing mutation. With the
// summary-after-records layout, draining decodes the trailing summary into the
// stream's private header; a retained Header() snapshot must NOT see that change.
//
// Run under -race: the concurrent read of the retained snapshot vs. the draining
// producer is exactly the orchestrator's access pattern. Before the fix (Header
// returning &stream.header) this both races and mutates the caller's value.
func TestJSONStoreHeaderSnapshotStableWhileRecordsDrain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.recordindex.json")
	if err := os.WriteFile(path, []byte(indexSummaryAfterRecords), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()

	snap, err := st.Header()
	if err != nil {
		t.Fatalf("Header: %v", err)
	}
	before := snap.Summary // value snapshot of the retained header's summary

	iter, err := st.Records(context.Background())
	if err != nil {
		t.Fatalf("Records: %v", err)
	}

	// Drain records in one goroutine while the main goroutine repeatedly reads the
	// retained snapshot. The snapshot must stay value-stable throughout.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, nerr := iter.Next(); nerr != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-done:
			if snap.Summary != before {
				t.Fatalf("retained Header() snapshot mutated during drain: got %+v, want %+v", snap.Summary, before)
			}
			return
		default:
			if snap.Summary != before {
				t.Fatalf("retained Header() snapshot mutated mid-drain: got %+v, want %+v", snap.Summary, before)
			}
		}
	}
}
