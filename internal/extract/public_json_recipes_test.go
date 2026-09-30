package extract_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fulmenhq/sumpter/internal/extract"
)

// publicJSONRecipe runs a public-data JSON recipe from examples/config/extract
// against a small synthetic document shaped like the published source. The
// inputs are hand-written; no source data ships with the repo.
type publicJSONRecipe struct {
	name      string
	signature string
	extract   string
	input     string
	want      []string // each record's extract.data
}

var publicJSONRecipes = []publicJSONRecipe{
	{
		name:      "usgs-geojson",
		signature: "usgs-geojson-feature-signature.yaml",
		extract:   "usgs-geojson-feature-extract.yaml",
		input:     `{"type":"FeatureCollection","metadata":{"generated":1790000000000,"count":2},"features":[{"type":"Feature","properties":{"mag":1.23,"place":"10 km N of Example","time":1789990000000,"tz":null,"status":"automatic","tsunami":0,"magType":"ml"},"geometry":{"type":"Point","coordinates":[-117.5,35.25,7.8]},"id":"xx1"},{"type":"Feature","properties":{"mag":null,"place":"Offshore Example","time":1789980000000,"status":"reviewed","tsunami":1,"magType":"mb"},"geometry":{"type":"Point","coordinates":[142.1,-3.5,10]},"id":"xx2"}],"bbox":[-117.5,-3.5,7.8,142.1,35.25,10]}`,
		want: []string{
			`{"event_id":"xx1","magnitude":1.23,"magnitude_type":"ml","place":"10 km N of Example","time_ms":1789990000000,"status":"automatic","tsunami":0,"longitude":-117.5,"latitude":35.25,"depth_km":7.8}`,
			`{"event_id":"xx2","magnitude_type":"mb","place":"Offshore Example","time_ms":1789980000000,"status":"reviewed","tsunami":1,"longitude":142.1,"latitude":-3.5,"depth_km":10}`,
		},
	},
	{
		name:      "sec-companyfacts",
		signature: "sec-edgar-companyfacts-signature.yaml",
		extract:   "sec-edgar-companyfacts-usd-extract.yaml",
		input:     `{"cik":1234567,"entityName":"Example Corp","facts":{"dei":{"EntityCommonStockSharesOutstanding":{"label":"l","units":{"shares":[{"end":"2026-01-15","val":1500,"accn":"0000000000-26-000001","fy":2026,"fp":"Q1","form":"10-Q","filed":"2026-01-30"}]}}},"us-gaap":{"AccountsPayableCurrent":{"label":"l","units":{"USD":[{"end":"2025-09-27","val":68960000000,"accn":"0000000000-25-000002","fy":2025,"fp":"FY","form":"10-K","filed":"2025-10-31","frame":"CY2025Q3I"},{"end":"2025-12-27","val":9007199254740993,"accn":"0000000000-26-000001","fy":2026,"fp":"Q1","form":"10-Q","filed":"2026-01-30"}]}}}}}`,
		want: []string{
			`{"cik":"1234567","entity_name":"Example Corp","taxonomy":"us-gaap","concept":"AccountsPayableCurrent","period_end":"2025-09-27","value":68960000000,"value_text":"68960000000","accession":"0000000000-25-000002","fiscal_year":2025,"fiscal_period":"FY","form":"10-K","filed":"2025-10-31","frame":"CY2025Q3I"}`,
			// value loses precision above 2^53; value_text keeps the source lexeme.
			`{"cik":"1234567","entity_name":"Example Corp","taxonomy":"us-gaap","concept":"AccountsPayableCurrent","period_end":"2025-12-27","value":9007199254740992,"value_text":"9007199254740993","accession":"0000000000-26-000001","fiscal_year":2026,"fiscal_period":"Q1","form":"10-Q","filed":"2026-01-30"}`,
		},
	},
	{
		name:      "sec-submissions",
		signature: "sec-edgar-submissions-signature.yaml",
		extract:   "sec-edgar-submissions-company-extract.yaml",
		input:     `{"cik":"0001234567","name":"Example Corp","tickers":["EXC"],"filings":{"recent":{"accessionNumber":["0000000000-26-000001","0000000000-25-000002"],"filingDate":["2026-01-30","2025-10-31"],"form":["10-Q","10-K"]},"files":[{"name":"CIK0001234567-submissions-001.json","filingCount":1200}]}}`,
		want: []string{
			`{"cik":"0001234567","entity_name":"Example Corp","tickers":["EXC"],"recent_filing_count":2,"recent_accession_numbers":["0000000000-26-000001","0000000000-25-000002"],"recent_forms":["10-Q","10-K"],"recent_filing_dates":["2026-01-30","2025-10-31"],"older_filing_files":["CIK0001234567-submissions-001.json"]}`,
		},
	},
	{
		name:      "openfda-drug-event",
		signature: "openfda-drug-event-signature.yaml",
		extract:   "openfda-drug-event-extract.yaml",
		input:     `{"meta":{"last_updated":"2026-09-28"},"results":[{"safetyreportid":"10000001","receivedate":"20260401","serious":"1","seriousnessdeath":"1","primarysource":{"reportercountry":"US"},"patient":{"patientsex":"2","reaction":[{"reactionmeddrapt":"Nausea"},{"reactionmeddrapt":"Headache"}],"drug":[{"medicinalproduct":"DRUG A","drugcharacterization":"1"},{"medicinalproduct":"DRUG B","drugcharacterization":"2"}]}},{"safetyreportid":"10000002","receivedate":"20260402","serious":"2","patient":{"reaction":[{"reactionmeddrapt":"Rash"}],"drug":[{"medicinalproduct":"DRUG C","drugcharacterization":"1"}]}}]}`,
		want: []string{
			`{"safetyreportid":"10000001","receivedate":"20260401","serious":"1","seriousness_death":"1","reporter_country":"US","patient_sex":"2","reactions":["Nausea","Headache"],"drug_count":2,"suspect_products":["DRUG A"]}`,
			`{"safetyreportid":"10000002","receivedate":"20260402","serious":"2","reactions":["Rash"],"drug_count":1,"suspect_products":["DRUG C"]}`,
		},
	},
}

func TestPublicJSONRecipes(t *testing.T) {
	cfgDir := filepath.Join("..", "..", "examples", "config", "extract")
	for _, tc := range publicJSONRecipes {
		t.Run(tc.name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "input.json")
			if err := os.WriteFile(input, []byte(tc.input), 0o600); err != nil {
				t.Fatal(err)
			}
			got := runTwinRaw(t, filepath.Join(cfgDir, tc.signature), filepath.Join(cfgDir, tc.extract),
				input, "", false, extract.FormatJSON)
			want := make([][]byte, len(tc.want))
			for i, w := range tc.want {
				want[i] = []byte(`{"data":` + w + `}`)
			}
			if err := typedRecordsEqual(want, got); err != nil {
				t.Fatal(err)
			}
		})
	}
}
