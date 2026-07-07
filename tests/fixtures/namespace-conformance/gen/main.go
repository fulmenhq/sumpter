// Command gen deterministically generates a namespace-conformance ledger
// document for the namespace mode-parity oracle. It exists so the streaming
// parse path can be exercised across the 100 MB threshold without committing a
// large binary fixture: the standing mode-parity gate generates the large
// variant on demand and asserts identical records against the small trio.
//
// Output is byte-deterministic for a given (shape, records) pair — no clock,
// no randomness — so a generated document is a stable oracle input.
//
// The generated core content mirrors the committed small trio
// (prefixed.xml / default-ns.xml / dual.xml): the same core vocabulary
// (urn:example:sumpter-records) and the same per-record fields, so a bound
// recipe extracts the same record shape at any size.
//
// Usage:
//
//	go run ./tests/fixtures/namespace-conformance/gen -shape b -records 500000 -out big.xml
//	go run ./tests/fixtures/namespace-conformance/gen -shape b -target-mb 120 -out big.xml
//
// -shape selects the serialization: a=fully-prefixed, b=default-namespace,
// c=dual (adds the extension namespace). -records sets an exact count;
// -target-mb inflates the record count until the output is at least that many
// megabytes (rounded up). Exactly one of -records/-target-mb must be set.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
)

const (
	coreURI = "urn:example:sumpter-records"
	extURI  = "urn:example:sumpter-records-ext"
)

// labels is a fixed, generic cycle so content is deterministic and carries no
// real-world/vertical tell.
var labels = []string{"Alpha", "Beta", "Gamma", "Delta", "Epsilon"}

func main() {
	shape := flag.String("shape", "b", "serialization shape: a=prefixed, b=default-ns, c=dual")
	records := flag.Int("records", 0, "exact number of core records to emit")
	targetMB := flag.Int("target-mb", 0, "inflate record count until output is at least this many MB")
	out := flag.String("out", "", "output file path (default: stdout)")
	flag.Parse()

	if (*records == 0) == (*targetMB == 0) {
		fmt.Fprintln(os.Stderr, "gen: set exactly one of -records or -target-mb")
		os.Exit(2)
	}
	if *shape != "a" && *shape != "b" && *shape != "c" {
		fmt.Fprintf(os.Stderr, "gen: unknown -shape %q (want a, b, or c)\n", *shape)
		os.Exit(2)
	}

	w := os.Stdout
	if *out != "" {
		f, err := os.Create(*out) // #nosec G304 - developer-supplied fixture output path
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen: create %s: %v\n", *out, err)
			os.Exit(1)
		}
		defer func() { _ = f.Close() }()
		w = f
	}

	count := *records
	if *targetMB > 0 {
		count = recordsForMB(*shape, *targetMB)
	}

	bw := bufio.NewWriterSize(w, 1<<20)
	if err := write(bw, *shape, count); err != nil {
		fmt.Fprintf(os.Stderr, "gen: write: %v\n", err)
		os.Exit(1)
	}
	if err := bw.Flush(); err != nil {
		fmt.Fprintf(os.Stderr, "gen: flush: %v\n", err)
		os.Exit(1)
	}
}

// recordsForMB estimates the record count needed to reach targetMB by measuring
// one record's serialized size in the chosen shape, then rounds up.
func recordsForMB(shape string, targetMB int) int {
	perRecord := len(recordBytes(shape, 1))
	if perRecord == 0 {
		perRecord = 1
	}
	target := targetMB * 1024 * 1024
	n := target / perRecord
	if n*perRecord < target {
		n++
	}
	if n < 1 {
		n = 1
	}
	return n
}

func write(w *bufio.Writer, shape string, count int) error {
	if _, err := w.WriteString(openTag(shape)); err != nil {
		return err
	}
	for i := 1; i <= count; i++ {
		if _, err := w.WriteString(recordBytes(shape, i)); err != nil {
			return err
		}
	}
	if _, err := w.WriteString(closeTag(shape)); err != nil {
		return err
	}
	return nil
}

func openTag(shape string) string {
	switch shape {
	case "a":
		return fmt.Sprintf("<n:Ledger xmlns:n=%q>\n", coreURI)
	case "c":
		return fmt.Sprintf("<Ledger xmlns=%q xmlns:ext=%q>\n", coreURI, extURI)
	default: // b
		return fmt.Sprintf("<Ledger xmlns=%q>\n", coreURI)
	}
}

func closeTag(shape string) string {
	if shape == "a" {
		return "</n:Ledger>\n"
	}
	return "</Ledger>\n"
}

// recordBytes renders one deterministic core record in the chosen shape. Field
// values derive only from i, so output is reproducible.
func recordBytes(shape string, i int) string {
	id := fmt.Sprintf("R-%06d", i)
	label := labels[(i-1)%len(labels)]
	// Deterministic amount and date derived from i; generic, no real-world tell.
	amount := fmt.Sprintf("%d.%02d", 10+(i%90), i%100)
	date := fmt.Sprintf("2026-%02d-%02d", 1+(i%12), 1+(i%28))

	switch shape {
	case "a":
		return fmt.Sprintf(
			"  <n:Record id=%q>\n"+
				"    <n:Label>%s</n:Label>\n"+
				"    <n:Amount>%s</n:Amount>\n"+
				"    <n:PostedDate>%s</n:PostedDate>\n"+
				"  </n:Record>\n",
			id, label, amount, date)
	case "c":
		return fmt.Sprintf(
			"  <Record id=%q ext:origin=\"import\">\n"+
				"    <Label>%s</Label>\n"+
				"    <Amount>%s</Amount>\n"+
				"    <PostedDate>%s</PostedDate>\n"+
				"    <ext:Annotation>reviewed</ext:Annotation>\n"+
				"    <ext:Record id=\"X-%06d\">\n"+
				"      <ext:Note>extension-scoped record, same local name as core Record</ext:Note>\n"+
				"    </ext:Record>\n"+
				"  </Record>\n",
			id, label, amount, date, i)
	default: // b
		return fmt.Sprintf(
			"  <Record id=%q>\n"+
				"    <Label>%s</Label>\n"+
				"    <Amount>%s</Amount>\n"+
				"    <PostedDate>%s</PostedDate>\n"+
				"  </Record>\n",
			id, label, amount, date)
	}
}
