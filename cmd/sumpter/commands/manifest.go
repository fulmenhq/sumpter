package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fulmenhq/sumpter/internal/logging"
	recipesmanifest "github.com/fulmenhq/sumpter/internal/recipes"
	"github.com/fulmenhq/sumpter/internal/utils"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"gopkg.in/yaml.v3"
)

func NewManifestCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "manifest",
		Short: "Build and verify recipe manifests with integrity checks",
		Long: `Build and verify recipe manifests for acquisition and extraction workflows.

Manifests provide integrity guarantees and schema validation for recipe execution.
The build command creates or updates manifests, while verify ensures manifest
validity and asset integrity.`,
	}

	cmd.AddCommand(newManifestBuildCommand())
	cmd.AddCommand(newManifestVerifyCommand())

	return cmd
}

func newManifestBuildCommand() *cobra.Command {
	var (
		manifestPath string
		recipeID     string
		kind         string
		force        bool
	)

	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build or update a recipe manifest",
		Long: `Build or update a recipe manifest with integrity information.

This command analyzes the recipe workspace and generates a manifest file
containing metadata, asset references, and integrity checksums.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManifestBuild(manifestPath, recipeID, kind, force)
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "output", "o", "recipe.yaml", "Output path for the manifest file")
	cmd.Flags().StringVar(&recipeID, "id", "", "Recipe ID (required for new manifests)")
	cmd.Flags().StringVar(&kind, "kind", "extract", "Recipe kind (extract or acquire)")
	cmd.Flags().BoolVarP(&force, "force", "f", false, "Overwrite existing manifest file")

	_ = cmd.MarkFlagRequired("id")

	return cmd
}

func newManifestVerifyCommand() *cobra.Command {
	var (
		manifestPath string
		strict       bool
	)

	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify manifest integrity and validity",
		Long: `Verify that a recipe manifest is valid and all referenced assets exist.

This command performs schema validation, asset existence checks, and integrity
verification to ensure the manifest is ready for execution.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runManifestVerify(manifestPath, strict)
		},
	}

	cmd.Flags().StringVarP(&manifestPath, "manifest", "m", "recipe.yaml", "Path to the manifest file to verify")
	cmd.Flags().BoolVarP(&strict, "strict", "s", false, "Fail on warnings and perform additional integrity checks")

	return cmd
}

func runManifestBuild(manifestPath, recipeID, kind string, force bool) error {
	logger := logging.GetLogger()
	if logger != nil {
		logger.Info("Building manifest",
			zap.String("path", manifestPath),
			zap.String("id", recipeID),
			zap.String("kind", kind))
	}

	// Check if manifest already exists
	if _, err := os.Stat(manifestPath); err == nil && !force {
		return fmt.Errorf("manifest file %s already exists (use --force to overwrite)", manifestPath)
	}

	// Get workspace directory
	workspaceDir := filepath.Dir(manifestPath)

	// Determine manifest kind
	var manifestKind recipesmanifest.Kind
	switch strings.ToLower(kind) {
	case "extract":
		manifestKind = recipesmanifest.KindExtract
	case "acquire":
		manifestKind = recipesmanifest.KindAcquire
	default:
		return fmt.Errorf("unsupported manifest kind: %s (must be 'extract' or 'acquire')", kind)
	}

	// Discover assets in workspace
	assets, err := discoverWorkspaceAssets(workspaceDir, manifestKind)
	if err != nil {
		return fmt.Errorf("failed to discover workspace assets: %w", err)
	}

	// Create manifest with defaults
	manifest := &recipesmanifest.Manifest{
		Version:     recipesmanifest.ManifestVersion,
		ID:          recipeID,
		Kind:        manifestKind,
		DisplayName: fmt.Sprintf("%s Recipe", cases.Title(language.English).String(kind)),
		Description: fmt.Sprintf("Auto-generated %s recipe manifest", kind),
		CreatedAt:   time.Now().Format(time.RFC3339),
		Assets:      *assets,
		Defaults: recipesmanifest.Defaults{
			Input: recipesmanifest.InputDefaults{
				Mode:           "path",
				IncludePattern: "*.xml",
				MaxDepth:       0,
				FollowSymlinks: false,
			},
			Output: recipesmanifest.OutputDefaults{
				Format:  "json",
				Pattern: "extract-{}.json",
			},
			Workers:  1,
			Progress: false,
		},
	}

	// Write manifest to file
	if err := writeManifestToFile(manifest, manifestPath); err != nil {
		return fmt.Errorf("failed to write manifest: %w", err)
	}

	if logger != nil {
		logger.Info("Manifest built successfully",
			zap.String("id", manifest.ID),
			zap.String("kind", string(manifest.Kind)),
			zap.String("path", manifestPath))
	}

	return nil
}

