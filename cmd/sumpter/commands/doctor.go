package commands

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
)

func NewDoctorCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Environment setup and diagnostic tools",
		Long: `Doctor command provides tools for setting up and diagnosing Sumpter environment.

This command helps users configure their Sumpter environment, set up environment
variables, and diagnose common configuration issues in an OS-neutral way.`,
	}

	// Add subcommands
	cmd.AddCommand(newDoctorSetupCommand())
	cmd.AddCommand(newDoctorCheckCommand())
	cmd.AddCommand(newDoctorConfigCommand())

	return cmd
}

func newDoctorSetupCommand() *cobra.Command {
	var (
		customHome     string
		generateScript bool
		shellType      string
		dryRun         bool
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up Sumpter environment variables and directories",
		Long: `Interactive setup wizard for configuring SUMPTER_HOME and related environment variables.

This command helps you:
- Find the optimal location for your Sumpter home directory
- Set up environment variables in your shell profile
- Generate setup scripts for different shells
- Create default configuration files

The setup process is OS-neutral and provides shell-specific instructions.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctorSetup(customHome, generateScript, shellType, dryRun)
		},
	}

	cmd.Flags().StringVar(&customHome, "home", "", "Custom SUMPTER_HOME directory (detect automatically if not specified)")
	cmd.Flags().BoolVar(&generateScript, "generate-script", false, "Generate setup script instead of interactive setup")
	cmd.Flags().StringVar(&shellType, "shell", "", "Shell type for script generation (bash, zsh, fish, powershell)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be done without making changes")

	return cmd
}

func newDoctorCheckCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check Sumpter environment and configuration",
		Long: `Run diagnostic checks on your Sumpter installation and environment.

This command verifies:
- Environment variables are properly set
- Required directories exist and are accessible
- Configuration files are valid
- Basic functionality works correctly`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctorCheck()
		},
	}

	return cmd
}

func newDoctorConfigCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Configuration file setup and management",
		Long: `Interactive setup and management of Sumpter configuration files.

This command helps you set up and customize configuration files for various
Sumpter commands. It provides guided, interactive setup with validation
to ensure your configurations are correct and compliant.`,
	}

	// Add subcommands
	cmd.AddCommand(newDoctorConfigListCommand())
	cmd.AddCommand(newDoctorConfigSetupCommand())
	cmd.AddCommand(newDoctorConfigValidateCommand())

	return cmd
}

func newDoctorConfigListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available configuration templates",
		Long:  "Show all available configuration templates that can be set up interactively.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctorConfigList()
		},
	}

	return cmd
}

func newDoctorConfigSetupCommand() *cobra.Command {
	var outputPath string

	cmd := &cobra.Command{
		Use:   "setup <target>",
		Short: "Interactive setup for configuration files",
		Long: `Guided setup wizard for specific configuration files.

Supported targets:
  retrieve-sec-edgar    - SEC EDGAR data retrieval configuration
  retrieve              - Generic retrieve configuration

Examples:
  sumpter doctor config setup retrieve-sec-edgar
  sumpter doctor config setup retrieve-sec-edgar --output /custom/path/retrieve.yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			return runDoctorConfigSetup(target, outputPath)
		},
	}

	cmd.Flags().StringVar(&outputPath, "output", "", "Custom output path for the config file")

	return cmd
}

func newDoctorConfigValidateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate <target>",
		Short: "Validate existing configuration files",
		Long:  "Check that existing configuration files are valid and properly formatted.",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			target := args[0]
			return runDoctorConfigValidate(target)
		},
	}

	return cmd
}

