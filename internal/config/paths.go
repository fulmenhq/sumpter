package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Paths holds resolved directory paths for Sumpter
type Paths struct {
	Home    string // SUMPTER_HOME
	WorkDir string // SUMPTER_WORKDIR
	Cache   string // Cache directory
	Logs    string // Logs directory
	Configs string // Configs directory
	Temp    string // Temp directory
}

// ResolvePaths resolves all Sumpter directory paths with proper fallbacks
func ResolvePaths(homeOverride, workdirOverride string) (*Paths, error) {
	paths := &Paths{}

	// Resolve SUMPTER_HOME
	home, err := resolveHomeDir(homeOverride)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve SUMPTER_HOME: %w", err)
	}
	paths.Home = home

	// Resolve SUMPTER_WORKDIR
	workdir, err := resolveWorkDir(workdirOverride, home)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve SUMPTER_WORKDIR: %w", err)
	}
	paths.WorkDir = workdir

	// Resolve subdirectories
	paths.Cache = filepath.Join(home, "cache")
	paths.Logs = filepath.Join(home, "logs")
	paths.Configs = filepath.Join(home, "configs")
	paths.Temp = filepath.Join(workdir, "temp")

	// Create directories with proper permissions
	if err := createDirectories(paths); err != nil {
		return nil, fmt.Errorf("failed to create directories: %w", err)
	}

	return paths, nil
}

// resolveHomeDir resolves the SUMPTER_HOME directory
func resolveHomeDir(override string) (string, error) {
	// 1. Use override if provided
	if override != "" {
		return filepath.Abs(override)
	}

	// 2. Use SUMPTER_HOME environment variable
	if home := os.Getenv("SUMPTER_HOME"); home != "" {
		return filepath.Abs(home)
	}

	// 3. Use OS-specific user data directory
	return getOSUserDataDir()
}

// resolveWorkDir resolves the SUMPTER_WORKDIR directory
func resolveWorkDir(override, home string) (string, error) {
	// 1. Use override if provided
	if override != "" {
		return filepath.Abs(override)
	}

	// 2. Use SUMPTER_WORKDIR environment variable
	if workdir := os.Getenv("SUMPTER_WORKDIR"); workdir != "" {
		return filepath.Abs(workdir)
	}

	// 3. Use SUMPTER_HOME/work as default
	workDir := filepath.Join(home, "work")
	return workDir, nil
}

// getOSUserDataDir returns the OS-specific user data directory
func getOSUserDataDir() (string, error) {
	var baseDir string

	switch runtime.GOOS {
	case "darwin":
		// macOS: ~/Library/Application Support/Sumpter
		if home := os.Getenv("HOME"); home != "" {
			baseDir = filepath.Join(home, "Library", "Application Support", "Sumpter")
		} else {
			return "", fmt.Errorf("HOME environment variable not set")
		}

	case "windows":
		// Windows: %AppData%\Sumpter
		if appData := os.Getenv("AppData"); appData != "" {
			baseDir = filepath.Join(appData, "Sumpter")
		} else {
			return "", fmt.Errorf("AppData environment variable not set")
		}

	case "linux":
		// Linux: $XDG_DATA_HOME/sumpter or $HOME/.local/share/sumpter
		if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
			baseDir = filepath.Join(xdgData, "sumpter")
		} else if home := os.Getenv("HOME"); home != "" {
			baseDir = filepath.Join(home, ".local", "share", "sumpter")
		} else {
			return "", fmt.Errorf("neither XDG_DATA_HOME nor HOME environment variables set")
		}

	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	return baseDir, nil
}

// createDirectories creates all required directories with proper permissions
func createDirectories(paths *Paths) error {
	dirs := []string{
		paths.Home,
		paths.WorkDir,
		paths.Cache,
		paths.Logs,
		paths.Configs,
		paths.Temp,
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	return nil
}

// GetDefaultConfigPath returns the default path for the main config file
func (p *Paths) GetDefaultConfigPath() string {
	return filepath.Join(p.Configs, "sumpter.yaml")
}

// GetLoggerConfigPath returns the default path for the logger config file
func (p *Paths) GetLoggerConfigPath() string {
	return filepath.Join(p.Configs, "logger.yaml")
}

// GetPIIConfigPath returns the default path for the PII config file
func (p *Paths) GetPIIConfigPath() string {
	return filepath.Join(p.Configs, "pii.yaml")
}

// GetDefaultLogPath returns the default path for log files
func (p *Paths) GetDefaultLogPath() string {
	return filepath.Join(p.Logs, "sumpter.log")
}

// GetCachePath returns a path within the cache directory
func (p *Paths) GetCachePath(filename string) string {
	return filepath.Join(p.Cache, filename)
}

// GetTempPath returns a path within the temp directory
func (p *Paths) GetTempPath(filename string) string {
	return filepath.Join(p.Temp, filename)
}

// EnsurePaths ensures all paths are properly resolved and directories exist
func (p *Paths) EnsurePaths() error {
	return createDirectories(p)
}
