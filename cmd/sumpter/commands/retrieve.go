package commands

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/utils"
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
	file, err := os.Open(path) // #nosec G304 - User-specified source path (top-level input)
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

	// Note: No path validation here because users explicitly specify where to write files
	// Users should be able to write to any location they have OS permissions for

	// Try to create the file (this will fail if file exists and is not writable)
	file, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600) // #nosec G304 - User-specified output file
	if err != nil {
		return fmt.Errorf("cannot create/write to file: %s: %w", filePath, err)
	}
	_ = file.Close()

	return nil
}

// ensureWritableTarget checks that path can be written without creating or
// truncating the target as a validation side effect.
func ensureWritableTarget(filePath string) error {
	if filePath == "" {
		return fmt.Errorf("file path cannot be empty")
	}

	parentDir := filepath.Dir(filePath)
	parentInfo, err := os.Stat(parentDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("parent directory does not exist: %s", parentDir)
		}
		return fmt.Errorf("cannot access parent directory: %s: %w", parentDir, err)
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("parent path is not a directory: %s", parentDir)
	}

	if info, err := os.Stat(filePath); err == nil {
		if info.IsDir() {
			return fmt.Errorf("output path is a directory: %s", filePath)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("output path is not a regular file: %s", filePath)
		}
		file, err := os.OpenFile(filePath, os.O_WRONLY, 0) // #nosec G304 - User-specified output path validated by caller.
		if err != nil {
			return fmt.Errorf("output file is not writable: %s: %w", filePath, err)
		}
		_ = file.Close()
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("cannot access output path: %s: %w", filePath, err)
	}

	tempFile, err := os.CreateTemp(parentDir, ".sumpter-find-write-*")
	if err != nil {
		return fmt.Errorf("parent directory is not writable: %s: %w", parentDir, err)
	}
	_ = tempFile.Close()
	_ = os.Remove(tempFile.Name())

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

	_ = cmd.MarkFlagRequired("input-path")

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
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current directory: %w", err)
		}
		if err := utils.ValidateUserPathWithRoot(outputPath, utils.RootCwd, cwd); err != nil {
			return fmt.Errorf("invalid output path: %w", err)
		}
		if err := ensureWritableTarget(outputPath); err != nil {
			return fmt.Errorf("output path validation failed: %w", err)
		}
	}

	// Convert includePattern to glob if needed
	if !strings.Contains(includePattern, "*") && !strings.Contains(includePattern, "?") {
		includePattern = "*" + includePattern + "*"
	}

	var output io.Writer = os.Stdout
	var outputFile *os.File
	if outputPath != "" {
		var err2 error
		outputFile, err2 = os.Create(outputPath) // #nosec G304 - Path validated above.
		if err2 != nil {
			return fmt.Errorf("failed to create output file: %w", err2)
		}
		defer func() {
			if outputFile != nil {
				_ = outputFile.Close()
			}
		}()
		output = outputFile
	}

	writer := bufio.NewWriter(output)

	type findMatch struct {
		Path string `json:"path"`
	}
	var matches []findMatch

	// Walk the directory tree
	if err := filepath.Walk(inputPath, func(currentPath string, info os.FileInfo, err error) error {
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
				matches = append(matches, findMatch{Path: line})
			} else {
				if _, err := fmt.Fprintln(writer, line); err != nil {
					return err
				}
			}
		}

		return nil
	}); err != nil {
		return err
	}

	if format == "json" {
		if err := json.NewEncoder(writer).Encode(matches); err != nil {
			return fmt.Errorf("failed to write JSON find results: %w", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush find results: %w", err)
	}
	if outputFile != nil {
		if err := outputFile.Close(); err != nil {
			return fmt.Errorf("failed to close output file: %w", err)
		}
		outputFile = nil
	}

	return nil
}