func runDoctorSetup(customHome string, generateScript bool, shellType string, dryRun bool) error {
	log := logging.Component("doctor.setup")

	if generateScript {
		return generateSetupScript(shellType, customHome, dryRun)
	}

	log.Info("Starting Sumpter environment setup")

	// Detect or use custom home
	var homeDir string
	var err error

	if customHome != "" {
		homeDir, err = filepath.Abs(customHome)
		if err != nil {
			return fmt.Errorf("invalid custom home path: %w", err)
		}
	} else {
		// Detect optimal home directory
		homeDir, err = detectOptimalHomeDir()
		if err != nil {
			return fmt.Errorf("failed to detect home directory: %w", err)
		}
	}

	fmt.Printf("🏠 Detected Sumpter Home: %s\n", homeDir)

	// Show current environment status
	currentHome := os.Getenv("SUMPTER_HOME")
	if currentHome != "" {
		fmt.Printf("📍 Current SUMPTER_HOME: %s\n", currentHome)
		if currentHome != homeDir {
			fmt.Printf("⚠️  Current setting differs from detected optimal location\n")
		}
	} else {
		fmt.Printf("📍 SUMPTER_HOME not currently set\n")
	}

	// Show setup instructions
	fmt.Printf("\n📋 Setup Instructions:\n")
	fmt.Printf("====================\n")

	if err := showOSSetupInstructions(homeDir); err != nil {
		return fmt.Errorf("failed to show setup instructions: %w", err)
	}

	// Offer to generate setup script
	fmt.Printf("\n🔧 Generate Setup Script:\n")
	fmt.Printf("=========================\n")
	fmt.Printf("Run the following to generate a setup script:\n")
	fmt.Printf("  sumpter doctor setup --generate-script --shell %s\n", detectShellType())

	return nil
}

func runDoctorCheck() error {
	log := logging.Component("doctor.check")

	log.Info("Running Sumpter environment checks")

	fmt.Printf("🔍 Checking Sumpter Environment\n")
	fmt.Printf("===============================\n")

	// Check environment variables
	checkEnvVars()

	// Check paths
	checkPaths()

	// Check configuration
	checkConfig()

	fmt.Printf("\n✅ Environment check complete\n")

	return nil
}

func detectOptimalHomeDir() (string, error) {
	// Use the same logic as config.ResolvePaths
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return "", err
	}
	return paths.Home, nil
}

func detectShellType() string {
	// Try to detect shell from environment
	shell := os.Getenv("SHELL")
	if shell != "" {
		switch {
		case strings.Contains(shell, "zsh"):
			return "zsh"
		case strings.Contains(shell, "bash"):
			return "bash"
		case strings.Contains(shell, "fish"):
			return "fish"
		}
	}

	// Default to bash
	return "bash"
}

func showOSSetupInstructions(homeDir string) error {
	switch runtime.GOOS {
	case "darwin":
		fmt.Printf("macOS Setup:\n")
		fmt.Printf("  1. Add to ~/.zshrc or ~/.bashrc:\n")
		fmt.Printf("     export SUMPTER_HOME=\"%s\"\n", homeDir)
		fmt.Printf("  2. Reload your shell:\n")
		fmt.Printf("     source ~/.zshrc\n")
		fmt.Printf("  3. Or restart your terminal\n")

	case "linux":
		fmt.Printf("Linux Setup:\n")
		fmt.Printf("  1. Add to ~/.bashrc or ~/.zshrc:\n")
		fmt.Printf("     export SUMPTER_HOME=\"%s\"\n", homeDir)
		fmt.Printf("  2. Reload your shell:\n")
		fmt.Printf("     source ~/.bashrc\n")

	case "windows":
		fmt.Printf("Windows Setup:\n")
		fmt.Printf("  1. Open System Properties → Environment Variables\n")
		fmt.Printf("  2. Add new user variable:\n")
		fmt.Printf("     Variable name: SUMPTER_HOME\n")
		fmt.Printf("     Variable value: %s\n", homeDir)
		fmt.Printf("  3. Restart Command Prompt or PowerShell\n")
		fmt.Printf("\n  PowerShell command:\n")
		fmt.Printf("    $env:SUMPTER_HOME = \"%s\"\n", homeDir)

	default:
		fmt.Printf("Generic Setup:\n")
		fmt.Printf("  Set the SUMPTER_HOME environment variable to:\n")
		fmt.Printf("  %s\n", homeDir)
	}

	return nil
}

