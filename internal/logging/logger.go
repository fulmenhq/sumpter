package logging

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	// globalLogger is the singleton logger instance
	globalLogger *zap.Logger
	// sugar provides a more convenient API
	sugar *zap.SugaredLogger
	// piiDetector handles PII detection and sanitization
	piiDetector *PIIDetector
)

// LogLevel represents the logging level
type LogLevel string

const (
	TraceLevel LogLevel = "trace"
	DebugLevel LogLevel = "debug"
	InfoLevel  LogLevel = "info"
	WarnLevel  LogLevel = "warn"
	ErrorLevel LogLevel = "error"
)

// Initialize sets up the global logger with RFC3339 formatting and stderr output
func Initialize(config Config) error {
	// Map our log levels to zap levels
	var zapLevel zapcore.Level
	switch config.Level {
	case TraceLevel, DebugLevel:
		zapLevel = zapcore.DebugLevel
	case InfoLevel:
		zapLevel = zapcore.InfoLevel
	case WarnLevel:
		zapLevel = zapcore.WarnLevel
	case ErrorLevel:
		zapLevel = zapcore.ErrorLevel
	default:
		zapLevel = zapcore.InfoLevel
	}

	// Initialize PII detector
	piiDetector = NewPIIDetector(config.PIIMode, config.AllowedPIIContexts)

	// Create encoder config with RFC3339 timestamps (Fulmen standard)
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    zapcore.OmitKey,
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     rfc3339TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// Use colored output if requested and supported
	if config.UseColor && isTerminal() {
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// Create encoder based on format preference
	var encoder zapcore.Encoder
	if config.EnableTelemetry {
		// Use JSON for telemetry/shipping
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		// Use console encoder for human-readable output
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	// Create core that writes to stderr (following Fulmen standards)
	core := zapcore.NewCore(
		encoder,
		zapcore.AddSync(os.Stderr),
		zapLevel,
	)

	// Add file logging if configured
	if config.LogFile != "" {
		fileCore, err := newFileCore(config)
		if err != nil {
			return fmt.Errorf("failed to create file logger: %w", err)
		}
		core = zapcore.NewTee(core, fileCore)
	}

	// Create logger with caller information
	globalLogger = zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))

	// Add component name if provided
	if config.Component != "" {
		globalLogger = globalLogger.Named(config.Component)
	}

	// Add telemetry fields if enabled
	if config.EnableTelemetry {
		globalLogger = globalLogger.With(
			zap.String("service", config.ServiceName),
			zap.String("version", config.ServiceVersion),
			zap.String("environment", config.Environment),
		)
	}

	// Create sugar logger for convenience
	sugar = globalLogger.Sugar()

	return nil
}

// rfc3339TimeEncoder encodes time in RFC3339 format (Fulmen standard)
func rfc3339TimeEncoder(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString(t.Format(time.RFC3339))
}

// isTerminal checks if stderr is a terminal for color support
func isTerminal() bool {
	if fileInfo, _ := os.Stderr.Stat(); fileInfo != nil {
		return (fileInfo.Mode() & os.ModeCharDevice) != 0
	}
	return false
}

// Global logging functions with PII sanitization

// nopLogger is returned by GetLogger when no global logger has been configured, so
// callers that hold the returned *zap.Logger and call methods on it (e.g.
// GetLogger().Info(...) or `l := GetLogger(); l.Warn(...)`) never panic on a nil
// logger. The package-level Info/Warn/Error/Debug helpers already nil-guard; this
// extends the same safety to the accessor. A no-op logger drops all output, matching
// the unconfigured-logger intent.
var nopLogger = zap.NewNop()
var nopSugar = nopLogger.Sugar()

// GetLogger returns the global logger instance, or a no-op logger when none has been
// configured. It never returns nil, so callers can invoke methods on the result
// directly without a nil check.
func GetLogger() *zap.Logger {
	if globalLogger == nil {
		return nopLogger
	}
	return globalLogger
}

// GetSugar returns the global sugar logger instance, or a no-op sugar logger
// when none has been configured.
func GetSugar() *zap.SugaredLogger {
	if sugar == nil {
		return nopSugar
	}
	return sugar
}

// Info logs an info level message with PII sanitization
func Info(msg string, fields ...zap.Field) {
	if globalLogger != nil {
		globalLogger.Info(msg, fields...)
	}
}

// Warn logs a warning level message with PII sanitization
func Warn(msg string, fields ...zap.Field) {
	if globalLogger != nil {
		globalLogger.Warn(msg, fields...)
	}
}

// Error logs an error level message with PII sanitization
func Error(msg string, fields ...zap.Field) {
	if globalLogger != nil {
		globalLogger.Error(msg, fields...)
	}
}

// Debug logs a debug level message with PII sanitization
func Debug(msg string, fields ...zap.Field) {
	if globalLogger != nil {
		globalLogger.Debug(msg, fields...)
	}
}

// Sync flushes any buffered log entries
func Sync() error {
	if globalLogger != nil {
		return globalLogger.Sync()
	}
	return nil
}
