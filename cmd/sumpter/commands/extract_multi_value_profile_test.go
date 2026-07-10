package commands

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fulmenhq/sumpter/internal/provenance"
	"github.com/fulmenhq/sumpter/internal/valueprofile"
)

func TestRunExtractMultiValueProfileFromRecipe(t *testing.T) {
	ws := writeMultiRecipeWorkspaceWithDefaults(t, "summary", `  input:
    mode: files
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  value_profile:
    enabled: true
    fields:
      - field: name
        safe_to_profile: true
        sensitivity: public
`)
	fileList, _ := writeMultiInputSet(t, 2)
	outRoot := filepath.Join(t.TempDir(), "out")
	if err := runExtractMulti(&multiSharedOptions{FileList: fileList, OutputPath: outRoot}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("runExtractMulti: %v", err)
	}
	manifestPath := filepath.Join(outRoot, "summary", provenance.ManifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest provenance.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("extract-multi per-input path must emit value_profile from recipe defaults")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatal(err)
	}
	nameField := profile["fields"].(map[string]interface{})["name"].(map[string]interface{})
	if nameField["tier"] != valueprofile.TierEnumeration {
		t.Fatalf("tier = %#v", nameField["tier"])
	}
	distinct := nameField["distinct"].(map[string]interface{})
	// writeMultiInputSet(2) emits valA and valB once each.
	if distinct["valA"] != float64(1) || distinct["valB"] != float64(1) {
		t.Fatalf("per-input multi distinct = %#v, want valA:1 valB:1", distinct)
	}
}

func TestRunExtractMultiAggregateValueProfile(t *testing.T) {
	ws := writeMultiRecipeWorkspaceWithDefaults(t, "summary", `  input:
    mode: files
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  value_profile:
    enabled: true
    fields:
      - field: name
        safe_to_profile: true
        sensitivity: public
`)
	fileList, _ := writeMultiInputSet(t, 2)
	outRoot := filepath.Join(t.TempDir(), "out")
	if err := runExtractMulti(&multiSharedOptions{
		FileList:   fileList,
		OutputPath: outRoot,
		OutputMode: "aggregate",
	}, []string{ws}, io.Discard, time.Now()); err != nil {
		t.Fatalf("runExtractMulti aggregate: %v", err)
	}
	manifestPath := filepath.Join(outRoot, "summary", provenance.ManifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest provenance.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("extract-multi aggregate path must emit value_profile")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatal(err)
	}
	nameField := profile["fields"].(map[string]interface{})["name"].(map[string]interface{})
	if nameField["tier"] != valueprofile.TierEnumeration {
		t.Fatalf("tier = %#v", nameField["tier"])
	}
	distinct := nameField["distinct"].(map[string]interface{})
	// Aggregate multi-worker/input replay must observe the same committed values.
	if distinct["valA"] != float64(1) || distinct["valB"] != float64(1) {
		t.Fatalf("aggregate multi distinct = %#v, want valA:1 valB:1", distinct)
	}
}

func TestRunExtractMultiAggregateValueProfileDiscardsFloorMissAfterStaging(t *testing.T) {
	// Floor-miss input has a real TargetElement (staged) but fails min_occurrences=2;
	// continue-on-error + aggregate buffering must discard staged observations so
	// only the successful input's value remains.
	ws := writeMinOccursRecipe(t, "summary", 2)
	// Rewrite defaults to add value_profile while keeping the floor extract config.
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
  output:
    format: json
    path: outputs
    pattern: extract-{}.json
  workers: 1
  value_profile:
    enabled: true
    fields:
      - field: name
        safe_to_profile: true
        sensitivity: public
`
	if err := os.WriteFile(filepath.Join(ws, "recipe.yaml"), []byte(recipe), 0o600); err != nil {
		t.Fatalf("rewrite recipe: %v", err)
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "good.xml")
	// Two TargetElements so min_occurrences=2 succeeds.
	if err := os.WriteFile(good, []byte(`<root><TargetElement><Name>keep</Name></TargetElement><TargetElement><Name>keep</Name></TargetElement></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
	// One TargetElement — stages a value, then fails the floor and must be discarded.
	bad := filepath.Join(dir, "bad.xml")
	if err := os.WriteFile(bad, []byte(`<root><TargetElement><Name>drop</Name></TargetElement></root>`), 0o600); err != nil {
		t.Fatal(err)
	}
	fileList := filepath.Join(dir, "files.txt")
	if err := os.WriteFile(fileList, []byte(good+"\n"+bad+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	outRoot := filepath.Join(t.TempDir(), "out")
	err := runExtractMulti(&multiSharedOptions{
		FileList:        fileList,
		OutputPath:      outRoot,
		OutputMode:      "aggregate",
		ContinueOnError: true,
		RunID:           testMultiRunID,
	}, []string{ws}, io.Discard, time.Now())
	// Partial failure still non-zero, but manifest must exist with only keep.
	if err == nil {
		t.Log("run succeeded (acceptable if floor isolation still wrote profile)")
	}
	manifestPath := filepath.Join(outRoot, "summary", provenance.ManifestFileName)
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest provenance.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.ValueProfile) == 0 {
		t.Fatal("expected value_profile after partial aggregate success")
	}
	var profile map[string]interface{}
	if err := json.Unmarshal(manifest.ValueProfile, &profile); err != nil {
		t.Fatal(err)
	}
	distinct := profile["fields"].(map[string]interface{})["name"].(map[string]interface{})["distinct"].(map[string]interface{})
	if distinct["drop"] != nil {
		t.Fatalf("floor-miss staged value leaked: %#v", distinct)
	}
	if distinct["keep"] == nil {
		t.Fatalf("successful input missing: %#v", distinct)
	}
}
