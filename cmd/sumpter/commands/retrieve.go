package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/sourcedata/finance"
	"github.com/spf13/cobra"
)

// validateReadablePath checks if a path exists and is readable
func validateReadablePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	_, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %s", path)
		}
		return fmt.Errorf("cannot access path: %s: %w", path, err)
	}

	// Check if readable
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("path is not readable: %s: %w", path, err)
	}
	_ = file.Close()

	return nil
}

// validateReadableDir checks if a directory exists and is readable
func validateReadableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("directory path cannot be empty")
	}

	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("directory does not exist: %s", dir)
		}
		return fmt.Errorf("cannot access directory: %s: %w", dir, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", dir)
	}

	// Check if readable by trying to read directory contents
	_, err = os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("directory is not readable: %s: %w", dir, err)
	}

	return nil
}

// validateWritableDir checks if a directory is writable (creates it if it doesn't exist)
func validateWritableDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("directory path cannot be empty")
	}

	// Try to create the directory (with parents if needed)
	err := os.MkdirAll(dir, 0750)
	if err != nil {
		return fmt.Errorf("cannot create directory: %s: %w", dir, err)
	}

	// Test if directory is actually writable by creating a temp file
	tempFile := filepath.Join(dir, ".sumpter-test-write")
	err = os.WriteFile(tempFile, []byte("test"), 0600)
	if err != nil {
		return fmt.Errorf("directory is not writable: %s: %w", dir, err)
	}

	// Clean up temp file
	_ = os.Remove(tempFile)

	return nil
}

// validateWritableFile checks if a file can be created/written to
func validateWritableFile(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	// Get directory part
	dir := filepath.Dir(filePath)

	// Validate the directory is writable
	err := validateWritableDir(dir)
	if err != nil {
		return fmt.Errorf("cannot write to file (directory issue): %s: %w", filePath, err)
	}

	// Try to create the file (this will fail if file exists and is not writable)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("cannot create/write to file: %s: %w", filePath, err)
	}
	_ = file.Close()

	return nil
}

type RetrieveOptions struct {
	OutputBase string
	Flatten    bool
	ConfigPath string
	cmd        *cobra.Command // Reference to command for flag access
}

func NewRetrieveCommand() *cobra.Command {
	opts := &RetrieveOptions{}

	cmd := &cobra.Command{
		Use:   "retrieve",
		Short: "Acquire and organize data from various sources",
		Long: `Acquire data through APIs, web scraping, or file system operations and organize it for processing.

Supports recipe-driven acquisitions, deep directory traversal, and cloud storage integration.`,
	}

	// Resolve paths to get the default work directory
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		// Fallback to relative path if path resolution fails
		cmd.PersistentFlags().StringVar(&opts.OutputBase, "output-base", ".scratchpad/data", "Base output directory for acquired data")
	} else {
		cmd.PersistentFlags().StringVar(&opts.OutputBase, "output-base", paths.WorkDir, "Base output directory for acquired data")
	}
	cmd.PersistentFlags().StringVar(&opts.ConfigPath, "config-path", "", "Path to retrieve configuration file (default: SUMPTER_HOME/configs/retrieve.yaml)")

	// Store command reference for flag access
	opts.cmd = cmd

	// Add core operation subcommands
	cmd.AddCommand(newCopyCommand(opts))
	cmd.AddCommand(newFindCommand(opts))
	cmd.AddCommand(newRecipeCommand(opts))

	return cmd
}

func newCopyCommand(opts *RetrieveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "copy <source> <destination>",
		Short: "Copy data from source to destination",
		Long: `Copy data from various sources to destinations.

Sources can be:
- API endpoints (e.g., sec-edgar://AAPL/10-K/2024)
- File paths (e.g., /path/to/files/*.xml)
- Recipe references (e.g., recipe://finance/sec-edgar)

Destinations can be:
- Local paths
- Cloud storage URIs (future)`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			source := args[0]
			destination := args[1]
			return runCopy(opts, source, destination)
		},
	}

	return cmd
}