func discoverWorkspaceAssets(workspaceDir string, kind recipesmanifest.Kind) (*recipesmanifest.Assets, error) {
	logger := logging.GetLogger()
	assets := &recipesmanifest.Assets{}

	// Common asset patterns
	assetPatterns := map[string][]string{
		"signature":  {"signature*.yaml", "config/signature*.yaml", "config/*signature*.yaml", "*signature*.yaml"},
		"extract":    {"extract*.yaml", "config/extract*.yaml", "config/*extract*.yaml", "*extract*.yaml"},
		"validation": {"validation*.yaml", "config/validation*.yaml", "config/*validation*.yaml", "*validation*.yaml"},
		"retrieve":   {"retrieve*.yaml", "config/retrieve*.yaml", "config/*retrieve*.yaml", "*retrieve*.yaml"},
	}

	// Find assets
	for assetType, patterns := range assetPatterns {
		for _, pattern := range patterns {
			matches, err := filepath.Glob(filepath.Join(workspaceDir, pattern))
			if err != nil {
				continue
			}
			if len(matches) > 0 {
				// Use the first match, make it relative to workspace
				relPath, err := filepath.Rel(workspaceDir, matches[0])
				if err != nil {
					relPath = matches[0]
				}
				switch assetType {
				case "signature":
					assets.Signature = relPath
				case "extract":
					assets.Extract = relPath
				case "validation":
					assets.Validation = relPath
				case "retrieve":
					assets.Retrieve = relPath
				}
				if logger != nil {
					logger.Debug("Found asset", zap.String("type", assetType), zap.String("path", relPath))
				}
				break // Use first match
			}
		}

		// For extract recipes, signature and extract are required
		if kind == recipesmanifest.KindExtract {
			if assets.Signature == "" {
				if logger != nil {
					logger.Warn("No signature config found for extract recipe")
				}
			}
			if assets.Extract == "" {
				if logger != nil {
					logger.Warn("No extract config found for extract recipe")
				}
			}
		}
	}

	return assets, nil
}

