package commands

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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

func TestExecuteExtractRecipeInjectsManifestAndCLIParameters(t *testing.T) {
	initExtractManifestTestLogger(t)

	workspace := createRecipeParameterWorkspace(t)
	cmd := recipeRunExtractTestCommand()

	err := executeExtractRecipe(cmd, workspace, &recipeRunExtractOptions{
		ManifestPath: "recipe.yaml",
		ClientID:     "client-flag",
		SiteID:       "site-flag",
		Parameters:   []string{"region_id=east", "tenant_id=tenant-cli", "site_id=site-param"},
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
		"name":      "Alpha",
		"region_id": "east",
		"tenant_id": "tenant-cli",
		"client_id": "client-param-default",
		"site_id":   "site-param",
	}
	for key, value := range want {
		if data[key] != value {
			t.Fatalf("extract.data[%s] = %#v, want %#v (data: %#v)", key, data[key], value, data)
		}
	}
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
  parameters_required:
    - client_id
    - site_id
    - region_id
    - tenant_id
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
    client_id:
      type: string
    site_id:
      type: string
    region_id:
      type: string
    tenant_id:
      type: string
`)
	mustWriteFile(t, filepath.Join(workspace, "testdata", "input.xml"), `<root><item><name>Alpha</name></item></root>`)
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
