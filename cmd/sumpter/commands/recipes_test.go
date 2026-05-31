package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/validation"
	"github.com/parquet-go/parquet-go"
	"github.com/spf13/cobra"
)

func TestRecipesCommand(t *testing.T) {
	cmd := NewRecipesCommand()

	if cmd == nil {
		t.Fatal("expected recipes command, got nil")
	}

	if cmd.Use != "recipes" {
		t.Errorf("Use = %q, want 'recipes'", cmd.Use)
	}

	if cmd.Short == "" {
		t.Error("Short description is empty")
	}

	// Verify command has subcommands
	subCommands := cmd.Commands()
	if len(subCommands) == 0 {
		t.Error("expected subcommands, got none")
	}
}

func TestRecipesSubcommands(t *testing.T) {
	tests := []struct {
		name string
		use  string
	}{
		{"init subcommand", "init"},
		{"retrieve subcommand", "retrieve"},
		{"run subcommand", "run"},
	}

	recipesCmd := NewRecipesCommand()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found := false
			for _, subCmd := range recipesCmd.Commands() {
				// Check if Use starts with the command name (handles "command <args>" format)
				if subCmd.Use == tt.use || (len(subCmd.Use) > len(tt.use) && subCmd.Use[:len(tt.use)] == tt.use) {
					found = true
					if subCmd.Short == "" {
						t.Errorf("subcommand %q has empty Short description", tt.use)
					}
					break
				}
			}
			if !found {
				t.Errorf("subcommand %q not found", tt.use)
			}
		})
	}
}

func TestRecipeRunExtractCommandRegistersFormatsFlag(t *testing.T) {
	cmd := newRecipeRunExtractCommand()
	if flag := cmd.Flags().Lookup("formats"); flag == nil {
		t.Fatalf("recipes run extract command missing --formats flag")
	}
	if flag := cmd.Flags().Lookup("continue-on-error"); flag == nil {
		t.Fatalf("recipes run extract command missing --continue-on-error flag")
	}
}

func TestExecuteExtractRecipeApplicabilityNotApplicable(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><Other>skip</Other></root>`)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if len(manifest.Inputs) != 1 {
		t.Fatalf("manifest inputs len = %d, want 1", len(manifest.Inputs))
	}
	input := manifest.Inputs[0]
	if input.Disposition != "not_applicable" {
		t.Fatalf("input disposition = %q, want not_applicable", input.Disposition)
	}
	if input.DispositionReason != "applicability_predicate_false" {
		t.Fatalf("input disposition_reason = %q, want applicability_predicate_false", input.DispositionReason)
	}

	summary := readDispositionSummary(t, filepath.Join(workspace, "outputs", "dispositions.json"))
	if summary["not_applicable"] != float64(1) || summary["applied"] != float64(0) || summary["failed"] != float64(0) {
		t.Fatalf("disposition counts = %#v, want one not_applicable", summary)
	}
}

func TestExecuteExtractRecipeApplicabilityApplied(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><TargetElement><Name>Alpha</Name></TargetElement></root>`)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	outputPath := filepath.Join(workspace, "outputs", "extract-input.xml.json")
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("Open output: %v", err)
	}
	defer func() { _ = file.Close() }()

	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("Decode output: %v", err)
	}
	data := extractData(t, record)
	if data["name"] != "Alpha" {
		t.Fatalf("extract.data[name] = %#v, want Alpha", data["name"])
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if got := manifest.Inputs[0].Disposition; got != "applied" {
		t.Fatalf("input disposition = %q, want applied", got)
	}
	summary := readDispositionSummary(t, filepath.Join(workspace, "outputs", "dispositions.json"))
	if summary["applied"] != float64(1) || summary["not_applicable"] != float64(0) || summary["failed"] != float64(0) {
		t.Fatalf("disposition counts = %#v, want one applied", summary)
	}
}

