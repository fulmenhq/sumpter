package parallel

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

// TestParallelDataIntegritySingleVsManyWorkers is the race-fix load-bearing
// data-integrity gate. The race detector only catches races it observes; correctness
// must also be argued by output. It extracts the same multi-record corpus
// single-worker and at high parallelism and asserts byte-identical ordered output
// (same records, same order, same record numbers) — proving no corruption, mis-order,
// drop, or duplicate from either race fix.
//
// It exercises both fixes: the index-header snapshot (site A — a wrong/zero
// TotalRecords must not drop the buffered tail) and per-worker XPath plans (site B —
// many workers evaluate an XPath-heavy config concurrently). Run under -race to also
// pin race-freedom.
func TestParallelDataIntegritySingleVsManyWorkers(t *testing.T) {
	const n = 120
	tmpDir := t.TempDir()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<root>\n")
	for i := 1; i <= n; i++ {
		tag := "even"
		if i%2 == 1 {
			tag = "odd"
		}
		fmt.Fprintf(&b, "  <record><seq>%d</seq><name>name-%d</name><val>%d</val><tag>%s</tag></record>\n", i, i, i*100, tag)
	}
	b.WriteString("</root>\n")

	xmlPath := filepath.Join(tmpDir, "corpus.xml")
	if err := os.WriteFile(xmlPath, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write corpus: %v", err)
	}
	indexPath := filepath.Join(tmpDir, "corpus.recordindex.json")
	if err := buildTestIndex(t, xmlPath, indexPath, "//record"); err != nil {
		t.Fatalf("build index: %v", err)
	}

	run := func(workers int) []map[string]interface{} {
		extCfg := &extract.ExtractRecordMatch{
			FieldMappings: []extract.FieldMapping{
				{OutputField: "seq", XPath: "//seq", Type: "string"},
				{OutputField: "name", XPath: "//name", Type: "string"},
				{OutputField: "val", XPath: "//val", Type: "int"},
				{OutputField: "tag", XPath: "//tag", Type: "string"},
				{OutputField: "name_upper", Expression: "upper(name)", Type: "string"},
			},
		}
		opts := ExtractionOptions{
			IndexPath:       indexPath,
			SourcePath:      xmlPath,
			Workers:         workers,
			MaxRecordSizeMB: 10,
			ExtractConfig:   extCfg,
			SignatureConfig: &extract.FileSignature{},
		}
		records, err := NewParallelExtractor(opts).Extract()
		if err != nil {
			t.Fatalf("Extract(workers=%d): %v", workers, err)
		}
		return records
	}

	single := run(1)
	many := run(8)

	if len(single) != n || len(many) != n {
		t.Fatalf("record counts: single=%d many=%d, want %d each (a dropped tail would show here)", len(single), len(many), n)
	}

	for i := range single {
		// Document order preserved + no drop/dup: record_num at position i is i+1 in
		// both runs. record_num comes from the work item (not XPath), so it is the
		// reliable ordering/integrity signal — a dropped tail (the snapshot total=0
		// risk) or a mis-order would surface here.
		if sNum, mNum := recordNum(t, single[i]), recordNum(t, many[i]); sNum != i+1 || mNum != i+1 {
			t.Fatalf("position %d: single record_num=%d, 8-worker record_num=%d, want %d (drop/dup/mis-order)", i, sNum, mNum, i+1)
		}
		// Cross-worker equality of the full extract.data block: a race that corrupted
		// a field under concurrency (site B) would diverge from the single-worker run.
		if sData, mData := dataBlock(t, single[i]), dataBlock(t, many[i]); !reflect.DeepEqual(sData, mData) {
			t.Fatalf("record %d data differs single vs 8-worker:\n single=%#v\n many=%#v", i, sData, mData)
		}
	}
}

func recordNum(t *testing.T, record map[string]interface{}) int {
	t.Helper()
	rt, ok := record["_runtime"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing _runtime block: %#v", record)
	}
	num, ok := rt["record_num"].(int)
	if !ok {
		t.Fatalf("record_num not an int: %#v", rt["record_num"])
	}
	return num
}

func dataBlock(t *testing.T, record map[string]interface{}) map[string]interface{} {
	t.Helper()
	extractBlock, ok := record["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extract block: %#v", record)
	}
	data, ok := extractBlock["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing extract.data block: %#v", extractBlock)
	}
	return data
}
