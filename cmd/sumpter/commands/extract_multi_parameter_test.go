package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/extract"
	"github.com/spf13/cobra"
)

// writeMultiRecipeWorkspaceWithDefaults is writeMultiRecipeWorkspace with a
// custom defaults block (so a recipe can declare its own defaults.parameters and
// parameters_required) while keeping the same signature/extract assets. The
// caller supplies the YAML body of `defaults:` (already indented two spaces).
func writeMultiRecipeWorkspaceWithDefaults(t *testing.T, id, defaultsBody string) string {
	t.Helper()
	ws := writeMultiRecipeWorkspace(t, id)
	recipe := `version: recipe/v0.1.0
kind: extract
id: ` + id + `
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
` + defaultsBody
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(recipe), 0o600); err != nil {
		t.Fatalf("rewrite recipe.yaml: %v", err)
	}
	return ws
}

func writeMultiClassifierWorkspace(t *testing.T, id string) string {
	t.Helper()
	ws := writeMultiRecipeWorkspaceWithDefaults(t, id, `  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters_required:
    - curated_prefixes
`)
	extractYAML := `record_type: ` + id + `_record
match_selectors:
  - xpath: //TargetElement
field_mappings:
  - output_field: name
    xpath: Name
    type: string
  - output_field: is_curated
    expression: 'starts_with_any(name, curated_prefixes)'
    type: boolean
output_schema:
  type: object
  properties:
    name:
      type: string
    is_curated:
      type: boolean
    curated_prefixes:
      type: array
      items:
        type: string
`
	if err := os.WriteFile(filepath.Join(ws, "extract", "extract.yaml"), []byte(extractYAML), 0o600); err != nil {
		t.Fatalf("rewrite classifier extract.yaml: %v", err)
	}
	return ws
}

const multiParamSharedRunID = "0190a3f4-1c2d-7abc-9def-0123456789ab"