func newFindCommand(opts *RetrieveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "find",
		Short: "Find files recursively with pattern matching",
		Long: `Recursively find files matching patterns in directory trees.

Useful for discovering data files in complex directory structures.
Outputs file paths that can be used with other commands.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			flatten, _ := cmd.Flags().GetBool("flatten")
			opts.Flatten = flatten
			inputPath, _ := cmd.Flags().GetString("input-path")
			includePattern, _ := cmd.Flags().GetString("include-pattern")
			excludePattern, _ := cmd.Flags().GetString("exclude-pattern")
			maxDepth, _ := cmd.Flags().GetInt("max-depth")
			followSymlinks, _ := cmd.Flags().GetBool("follow-symlinks")
			format, _ := cmd.Flags().GetString("format")
			outputPath, _ := cmd.Flags().GetString("output-path")
			progress, _ := cmd.Flags().GetBool("progress")
			return runFind(opts, inputPath, includePattern, excludePattern, maxDepth, followSymlinks, format, outputPath, progress)
		},
	}

	cmd.Flags().String("input-path", "", "Input path to search (directory)")
	cmd.Flags().String("include-pattern", "*", "File inclusion pattern")
	cmd.Flags().String("exclude-pattern", "", "File exclusion pattern")
	cmd.Flags().Int("max-depth", 0, "Maximum directory depth to search (0 = unlimited)")
	cmd.Flags().Bool("follow-symlinks", false, "Follow symbolic links")
	cmd.Flags().StringP("format", "f", "text", "Output format: text, json")
	cmd.Flags().StringP("output-path", "o", "", "Output file path (stdout if not specified)")
	cmd.Flags().BoolP("progress", "p", false, "Show progress indicators")
	cmd.Flags().Bool("flatten", false, "Output flattened relative paths instead of absolute paths")

	cmd.MarkFlagRequired("input-path")

	return cmd
}

func newRecipeCommand(opts *RetrieveOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recipe <realm> <domain-tag>",
		Short: "Run a data acquisition recipe for a specific realm and domain",
		Long: `Execute data acquisition recipes for specific realms and domains.

Supported realms: finance
Supported domain-tags: sec-edgar

Configuration is loaded from retrieve.yaml (use --config-path to override).
For finance realm, configure user_agent for SEC compliance.

Example: sumpter retrieve recipe finance sec-edgar --ticker AAPL --filing-type 10-K --year 2024`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			realm := args[0]
			domainTag := args[1]

			// Validate realm is supported
			if !config.IsValidRealm(realm) {
				return fmt.Errorf("unsupported realm: %s (supported realms: %v)", realm, config.ValidRealms())
			}

			return runRecipe(opts, cmd, realm, domainTag)
		},
	}

	// Add flags for recipe options
	cmd.Flags().String("ticker", "", "Stock ticker symbol (for finance/sec-edgar)")
	cmd.Flags().String("filing-type", "", "Filing type (e.g., 10-K, 10-Q) (for finance/sec-edgar)")
	cmd.Flags().String("year", "", "Filing year (for finance/sec-edgar)")

	cmd.MarkFlagRequired("ticker")
	cmd.MarkFlagRequired("filing-type")
	cmd.MarkFlagRequired("year")

	return cmd
}

func runCopy(opts *RetrieveOptions, source, destination string) error {
	// Validate source path (could be file or directory)
	if err := validateReadablePath(source); err != nil {
		return fmt.Errorf("source path validation failed: %w", err)
	}

	// Validate destination (check if it's a directory or file)
	destInfo, err := os.Stat(destination)
	if err != nil {
		if os.IsNotExist(err) {
			// Destination doesn't exist, check if parent directory is writable
			parentDir := filepath.Dir(destination)
			if err := validateWritableDir(parentDir); err != nil {
				return fmt.Errorf("destination parent directory validation failed: %w", err)
			}
		} else {
			return fmt.Errorf("cannot access destination: %s: %w", destination, err)
		}
	} else {
		// Destination exists
		if destInfo.IsDir() {
			// If destination is a directory, check if it's writable
			if err := validateWritableDir(destination); err != nil {
				return fmt.Errorf("destination directory validation failed: %w", err)
			}
		} else {
			// If destination is a file, check if it's writable
			if err := validateWritableFile(destination); err != nil {
				return fmt.Errorf("destination file validation failed: %w", err)
			}
		}
	}

	// TODO: Implement copy logic for different source types
	// For now, just handle basic file operations
	fmt.Printf("Copying from %s to %s\n", source, destination)
	return fmt.Errorf("copy command not yet implemented")
}

func runFind(opts *RetrieveOptions, inputPath, includePattern, excludePattern string, maxDepth int, followSymlinks bool, format, outputPath string, progress bool) error {
	// Expand ~ in inputPath
	if strings.HasPrefix(inputPath, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		inputPath = filepath.Join(home, inputPath[1:])
	}

	// Validate input directory
	if err := validateReadableDir(inputPath); err != nil {
		return fmt.Errorf("input path validation failed: %w", err)
	}

	// Validate output file if specified
	if outputPath != "" {
		if err := validateWritableFile(outputPath); err != nil {
			return fmt.Errorf("output path validation failed: %w", err)
		}
	}

	// Convert includePattern to glob if needed
	if !strings.Contains(includePattern, "*") && !strings.Contains(includePattern, "?") {
		includePattern = "*" + includePattern + "*"
	}

	var output *os.File = os.Stdout
	if outputPath != "" {
		var err error
		output, err = os.Create(outputPath)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer output.Close()
	}

	// Walk the directory tree
	return filepath.Walk(inputPath, func(currentPath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Check symlink
		if !followSymlinks && info.Mode()&os.ModeSymlink != 0 {
			return nil
		}

		if info.IsDir() {
			// Check depth
			rel, err := filepath.Rel(inputPath, currentPath)
			if err != nil {
				return err
			}
			depth := strings.Count(rel, string(filepath.Separator))
			if maxDepth > 0 && depth > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		// Check exclude pattern
		if excludePattern != "" {
			excluded, err := filepath.Match(excludePattern, info.Name())
			if err != nil {
				return err
			}
			if excluded {
				return nil
			}
		}

		// Check if filename matches include pattern
		filename := filepath.Base(currentPath)
		matched, err := filepath.Match(includePattern, filename)
		if err != nil {
			return err
		}

		if matched {
			var line string
			if opts.Flatten {
				// Output relative path from search root
				relPath, err := filepath.Rel(inputPath, currentPath)
				if err != nil {
					return err
				}
				line = relPath
			} else {
				// Output absolute path
				line = currentPath
			}

			if format == "json" {
				// Output as JSON
				fmt.Fprintf(output, "{\"path\": %q}\n", line)
			} else {
				fmt.Fprintln(output, line)
			}
		}

		return nil
	})
}

func runRecipe(opts *RetrieveOptions, cmd *cobra.Command, realm, domainTag string) error {
	switch realm {
	case "finance":
		return runFinanceRecipe(opts, cmd, domainTag)
	default:
		return fmt.Errorf("unsupported realm: %s", realm)
	}
}

func runFinanceRecipe(opts *RetrieveOptions, cmd *cobra.Command, domainTag string) error {
	switch domainTag {
	case "sec-edgar":
		return runSecEdgarRecipe(opts, cmd)
	default:
		return fmt.Errorf("unsupported finance domain-tag: %s", domainTag)
	}
}

func runSecEdgarRecipe(opts *RetrieveOptions, cmd *cobra.Command) error {
	// Validate output directory
	if err := validateWritableDir(opts.OutputBase); err != nil {
		return fmt.Errorf("output directory validation failed: %w", err)
	}

	// Get flags
	ticker, _ := cmd.Flags().GetString("ticker")
	filingType, _ := cmd.Flags().GetString("filing-type")
	year, _ := cmd.Flags().GetString("year")

	// Load retrieve config
	paths, err := config.ResolvePaths("", "")
	if err != nil {
		return fmt.Errorf("failed to resolve paths: %w", err)
	}

	loader := config.NewLoader(paths)
	sourceConfig, err := loader.LoadRetrieveConfig(opts.ConfigPath)
	if err != nil {
		return fmt.Errorf("failed to load retrieve config: %w", err)
	}

	// Get finance realm config
	realmConfig, exists := sourceConfig.Realms["finance"]
	if !exists {
		return fmt.Errorf("finance realm not configured in retrieve config")
	}

	// Validate required user agent
	if realmConfig.Client.UserAgent == "" {
		return fmt.Errorf("user_agent is required in finance realm config for SEC compliance (set in retrieve.yaml)")
	}

	client := finance.NewSecEdgarClient(realmConfig.Client.UserAgent, realmConfig.RateLimits.RequestsPerSecond)
	defer client.Close()

	return client.DownloadFiling(ticker, filingType, year, opts.OutputBase)
}
