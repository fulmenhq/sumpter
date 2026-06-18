package extract

import (
	"strings"
	"sync"
	"testing"

	"github.com/antchfx/xmlquery"
)

// TestCloneRecordMatchGivesPerHolderXPathState is the focused race-fix site-B test:
// it proves that workers evaluating an XPath-heavy config concurrently do NOT share
// mutable compiled *xpath.Expr state when each holds its own CloneRecordMatch — the
// per-worker-plan fix that replaced a shared extractor. Run under -race: each
// goroutine compiles and owns its plans via the clone, so concurrent Select is
// race-free; sharing one prepared config (the pre-fix shape) races inside
// antchfx/xpath's query evaluation.
//
// Covers match-selector XPath, field XPath, nested array item_mapping, and a
// polymorphic match_xpath + nested field — the full compiled-state surface the clone
// clears.
func TestCloneRecordMatchGivesPerHolderXPathState(t *testing.T) {
	doc, err := xmlquery.Parse(strings.NewReader(`<?xml version="1.0"?><Envelope>` +
		`<Record><Status>online</Status><Items><Item><Code>A</Code></Item><Item><Code>B</Code></Item></Items>` +
		`<Payload kind="alpha"><Alpha>av</Alpha></Payload></Record>` +
		`<Record><Status>training</Status><Items><Item><Code>C</Code></Item></Items>` +
		`<Payload kind="beta"><Beta>bv</Beta></Payload></Record></Envelope>`))
	if err != nil {
		t.Fatalf("parse xml: %v", err)
	}

	base := &ExtractRecordMatch{
		RecordType:     "rec",
		MatchSelectors: []MatchSelector{{XPath: "//Record"}},
		FieldMappings: []FieldMapping{
			{OutputField: "status", XPath: "Status", Type: "string"},
			{OutputField: "status_friendly", Expression: `status == "online" ? "ready" : status`, Type: "string"},
			{OutputField: "codes", XPath: "Items/Item", Type: "array", ItemMapping: []FieldMapping{
				{OutputField: "code", XPath: "Code", Type: "string"},
			}},
			{OutputField: "payload", XPath: "Payload", Type: "object", Polymorphic: []PolymorphicMapping{
				{MatchXPath: "@kind='alpha'", FieldMappings: []FieldMapping{{OutputField: "alpha", XPath: "Alpha", Type: "string"}}},
				{MatchXPath: "@kind='beta'", FieldMappings: []FieldMapping{{OutputField: "beta", XPath: "Beta", Type: "string"}}},
			}},
		},
	}

	// Sequential reference result (single owner) to compare against.
	refCfg := CloneRecordMatch(base)
	if err := prepareExtractConfig(refCfg); err != nil {
		t.Fatalf("prepare reference: %v", err)
	}
	want, err := extractRecords(doc, refCfg, nil)
	if err != nil {
		t.Fatalf("reference extract: %v", err)
	}
	if len(want) != 2 || want[0]["status_friendly"] != "ready" {
		t.Fatalf("reference extract wrong: %#v", want)
	}

	const workers = 8
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Each worker owns its own compiled XPath state via the clone.
			cfg := CloneRecordMatch(base)
			if perr := prepareExtractConfig(cfg); perr != nil {
				errs <- perr
				return
			}
			for iter := 0; iter < 50; iter++ {
				got, gerr := extractRecords(doc, cfg, nil)
				if gerr != nil {
					errs <- gerr
					return
				}
				if len(got) != len(want) ||
					got[0]["status_friendly"] != "ready" ||
					got[1]["status_friendly"] != "training" {
					errs <- errMismatch(got)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Fatalf("concurrent per-worker extraction failed: %v", e)
	}
}

type extractMismatch struct{ got []map[string]interface{} }

func (e extractMismatch) Error() string { return "concurrent extraction produced unexpected records" }

func errMismatch(got []map[string]interface{}) error { return extractMismatch{got: got} }
