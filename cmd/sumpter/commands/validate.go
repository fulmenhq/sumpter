package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/validation"
	"github.com/spf13/cobra"
)

// validateCmd represents the validate command
var validateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate configuration files against schemas",
	Long: `Validate Sumpter configuration files against their JSON schemas.

This command validates configuration files to ensure they conform to the expected
schema structure and values. It supports validation of:

- Main configuration (sumpter.yaml)
- Logger configuration (logger.yaml) 
- PII configuration (pii.yaml)

Examples:
  sumpter validate                    # Validate all configs in default locations
  sumpter validate config.yaml       # Validate specific config file
  sumpter validate --dir ./configs   # Validate all configs in directory
  sumpter validate --json            # Output results in JSON format`,
	RunE: runValidate,
}

var (
	validateDir  string
	validateJSON bool
)

func init() {
	rootCmd.AddCommand(validateCmd)

	validateCmd.Flags().StringVarP(&validateDir, "dir", "d", "", "Directory containing config files to validate")
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "Output results in JSON format")
}

func runValidate(cmd *cobra.Command, args []string) error {
	// Get paths
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return fmt.Errorf("failed to get paths: %w", err)
	}

	// Create loader
	loader := config.NewLoader(paths)

	var results map[string]*validation.ValidationResult

	if len(args) > 0 {
		// Validate specific file
		configFile := args[0]
		if !filepath.IsAbs(configFile) {
			configFile = filepath.Join(paths.Home, configFile)
		}

		result, err := loader.ValidateConfigFile(configFile)
		if err != nil {
			return fmt.Errorf("failed to validate %s: %w", configFile, err)
		}

		results = map[string]*validation.ValidationResult{
			configFile: result,
		}
	} else if validateDir != "" {
		// Validate directory
		results, err = loader.ValidateConfigDirectory(validateDir)
		if err != nil {
			return fmt.Errorf("failed to validate directory %s: %w", validateDir, err)
		}
	} else {
		// Validate default config files
		results = make(map[string]*validation.ValidationResult)

		// Check main config
		mainConfigPath := paths.GetDefaultConfigPath()
		if _, statErr := os.Stat(mainConfigPath); statErr == nil {
			result, validateErr := loader.ValidateConfigFile(mainConfigPath)
			if validateErr != nil {
				return fmt.Errorf("failed to validate main config: %w", validateErr)
			}
			results[mainConfigPath] = result
		}

		// Check logger config
		loggerConfigPath := paths.GetLoggerConfigPath()
		if _, statErr := os.Stat(loggerConfigPath); statErr == nil {
			result, validateErr := loader.ValidateConfigFile(loggerConfigPath)
			if validateErr != nil {
				return fmt.Errorf("failed to validate logger config: %w", validateErr)
			}
			results[loggerConfigPath] = result
		}

		// Check PII config
		piiConfigPath := paths.GetPIIConfigPath()
		if _, statErr := os.Stat(piiConfigPath); statErr == nil {
			result, validateErr := loader.ValidateConfigFile(piiConfigPath)
			if validateErr != nil {
				return fmt.Errorf("failed to validate PII config: %w", validateErr)
			}
			results[piiConfigPath] = result
		}
	}

	// Output results
	if validateJSON {
		return outputJSONResults(cmd, results)
	}

	return outputTextResults(cmd, results)
}

func outputJSONResults(cmd *cobra.Command, results map[string]*validation.ValidationResult) error {
	// Convert to JSON-serializable format
	output := make(map[string]interface{})
	output["files"] = results

	// Add summary
	totalFiles := len(results)
	validFiles := 0
	totalErrors := 0

	for _, result := range results {
		if result.IsValid() {
			validFiles++
		} else {
			totalErrors += result.ErrorCount()
		}
	}

	output["summary"] = map[string]interface{}{
		"total_files":   totalFiles,
		"valid_files":   validFiles,
		"invalid_files": totalFiles - validFiles,
		"total_errors":  totalErrors,
	}

	// Marshal and output
	jsonData, err := json.MarshalIndent(output, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal results: %w", err)
	}

	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(jsonData))
	return err
}

func outputTextResults(cmd *cobra.Command, results map[string]*validation.ValidationResult) error {
	if len(results) == 0 {
		if _, err := fmt.Fprintln(cmd.OutOrStdout(), "No configuration files found to validate."); err != nil {
			return fmt.Errorf("failed to write output: %w", err)
		}
		return nil
	}

	// Summary
	totalFiles := len(results)
	validFiles := 0
	totalErrors := 0

	for _, result := range results {
		if result.IsValid() {
			validFiles++
		} else {
			totalErrors += result.ErrorCount()
		}
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Configuration Validation Results\n")
	fmt.Fprintf(cmd.OutOrStdout(), "==============================\n")
	fmt.Fprintf(cmd.OutOrStdout(), "Total files: %d\n", totalFiles)
	fmt.Fprintf(cmd.OutOrStdout(), "Valid files: %d\n", validFiles)
	fmt.Fprintf(cmd.OutOrStdout(), "Invalid files: %d\n", totalFiles-validFiles)
	fmt.Fprintf(cmd.OutOrStdout(), "Total errors: %d\n\n", totalErrors)

	// Individual file results
	for filePath, result := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "File: %s\n", filePath)
		if result.IsValid() {
			fmt.Fprintf(cmd.OutOrStdout(), "Status: ✅ Valid\n")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Status: ❌ Invalid (%d errors)\n", result.ErrorCount())
			for i, err := range result.Errors {
				fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s: %s", i+1, err.Path, err.Message)
				if err.Line > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), " (line %d)", err.Line)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\n")
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "\n")
	}

	// Overall result
	if totalErrors == 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "🎉 All configuration files are valid!\n")
		return nil
	} else {
		return fmt.Errorf("validation failed with %d errors across %d files", totalErrors, totalFiles-validFiles)
	}
}