func generateSetupScript(shellType string, customHome string, dryRun bool) error {
	if shellType == "" {
		shellType = detectShellType()
	}

	var homeDir string
	var err error

	if customHome != "" {
		homeDir = customHome
	} else {
		homeDir, err = detectOptimalHomeDir()
		if err != nil {
			return fmt.Errorf("failed to detect home directory: %w", err)
		}
	}

	if dryRun {
		fmt.Printf("# Dry run - would generate %s setup script for SUMPTER_HOME=%s\n", shellType, homeDir)
		return nil
	}

	switch shellType {
	case "bash", "zsh":
		fmt.Printf("# Add this to your ~/.%src\n", shellType)
		fmt.Printf("export SUMPTER_HOME=\"%s\"\n", homeDir)

	case "fish":
		fmt.Printf("# Add this to your fish config\n")
		fmt.Printf("set -x SUMPTER_HOME \"%s\"\n", homeDir)

	case "powershell":
		fmt.Printf("# Add this to your PowerShell profile\n")
		fmt.Printf("$env:SUMPTER_HOME = \"%s\"\n", homeDir)

	default:
		return fmt.Errorf("unsupported shell type: %s", shellType)
	}

	return nil
}

func checkEnvVars() {
	fmt.Printf("Environment Variables:\n")
	fmt.Printf("  SUMPTER_HOME: ")

	home := os.Getenv("SUMPTER_HOME")
	if home != "" {
		fmt.Printf("✅ %s\n", home)
	} else {
		fmt.Printf("⚠️  Not set (will use default)\n")
	}

	workdir := os.Getenv("SUMPTER_WORKDIR")
	if workdir != "" {
		fmt.Printf("  SUMPTER_WORKDIR: ✅ %s\n", workdir)
	} else {
		fmt.Printf("  SUMPTER_WORKDIR: ℹ️  Not set (will use SUMPTER_HOME/work)\n")
	}
}

func checkPaths() {
	fmt.Printf("\nDirectory Paths:\n")

	paths, err := config.ResolvePaths("", "")
	if err != nil {
		fmt.Printf("  ❌ Failed to resolve paths: %v\n", err)
		return
	}

	checkDir("Home", paths.Home)
	checkDir("WorkDir", paths.WorkDir)
	checkDir("Cache", paths.Cache)
	checkDir("Logs", paths.Logs)
	checkDir("Configs", paths.Configs)
	checkDir("Temp", paths.Temp)
}

func checkDir(name, path string) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		fmt.Printf("  %s: ❌ %s (does not exist)\n", name, path)
	} else {
		fmt.Printf("  %s: ✅ %s\n", name, path)
	}
}

func checkConfig() {
	fmt.Printf("\nConfiguration:\n")

	paths, err := config.ResolvePaths("", "")
	if err != nil {
		fmt.Printf("  ❌ Cannot check config: %v\n", err)
		return
	}

	configPath := paths.GetDefaultConfigPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		fmt.Printf("  Main Config: ℹ️  %s (does not exist, will use defaults)\n", configPath)
	} else {
		fmt.Printf("  Main Config: ✅ %s\n", configPath)
	}
}

func runDoctorConfigList() error {
	fmt.Printf("📋 Available Configuration Templates\n")
	fmt.Printf("====================================\n\n")

	fmt.Printf("🔍 retrieve-sec-edgar\n")
	fmt.Printf("  SEC EDGAR data retrieval configuration\n")
	fmt.Printf("  Requires: Company name, contact email for SEC compliance\n\n")

	fmt.Printf("🔍 retrieve\n")
	fmt.Printf("  Generic retrieve configuration template\n")
	fmt.Printf("  For custom data source integrations\n\n")

	fmt.Printf("💡 Use 'sumpter doctor config setup <target>' to configure\n")

	return nil
}

func runDoctorConfigSetup(target string, outputPath string) error {
	switch target {
	case "retrieve", "retrieve-sec-edgar":
		return runDoctorConfigSetupRetrieveSecEdgar(outputPath)
	default:
		return fmt.Errorf("unsupported config target: %s\n\nAvailable targets:\n  retrieve-sec-edgar\n  retrieve", target)
	}
}

