package commands

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/fulmenhq/sumpter/internal/config"
	"github.com/fulmenhq/sumpter/internal/logging"
	"github.com/spf13/cobra"
	"go.uber.org/zap"
)

// DefaultVersion is the fallback version when VERSION file is not found
const DefaultVersion = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "sumpter",
	Short: "Sumpter - XML Streaming Engine",
	Long: `Sumpter (XML Streaming Engine) - High-performance XML processing for enterprise data.

Sumpter is a Go-based streaming XML engine that transforms massive, malformed,
and variant-heavy XML into clean, analytics-ready tables. With sub-second inspection,
auto-generated extraction configs, and resilient outputs to JSON, NDJSON, or Parquet,
Sumpter helps teams start fast and thrive on scale.

Built for: Enterprise XML processing, data transformation, and analytics pipelines.

Inspired by the Fulmen ecosystem and the American West's "sumpter" horses.`,
	Version:          getVersionFromBuild(),
	PersistentPreRun: initializeEnvironment,
	SilenceUsage:     true,
}

// Execute runs the root command under a cancelable context bound to SIGINT/SIGTERM.
// That context is the production source of process-run "canceled" terminals for
// extract-multi (Cobra cmd.Context()), without introducing a control socket (C4).
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rootCmd.ExecuteContext(ctx)
}

func init() {
	// Add persistent flags
	rootCmd.PersistentFlags().String("log-level", "info", "Log level: debug|info|warn|error")
	rootCmd.PersistentFlags().String("log-format", "console", "Log format: console|json")
	rootCmd.PersistentFlags().Bool("allow-large-files", false, "Allow processing of very large XML files (>1GB)")

	// Application environment flags
	rootCmd.PersistentFlags().String("home", "", "Override SUMPTER_HOME directory")
	rootCmd.PersistentFlags().String("workdir", "", "Override SUMPTER_WORKDIR directory")
	rootCmd.PersistentFlags().String("config", "", "Path to config file (default: configs/sumpter.yaml)")

	// Logging flags
	rootCmd.PersistentFlags().String("log-file", "", "Path to log file (default: logs/sumpter.log)")
	rootCmd.PersistentFlags().Bool("log-color", true, "Enable colored log output")
	rootCmd.PersistentFlags().Bool("log-telemetry", false, "Enable telemetry logging in JSON format")

	// Add commands here
	rootCmd.AddCommand(NewVersionCommand())
	rootCmd.AddCommand(NewEnvInfoCommand())
	rootCmd.AddCommand(NewInspectCommand())
	rootCmd.AddCommand(NewExtractCommand())
	rootCmd.AddCommand(NewRetrieveCommand())
	rootCmd.AddCommand(NewDoctorCommand())
	rootCmd.AddCommand(NewRecipesCommand())
	rootCmd.AddCommand(NewManifestCommand())
	rootCmd.AddCommand(NewIndexCommand())
}

// initializeEnvironment sets up the Sumpter environment
func initializeEnvironment(cmd *cobra.Command, args []string) {
	// Get flag values
	homeOverride, _ := cmd.Flags().GetString("home")
	workdirOverride, _ := cmd.Flags().GetString("workdir")
	configPath, _ := cmd.Flags().GetString("config")
	logLevel, _ := cmd.Flags().GetString("log-level")
	logFormat, _ := cmd.Flags().GetString("log-format")
	logFile, _ := cmd.Flags().GetString("log-file")
	logColor, _ := cmd.Flags().GetBool("log-color")
	logTelemetry, _ := cmd.Flags().GetBool("log-telemetry")

	// Resolve application paths
	paths, err := config.ResolvePaths(homeOverride, workdirOverride)
	if err != nil {
		log.Fatalf("Failed to resolve application paths: %v", err)
	}

	// Create config loader
	loader := config.NewLoader(paths)

	// Load main configuration
	mainCfg, err := loader.LoadMainConfig()
	if err != nil {
		log.Printf("Failed to load main config, using defaults: %v", err)
		// Create default config manually
		mainCfg = &config.MainConfig{
			Version: "config/v0.1.0",
			Logging: config.LoggerConfig{
				Version:   "logger-config/v0.1.0",
				Level:     "info",
				Format:    "pretty",
				UseColor:  true,
				Component: "sumpter",
			},
			PII: config.PIIConfig{
				Version:  "pii-config/v0.1.0",
				Mode:     "safe",
				SafeOnly: true,
			},
			Paths: config.PathsConfig{
				CacheDir:  "cache",
				TempDir:   "temp",
				OutputDir: "output",
			},
			Performance: config.PerformanceConfig{
				MaxMemoryMB:    512,
				BufferSizeKB:   64,
				WorkerCount:    4,
				TimeoutSeconds: 300,
			},
			Telemetry: config.TelemetryConfig{
				Enabled:              false,
				ServiceName:          "sumpter",
				ServiceVersion:       "dev",
				Environment:          "development",
				BatchSize:            100,
				FlushIntervalSeconds: 30,
			},
		}
	}

	// Override config with command-line flags
	resolvedLogFile := resolveLogFilePath(logFile, paths)
	mainCfg.Logging.Level = logLevel
	mainCfg.Logging.Format = logFormat
	mainCfg.Logging.UseColor = logColor
	mainCfg.Logging.File.Enabled = logFile != ""
	if logFile != "" {
		mainCfg.Logging.File.Path = resolvedLogFile
	}
	mainCfg.Logging.Telemetry.Enabled = logTelemetry

	// Convert to logging config
	logCfg := logging.Config{
		Level:           logging.LogLevel(logLevel),
		UseColor:        logColor,
		Component:       "sumpter",
		PIIMode:         logging.PIIModeSafe,
		PIISafeOnly:     true,
		LogFile:         resolvedLogFile,
		EnableTelemetry: logTelemetry,
		ServiceName:     "sumpter",
		ServiceVersion:  "dev",
		Environment:     "development",
		LogRotation: logging.LogRotationConfig{
			Enabled:    true,
			MaxSizeMB:  10,
			MaxAgeDays: 30,
			MaxBackups: 5,
			Compress:   true,
			TimeFormat: "2006-01-02",
		},
	}

	// Initialize logger
	if err := logging.Initialize(logCfg); err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	// Create component logger for root command
	rootLog := logging.Component("root")

	rootLog.Info("Sumpter environment initialized",
		zap.String("home", paths.Home),
		zap.String("workdir", paths.WorkDir),
		zap.String("config", configPath),
		zap.String("log_level", logLevel))

	// Store paths in context for use by subcommands
	cmd.Context()
}