func TestExecuteExtractRecipeApplicabilityAffectsRecipeProvenance(t *testing.T) {
	initExtractManifestTestLogger(t)

	inputXML := `<root><TargetElement><Name>Alpha</Name></TargetElement></root>`
	workspaceA := createRecipeApplicabilityWorkspace(t, inputXML)
	cmdA := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmdA, workspaceA, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false}); err != nil {
		t.Fatalf("executeExtractRecipe A: %v", err)
	}
	manifestA := readManifest(t, filepath.Join(workspaceA, "outputs", provenance.ManifestFileName))
	if manifestA.Recipe == nil {
		t.Fatal("recipe provenance A missing")
	}
	if !strings.Contains(manifestA.Recipe.ApplicabilityYAML, "count(//TargetElement) > 0") {
		t.Fatalf("applicability YAML A = %q, want predicate embedded", manifestA.Recipe.ApplicabilityYAML)
	}

	workspaceB := createRecipeApplicabilityWorkspace(t, inputXML)
	mustWriteFile(t, filepath.Join(workspaceB, "applicability", "applicability.yaml"), `applicability:
  type: xpath
  expression: "count(//Name) > 0"
  description: "Applies to inputs with Name"
`)
	cmdB := recipeRunExtractTestCommand()
	if err := executeExtractRecipe(cmdB, workspaceB, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false}); err != nil {
		t.Fatalf("executeExtractRecipe B: %v", err)
	}
	manifestB := readManifest(t, filepath.Join(workspaceB, "outputs", provenance.ManifestFileName))
	if manifestB.Recipe == nil {
		t.Fatal("recipe provenance B missing")
	}
	if !strings.Contains(manifestB.Recipe.ApplicabilityYAML, "count(//Name) > 0") {
		t.Fatalf("applicability YAML B = %q, want changed predicate embedded", manifestB.Recipe.ApplicabilityYAML)
	}

	if manifestA.Recipe.ContentHash == "" || manifestB.Recipe.ContentHash == "" {
		t.Fatalf("content hashes must be populated: A=%q B=%q", manifestA.Recipe.ContentHash, manifestB.Recipe.ContentHash)
	}
	if manifestA.Recipe.ContentHash == manifestB.Recipe.ContentHash {
		t.Fatalf("different applicability assets produced same content hash: %s", manifestA.Recipe.ContentHash)
	}
}

func TestExecuteExtractRecipeApplicabilityTrueSignatureMismatchFailed(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><TargetElement><Name>Alpha</Name></TargetElement></root>`)
	mustWriteFile(t, filepath.Join(workspace, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: missing
    name: Missing
    selector: /MissingRoot
    weight: 1
confidence_threshold: 1
`)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil {
		t.Fatal("expected signature mismatch disposition failure")
	}
	if !strings.Contains(err.Error(), "signature_mismatch") {
		t.Fatalf("error = %v, want signature_mismatch", err)
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if got := manifest.Inputs[0].Disposition; got != "failed" {
		t.Fatalf("input disposition = %q, want failed", got)
	}
	if got := manifest.Inputs[0].DispositionReason; got != "signature_mismatch" {
		t.Fatalf("input disposition_reason = %q, want signature_mismatch", got)
	}
	summary := readDispositionSummary(t, filepath.Join(workspace, "outputs", "dispositions.json"))
	if summary["failed"] != float64(1) || summary["applied"] != float64(0) || summary["not_applicable"] != float64(0) {
		t.Fatalf("disposition counts = %#v, want one failed", summary)
	}
}

func TestExecuteExtractRecipeApplicabilityTrueMinOccurrencesFailed(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><TargetElement><Name>Alpha</Name></TargetElement></root>`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //MissingElement
    min_occurrences: 1
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
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil {
		t.Fatal("expected min_occurrences disposition failure")
	}
	if !strings.Contains(err.Error(), "min_occurrences violation") {
		t.Fatalf("error = %v, want min_occurrences violation", err)
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if got := manifest.Inputs[0].Disposition; got != "failed" {
		t.Fatalf("input disposition = %q, want failed", got)
	}
	if got := manifest.Inputs[0].DispositionReason; got != "min_occurrences_violation" {
		t.Fatalf("input disposition_reason = %q, want min_occurrences_violation", got)
	}
	if strings.Contains(manifest.Inputs[0].DispositionDetail, workspace) {
		t.Fatalf("manifest disposition_detail leaked workspace path: %q", manifest.Inputs[0].DispositionDetail)
	}
	dispositionsPath := filepath.Join(workspace, "outputs", "dispositions.json")
	summary := readDispositionSummary(t, dispositionsPath)
	if summary["failed"] != float64(1) || summary["applied"] != float64(0) || summary["not_applicable"] != float64(0) {
		t.Fatalf("disposition counts = %#v, want one failed", summary)
	}
	if summaryText := readFileString(t, dispositionsPath); strings.Contains(summaryText, workspace) {
		t.Fatalf("dispositions summary leaked workspace path: %s", summaryText)
	}
}

func TestExecuteExtractRecipeWithoutApplicabilityPreservesMinOccurrencesFailure(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><Other>skip</Other></root>`)
	manifestText := strings.ReplaceAll(readFileString(t, filepath.Join(workspace, "recipe.yaml")), "  applicability: applicability/applicability.yaml\n", "")
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), manifestText)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil {
		t.Fatal("expected min_occurrences failure without applicability")
	}
	if !strings.Contains(err.Error(), "min_occurrences violation") {
		t.Fatalf("error = %v, want min_occurrences violation", err)
	}
	if _, statErr := os.Stat(filepath.Join(workspace, "outputs", "dispositions.json")); !os.IsNotExist(statErr) {
		t.Fatalf("dispositions.json stat error = %v, want not exist", statErr)
	}
}

