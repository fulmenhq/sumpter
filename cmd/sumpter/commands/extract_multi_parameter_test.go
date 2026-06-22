package commands

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
