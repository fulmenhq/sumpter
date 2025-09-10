package config

import "strings"

// EnvVarType represents the type of environment variable value
type EnvVarType string

const (
	EnvVarTypeString EnvVarType = "string"
	EnvVarTypePath   EnvVarType = "path"
	EnvVarTypeBool   EnvVarType = "bool"
	EnvVarTypeInt    EnvVarType = "int"
)

// EnvVarDefinition defines a SUMPTER environment variable
type EnvVarDefinition struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Type        EnvVarType `json:"type"`
	Default     string     `json:"default,omitempty"`
	Required    bool       `json:"required"`
	Example     string     `json:"example,omitempty"`
	Category    string     `json:"category"`
}

// SumpterEnvironmentVariables is the single source of truth for all SUMPTER_ prefixed environment variables
var SumpterEnvironmentVariables = map[string]EnvVarDefinition{
	"SUMPTER_HOME": {
		Name:        "SUMPTER_HOME",
		Description: "Root directory for Sumpter user data and configuration",
		Type:        EnvVarTypePath,
		Default:     "", // Uses OS-specific default
		Required:    false,
		Example:     "/Users/username/.sumpter",
		Category:    "paths",
	},
	"SUMPTER_WORKDIR": {
		Name:        "SUMPTER_WORKDIR",
		Description: "Preferred location for large temporary files and work artifacts",
		Type:        EnvVarTypePath,
		Default:     "", // Falls back to SUMPTER_HOME/work
		Required:    false,
		Example:     "/tmp/sumpter-work",
		Category:    "paths",
	},
	"SUMPTER_ENV": {
		Name:        "SUMPTER_ENV",
		Description: "Runtime environment (development, staging, production)",
		Type:        EnvVarTypeString,
		Default:     "development",
		Required:    false,
		Example:     "production",
		Category:    "runtime",
	},
	"SUMPTER_LOG_LEVEL": {
		Name:        "SUMPTER_LOG_LEVEL",
		Description: "Logging level (trace, debug, info, warn, error)",
		Type:        EnvVarTypeString,
		Default:     "info",
		Required:    false,
		Example:     "debug",
		Category:    "logging",
	},
	"SUMPTER_LOG_FORMAT": {
		Name:        "SUMPTER_LOG_FORMAT",
		Description: "Log output format (console, json)",
		Type:        EnvVarTypeString,
		Default:     "console",
		Required:    false,
		Example:     "json",
		Category:    "logging",
	},
	"SUMPTER_CONFIG": {
		Name:        "SUMPTER_CONFIG",
		Description: "Path to Sumpter configuration file",
		Type:        EnvVarTypePath,
		Default:     "",
		Required:    false,
		Example:     "/etc/sumpter/config.yaml",
		Category:    "configuration",
	},
	"SUMPTER_MAX_MEMORY": {
		Name:        "SUMPTER_MAX_MEMORY",
		Description: "Maximum memory usage target in MB",
		Type:        EnvVarTypeInt,
		Default:     "512",
		Required:    false,
		Example:     "1024",
		Category:    "performance",
	},
	"SUMPTER_WORKER_COUNT": {
		Name:        "SUMPTER_WORKER_COUNT",
		Description: "Number of worker goroutines for parallel processing",
		Type:        EnvVarTypeInt,
		Default:     "4",
		Required:    false,
		Example:     "8",
		Category:    "performance",
	},
	"SUMPTER_TIMEOUT": {
		Name:        "SUMPTER_TIMEOUT",
		Description: "Default timeout for operations in seconds",
		Type:        EnvVarTypeInt,
		Default:     "300",
		Required:    false,
		Example:     "600",
		Category:    "performance",
	},
	"SUMPTER_TELEMETRY_ENABLED": {
		Name:        "SUMPTER_TELEMETRY_ENABLED",
		Description: "Enable telemetry data collection",
		Type:        EnvVarTypeBool,
		Default:     "false",
		Required:    false,
		Example:     "true",
		Category:    "telemetry",
	},
	"SUMPTER_SERVICE_NAME": {
		Name:        "SUMPTER_SERVICE_NAME",
		Description: "Service name for telemetry identification",
		Type:        EnvVarTypeString,
		Default:     "sumpter",
		Required:    false,
		Example:     "sumpter-prod",
		Category:    "telemetry",
	},
}

// GetSumpterEnvVars returns all SUMPTER environment variables
func GetSumpterEnvVars() map[string]EnvVarDefinition {
	return SumpterEnvironmentVariables
}

// GetSumpterEnvVarsByCategory returns environment variables grouped by category
func GetSumpterEnvVarsByCategory() map[string][]EnvVarDefinition {
	categories := make(map[string][]EnvVarDefinition)

	for _, envVar := range SumpterEnvironmentVariables {
		categories[envVar.Category] = append(categories[envVar.Category], envVar)
	}

	return categories
}

// IsSumpterEnvVar checks if a given environment variable name is a recognized SUMPTER variable
func IsSumpterEnvVar(name string) bool {
	_, exists := SumpterEnvironmentVariables[name]
	return exists
}

// GetSumpterEnvVarDefinition returns the definition for a SUMPTER environment variable
func GetSumpterEnvVarDefinition(name string) (EnvVarDefinition, bool) {
	def, exists := SumpterEnvironmentVariables[name]
	return def, exists
}

// GetAllSumpterPrefixes returns all SUMPTER_ prefixes for pattern matching
func GetAllSumpterPrefixes() []string {
	prefixes := make([]string, 0, len(SumpterEnvironmentVariables))
	seen := make(map[string]bool)

	for name := range SumpterEnvironmentVariables {
		if strings.HasPrefix(name, "SUMPTER_") {
			prefix := strings.SplitN(name, "_", 2)[0] + "_"
			if !seen[prefix] {
				seen[prefix] = true
				prefixes = append(prefixes, prefix)
			}
		}
	}

	return prefixes
}
