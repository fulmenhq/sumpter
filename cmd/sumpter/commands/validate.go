package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fulmenhq/sumpter/internal/artifactcontract"
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
	validateDir                  string
	validateJSON                 bool
	validateArtifactContractBase string
)

func init() {
	rootCmd.AddCommand(validateCmd)

	validateCmd.Flags().StringVarP(&validateDir, "dir", "d", "", "Directory containing config files to validate")
	validateCmd.Flags().BoolVar(&validateJSON, "json", false, "Output results in JSON format")
	validateCmd.AddCommand(validateArtifactDescriptorCmd)
	validateArtifactDescriptorCmd.Flags().StringVar(&validateArtifactContractBase, "contract-base", "", "Local directory containing contract.json and its relative entry schema")
	validateArtifactDescriptorCmd.Flags().BoolVar(&validateJSON, "json", false, "Output result in JSON format")
}

var validateArtifactDescriptorCmd = &cobra.Command{
	Use:   "artifact-descriptor <descriptor.json>",
	Short: "Validate a portable data artifact descriptor",
	Long: `Validate a portable data artifact descriptor against a host-less contract base.

The descriptor must carry the capability token "contract: data-artifact/v0".
The --contract-base directory must contain contract.json, whose capability must
match that token and whose entry_schema points to a schema file in the same
local contract base. Resolution fails closed on missing manifests, mismatched
capabilities, or missing/unsafe entry schemas.`,
	Args: cobra.ExactArgs(1),
	RunE: runValidateArtifactDescriptor,
}

func runValidateArtifactDescriptor(cmd *cobra.Command, args []string) error {
	resolved, err := artifactcontract.ResolveBaseline(validateArtifactContractBase)
	if err != nil {
		return err
	}
	descriptorBytes, err := os.ReadFile(args[0]) // #nosec G304 - explicit user-selected descriptor path
	if err != nil {
		return fmt.Errorf("read descriptor: %w", err)
	}
	result, err := artifactcontract.ValidateDescriptorBytes(resolved, descriptorBytes, args[0])
	if err != nil {
		return err
	}
	if validateJSON {
		payload := map[string]interface{}{
			"contract_baseline": map[string]string{
				"capability":             artifactcontract.DataArtifactCapability,
				"source":                 artifactcontract.BaselineSource,
				"released_tag":           artifactcontract.BaselineReleasedTag,
				"resolved_bundle_sha256": resolved.BundleSHA256,
				"expected_bundle_sha256": artifactcontract.BaselineBundleSHA256,
				"entry_schema":           resolved.EntrySchema,
			},
			"result": result,
		}
		out, marshalErr := json.MarshalIndent(payload, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("marshal artifact descriptor validation result: %w", marshalErr)
		}
		if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), string(out)); writeErr != nil {
			return fmt.Errorf("write artifact descriptor validation result: %w", writeErr)
		}
	} else {
		if _, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "Artifact descriptor: %s\n", result.File); writeErr != nil {
			return fmt.Errorf("write artifact descriptor validation result: %w", writeErr)
		}
		if _, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "Contract: %s (%s)\n", artifactcontract.DataArtifactCapability, resolved.BundleSHA256); writeErr != nil {
			return fmt.Errorf("write artifact descriptor validation result: %w", writeErr)
		}
		if result.Valid {
			if _, writeErr := fmt.Fprintln(cmd.OutOrStdout(), "Status: valid"); writeErr != nil {
				return fmt.Errorf("write artifact descriptor validation result: %w", writeErr)
			}
		} else {
			if _, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "Status: invalid (%d errors)\n", len(result.Errors)); writeErr != nil {
				return fmt.Errorf("write artifact descriptor validation result: %w", writeErr)
			}
			for i, validationErr := range result.Errors {
				if _, writeErr := fmt.Fprintf(cmd.OutOrStdout(), "  %d. %s: %s\n", i+1, validationErr.Path, validationErr.Message); writeErr != nil {
					return fmt.Errorf("write artifact descriptor validation error: %w", writeErr)
				}
			}
		}
	}
	if !result.Valid {
		return fmt.Errorf("artifact descriptor validation failed with %d errors", len(result.Errors))
	}
	return nil
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

	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Configuration Validation Results\n"); err != nil {
		return fmt.Errorf("failed to write header: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "==============================\n"); err != nil {
		return fmt.Errorf("failed to write separator: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Total files: %d\n", totalFiles); err != nil {
		return fmt.Errorf("failed to write total files: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Valid files: %d\n", validFiles); err != nil {
		return fmt.Errorf("failed to write valid files: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Invalid files: %d\n", totalFiles-validFiles); err != nil {
		return fmt.Errorf("failed to write invalid files: %w", err)
	}
	if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Total errors: %d\n\n", totalErrors); err != nil {
		return fmt.Errorf("failed to write total errors: %w", err)
	}

	// Individual file results
	for filePath, result := range results {
		if err := writeFprintf(cmd.OutOrStdout(), "File: %s\n", filePath); err != nil {
			return fmt.Errorf("failed to write file path: %w", err)
		}
		if result.IsValid() {
			if err := writeFprintf(cmd.OutOrStdout(), "Status: ✅ Valid\n"); err != nil {
				return fmt.Errorf("failed to write valid status: %w", err)
			}
		} else {
			if err := writeFprintf(cmd.OutOrStdout(), "Status: ❌ Invalid (%d errors)\n", result.ErrorCount()); err != nil {
				return fmt.Errorf("failed to write invalid status: %w", err)
			}
			for i, err := range result.Errors {
				if writeErr := writeFprintf(cmd.OutOrStdout(), "  %d. %s: %s", i+1, err.Path, err.Message); writeErr != nil {
					return fmt.Errorf("failed to write error details: %w", writeErr)
				}
				if err.Line > 0 {
					if writeErr := writeFprintf(cmd.OutOrStdout(), " (line %d)", err.Line); writeErr != nil {
						return fmt.Errorf("failed to write line number: %w", writeErr)
					}
				}
				if writeErr := writeFprintf(cmd.OutOrStdout(), "\n"); writeErr != nil {
					return fmt.Errorf("failed to write newline: %w", writeErr)
				}
			}
		}
		if err := writeFprintf(cmd.OutOrStdout(), "\n"); err != nil {
			return fmt.Errorf("failed to write separator: %w", err)
		}
	}

	// Overall result
	if totalErrors == 0 {
		if err := writeFprintf(cmd.OutOrStdout(), "🎉 All configuration files are valid!\n"); err != nil {
			return fmt.Errorf("failed to write success message: %w", err)
		}
		return nil
	} else {
		return fmt.Errorf("validation failed with %d errors across %d files", totalErrors, totalFiles-validFiles)
	}
}