func TestExecuteExtractRecipeApplicabilityRequiresOutputPath(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><Other>skip</Other></root>`)
	manifestText := strings.ReplaceAll(readFileString(t, filepath.Join(workspace, "recipe.yaml")), "    path: outputs\n", "    path: \"\"\n")
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), manifestText)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil {
		t.Fatal("expected applicability output-path requirement error")
	}
	if !strings.Contains(err.Error(), "requires --output-path") {
		t.Fatalf("error = %v, want output-path requirement", err)
	}
}

func TestExecuteExtractRecipeApplicabilityRejectsEscapingAsset(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeApplicabilityWorkspace(t, `<root><TargetElement><Name>Alpha</Name></TargetElement></root>`)
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), strings.ReplaceAll(readFileString(t, filepath.Join(workspace, "recipe.yaml")), "applicability: applicability/applicability.yaml", "applicability: ../outside.yaml"))
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{ManifestPath: "recipe.yaml", Progress: false})
	if err == nil {
		t.Fatal("expected escaping applicability asset error")
	}
	if !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("error = %v, want escapes workspace", err)
	}
}

func TestExecuteExtractRecipeInjectsManifestAndCLIParameters(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeParameterWorkspace(t)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		ClientID:     "client-flag",
		SiteID:       "site-flag",
		Parameters:   []string{"region_id=east", "tenant_id=tenant-cli", "tenant_label=tenant-a", "site_id=site-param"},
		Progress:     false,
	})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	outputPath := filepath.Join(workspace, "outputs", "extract-input.xml.json")
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatalf("Open output: %v", err)
	}
	defer func() { _ = file.Close() }()

	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("Decode output: %v", err)
	}
	data := extractData(t, record)

	want := map[string]string{
		"name":          "Alpha",
		"region_id":     "east",
		"tenant_id":     "tenant-cli",
		"tenant_label":  "tenant-a",
		"tenant_bucket": "in_scope",
		"client_id":     "client-param-default",
		"site_id":       "site-param",
	}
	for key, value := range want {
		if data[key] != value {
			t.Fatalf("extract.data[%s] = %#v, want %#v (data: %#v)", key, data[key], value, data)
		}
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if manifest.Recipe == nil {
		t.Fatal("manifest recipe provenance is nil")
	}
	if manifest.Recipe.Cadence != "" {
		t.Fatalf("manifest recipe cadence = %q, want empty", manifest.Recipe.Cadence)
	}
}

func TestExecuteExtractRecipeWritesJSONLAndParquetOutputs(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeParameterWorkspace(t)
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: parameter_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  cadence: daily-rolling
  input:
    mode: files
    files:
      - testdata/input.xml
    include_pattern: "*.xml"
  output:
    formats: [json, parquet]
    path: outputs
    patterns:
      json: extract-{}.jsonl
      parquet: extract-{}.parquet
    parquet:
      compression: none
  parameters:
    region_id: west
    tenant_id: tenant-default
  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
`)

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		ClientID:     "client-cli",
		SiteID:       "site-cli",
		Progress:     false,
	})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	jsonPath := filepath.Join(workspace, "outputs", "extract-input.xml.jsonl")
	file, err := os.Open(jsonPath)
	if err != nil {
		t.Fatalf("Open JSONL output: %v", err)
	}
	defer func() { _ = file.Close() }()
	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("Decode JSONL output: %v", err)
	}
	if _, ok := record["_runtime"]; !ok {
		t.Fatalf("JSONL record missing _runtime envelope: %#v", record)
	}
	data := extractData(t, record)
	if data["client_id"] != "client-cli" || data["site_id"] != "site-cli" {
		t.Fatalf("JSONL injected fields = client_id:%#v site_id:%#v, want client-cli/site-cli", data["client_id"], data["site_id"])
	}

	type Row struct {
		Name     string `parquet:"name,optional"`
		ClientID string `parquet:"client_id,optional"`
		SiteID   string `parquet:"site_id,optional"`
	}
	parquetPath := filepath.Join(workspace, "outputs", "extract-input.xml.parquet")
	rows, err := parquet.ReadFile[Row](parquetPath)
	if err != nil {
		t.Fatalf("ReadFile parquet output: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "Alpha" || rows[0].ClientID != "client-cli" || rows[0].SiteID != "site-cli" {
		t.Fatalf("parquet rows = %#v, want Alpha/client-cli/site-cli", rows)
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	if manifest.Recipe == nil {
		t.Fatal("manifest recipe provenance is nil")
	}
	if manifest.Recipe.Cadence != "daily-rolling" {
		t.Fatalf("manifest recipe cadence = %q, want daily-rolling", manifest.Recipe.Cadence)
	}
	if len(manifest.Outputs) != 2 {
		t.Fatalf("manifest outputs = %#v, want 2 outputs", manifest.Outputs)
	}
	formats := map[string]bool{}
	for _, output := range manifest.Outputs {
		formats[output.Format] = true
	}
	if !formats["json"] || !formats["parquet"] {
		t.Fatalf("manifest output formats = %#v, want json and parquet", formats)
	}
}

func TestExecuteExtractRecipeUniformSchemaWritesNulls(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeParameterWorkspace(t)
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: uniform_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.jsonl
    uniform_schema: true
  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
  - output_field: missing_code
    xpath: missing-code
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    missing_code:
      type: string
  required:
    - name
    - missing_code
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "input.xml"), `<root><item><name>Alpha</name></item></root>`)

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	outputPath := filepath.Join(workspace, "outputs", "extract-input.xml.jsonl")
	raw := readFileString(t, outputPath)
	if !strings.Contains(raw, `"missing_code":null`) {
		t.Fatalf("JSONL output missing explicit null field: %s", raw)
	}
	var record map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &record); err != nil {
		t.Fatalf("Decode JSONL output: %v", err)
	}
	data := extractData(t, record)
	if value, ok := data["missing_code"]; !ok || value != nil {
		t.Fatalf("missing_code = %#v/%t, want nil/true in %#v", value, ok, data)
	}
}

