package logging

import (
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/term"
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

	// Determine if color should be enabled for console output
	useColor := shouldEnableColor(config)

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
		EncodeLevel:    cleanLevelEncoder,
		EncodeTime:     rfc3339TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	// If pretty console and color is enabled, use zap's color encoder
	if !config.EnableTelemetry && useColor {
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

// cleanLevelEncoder encodes log levels as clean UTF-8 text without ANSI escape sequences
func cleanLevelEncoder(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	enc.AppendString("[" + l.CapitalString() + "]")
}

// shouldEnableColor decides whether to enable color for console logs
func shouldEnableColor(cfg Config) bool {
	if !cfg.UseColor {
		return false
	}
	// JSON/telemetry logs must not include color
	if cfg.EnableTelemetry {
		return false
	}
	// Respect NO_COLOR and dumb terminals
	if strings.TrimSpace(os.Getenv("NO_COLOR")) != "" {
		return false
	}
	if os.Getenv("TERM") == "dumb" {
		return false
	}
	// Require a TTY on stderr (logger writes to stderr)
	return term.IsTerminal(int(os.Stderr.Fd()))
}

// Global logging functions with PII sanitization

// GetLogger returns the global logger instance
func GetLogger() *zap.Logger {
	return globalLogger
}

// GetSugar returns the global sugar logger instance
func GetSugar() *zap.SugaredLogger {
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
