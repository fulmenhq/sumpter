package commands

import (
	"io"
	"os"
	"path/filepath"
	"testing"
)

// writeMultiRecipeWorkspace creates a minimal, self-contained extract recipe
// workspace with the given manifest id. Input is intentionally minimal because
// an extract-multi run supplies a shared input set; the loader overrides it.
func writeMultiRecipeWorkspace(t *testing.T, id string) string {
	t.Helper()
	ws := t.TempDir()
	for _, d := range []string{"signature", "extract"} {
		if err := os.MkdirAll(filepath.Join(ws, d), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", d, err)
		}
	}
	write := func(rel, content string) {
		if err := os.WriteFile(filepath.Join(ws, rel), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
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
  workers: 1
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

func TestLoadRecipePlan_BuildsIsolatedPlans(t *testing.T) {
	wsA := writeMultiRecipeWorkspace(t, "summary")
	wsB := writeMultiRecipeWorkspace(t, "line-items")
	outputRoot := filepath.Join(t.TempDir(), "out")

	dirs, err := resolveRecipeOutputDirs(outputRoot, []string{"summary", "line-items"})
	if err != nil {
		t.Fatalf("resolveRecipeOutputDirs: %v", err)
	}

	shared := &multiSharedOptions{
		FileList: filepath.Join(t.TempDir(), "files.txt"),
		RunID:    "0190a3f4-1c2d-7abc-9def-0123456789ab", // one UUIDv7 run id shared across recipes
		Workers:  1,
	}

	planA, err := loadRecipePlan(wsA, shared, dirs[0].Dir, io.Discard)
	if err != nil {
		t.Fatalf("loadRecipePlan A: %v", err)
	}
	planB, err := loadRecipePlan(wsB, shared, dirs[1].Dir, io.Discard)
	if err != nil {
		t.Fatalf("loadRecipePlan B: %v", err)
	}

	// Identity + validated output destination per recipe.
	if planA.RecipeID != "summary" || planB.RecipeID != "line-items" {
		t.Errorf("recipe ids = %q,%q; want summary,line-items", planA.RecipeID, planB.RecipeID)
	}
	if planA.OutputDir != dirs[0].Dir || planB.OutputDir != dirs[1].Dir {
		t.Errorf("output dirs not bound to the validated preflight dirs")
	}
	if planA.opts.OutputPath != dirs[0].Dir {
		t.Errorf("plan A opts.OutputPath = %q, want validated dir %q", planA.opts.OutputPath, dirs[0].Dir)
	}

	// Isolation by construction: no shared mutable extract/signature/field state.
	if planA.extCfg == planB.extCfg {
		t.Error("plans share the same *ExtractRecordMatch pointer (extract config must be per-recipe)")
	}
	if planA.sigCfg == planB.sigCfg {
		t.Error("plans share the same *FileSignature pointer")
	}
	if planA.fieldPlan == planB.fieldPlan {
		t.Error("plans share the same external field plan")
	}
	if planA.opts == planB.opts {
		t.Error("plans share the same *ExtractOptions")
	}
	if planA.extCfg.RecordType != "summary_record" || planB.extCfg.RecordType != "line-items_record" {
		t.Errorf("record types not loaded per recipe: %q,%q", planA.extCfg.RecordType, planB.extCfg.RecordType)
	}

	// Each recipe's reference-table containment root is its OWN workspace,
	// never cross-wired.
	if planA.opts.ReferenceTableRoot != planA.Workspace || planB.opts.ReferenceTableRoot != planB.Workspace {
		t.Error("reference-table root is not bound per-recipe to its own workspace")
	}
	if planA.opts.ReferenceTableRoot == planB.opts.ReferenceTableRoot {
		t.Error("two recipes share a reference-table containment root")
	}

	// Shared input is identical across recipes (one input set, parsed once).
	if planA.opts.FileList != shared.FileList || planB.opts.FileList != shared.FileList {
		t.Error("shared input not applied identically to every plan")
	}
	// Shared run id ties per-recipe provenance to one invocation.
	if planA.runtimeProvenance.RunID != planB.runtimeProvenance.RunID {
		t.Errorf("plans have different run ids (%q vs %q); a multi run shares one",
			planA.runtimeProvenance.RunID, planB.runtimeProvenance.RunID)
	}
	if planA.warnLimiter == nil || planB.warnLimiter == nil {
		t.Error("warn limiter not initialized per plan")
	}
	// These fixtures declare no applicability asset, so the gate is absent.
	if planA.appCfg != nil || planB.appCfg != nil {
		t.Error("applicability config should be nil when the recipe declares none")
	}
}

func TestLoadRecipePlan_RequiresSharedOptions(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	if _, err := loadRecipePlan(ws, nil, filepath.Join(t.TempDir(), "summary"), io.Discard); err == nil {
		t.Fatal("expected error when shared options are nil, got nil")
	}
}

// testMultiRunID is a valid UUIDv7 used as the shared run id in loader tests.
const testMultiRunID = "0190a3f4-1c2d-7abc-9def-0123456789ab"

func TestLoadRecipePlan_RejectsEscapingApplicabilityAsset(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	// Repoint the applicability asset outside the workspace. The multi loader
	// must reject it with the same containment guard as the single-recipe path,
	// before the asset is read.
	recipe := `version: recipe/v0.1.0
kind: extract
id: summary
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
  applicability: ../outside.yaml
defaults:
  input:
    mode: files
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
`
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(recipe), 0o600); err != nil {
		t.Fatalf("rewrite recipe.yaml: %v", err)
	}
	shared := &multiSharedOptions{FileList: filepath.Join(t.TempDir(), "f.txt"), RunID: testMultiRunID}
	if _, err := loadRecipePlan(ws, shared, filepath.Join(t.TempDir(), "summary"), io.Discard); err == nil {
		t.Fatal("expected applicability asset escaping the workspace to be rejected, got nil")
	}
}

func TestLoadRecipePlan_ResolvesManifestInputPathAgainstWorkspace(t *testing.T) {
	ws := writeMultiRecipeWorkspace(t, "summary")
	// A relative defaults.input.path must be resolved against the recipe
	// workspace so source_extraction relative_path keeps a stable, workspace
	// -rooted root even when input is supplied via a shared --file-list.
	recipe := `version: recipe/v0.1.0
kind: extract
id: summary
content_version: "0.0.1"
assets:
  signature: signature/signature.yaml
  extract: extract/extract.yaml
defaults:
  input:
    mode: files
    path: data
    files:
      - testdata/input.xml
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
`
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(recipe), 0o600); err != nil {
		t.Fatalf("rewrite recipe.yaml: %v", err)
	}
	shared := &multiSharedOptions{FileList: filepath.Join(t.TempDir(), "f.txt"), RunID: testMultiRunID}
	plan, err := loadRecipePlan(ws, shared, filepath.Join(t.TempDir(), "summary"), io.Discard)
	if err != nil {
		t.Fatalf("loadRecipePlan: %v", err)
	}
	want := filepath.Join(plan.Workspace, "data")
	if plan.opts.SourceExtractionInput.Path != want {
		t.Errorf("SourceExtractionInput.Path = %q, want workspace-rooted %q", plan.opts.SourceExtractionInput.Path, want)
	}
}