func TestExecuteExtractRecipeWithholdsParquetPartitionColumns(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeParameterWorkspace(t)
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: parameter_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
    include_pattern: "*.xml"
  output:
    formats: [json, parquet]
    path: outputs
    patterns:
      json: extract-{}.jsonl
      parquet: extract-{}.parquet
    parquet:
      compression: none
      withhold_columns: [client_id, site_id]
  parameters:
    tenant_label: tenant-a
  workers: 1
  progress: false
`)

	cmd := recipeRunExtractTestCommand()
	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		ClientID:     "client-cli",
		SiteID:       "site-cli",
		Progress:     false,
	})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	jsonPath := filepath.Join(workspace, "outputs", "extract-input.xml.jsonl")
	file, err := os.Open(jsonPath)
	if err != nil {
		t.Fatalf("Open JSONL output: %v", err)
	}
	defer func() { _ = file.Close() }()
	var record map[string]interface{}
	if err := json.NewDecoder(file).Decode(&record); err != nil {
		t.Fatalf("Decode JSONL output: %v", err)
	}
	data := extractData(t, record)
	if data["client_id"] != "client-cli" || data["site_id"] != "site-cli" {
		t.Fatalf("JSONL injected fields = client_id:%#v site_id:%#v, want client-cli/site-cli", data["client_id"], data["site_id"])
	}

	parquetPath := filepath.Join(workspace, "outputs", "extract-input.xml.parquet")
	pqFile := openCommandParquetFile(t, parquetPath)
	if got, ok := pqFile.Lookup("sumpter.parquet.withhold_columns"); !ok || got != "client_id,site_id" {
		t.Fatalf("withhold metadata = %q/%t, want client_id,site_id/true", got, ok)
	}
	fields := commandParquetFieldNames(pqFile)
	for _, omitted := range []string{"client_id", "site_id"} {
		if fields[omitted] {
			t.Fatalf("field %q was written to parquet schema: %#v", omitted, fields)
		}
	}
	if !fields["name"] {
		t.Fatalf("name missing from parquet schema: %#v", fields)
	}

	manifest := readManifest(t, filepath.Join(workspace, "outputs", provenance.ManifestFileName))
	for _, output := range manifest.Outputs {
		if output.Format == "json" && len(output.WithholdColumns) != 0 {
			t.Fatalf("JSON output withhold_columns = %#v, want empty", output.WithholdColumns)
		}
		if output.Format == "parquet" {
			if len(output.WithholdColumns) != 2 || output.WithholdColumns[0] != "client_id" || output.WithholdColumns[1] != "site_id" {
				t.Fatalf("Parquet output withhold_columns = %#v, want client_id/site_id", output.WithholdColumns)
			}
		}
	}
}

func TestExecuteExtractRecipeInjectsSourceExtractionPerFile(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeSourceExtractionWorkspace(t)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		Progress:     false,
	})
	if err != nil {
		t.Fatalf("executeExtractRecipe: %v", err)
	}

	tests := []struct {
		output       string
		wantName     string
		wantDate     string
		wantSourceID string
	}{
		{
			output:       "extract-2026-05-15-register.xml.json",
			wantName:     "Alpha",
			wantDate:     "2026-05-15",
			wantSourceID: "store-17",
		},
		{
			output:       "extract-2026-05-16-register.xml.json",
			wantName:     "Beta",
			wantDate:     "2026-05-16",
			wantSourceID: "store-22",
		},
	}

	for _, tt := range tests {
		t.Run(tt.output, func(t *testing.T) {
			outputPath := filepath.Join(workspace, "outputs", tt.output)
			file, err := os.Open(outputPath)
			if err != nil {
				t.Fatalf("Open output: %v", err)
			}
			defer func() { _ = file.Close() }()

			var record map[string]interface{}
			if err := json.NewDecoder(file).Decode(&record); err != nil {
				t.Fatalf("Decode output: %v", err)
			}
			data := extractData(t, record)

			want := map[string]string{
				"name":           tt.wantName,
				"business_date":  tt.wantDate,
				"source_site_id": tt.wantSourceID,
				"tenant_id":      "tenant-default",
			}
			for key, value := range want {
				if data[key] != value {
					t.Fatalf("extract.data[%s] = %#v, want %#v (data: %#v)", key, data[key], value, data)
				}
			}
		})
	}
}

func readDispositionSummary(t *testing.T, path string) map[string]interface{} {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test-owned temp path.
	if err != nil {
		t.Fatalf("ReadFile dispositions summary: %v", err)
	}
	validator := validation.NewSchemaValidator(filepath.Join("..", "..", "..", "schemas"))
	result, err := validator.ValidateDispositionSummary(data, filepath.Base(path))
	if err != nil {
		t.Fatalf("ValidateDispositionSummary: %v", err)
	}
	if !result.IsValid() {
		t.Fatalf("dispositions summary failed schema validation: %+v", result.Errors)
	}
	var summary map[string]interface{}
	if err := json.Unmarshal(data, &summary); err != nil {
		t.Fatalf("Decode dispositions summary: %v", err)
	}
	return summary
}

func readFileString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 - test-owned temp path.
	if err != nil {
		t.Fatalf("ReadFile %s: %v", path, err)
	}
	return string(data)
}

func createRecipeApplicabilityWorkspace(t *testing.T, inputXML string) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "applicability", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: applicability_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
  applicability: applicability/applicability.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(workspace, "applicability", "applicability.yaml"), `applicability:
  type: xpath
  expression: "count(//TargetElement) > 0"
  description: "Applies to inputs with TargetElement"
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //TargetElement
    min_occurrences: 1
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
	mustWriteFile(t, filepath.Join(workspace, "testdata", "input.xml"), inputXML)
	return workspace
}

