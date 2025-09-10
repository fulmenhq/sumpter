package commands

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/fulmenhq/sumpter/internal/assets"
	"github.com/spf13/cobra"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var docsCmd = &cobra.Command{
	Use:   "docs",
	Short: "Access embedded documentation",
	Long: `Access Sumpter's embedded documentation system.

Sumpter includes comprehensive documentation for all commands, schemas,
and user guides. Use this command to browse and view documentation
without leaving the terminal.`,
}

var docsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all available documentation",
	Long:  `List all embedded documentation files with their categories and sizes.`,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Sumpter Embedded Documentation")
		fmt.Println("==============================")
		fmt.Println()

		// Get embedded docs filesystem
		docsFS, err := assets.GetDocsFS()
		if err != nil {
			fmt.Printf("Error accessing embedded docs: %v\n", err)
			return
		}

		// Walk through embedded docs and categorize
		categories := make(map[string][]string)
		err = fs.WalkDir(docsFS, ".", func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			// Categorize by directory
			dir := filepath.Dir(path)
			if dir == "." {
				dir = "root"
			}
			categories[dir] = append(categories[dir], filepath.Base(path))
			return nil
		})

		if err != nil {
			fmt.Printf("Error walking embedded docs: %v\n", err)
			return
		}

		// Display categorized documentation
		for category, files := range categories {
			fmt.Printf("📚 %s:\n", cases.Title(language.English).String(category))
			for _, file := range files {
				fmt.Printf("  ├── %s\n", file)
			}
			fmt.Println()
		}

		fmt.Println("💡 Tip: Use 'sumpter docs show <path>' to view specific documentation")
		fmt.Println("   Example: sumpter docs show standards/application-environment")
	},
}

var docsShowCmd = &cobra.Command{
	Use:   "show <path>",
	Short: "Show a specific documentation file",
	Long: `Show the content of a specific documentation file by its path.

Examples:
  sumpter docs show standards/application-environment
  sumpter docs show sop/repository-operations-sop
  sumpter docs show sumpter_overview`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		path := args[0]

		// Get embedded docs filesystem
		docsFS, err := assets.GetDocsFS()
		if err != nil {
			fmt.Printf("Error accessing embedded docs: %v\n", err)
			return
		}

		// Try to read the file - first try as-is, then with docs/ prefix
		content, err := fs.ReadFile(docsFS, path)
		if err != nil {
			// Try with docs/ prefix
			content, err = fs.ReadFile(docsFS, "docs/"+path)
		}

		if err != nil {
			fmt.Printf("Documentation for '%s' not found in embedded docs.\n", path)
			fmt.Println()
			fmt.Println("Available documentation:")
			fmt.Println("  - standards/ (development standards)")
			fmt.Println("  - sop/ (standard operating procedures)")
			fmt.Println("  - user-guide/ (command documentation)")
			fmt.Println("  - examples/ (sample files)")
			fmt.Println()
			fmt.Println("Use 'sumpter docs list' to see all available files.")
			return
		}

		// Display the content
		fmt.Printf("📖 Sumpter Documentation: %s\n", path)
		fmt.Println(strings.Repeat("=", 50))
		fmt.Println()
		fmt.Print(string(content))
	},
}

func init() {
	docsCmd.AddCommand(docsListCmd)
	docsCmd.AddCommand(docsShowCmd)
	rootCmd.AddCommand(docsCmd)
}
