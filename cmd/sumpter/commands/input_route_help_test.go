package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestInputRouteCommandHelp(t *testing.T) {
	// A root help-only copy avoids executing or mutating the package-global
	// command, its flags or environment initialization.
	root := &cobra.Command{Use: rootCmd.Use, Short: rootCmd.Short, Long: rootCmd.Long}
	tests := []struct {
		name string
		cmd  *cobra.Command
		want []string
	}{
		{"root", root, []string{
			"Recipe-driven extraction from XML, JSON, and NDJSON, with route-specific streaming and indexed processing.",
			"Extract files    xml, json, ndjson", "Inspect          xml, json",
			"Record analysis  xml", "Record indexes   xml, json; uncompressed source", "Extract-multi    xml",
			"eligible selectors and outputs", "NDJSON is not an inspect or index input",
			"not the index store", "DOM and buffered outputs",
		}},
		{"extract", NewExtractCommand(), []string{"XML, JSON, or NDJSON", "format_type", "record-scoped", "output --format"}},
		{"extract files", newExtractFilesCommand(), []string{"XML, JSON, or NDJSON", "format_type", "match_scope: record", "select OUTPUT", "no whole-document or indexed route"}},
		{"recipes run extract", newRecipeRunExtractCommand(), []string{"defaults.input.format", "xml (default), json or ndjson", "format_type", "extract-multi remains XML only", "recipe's declared input format"}},
		{"inspect", NewInspectCommand(), []string{"XML or JSON", "NDJSON inspection", "JSON/NDJSON --analyze-records are refused", "--force-encoding is XML only", "--format selects the REPORT format"}},
		{"envinfo", NewEnvInfoCommand(), []string{"built-in input-route support", "record-scoped recipes", "NDJSON is not an", "inspect or index input", "omitted from JSON/export", "single-purpose subcommands", "no runtime capability probe"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			tt.cmd.SetOut(&out)
			if err := tt.cmd.Help(); err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(out.String(), want) {
					t.Errorf("help missing %q:\n%s", want, out.String())
				}
			}
			if strings.Contains(out.String(), "Directory of XML files") || strings.Contains(out.String(), "XML Streaming Engine") {
				t.Fatal("obsolete XML-only positioning")
			}
		})
	}
}

func TestInputRouteHelpPreservesFlagContracts(t *testing.T) {
	files := newExtractFilesCommand()
	recipe := newRecipeRunExtractCommand()
	for _, cmd := range []*cobra.Command{files, recipe} {
		if cmd.Flags().Lookup("input-format") != nil {
			t.Fatal("extraction gained an input-format flag")
		}
	}
	checks := []struct {
		cmd  *cobra.Command
		name string
		want string
	}{
		{files, "include-pattern", "*.xml"},
		{files, "format", "json"},
		{files, "workers", "1"},
		{recipe, "include-pattern", ""},
		{recipe, "format", ""},
		{NewInspectCommand(), "input-format", "xml"},
		{NewInspectCommand(), "analyze-records", "false"},
		{NewEnvInfoCommand(), "xml", "false"},
	}
	for _, check := range checks {
		flag := check.cmd.Flags().Lookup(check.name)
		if flag == nil || flag.DefValue != check.want {
			t.Errorf("%s --%s default changed: %v", check.cmd.Name(), check.name, flag)
		}
	}
}