func recipeRunExtractTestCommand() *cobra.Command {
	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	cmd := &cobra.Command{Use: "extract"}
	root.AddCommand(cmd)
	return cmd
}

func createRecipeParameterWorkspace(t *testing.T) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{"signature", "extract", "testdata", "outputs"} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: parameter_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
    include_pattern: "*.xml"
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  client_id: client-default
  site_id: site-default
  parameters:
    client_id: client-param-default
    region_id: west
    tenant_id: tenant-default
    tenant_label: tenant-default
  parameters_required:
    - client_id
    - site_id
    - region_id
    - tenant_id
    - tenant_label
  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
  - output_field: extracted_tenant
    xpath: tenant
    type: string
  - output_field: tenant_bucket
    expression: 'lower(extracted_tenant) == tenant_label ? "in_scope" : "out_of_scope"'
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    extracted_tenant:
      type: string
    tenant_bucket:
      type: string
    client_id:
      type: string
    site_id:
      type: string
    region_id:
      type: string
    tenant_id:
      type: string
    tenant_label:
      type: string
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "input.xml"), `<root><item><name>Alpha</name><tenant>Tenant-A</tenant></item></root>`)
	return workspace
}

func createRecipeSourceExtractionWorkspace(t *testing.T) string {
	t.Helper()
	workspace := createWorkingTempDir(t)
	for _, dir := range []string{
		"signature",
		"extract",
		"testdata/sites/store-17",
		"testdata/sites/store-22",
		"outputs",
	} {
		if err := os.MkdirAll(filepath.Join(workspace, dir), 0o750); err != nil {
			t.Fatalf("MkdirAll %s: %v", dir, err)
		}
	}
	mustWriteFile(t, filepath.Join(workspace, "recipe.yaml"), `version: recipe/v0.1.0
kind: extract
id: source_extraction_recipe
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: testdata
    files:
      - testdata/sites/store-17/2026-05-15-register.xml
      - testdata/sites/store-22/2026-05-16-register.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters:
    tenant_id: tenant-default
  source_extraction:
    - id: filename-date-token
      source: filename
      pattern: '^(?P<business_date>\d{4}-\d{2}-\d{2})-.*\.xml$'
    - id: path-site-identifier
      source: relative_path
      pattern: '^sites/(?P<source_site_id>[a-z0-9-]+)/'
  source_extraction_required:
    - business_date
    - source_site_id
  workers: 1
  progress: false
`)
	mustWriteFile(t, filepath.Join(workspace, "signature", "signature.yaml"), `signature_id: sample
name: Sample
match_patterns:
  - pattern_id: root
    name: Root
    selector: /root
    weight: 1
confidence_threshold: 1
`)
	mustWriteFile(t, filepath.Join(workspace, "extract", "extract.yaml"), `record_type: sample_record
match_selectors:
  - xpath: //item
field_mappings:
  - output_field: name
    xpath: name
    type: string
output_schema:
  type: object
  properties:
    name:
      type: string
    business_date:
      type: string
    source_site_id:
      type: string
    tenant_id:
      type: string
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "sites", "store-17", "2026-05-15-register.xml"), `<root><item><name>Alpha</name></item></root>`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "sites", "store-22", "2026-05-16-register.xml"), `<root><item><name>Beta</name></item></root>`)
	return workspace
}

func extractData(t *testing.T, record map[string]interface{}) map[string]interface{} {
	t.Helper()
	extractBlock, ok := record["extract"].(map[string]interface{})
	if !ok {
		t.Fatalf("record missing extract block: %#v", record)
	}
	data, ok := extractBlock["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("record missing extract.data block: %#v", record)
	}
	return data
}

func openCommandParquetFile(t *testing.T, path string) *parquet.File {
	t.Helper()
	f, err := os.Open(path) // #nosec G304 - test-owned temp path.
	if err != nil {
		t.Fatalf("Open parquet: %v", err)
	}
	t.Cleanup(func() { _ = f.Close() })
	stat, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat parquet: %v", err)
	}
	pqFile, err := parquet.OpenFile(f, stat.Size())
	if err != nil {
		t.Fatalf("OpenFile parquet: %v", err)
	}
	return pqFile
}

func commandParquetFieldNames(file *parquet.File) map[string]bool {
	fields := make(map[string]bool)
	for _, field := range file.Schema().Fields() {
		fields[field.Name()] = true
	}
	return fields
}
