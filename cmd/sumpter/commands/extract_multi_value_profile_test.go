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
	if len(distinct) == 0 {
		t.Fatalf("expected observed name values, got %#v", distinct)
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
}
