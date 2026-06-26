package commands

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antchfx/xmlquery"
)

// BenchmarkExtractMultiParseAmortization demonstrates the headline throughput
// property of extract-multi: with M recipes over one input set, each file is
// read and parsed ONCE, not once per recipe — so the read/parse component drops
// from ~M*K parses to K (an ~M-to-1 reduction).
//
// Each sub-benchmark reports a custom "parses/op" metric (counted via the
// injectable parse seam, so it is exact, not timing-inferred) alongside ns/op:
//   - multi_parse_once: one extract-multi run over M recipes      -> K parses/op
//   - sequential_per_recipe: M single-recipe runs (one each)       -> M*K parses/op
//
// The deterministic 1-parse-per-file guarantee is also asserted in
// TestRunExtractMulti_ParsesEachFileOnceForAllRecipes; this benchmark quantifies
// the amortization (parses/op) and its wall-clock effect.
func BenchmarkExtractMultiParseAmortization(b *testing.B) {
	const mRecipes, kFiles = 8, 50
	workspaces := make([]string, mRecipes)
	for i := range workspaces {
		workspaces[i] = writeBenchRecipeWorkspace(b, fmt.Sprintf("proj%d", i))
	}
	fileList := writeBenchInputSet(b, kFiles)

	// countingDispatch runs one dispatch with a parse-counting seam and returns
	// the number of file parses performed.
	countingDispatch := func(b *testing.B, recipes []string, outRoot string) int {
		shared := &multiSharedOptions{FileList: fileList, OutputPath: outRoot, RunID: testMultiRunID}
		d := newMultiDispatcher(shared, io.Discard)
		parses := 0
		real := d.parseFile
		d.parseFile = func(p string, a bool) (*xmlquery.Node, error) {
			parses++
			return real(p, a)
		}
		if err := d.run(recipes, time.Now()); err != nil {
			b.Fatalf("dispatch: %v", err)
		}
		return parses
	}

	b.Run("multi_parse_once", func(b *testing.B) {
		var parses int
		for i := 0; i < b.N; i++ {
			parses = countingDispatch(b, workspaces, filepath.Join(b.TempDir(), fmt.Sprintf("m%d", i)))
		}
		b.ReportMetric(float64(parses), "parses/op") // K — independent of M
	})

	b.Run("sequential_per_recipe", func(b *testing.B) {
		var parses int
		for i := 0; i < b.N; i++ {
			parses = 0
			for j, ws := range workspaces {
				parses += countingDispatch(b, []string{ws}, filepath.Join(b.TempDir(), fmt.Sprintf("s%d_%d", i, j)))
			}
		}
		b.ReportMetric(float64(parses), "parses/op") // M*K — scales with M
	})
}

// BenchmarkExtractMultiParseWorkers quantifies parse-parallelism scaling: one
// extract-multi aggregate invocation over a PARSE-BOUND input set, run at increasing
// --parse-workers. Each input is a large, deeply-structured document (lots of filler
// nodes) from which the recipe extracts only a few records — a common real shape (big
// documents, sparse records of interest) where DOM construction is the dominant per-input
// cost. That is exactly what this feature parallelizes.
//
// Why this shape: the conservative v0.2.4 cut parallelizes the READ+PARSE only;
// extraction, writing, and manifest bookkeeping stay serial on the ordered drain. So the
// wall-clock win is workload-dependent — large on parse-bound inputs (here), modest when
// extraction dominates (dense recipes) or when per-file drain overhead dominates (many
// tiny files). Aggregate mode keeps output-file-creation noise out of the measurement.
//
// It reports only ns/op; absolute throughput numbers are intentionally NOT asserted or
// committed (machine-dependent; field scale figures stay off the public surface). A human
// reads the ns/op trend across worker counts to see scaling toward the available cores /
// parse-I/O ceiling. There is deliberately no wall-clock gate (it would be flaky).
func BenchmarkExtractMultiParseWorkers(b *testing.B) {
	const (
		kFiles      = 200
		fillerNodes = 4000 // big DOM per input; the recipe extracts only the lone TargetElement
	)
	ws := writeBenchRecipeWorkspace(b, "proj0")
	fileList := writeBenchParseBoundInputSet(b, kFiles, fillerNodes)

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("parse_workers_%d", workers), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				shared := &multiSharedOptions{
					FileList:     fileList,
					OutputPath:   filepath.Join(b.TempDir(), fmt.Sprintf("w%d_%d", workers, i)),
					OutputMode:   outputModeAggregate,
					RunID:        testMultiRunID,
					ParseWorkers: workers,
				}
				if err := runExtractMulti(shared, []string{ws}, io.Discard, time.Now()); err != nil {
					b.Fatalf("workers=%d: %v", workers, err)
				}
			}
		})
	}
}

// writeBenchParseBoundInputSet writes n parse-bound inputs: each carries fillerNodes
// filler elements (so the DOM build is the dominant cost) plus a single TargetElement
// the recipe extracts (so extraction stays cheap and the parse is the long pole).
func writeBenchParseBoundInputSet(b *testing.B, n, fillerNodes int) string {
	b.Helper()
	dir := b.TempDir()
	var body strings.Builder
	body.WriteString("<root>")
	for j := 0; j < fillerNodes; j++ {
		fmt.Fprintf(&body, `<Filler id="%d"><a><b>x</b></a></Filler>`, j)
	}
	body.WriteString(`<TargetElement><Name>only</Name></TargetElement></root>`)
	payload := []byte(body.String())

	inputs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("in%04d.xml", i))
		if err := os.WriteFile(p, payload, 0o600); err != nil {
			b.Fatal(err)
		}
		inputs = append(inputs, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(inputs, "\n")+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	return fileList
}

func writeBenchRecipeWorkspace(b *testing.B, id string) string {
	b.Helper()
	ws := b.TempDir()
	for _, d := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			b.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(ws, rel), []byte(content), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	write("recipe.yaml", `version: recipe/v0.1.0
kind: extract
id: `+id+`
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
`)
	write("signature/signature.yaml", `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	write("extract/extract.yaml", `record_type: `+id+`_record
match_selectors:
  - xpath: //TargetElement
field_mappings:
  - output_field: name
    xpath: Name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)
	return ws
}

func writeBenchInputSet(b *testing.B, n int) string {
	b.Helper()
	dir := b.TempDir()
	var inputs []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("in%04d.xml", i))
		body := fmt.Sprintf(`<root><TargetElement><Name>val%d</Name></TargetElement></root>`, i)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			b.Fatal(err)
		}
		inputs = append(inputs, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(inputs, "\n")+"\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	return fileList
}