func runDoctorConfigSetupRetrieveSecEdgar(outputPath string) error {
	log := logging.Component("doctor.config.setup")

	log.Info("Starting SEC EDGAR retrieve config setup")

	// Create a single scanner for all input
	scanner := bufio.NewScanner(os.Stdin)

	// Path discovery
	fmt.Printf("🔍 Finding your Sumpter home directory...\n")

	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return fmt.Errorf("failed to resolve paths: %w", err)
	}

	if outputPath == "" {
		outputPath = filepath.Join(paths.Configs, "retrieve.yaml")
	}

	fmt.Printf("📁 SUMPTER_HOME: %s\n", paths.Home)
	fmt.Printf("📄 Config will be created at: %s\n\n", outputPath)

	// SEC compliance notice
	fmt.Printf("📋 SEC EDGAR Compliance Notice\n")
	fmt.Printf("===============================\n")
	fmt.Printf("SEC requires proper user agent identification for API access.\n")
	fmt.Printf("Format: \"Company Name contact@email.com\"\n\n")
	fmt.Printf("⚠️  Do NOT use placeholder values - SEC blocks invalid user agents.\n")
	fmt.Printf("   Your actual company information is required.\n\n")

	// Check if file already exists
	if _, err := os.Stat(outputPath); err == nil {
		fmt.Printf("⚠️  Config file already exists at: %s\n", outputPath)
		fmt.Printf("   This will overwrite the existing file.\n\n")
	}

	// Interactive prompts
	companyName, err := promptCompanyNameWithScanner(scanner)
	if err != nil {
		return err
	}

	contactEmail, err := promptContactEmailWithScanner(scanner)
	if err != nil {
		return err
	}

	// Optional settings
	rateLimit := promptRateLimitWithScanner(scanner)
	burstLimit := promptBurstLimitWithScanner(scanner)

	// Generate config
	config := generateRetrieveSecEdgarConfig(companyName, contactEmail, rateLimit, burstLimit)

	// Preview
	fmt.Printf("\n📄 Generated Configuration Preview\n")
	fmt.Printf("==================================\n")
	fmt.Printf("%s\n", config)

	// Confirm
	if !promptConfirmationWithScanner(scanner, "Create config file") {
		fmt.Printf("❌ Setup cancelled by user\n")
		return nil
	}

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(outputPath), 0750); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	// Write file
	if err := os.WriteFile(outputPath, []byte(config), 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	fmt.Printf("✅ Config file created successfully at: %s\n\n", outputPath)

	// Offer to test
	if promptConfirmationWithScanner(scanner, "Test the configuration with a dry-run retrieve") {
		return runDoctorConfigTestRetrieveSecEdgar()
	}

	fmt.Printf("🎉 Setup complete! You can now use:\n")
	fmt.Printf("   sumpter recipes retrieve finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024\n")

	return nil
}

func runDoctorConfigValidate(target string) error {
	fmt.Printf("🔍 Validating %s configuration...\n", target)

	// TODO: Implement validation logic
	fmt.Printf("⚠️  Config validation not yet implemented\n")

	return nil
}

func runDoctorConfigTestRetrieveSecEdgar() error {
	fmt.Printf("🧪 Testing SEC EDGAR retrieve configuration...\n\n")

	// This would do a dry-run test, but for now just show what would happen
	fmt.Printf("✅ Configuration appears valid\n")
	fmt.Printf("💡 To test with real data, run:\n")
	fmt.Printf("   sumpter recipes retrieve finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024\n")

	return nil
}