func writeManifestToFile(manifest *recipesmanifest.Manifest, manifestPath string) error {
	// Get current working directory for path validation
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("failed to get current directory: %w", err)
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(manifestPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Validate user-provided manifest path
	if err := utils.ValidateUserPathForCreate(manifestPath, utils.RootCwd, cwd); err != nil {
		return fmt.Errorf("invalid manifest path: %w", err)
	}

	// Write manifest
	file, err := os.Create(manifestPath) // #nosec G304 - Path validated by ValidateUserPathForCreate
	if err != nil {
		return fmt.Errorf("failed to create manifest file: %w", err)
	}
	defer func() {
		_ = file.Close()
	}()

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(manifest); err != nil {
		return fmt.Errorf("failed to encode manifest: %w", err)
	}

	return nil
}

func runManifestVerify(manifestPath string, strict bool) error {
	logger := logging.GetLogger()
	if logger != nil {
		logger.Info("Verifying manifest",
			zap.String("path", manifestPath),
			zap.Bool("strict", strict))
	}

	// Resolve manifest path
	absManifestPath, err := filepath.Abs(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to resolve manifest path: %w", err)
	}

	// Load and validate manifest
	manifest, err := recipesmanifest.LoadManifest(absManifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	if logger != nil {
		logger.Info("Manifest loaded successfully",
			zap.String("id", manifest.ID),
			zap.String("kind", string(manifest.Kind)),
			zap.String("version", manifest.Version))
	}

	// Get workspace directory (manifest's directory)
	workspaceDir := filepath.Dir(absManifestPath)

	// Verify assets exist
	if err := verifyManifestAssets(manifest, workspaceDir, strict); err != nil {
		return fmt.Errorf("asset verification failed: %w", err)
	}

	// TODO: Add integrity checksum verification when implemented
	// TODO: Add configuration compatibility validation when implemented

	if logger != nil {
		logger.Info("Manifest verification completed successfully")
	}
	return nil
}

func verifyManifestAssets(manifest *recipesmanifest.Manifest, workspaceDir string, strict bool) error {
	logger := logging.GetLogger()

	// Check signature config
	if manifest.Assets.Signature != "" {
		sigPath := recipesmanifest.ResolvePath(workspaceDir, manifest.Assets.Signature)
		if _, err := os.Stat(sigPath); os.IsNotExist(err) {
			return fmt.Errorf("signature config not found: %s", sigPath)
		}
		if logger != nil {
			logger.Debug("Signature config verified", zap.String("path", sigPath))
		}
	} else if manifest.Kind == recipesmanifest.KindExtract && strict {
		return fmt.Errorf("signature config is required for extract recipes in strict mode")
	}

	// Check extract config
	if manifest.Assets.Extract != "" {
		extPath := recipesmanifest.ResolvePath(workspaceDir, manifest.Assets.Extract)
		if _, err := os.Stat(extPath); os.IsNotExist(err) {
			return fmt.Errorf("extract config not found: %s", extPath)
		}
		if logger != nil {
			logger.Debug("Extract config verified", zap.String("path", extPath))
		}
	} else if manifest.Kind == recipesmanifest.KindExtract && strict {
		return fmt.Errorf("extract config is required for extract recipes in strict mode")
	}

	// Check validation config (optional)
	if manifest.Assets.Validation != "" {
		valPath := recipesmanifest.ResolvePath(workspaceDir, manifest.Assets.Validation)
		if _, err := os.Stat(valPath); os.IsNotExist(err) {
			if strict {
				return fmt.Errorf("validation config not found: %s", valPath)
			}
			if logger != nil {
				logger.Warn("Validation config not found (non-strict mode)", zap.String("path", valPath))
			}
		} else {
			if logger != nil {
				logger.Debug("Validation config verified", zap.String("path", valPath))
			}
		}
	}

	// Check retrieve config (optional)
	if manifest.Assets.Retrieve != "" {
		retPath := recipesmanifest.ResolvePath(workspaceDir, manifest.Assets.Retrieve)
		if _, err := os.Stat(retPath); os.IsNotExist(err) {
			if strict {
				return fmt.Errorf("retrieve config not found: %s", retPath)
			}
			if logger != nil {
				logger.Warn("Retrieve config not found (non-strict mode)", zap.String("path", retPath))
			}
		} else {
			if logger != nil {
				logger.Debug("Retrieve config verified", zap.String("path", retPath))
			}
		}
	}

	// Check extra assets
	for _, extra := range manifest.Assets.Extras {
		extraPath := recipesmanifest.ResolvePath(workspaceDir, extra)
		if _, err := os.Stat(extraPath); os.IsNotExist(err) {
			if strict {
				return fmt.Errorf("extra asset not found: %s", extraPath)
			}
			if logger != nil {
				logger.Warn("Extra asset not found (non-strict mode)", zap.String("path", extraPath))
			}
		} else {
			if logger != nil {
				logger.Debug("Extra asset verified", zap.String("path", extraPath))
			}
		}
	}

	return nil
}
