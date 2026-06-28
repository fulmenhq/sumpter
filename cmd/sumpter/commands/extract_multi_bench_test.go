package commands

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
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

// BenchmarkExtractMultiInputWorkers quantifies input-worker scaling on the PARSE-BOUND
// shape — one of the two shapes the feature speeds up (the other is the tiny-file regime in
// BenchmarkExtractMultiTinyFile). One extract-multi aggregate invocation over inputs that
// are each a large, deeply-structured document (lots of filler nodes) from which the recipe
// extracts only a few records — a common real shape (big documents, sparse records of
// interest) where DOM construction dominates each input's cost.
//
// Why this shape: the input workers run each input's full per-input processing — parse AND
// per-recipe application — so the wall-clock win is workload-dependent. Here DOM construction
// is the long pole, so it scales toward the core count; tiny-file batches scale on the
// application long pole instead (see BenchmarkExtractMultiTinyFile). Only the durable commit
// (writing, ledgers/manifest, finalization) stays single-owner on the ordered committer, and
// that is the eventual ceiling. Aggregate mode keeps output-file-creation noise out of the
// measurement.
//
// It reports only ns/op; absolute throughput numbers are intentionally NOT asserted or
// committed (machine-dependent; field scale figures stay off the public surface). A human
// reads the ns/op trend across worker counts to see scaling toward the available cores /
// I/O ceiling. There is deliberately no wall-clock gate (it would be flaky).
func BenchmarkExtractMultiInputWorkers(b *testing.B) {
	const (
		kFiles      = 200
		fillerNodes = 4000 // big DOM per input; the recipe extracts only the lone TargetElement
	)
	ws := writeBenchRecipeWorkspace(b, "proj0")
	fileList := writeBenchParseBoundInputSet(b, kFiles, fillerNodes)

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("input_workers_%d", workers), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				shared := &multiSharedOptions{
					FileList:     fileList,
					OutputPath:   filepath.Join(b.TempDir(), fmt.Sprintf("w%d_%d", workers, i)),
					OutputMode:   outputModeAggregate,
					RunID:        testMultiRunID,
					InputWorkers: workers,
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

func writeBenchRecipeWorkspace(tb testing.TB, id string) string {
	tb.Helper()
	ws := tb.TempDir()
	for _, d := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			tb.Fatal(err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(ws, rel), []byte(content), 0o600); err != nil {
			tb.Fatal(err)
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

func writeBenchInputSet(tb testing.TB, n int) string {
	tb.Helper()
	dir := tb.TempDir()
	var inputs []string
	for i := 0; i < n; i++ {
		p := filepath.Join(dir, fmt.Sprintf("in%06d.xml", i))
		body := fmt.Sprintf(`<root><TargetElement><Name>val%d</Name></TargetElement></root>`, i)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			tb.Fatal(err)
		}
		inputs = append(inputs, p)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(strings.Join(inputs, "\n")+"\n"), 0o600); err != nil {
		tb.Fatal(err)
	}
	return fileList
}

// benchEnvInt reads a positive integer from env var name, falling back to def. It lets the
// on-demand large-population perf runs scale the generated corpus / recipe count / worker
// set without code changes (e.g. SUMPTER_BENCH_TINY_FILES=10000).
func benchEnvInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// BenchmarkExtractMultiTinyFile quantifies input-worker scaling in the TINY-FILE regime:
// many small inputs, several recipes. Per-file parse is cheap, so the per-input recipe
// APPLICATION (M recipes x signature/applicability/extraction) is the long pole — exactly
// the regime SUM-068 targets and which SUM-066's parse-only workers could not speed up.
// Contrast BenchmarkExtractMultiInputWorkers (parse-bound, big DOMs). Sterile generated
// corpus; reports ns/op trend only (no committed absolute/throughput figures — those are
// machine-dependent and stay off the public surface). File count and recipe count are
// env-configurable for the on-demand large-population run.
func BenchmarkExtractMultiTinyFile(b *testing.B) {
	n := benchEnvInt("SUMPTER_BENCH_TINY_FILES", 2000)
	mRecipes := benchEnvInt("SUMPTER_BENCH_RECIPES", 3)
	workspaces := make([]string, mRecipes)
	for i := range workspaces {
		workspaces[i] = writeBenchRecipeWorkspace(b, fmt.Sprintf("proj%d", i))
	}
	fileList := writeBenchInputSet(b, n)

	for _, workers := range []int{1, 2, 4, 8} {
		b.Run(fmt.Sprintf("input_workers_%d", workers), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				shared := &multiSharedOptions{
					FileList:     fileList,
					OutputPath:   filepath.Join(b.TempDir(), fmt.Sprintf("w%d_%d", workers, i)),
					OutputMode:   outputModeAggregate,
					RunID:        testMultiRunID,
					InputWorkers: workers,
				}
				if err := runExtractMulti(shared, workspaces, io.Discard, time.Now()); err != nil {
					b.Fatalf("workers=%d: %v", workers, err)
				}
			}
		})
	}
}

// TestExtractMultiTinyFilePerf is the on-demand (never-CI) tiny-file proof-of-difference /
// tuning harness @3leapsdave asked for: it generates a large population of small inputs and
// runs extract-multi at increasing --input-workers, printing each run's wall time, derived
// inputs/s, and the tool's own --stats summary (effective CPU vs input-workers). It is gated
// behind SUMPTER_PERF so it never runs in CI (no timing gate; runner-noise-flaky), and is
// trend-only — a human reads the wall/effective-CPU trend to size --input-workers. Throughput
// figures stay OOB (operator console), never committed to docs or the PR.
//
//	SUMPTER_PERF=1 SUMPTER_BENCH_TINY_FILES=10000 go test -tags '' \
//	  -run TestExtractMultiTinyFilePerf -v ./cmd/sumpter/commands/
func TestExtractMultiTinyFilePerf(t *testing.T) {
	if os.Getenv("SUMPTER_PERF") == "" {
		t.Skip("on-demand tiny-file perf harness; set SUMPTER_PERF=1 to run (never in CI)")
	}
	n := benchEnvInt("SUMPTER_BENCH_TINY_FILES", 10000)
	mRecipes := benchEnvInt("SUMPTER_BENCH_RECIPES", 3)
	workspaces := make([]string, mRecipes)
	for i := range workspaces {
		workspaces[i] = writeBenchRecipeWorkspace(t, fmt.Sprintf("proj%d", i))
	}
	fileList := writeBenchInputSet(t, n)

	t.Logf("tiny-file perf: %d files x %d recipes (sterile corpus; trend-only, OOB figures)", n, mRecipes)
	for _, workers := range []int{1, 2, 4, 8} {
		var stats bytes.Buffer
		shared := &multiSharedOptions{
			FileList:     fileList,
			OutputPath:   filepath.Join(t.TempDir(), fmt.Sprintf("w%d", workers)),
			OutputMode:   outputModeAggregate,
			RunID:        testMultiRunID,
			InputWorkers: workers,
			Stats:        true,
		}
		start := time.Now()
		if err := runExtractMulti(shared, workspaces, &stats, time.Now()); err != nil {
			t.Fatalf("workers=%d: %v", workers, err)
		}
		wall := time.Since(start)
		t.Logf("\n=== input-workers=%d ===\nwall=%s  inputs/s=%.0f\n%s",
			workers, wall.Round(time.Millisecond), float64(n)/wall.Seconds(), strings.TrimSpace(stats.String()))
	}
}
