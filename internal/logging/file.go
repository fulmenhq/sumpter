package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap/zapcore"
)

// newFileCore creates a file logging core with rotation support
func newFileCore(config Config) (zapcore.Core, error) {
	if config.LogFile == "" {
		return nil, fmt.Errorf("log file path is required")
	}

	// Ensure directory exists
	dir := filepath.Dir(config.LogFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create encoder config (same as main logger)
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

	// Use JSON format for file logging (better for log shipping)
	encoder := zapcore.NewJSONEncoder(encoderConfig)

	// Create write syncer with rotation
	writeSyncer, err := newRotatingWriteSyncer(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create rotating write syncer: %w", err)
	}

	// Map log level
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

	return zapcore.NewCore(encoder, writeSyncer, zapLevel), nil
}

// rotatingWriteSyncer implements log rotation
type rotatingWriteSyncer struct {
	config      Config
	currentFile *os.File
	filePath    string
	maxSize     int64
	currentSize int64
}

// newRotatingWriteSyncer creates a new rotating write syncer
func newRotatingWriteSyncer(config Config) (*rotatingWriteSyncer, error) {
	maxSizeBytes := int64(config.LogRotation.MaxSizeMB) * 1024 * 1024

	rws := &rotatingWriteSyncer{
		config:   config,
		filePath: config.LogFile,
		maxSize:  maxSizeBytes,
	}

	// Open initial file
	if err := rws.openFile(); err != nil {
		return nil, err
	}

	return rws, nil
}

// Write implements zapcore.WriteSyncer
func (rws *rotatingWriteSyncer) Write(p []byte) (n int, err error) {
	// Check if rotation is needed
	if rws.currentSize+int64(len(p)) > rws.maxSize && rws.config.LogRotation.Enabled {
		if err := rws.rotate(); err != nil {
			// Log rotation error to stderr (don't fail the write)
			fmt.Fprintf(os.Stderr, "Log rotation failed: %v\n", err)
		}
	}

	// Write to current file
	n, err = rws.currentFile.Write(p)
	rws.currentSize += int64(n)

	return n, err
}

// Sync implements zapcore.WriteSyncer
func (rws *rotatingWriteSyncer) Sync() error {
	if rws.currentFile != nil {
		return rws.currentFile.Sync()
	}
	return nil
}

// Close closes the current file
func (rws *rotatingWriteSyncer) Close() error {
	if rws.currentFile != nil {
		return rws.currentFile.Close()
	}
	return nil
}

// openFile opens or creates the log file
func (rws *rotatingWriteSyncer) openFile() error {
	// Close existing file if open
	if rws.currentFile != nil {
		_ = rws.currentFile.Close()
	}

	// Open file for append/create
	file, err := os.OpenFile(rws.filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}

	// Get current file size
	if stat, err := file.Stat(); err == nil {
		rws.currentSize = stat.Size()
	}

	rws.currentFile = file
	return nil
}

// rotate performs log rotation
func (rws *rotatingWriteSyncer) rotate() error {
	// Close current file
	if rws.currentFile != nil {
		_ = rws.currentFile.Close()
	}

	// Generate rotation filename with timestamp
	timestamp := time.Now().Format(rws.config.LogRotation.TimeFormat)
	ext := filepath.Ext(rws.filePath)
	base := strings.TrimSuffix(rws.filePath, ext)
	rotatedFile := fmt.Sprintf("%s.%s%s", base, timestamp, ext)

	// Rename current file
	if err := os.Rename(rws.filePath, rotatedFile); err != nil {
		return fmt.Errorf("failed to rotate log file: %w", err)
	}

	// Compress if enabled
	if rws.config.LogRotation.Compress {
		go rws.compressFile(rotatedFile) // Compress asynchronously
	}

	// Clean up old files
	if err := rws.cleanupOldFiles(); err != nil {
		// Don't fail rotation for cleanup errors
		fmt.Fprintf(os.Stderr, "Log cleanup warning: %v\n", err)
	}

	// Open new file
	return rws.openFile()
}

// cleanupOldFiles removes old rotated files beyond MaxBackups
func (rws *rotatingWriteSyncer) cleanupOldFiles() error {
	if rws.config.LogRotation.MaxBackups <= 0 {
		return nil
	}

	pattern := rws.filePath + ".*"
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return err
	}

	// Sort by modification time (newest first)
	type fileInfo struct {
		path string
		time time.Time
	}

	var files []fileInfo
	for _, match := range matches {
		if stat, err := os.Stat(match); err == nil {
			files = append(files, fileInfo{match, stat.ModTime()})
		}
	}

	// Sort by time (newest first)
	for i := 0; i < len(files)-1; i++ {
		for j := i + 1; j < len(files); j++ {
			if files[i].time.Before(files[j].time) {
				files[i], files[j] = files[j], files[i]
			}
		}
	}

	// Remove files beyond MaxBackups
	for i := rws.config.LogRotation.MaxBackups; i < len(files); i++ {
		if err := os.Remove(files[i].path); err != nil {
			return fmt.Errorf("failed to remove old log file %s: %w", files[i].path, err)
		}
	}

	return nil
}

// compressFile compresses a rotated log file (placeholder implementation)
func (rws *rotatingWriteSyncer) compressFile(filePath string) {
	// TODO: Implement gzip compression
	// For now, just log that compression would happen
	fmt.Fprintf(os.Stderr, "Log compression not yet implemented for: %s\n", filePath)
}

// GetLogFileInfo returns information about the current log file
func GetLogFileInfo(logPath string) (map[string]interface{}, error) {
	stat, err := os.Stat(logPath)
	if err != nil {
		return nil, err
	}

	// Count rotated files
	pattern := logPath + ".*"
	matches, _ := filepath.Glob(pattern)

	return map[string]interface{}{
		"current_size_mb": float64(stat.Size()) / (1024 * 1024),
		"rotated_files":   len(matches),
		"last_modified":   stat.ModTime().Format(time.RFC3339),
	}, nil
}