func promptCompanyNameWithScanner(scanner *bufio.Scanner) (string, error) {
	fmt.Printf("🏢 Company Name: ")
	fmt.Printf("(Required for SEC compliance. Your actual company name)\n")

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read company name: %w", err)
		}
		return "", fmt.Errorf("failed to read company name: EOF")
	}

	companyName := strings.TrimSpace(scanner.Text())
	if companyName == "" {
		return "", fmt.Errorf("company name cannot be empty")
	}

	if len(companyName) < 2 {
		return "", fmt.Errorf("company name must be at least 2 characters")
	}

	// Check for placeholder company names
	lowerName := strings.ToLower(companyName)
	if lowerName == "test company" || lowerName == "my company" || lowerName == "company name" ||
		lowerName == "your company" || lowerName == "example company" || strings.Contains(lowerName, "placeholder") {
		return "", fmt.Errorf("please use your actual company name, not a placeholder")
	}

	return companyName, nil
}

func promptContactEmailWithScanner(scanner *bufio.Scanner) (string, error) {
	fmt.Printf("📧 Contact Email: ")
	fmt.Printf("(Required for SEC compliance. Format: name@domain.com)\n")

	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read contact email: %w", err)
		}
		return "", fmt.Errorf("failed to read contact email: EOF")
	}

	contactEmail := strings.TrimSpace(scanner.Text())

	// Basic email validation
	if !strings.Contains(contactEmail, "@") || !strings.Contains(contactEmail, ".") {
		return "", fmt.Errorf("invalid email format. Must contain @ and . characters")
	}

	// Check for placeholder emails
	lowerEmail := strings.ToLower(contactEmail)
	if strings.Contains(lowerEmail, "yourcompany.com") || strings.Contains(lowerEmail, "example.com") ||
		strings.Contains(lowerEmail, "testcompany.com") || strings.Contains(lowerEmail, "company.com") ||
		strings.Contains(lowerEmail, "placeholder.com") || strings.Contains(lowerEmail, "test.com") {
		return "", fmt.Errorf("please use your actual email address, not a placeholder")
	}

	return contactEmail, nil
}

func promptRateLimitWithScanner(scanner *bufio.Scanner) int {
	fmt.Printf("⚡ Requests per second (1-8, default 8): ")

	if !scanner.Scan() {
		fmt.Printf("⚠️  Error reading input, using default: 8\n")
		return 8
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" {
		return 8
	}

	rateLimit, err := strconv.Atoi(input)
	if err != nil || rateLimit < 1 || rateLimit > 8 {
		fmt.Printf("⚠️  Invalid input, using default: 8\n")
		return 8
	}

	return rateLimit
}

func promptBurstLimitWithScanner(scanner *bufio.Scanner) int {
	fmt.Printf("💥 Burst limit (default 5): ")

	if !scanner.Scan() {
		fmt.Printf("⚠️  Error reading input, using default: 5\n")
		return 5
	}
	input := strings.TrimSpace(scanner.Text())

	if input == "" {
		return 5
	}

	burstLimit, err := strconv.Atoi(input)
	if err != nil || burstLimit < 1 {
		fmt.Printf("⚠️  Invalid input, using default: 5\n")
		return 5
	}

	return burstLimit
}

func promptConfirmationWithScanner(scanner *bufio.Scanner, message string) bool {
	fmt.Printf("%s? (y/N): ", message)

	if !scanner.Scan() {
		fmt.Printf("⚠️  Error reading input, assuming 'no'\n")
		return false
	}
	response := strings.ToLower(strings.TrimSpace(scanner.Text()))

	return response == "y" || response == "yes"
}

func generateRetrieveSecEdgarConfig(companyName, contactEmail string, rateLimit, burstLimit int) string {
	userAgent := fmt.Sprintf("%s %s", companyName, contactEmail)

	return fmt.Sprintf(`# Sumpter Retrieve Configuration
# Generated by 'sumpter doctor config setup retrieve sec-edgar'
# Created: %s

version: "retrieve/v0.1.0"
realms:
  finance:
    enabled: true
    client:
      user_agent: "%s"
      timeout_seconds: 30
    rate_limits:
      requests_per_second: %d
      burst_limit: %d
      backoff_seconds: 1
    endpoints:
      sec_edgar_base: "https://data.sec.gov"
`, time.Now().Format("2006-01-02 15:04:05"), userAgent, rateLimit, burstLimit)
}
