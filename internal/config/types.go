package config

// MainConfig represents the complete Sumpter configuration
type MainConfig struct {
	Version     string            `yaml:"version" json:"version"`
	Logging     LoggerConfig      `yaml:"logging" json:"logging"`
	PII         PIIConfig         `yaml:"pii" json:"pii"`
	Paths       PathsConfig       `yaml:"paths" json:"paths"`
	Performance PerformanceConfig `yaml:"performance" json:"performance"`
	Telemetry   TelemetryConfig   `yaml:"telemetry" json:"telemetry"`
}

// LoggerConfig represents logger-specific configuration
type LoggerConfig struct {
	Version   string            `yaml:"version" json:"version"`
	Level     string            `yaml:"level" json:"level"`
	Format    string            `yaml:"format" json:"format"`
	UseColor  bool              `yaml:"use_color" json:"use_color"`
	Component string            `yaml:"component" json:"component"`
	File      FileLoggingConfig `yaml:"file" json:"file"`
	Telemetry TelemetryConfig   `yaml:"telemetry" json:"telemetry"`
}

// FileLoggingConfig represents file logging configuration
type FileLoggingConfig struct {
	Enabled  bool              `yaml:"enabled" json:"enabled"`
	Path     string            `yaml:"path" json:"path"`
	Rotation LogRotationConfig `yaml:"rotation" json:"rotation"`
}

// LogRotationConfig represents log rotation settings
type LogRotationConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	MaxSizeMB  int    `yaml:"max_size_mb" json:"max_size_mb"`
	MaxAgeDays int    `yaml:"max_age_days" json:"max_age_days"`
	MaxBackups int    `yaml:"max_backups" json:"max_backups"`
	Compress   bool   `yaml:"compress" json:"compress"`
	TimeFormat string `yaml:"time_format" json:"time_format"`
}

// PIIConfig represents PII detection and handling configuration
type PIIConfig struct {
	Version         string             `yaml:"version" json:"version"`
	Mode            string             `yaml:"mode" json:"mode"`
	SafeOnly        bool               `yaml:"safe_only" json:"safe_only"`
	AllowedContexts []string           `yaml:"allowed_contexts" json:"allowed_contexts"`
	Patterns        PIIDetectionConfig `yaml:"patterns" json:"patterns"`
	Reporting       PIIReportingConfig `yaml:"reporting" json:"reporting"`
	Exclusions      PIIExclusions      `yaml:"exclusions" json:"exclusions"`
}

// PIIDetectionConfig represents PII detection pattern configuration
type PIIDetectionConfig struct {
	EnabledDefaults bool                   `yaml:"enabled_defaults" json:"enabled_defaults"`
	Custom          []PIICustomPattern     `yaml:"custom" json:"custom"`
	Overrides       map[string]PIIOverride `yaml:"overrides" json:"overrides"`
}

// PIICustomPattern represents a custom PII detection pattern
type PIICustomPattern struct {
	Name        string `yaml:"name" json:"name"`
	Pattern     string `yaml:"pattern" json:"pattern"`
	Replacement string `yaml:"replacement" json:"replacement"`
	Category    string `yaml:"category" json:"category"`
	Severity    string `yaml:"severity" json:"severity"`
	Enabled     bool   `yaml:"enabled" json:"enabled"`
}

// PIIOverride represents an override for a default PII pattern
type PIIOverride struct {
	Enabled     *bool  `yaml:"enabled" json:"enabled"`
	Replacement string `yaml:"replacement" json:"replacement"`
	Severity    string `yaml:"severity" json:"severity"`
}

// PIIReportingConfig represents PII reporting configuration
type PIIReportingConfig struct {
	LogDetections   bool               `yaml:"log_detections" json:"log_detections"`
	LogSummary      bool               `yaml:"log_summary" json:"log_summary"`
	AlertThresholds PIIAlertThresholds `yaml:"alert_thresholds" json:"alert_thresholds"`
}

// PIIAlertThresholds represents alert thresholds for PII detections
type PIIAlertThresholds struct {
	HighSeverityCount     int `yaml:"high_severity_count" json:"high_severity_count"`
	CriticalSeverityCount int `yaml:"critical_severity_count" json:"critical_severity_count"`
}

// PIIExclusions represents patterns to exclude from PII scanning
type PIIExclusions struct {
	FilePatterns      []string `yaml:"file_patterns" json:"file_patterns"`
	ElementPatterns   []string `yaml:"element_patterns" json:"element_patterns"`
	AttributePatterns []string `yaml:"attribute_patterns" json:"attribute_patterns"`
}

// PathsConfig represents directory path configuration
type PathsConfig struct {
	Home      string `yaml:"home" json:"home"`
	WorkDir   string `yaml:"workdir" json:"workdir"`
	CacheDir  string `yaml:"cache_dir" json:"cache_dir"`
	TempDir   string `yaml:"temp_dir" json:"temp_dir"`
	OutputDir string `yaml:"output_dir" json:"output_dir"`
}

// PerformanceConfig represents performance tuning options
type PerformanceConfig struct {
	MaxMemoryMB    int `yaml:"max_memory_mb" json:"max_memory_mb"`
	BufferSizeKB   int `yaml:"buffer_size_kb" json:"buffer_size_kb"`
	WorkerCount    int `yaml:"worker_count" json:"worker_count"`
	TimeoutSeconds int `yaml:"timeout_seconds" json:"timeout_seconds"`
}

// TelemetryConfig represents telemetry and observability settings
type TelemetryConfig struct {
	Enabled              bool   `yaml:"enabled" json:"enabled"`
	ServiceName          string `yaml:"service_name" json:"service_name"`
	ServiceVersion       string `yaml:"service_version" json:"service_version"`
	Environment          string `yaml:"environment" json:"environment"`
	Endpoint             string `yaml:"endpoint" json:"endpoint"`
	BatchSize            int    `yaml:"batch_size" json:"batch_size"`
	FlushIntervalSeconds int    `yaml:"flush_interval_seconds" json:"flush_interval_seconds"`
}

// RetrieveConfig represents configuration for data retrieval operations
type RetrieveConfig struct {
	Version string                 `yaml:"version" json:"version"`
	Realms  map[string]RealmConfig `yaml:"realms" json:"realms"`
}

// RealmConfig represents configuration for a specific data realm
type RealmConfig struct {
	Enabled     bool                   `yaml:"enabled" json:"enabled"`
	Client      ClientConfig           `yaml:"client" json:"client"`
	Credentials map[string]interface{} `yaml:"credentials" json:"credentials"`
	RateLimits  RateLimitConfig        `yaml:"rate_limits" json:"rate_limits"`
	Endpoints   map[string]string      `yaml:"endpoints" json:"endpoints"`
	Options     map[string]interface{} `yaml:"options" json:"options"`
}

// ClientConfig represents HTTP client configuration
type ClientConfig struct {
	UserAgent      string `yaml:"user_agent" json:"user_agent"`
	TimeoutSeconds int    `yaml:"timeout_seconds" json:"timeout_seconds"`
}

// RateLimitConfig represents rate limiting settings
type RateLimitConfig struct {
	RequestsPerSecond float64 `yaml:"requests_per_second" json:"requests_per_second"`
	BurstLimit        int     `yaml:"burst_limit" json:"burst_limit"`
	BackoffSeconds    float64 `yaml:"backoff_seconds" json:"backoff_seconds"`
}