// getVersion reads version from VERSION file (SSOT)
func getVersion() string {
	if version, err := os.ReadFile("VERSION"); err == nil {
		return strings.TrimSpace(string(version))
	}
	return DefaultVersion // fallback
}

// testableInitializeEnvironment is a version of initializeEnvironment that can be tested
// without calling log.Fatalf (returns errors instead)
// This version stops before logging initialization to avoid global state issues
func testableInitializeEnvironment(cmd *cobra.Command, args []string) (*config.Paths, error) {
	// Get flag values
	homeOverride, _ := cmd.Flags().GetString("home")
	workdirOverride, _ := cmd.Flags().GetString("workdir")
	logLevel, _ := cmd.Flags().GetString("log-level")
	logFormat, _ := cmd.Flags().GetString("log-format")
	logFile, _ := cmd.Flags().GetString("log-file")
	logColor, _ := cmd.Flags().GetBool("log-color")
	logTelemetry, _ := cmd.Flags().GetBool("log-telemetry")

	// Resolve application paths
	paths, err := config.ResolvePaths(homeOverride, workdirOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve application paths: %w", err)
	}

	// Create config loader
	loader := config.NewLoader(paths)

	// Load main configuration
	mainCfg, err := loader.LoadMainConfig()
	if err != nil {
		// Create default config manually (same as original function)
		mainCfg = &config.MainConfig{
			Version: "config/v0.1.0",
			Logging: config.LoggerConfig{
				Version:   "logger-config/v0.1.0",
				Level:     "info",
				Format:    "pretty",
				UseColor:  true,
				Component: "sumpter",
			},
			PII: config.PIIConfig{
				Version:  "pii-config/v0.1.0",
				Mode:     "safe",
				SafeOnly: true,
			},
			Paths: config.PathsConfig{
				CacheDir:  "cache",
				TempDir:   "temp",
				OutputDir: "output",
			},
			Performance: config.PerformanceConfig{
				MaxMemoryMB:    512,
				BufferSizeKB:   64,
				WorkerCount:    4,
				TimeoutSeconds: 300,
			},
			Telemetry: config.TelemetryConfig{
				Enabled:              false,
				ServiceName:          "sumpter",
				ServiceVersion:       "dev",
				Environment:          "development",
				BatchSize:            100,
				FlushIntervalSeconds: 30,
			},
		}
	}

	// Override config with command-line flags (same as original)
	resolvedLogFile := resolveLogFilePath(logFile, paths)
	mainCfg.Logging.Level = logLevel
	mainCfg.Logging.Format = logFormat
	mainCfg.Logging.UseColor = logColor
	mainCfg.Logging.File.Enabled = logFile != ""
	if logFile != "" {
		mainCfg.Logging.File.Path = resolvedLogFile
	}
	mainCfg.Logging.Telemetry.Enabled = logTelemetry

	// Convert to logging config (same as original)
	logCfg := logging.Config{
		Level:           logging.LogLevel(logLevel),
		UseColor:        logColor,
		Component:       "sumpter",
		PIIMode:         logging.PIIModeSafe,
		PIISafeOnly:     true,
		LogFile:         resolvedLogFile,
		EnableTelemetry: logTelemetry,
		ServiceName:     "sumpter",
		ServiceVersion:  "dev",
		Environment:     "development",
		LogRotation: logging.LogRotationConfig{
			Enabled:    true,
			MaxSizeMB:  10,
			MaxAgeDays: 30,
			MaxBackups: 5,
			Compress:   true,
			TimeFormat: "2006-01-02",
		},
	}
	_ = logCfg

	// Skip actual logging initialization to avoid global state issues
	// In a real scenario, this would call: logging.Initialize(logCfg)

	// Store paths in context (same as original)
	cmd.Context()

	return paths, nil
}

func resolveLogFilePath(logFile string, paths *config.Paths) string {
	if strings.TrimSpace(logFile) == "" {
		return ""
	}
	if filepath.IsAbs(logFile) {
		return logFile
	}
	return filepath.Join(paths.Logs, filepath.Base(filepath.Clean(logFile)))
}