// TestExtractMultiParameter_OverridesDivergentDefaults proves a single shared
// run-level --parameter overrides EACH recipe's own (divergent) defaults.parameters
// for the same key — run-level wins uniformly, matching single-recipe override
// direction (entarch ruling precedence: defaults.parameters -> shared --parameter).
func TestExtractMultiParameter_OverridesDivergentDefaults(t *testing.T) {
	wsA := writeMultiRecipeWorkspaceWithDefaults(t, "alpha", `  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters:
    stamp: alpha-default
`)
	wsB := writeMultiRecipeWorkspaceWithDefaults(t, "beta", `  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters:
    stamp: beta-default
`)
	outputRoot := filepath.Join(t.TempDir(), "out")
	dirs, err := resolveRecipeOutputDirs(outputRoot, []string{"alpha", "beta"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs: %v", err)
	}

	shared := &multiSharedOptions{
		FileList:   filepath.Join(t.TempDir(), "files.txt"),
		RunID:      multiParamSharedRunID,
		Workers:    1,
		Parameters: []string{"stamp=run-level"}, // one shared value over both recipes
	}

	for i, ws := range []string{wsA, wsB} {
		plan, err := loadRecipePlan(ws, shared, dirs[i].Dir, io.Discard)
		if err != nil {
			t.Fatalf("loadRecipePlan %s: %v", ws, err)
		}
		got := plan.fieldPlan.build(nil)["stamp"]
		if got != "run-level" {
			t.Errorf("recipe %q: stamp = %#v, want shared run-level value to override the manifest default", plan.RecipeID, got)
		}
	}
}

// TestExtractMultiParameter_SatisfiesRequiredPerRecipe proves a shared --parameter
// satisfies a recipe's defaults.parameters_required independently: without it the
// plan load fails the required check; with it the same recipe loads clean.
func TestExtractMultiParameter_SatisfiesRequiredPerRecipe(t *testing.T) {
	ws := writeMultiRecipeWorkspaceWithDefaults(t, "needs", `  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters_required:
    - stamp
`)
	outputRoot := filepath.Join(t.TempDir(), "out")
	dirs, err := resolveRecipeOutputDirs(outputRoot, []string{"needs"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs: %v", err)
	}

	base := func(params []string) *multiSharedOptions {
		return &multiSharedOptions{
			FileList:   filepath.Join(t.TempDir(), "files.txt"),
			RunID:      multiParamSharedRunID,
			Workers:    1,
			Parameters: params,
		}
	}

	if _, err := loadRecipePlan(ws, base(nil), dirs[0].Dir, io.Discard); err == nil {
		t.Fatal("expected required-parameter failure with no shared --parameter, got nil")
	}
	if _, err := loadRecipePlan(ws, base([]string{"stamp=run-level"}), dirs[0].Dir, io.Discard); err != nil {
		t.Fatalf("shared --parameter should satisfy parameters_required: %v", err)
	}
}

// TestExtractMultiParameter_EmitsOnEveryRecipeAndRecordsArgv runs the real cobra
// command with a shared --parameter and asserts (1) the run-level value reaches
// EVERY recipe's records, and (2) the provenance manifest argv records the
// --parameter, so the invocation is replayable.
func TestExtractMultiParameter_EmitsOnEveryRecipeAndRecordsArgv(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")

	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(newRecipeRunExtractMultiCommand())
	root.SetArgs([]string{
		"extract-multi", wsA, wsB,
		"--file-list", fileList,
		"--output-path", outRoot,
		"--parameter", "harness_version=v1.2.3",
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("extract-multi command: %v", err)
	}

	for _, id := range []string{"summary", "line-items"} {
		records := readDirConcat(t, filepath.Join(outRoot, id))
		if !strings.Contains(records, `"harness_version":"v1.2.3"`) {
			t.Errorf("recipe %q records missing the shared run-level parameter:\n%s", id, records)
		}
		manifest, err := os.ReadFile(filepath.Join(outRoot, id, "manifest.json"))
		if err != nil {
			t.Fatalf("read manifest %q: %v", id, err)
		}
		if !strings.Contains(string(manifest), "--parameter=harness_version=v1.2.3") {
			t.Errorf("recipe %q manifest argv missing the --parameter:\n%s", id, manifest)
		}
	}
}

// TestExtractMultiParameter_ListPassthrough proves a JSON-array --parameter
// survives cobra StringArray parsing and emits as a JSON array on records,
// byte-compatible with single-recipe list-parameter semantics (SUM-040).
func TestExtractMultiParameter_ListPassthrough(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")

	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(newRecipeRunExtractMultiCommand())
	root.SetArgs([]string{
		"extract-multi", ws,
		"--file-list", fileList,
		"--output-path", outRoot,
		"--parameter", `prefixes=["NM_","NR_"]`,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("extract-multi command: %v", err)
	}

	records := readDirConcat(t, filepath.Join(outRoot, "summary"))
	if !strings.Contains(records, `"prefixes":["NM_","NR_"]`) {
		t.Errorf("list parameter not emitted as a JSON array on records:\n%s", records)
	}
}

func TestBuildExtractMultiArgv_IncludesInternalParameters(t *testing.T) {
	argv := buildExtractMultiArgv([]string{"ws"}, &recipeRunExtractMultiOptions{
		OutputPath:         "out",
		Parameters:         []string{"visible=value"},
		InternalParameters: []string{`curated_prefixes=["NM_"]`, `tenant_prefixes=["XM_"]`},
		Stats:              true,
	})
	joined := strings.Join(argv, " ")
	if strings.Count(joined, "--parameter-internal=") != 2 {
		t.Fatalf("buildExtractMultiArgv did not include repeatable --parameter-internal: %s", joined)
	}
	if !strings.Contains(joined, `--parameter-internal=curated_prefixes=["NM_"]`) ||
		!strings.Contains(joined, `--parameter-internal=tenant_prefixes=["XM_"]`) {
		t.Fatalf("buildExtractMultiArgv missing internal parameter values: %s", joined)
	}
	if !strings.Contains(joined, "--parameter=visible=value") {
		t.Fatalf("buildExtractMultiArgv missing ordinary parameter: %s", joined)
	}
	if strings.Contains(joined, "--stats") {
		t.Fatalf("buildExtractMultiArgv must still omit --stats, got: %s", joined)
	}
}

func TestExtractMultiParameterInternal_BystanderSuppressedEverywhere(t *testing.T) {
	consumer := writeMultiClassifierWorkspace(t, "classifier")
	bystander := writeMultiRecipeWorkspace(t, "bystander")
	inputDir := t.TempDir()
	input := filepath.Join(inputDir, "in.xml")
	if err := os.WriteFile(input, []byte(`<root><TargetElement><Name>AA_001</Name></TargetElement></root>`), 0o600); err != nil {
		t.Fatalf("write input: %v", err)
	}
	fileList := filepath.Join(inputDir, "files.txt")
	if err := os.WriteFile(fileList, []byte(input+"\n"), 0o600); err != nil {
		t.Fatalf("write file list: %v", err)
	}
	outRoot := filepath.Join(t.TempDir(), "out")

	root := &cobra.Command{Use: "sumpter"}
	root.PersistentFlags().Bool("allow-large-files", false, "")
	root.AddCommand(newRecipeRunExtractMultiCommand())
	root.SetArgs([]string{
		"extract-multi", consumer, bystander,
		"--file-list", fileList,
		"--output-path", outRoot,
		"--parameter-internal", `curated_prefixes=["AA_"]`,
	})
	if err := root.Execute(); err != nil {
		t.Fatalf("extract-multi command: %v", err)
	}

	for _, id := range []string{"classifier", "bystander"} {
		records := readDirConcat(t, filepath.Join(outRoot, id))
		if strings.Contains(records, `"curated_prefixes":`) {
			t.Fatalf("recipe %q leaked run-level internal parameter into records:\n%s", id, records)
		}
		manifest := readFileString(t, filepath.Join(outRoot, id, "manifest.json"))
		if strings.Contains(manifest, "AA_") {
			t.Fatalf("recipe %q manifest leaked run-level internal parameter value:\n%s", id, manifest)
		}
		if !strings.Contains(manifest, `curated_prefixes=\u003cinternal\u003e`) {
			t.Fatalf("recipe %q manifest argv did not redact run-level internal parameter:\n%s", id, manifest)
		}
	}
	consumerRecords := readDirConcat(t, filepath.Join(outRoot, "classifier"))
	if !strings.Contains(consumerRecords, `"is_curated":true`) {
		t.Fatalf("run-level internal list did not stay in classifier expression scope:\n%s", consumerRecords)
	}
}

func TestExtractMultiParameterInternal_RejectsOverlap(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	fileList, _ := writeMultiInputSet(t, 1)
	err := runExtractMulti(&multiSharedOptions{
		FileList:           fileList,
		OutputPath:         filepath.Join(t.TempDir(), "out"),
		Parameters:         []string{"curated_prefixes=visible"},
		InternalParameters: []string{"curated_prefixes=hidden"},
		InputWorkers:       1,
	}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("expected overlap between --parameter and --parameter-internal to fail")
	}
	if !strings.Contains(err.Error(), `parameter "curated_prefixes" cannot be supplied with both --parameter and --parameter-internal`) {
		t.Fatalf("overlap error did not identify the key only: %v", err)
	}
	if strings.Contains(err.Error(), "hidden") || strings.Contains(err.Error(), "visible") {
		t.Fatalf("overlap error leaked a parameter value: %v", err)
	}
}

func TestExtractMultiParameterInternal_EmptyScalarStillFailsRequired(t *testing.T) {
	ws := writeMultiClassifierWorkspace(t, "classifier")
	fileList, _ := writeMultiInputSet(t, 1)
	err := runExtractMulti(&multiSharedOptions{
		FileList:           fileList,
		OutputPath:         filepath.Join(t.TempDir(), "out"),
		InternalParameters: []string{"curated_prefixes=   "},
		InputWorkers:       1,
	}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("expected empty scalar --parameter-internal to fail parameters_required")
	}
	if strings.Contains(err.Error(), "   ") {
		t.Fatalf("required error leaked the raw internal value: %v", err)
	}
}

func TestExtractMultiParameterInternal_EmptyListSatisfiesRequired(t *testing.T) {
	ws := writeMultiClassifierWorkspace(t, "classifier")
	fileList, _ := writeMultiInputSet(t, 1)
	err := runExtractMulti(&multiSharedOptions{
		FileList:           fileList,
		OutputPath:         filepath.Join(t.TempDir(), "out"),
		InternalParameters: []string{"curated_prefixes=[]"},
		InputWorkers:       1,
	}, []string{ws}, io.Discard, time.Now())
	if err != nil {
		t.Fatalf("empty list --parameter-internal should satisfy parameters_required: %v", err)
	}
}

func TestExtractMultiParameterInternal_CollisionFailsPreflight(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "collides")
	fileList, _ := writeMultiInputSet(t, 1)
	err := runExtractMulti(&multiSharedOptions{
		FileList:           fileList,
		OutputPath:         filepath.Join(t.TempDir(), "out"),
		InternalParameters: []string{"name=hidden"},
		InputWorkers:       1,
	}, []string{ws}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("expected internal parameter collision with output_field to fail")
	}
	if !strings.Contains(err.Error(), "collides with field_mappings output_field") {
		t.Fatalf("collision error not surfaced: %v", err)
	}
	if strings.Contains(err.Error(), "hidden") {
		t.Fatalf("collision error leaked the internal value: %v", err)
	}
}

func TestExtractMultiParameterInternal_UnionsWithRecipeInternal(t *testing.T) {
	ws := writeMultiRecipeWorkspaceWithDefaults(t, "both-internal", `  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  parameters:
    private_marker: recipe-private
  parameters_internal:
    - private_marker
`)
	outputRoot := filepath.Join(t.TempDir(), "out")
	dirs, err := resolveRecipeOutputDirs(outputRoot, []string{"both-internal"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs: %v", err)
	}
	plan, err := loadRecipePlan(ws, &multiSharedOptions{
		FileList:           filepath.Join(t.TempDir(), "files.txt"),
		RunID:              multiParamSharedRunID,
		InternalParameters: []string{"curated_prefixes=[]"},
	}, dirs[0].Dir, io.Discard)
	if err != nil {
		t.Fatalf("loadRecipePlan: %v", err)
	}
	fields := plan.fieldPlan.build(nil)
	if _, ok := fields["private_marker"].(extract.InternalField); !ok {
		t.Fatalf("recipe-declared internal key was not preserved in union: %#v", fields["private_marker"])
	}
	if _, ok := fields["curated_prefixes"].(extract.InternalField); !ok {
		t.Fatalf("run-level internal key was not unioned into recipe plan: %#v", fields["curated_prefixes"])
	}
}

// TestExtractMultiParameter_CollisionFailsPreflight proves a shared --parameter
// key colliding with ANY recipe's field_mappings[].output_field invalidates the
// run at plan-load preflight — before any output session opens — even under
// --continue-on-error, and writes no payload or manifest.
func TestExtractMultiParameter_CollisionFailsPreflight(t *testing.T) {
	// writeMultiRecipeWorkspace declares output_field "name"; a --parameter name=...
	// collides with it.
	good := writeMultiRecipeWorkspace(t, "good")
	collides := writeMultiRecipeWorkspace(t, "collides")
	fileList, _ := writeMultiInputSet(t, 1)
	outRoot := filepath.Join(t.TempDir(), "out")

	shared := &multiSharedOptions{
		FileList:        fileList,
		OutputPath:      outRoot,
		ContinueOnError: true, // preflight failures stay hard even under continue-on-error
		Parameters:      []string{"name=injected"},
	}
	err := runExtractMulti(shared, []string{good, collides}, io.Discard, time.Now())
	if err == nil {
		t.Fatal("expected collision between --parameter and an output_field to fail the run, got nil")
	}
	if !strings.Contains(err.Error(), "collides with field_mappings output_field") {
		t.Fatalf("collision error not surfaced: %v", err)
	}

	// Preflight failed before output sessions opened: no recipe wrote a manifest or
	// records (the run aborted, it did not silently drop or scope the key).
	for _, id := range []string{"good", "collides"} {
		if _, statErr := os.Stat(filepath.Join(outRoot, id, "manifest.json")); statErr == nil {
			t.Errorf("recipe %q wrote a manifest despite the preflight collision failure", id)
		}
	}
}
