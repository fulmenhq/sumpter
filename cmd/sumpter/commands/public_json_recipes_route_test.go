package commands

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The public-data JSON recipes in examples/config/extract declare which routes
// they support. These tests pin those claims on small synthetic documents
// shaped like the published sources.

func publicRecipePath(name string) string {
	return filepath.Join("..", "..", "..", "examples", "config", "extract", name)
}

func TestPublicJSONRecipesIndexedMatchesSequential(t *testing.T) {
	for _, tc := range []struct {
		name, sig, ext, selector, input string
		records                         int
	}{
		{
			name:     "usgs-geojson",
			sig:      "usgs-geojson-feature-signature.yaml",
			ext:      "usgs-geojson-feature-extract.yaml",
			selector: "features",
			input:    `{"type":"FeatureCollection","metadata":{"count":3},"features":[{"type":"Feature","properties":{"mag":1.2,"time":1789990000000,"status":"automatic","tsunami":0,"magType":"ml"},"geometry":{"type":"Point","coordinates":[-117.5,35.25,7.8]},"id":"xx1"},{"type":"Feature","properties":{"mag":null,"time":1789980000000,"status":"reviewed","tsunami":1,"magType":"mb"},"geometry":{"type":"Point","coordinates":[142.1,-3.5,10]},"id":"xx2"},{"type":"Feature","properties":{"mag":4.5,"time":1789970000000,"status":"reviewed","tsunami":0,"magType":"mww"},"geometry":{"type":"Point","coordinates":[20.5,38.1,12.25]},"id":"xx3"}],"bbox":[-117.5,-3.5,7.8,142.1,38.1,12.25]}`,
			records:  3,
		},
		{
			name:     "openfda-drug-event",
			sig:      "openfda-drug-event-signature.yaml",
			ext:      "openfda-drug-event-extract.yaml",
			selector: "results",
			input:    `{"meta":{"last_updated":"2026-09-28"},"results":[{"safetyreportid":"10000001","receivedate":"20260401","serious":"1","patient":{"patientsex":"2","reaction":[{"reactionmeddrapt":"Nausea"}],"drug":[{"medicinalproduct":"DRUG A","drugcharacterization":"1"}]}},{"safetyreportid":"10000002","receivedate":"20260402","serious":"2","patient":{"reaction":[{"reactionmeddrapt":"Rash"}],"drug":[]}},{"safetyreportid":"10000003","receivedate":"20260403","serious":"1","patient":{"reaction":[],"drug":[{"medicinalproduct":"DRUG B","drugcharacterization":"2"}]}}]}`,
			records:  3,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			src := filepath.Join(dir, "in.json")
			if err := os.WriteFile(src, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join(dir, "in")
			if _, err := runIndex(t, "build", src, "--input-format", "json", "--selector", tc.selector, "--progress=false", "--output", base); err != nil {
				t.Fatalf("index build: %v", err)
			}
			args := []string{"extract", "files", "--signature-config-path", publicRecipePath(tc.sig), "--extract-config-path", publicRecipePath(tc.ext), "--files", src}
			seq := filepath.Join(dir, "seq")
			par := filepath.Join(dir, "par")
			if err := runSumpter(t, append(append([]string{}, args...), "--output-path", seq)); err != nil {
				t.Fatalf("sequential: %v", err)
			}
			if err := runSumpter(t, append(append([]string{}, args...), "--output-path", par, "--record-index", base+".recordindex.json", "--workers", "2")); err != nil {
				t.Fatalf("indexed: %v", err)
			}
			seqRows := recordData(t, seq, "extract-*.json")
			parRows := recordData(t, par, "extract-*.json")
			if len(seqRows) != tc.records {
				t.Fatalf("sequential rows %d, want %d", len(seqRows), tc.records)
			}
			if !reflect.DeepEqual(seqRows, parRows) {
				t.Fatalf("indexed rows differ:\nsequential %v\nindexed    %v", seqRows, parRows)
			}
		})
	}
}

// The companyfacts recipe is document-scoped (each fact reads the company's
// cik and entityName from the document root), so the record-index route
// refuses it at config validation, before any indexed record is read.
func TestPublicJSONCompanyfactsRefusesRecordIndex(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "in.json")
	input := `{"cik":1234567,"entityName":"Example Corp","facts":{"us-gaap":{"AccountsPayableCurrent":{"units":{"USD":[{"end":"2025-09-27","val":100,"accn":"0000000000-25-000002","fy":2025,"fp":"FY","form":"10-K","filed":"2025-10-31"}]}}}}}`
	if err := os.WriteFile(src, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "in")
	if _, err := runIndex(t, "build", src, "--input-format", "json", "--selector", "USD", "--progress=false", "--output", base); err != nil {
		t.Fatalf("index build: %v", err)
	}
	out := filepath.Join(dir, "out")
	err = runSumpter(t, []string{"extract", "files",
		"--signature-config-path", publicRecipePath("sec-edgar-companyfacts-signature.yaml"),
		"--extract-config-path", publicRecipePath("sec-edgar-companyfacts-usd-extract.yaml"),
		"--files", src, "--record-index", base + ".recordindex.json", "--output-path", out})
	if err == nil || !strings.Contains(err.Error(), `declare "match_scope: record"`) {
		t.Fatalf("companyfacts record-index: %v", err)
	}
	if _, serr := os.Stat(out); !os.IsNotExist(serr) {
		t.Fatalf("refused run created %s", out)
	}
}
