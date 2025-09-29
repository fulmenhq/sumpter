package logging

import (
	"go.uber.org/zap"
)

// ComponentLogger provides component-specific logging
type ComponentLogger struct {
	logger *zap.Logger
	sugar  *zap.SugaredLogger
	name   string
}

// Component creates a component-specific logger
func Component(name string) *ComponentLogger {
	if globalLogger == nil {
		// Initialize with default config if not already initialized
		_ = Initialize(DefaultConfig())
	}

	return &ComponentLogger{
		logger: globalLogger.Named(name),
		sugar:  globalLogger.Sugar().Named(name),
		name:   name,
	}
}

// Info logs an info message for this component
func (c *ComponentLogger) Info(msg string, fields ...zap.Field) {
	c.logger.Info(msg, fields...)
}

// Debug logs a debug message for this component
func (c *ComponentLogger) Debug(msg string, fields ...zap.Field) {
	c.logger.Debug(msg, fields...)
}

// Warn logs a warning message for this component
func (c *ComponentLogger) Warn(msg string, fields ...zap.Field) {
	c.logger.Warn(msg, fields...)
}

// Error logs an error message for this component
func (c *ComponentLogger) Error(msg string, fields ...zap.Field) {
	c.logger.Error(msg, fields...)
}

// Infof logs an info message with formatting for this component
func (c *ComponentLogger) Infof(template string, args ...interface{}) {
	c.sugar.Infof(template, args...)
}

// Debugf logs a debug message with formatting for this component
func (c *ComponentLogger) Debugf(template string, args ...interface{}) {
	c.sugar.Debugf(template, args...)
}

// Warnf logs a warning message with formatting for this component
func (c *ComponentLogger) Warnf(template string, args ...interface{}) {
	c.sugar.Warnf(template, args...)
}

// Errorf logs an error message with formatting for this component
func (c *ComponentLogger) Errorf(template string, args ...interface{}) {
	c.sugar.Errorf(template, args...)
}

// WithFields creates a logger with additional fields for this component
func (c *ComponentLogger) WithFields(fields ...zap.Field) *zap.Logger {
	return c.logger.With(fields...)
}

// WithField adds a single field to the component logger
func (c *ComponentLogger) WithField(key string, value interface{}) *ComponentLogger {
	return &ComponentLogger{
		logger: c.logger.With(zap.Any(key, value)),
		sugar:  c.sugar.With(key, value),
		name:   c.name,
	}
}

// WithError adds an error field to the component logger
func (c *ComponentLogger) WithError(err error) *ComponentLogger {
	return &ComponentLogger{
		logger: c.logger.With(zap.Error(err)),
		sugar:  c.sugar.With("error", err),
		name:   c.name,
	}
}

// LogXMLProcessing logs XML processing events with PII awareness
func (c *ComponentLogger) LogXMLProcessing(event string, element string, attributes map[string]string, data interface{}) {
	// Sanitize element name and attributes for PII
	sanitizedElement := element
	if piiDetector != nil {
		sanitizedElement = piiDetector.SanitizeData(element, "xml_element")
	}

	sanitizedAttrs := make(map[string]string)
	for k, v := range attributes {
		if piiDetector != nil {
			sanitizedAttrs[k] = piiDetector.SanitizeData(v, "xml_attribute")
		} else {
			sanitizedAttrs[k] = v
		}
	}

	c.logger.Info("XML processing",
		zap.String("event", event),
		zap.String("element", sanitizedElement),
		zap.Any("attributes", sanitizedAttrs),
		zap.Any("data", data),
	)
}

// LogPerformance logs performance metrics
func (c *ComponentLogger) LogPerformance(operation string, duration int64, size int64) {
	c.logger.Info("Performance metric",
		zap.String("operation", operation),
		zap.Int64("duration_ms", duration),
		zap.Int64("size_bytes", size),
		zap.Float64("mb_per_sec", float64(size)/(float64(duration)/1000.0)/(1024*1024)),
	)
}

// LogSecurityEvent logs security-related events
func (c *ComponentLogger) LogSecurityEvent(event string, severity string, details map[string]interface{}) {
	logFunc := c.logger.Info
	switch severity {
	case "high", "critical":
		logFunc = c.logger.Error
	case "medium":
		logFunc = c.logger.Warn
	}

	logFunc("Security event",
		zap.String("event_type", "security_"+event),
		zap.String("severity", severity),
		zap.Any("details", details),
	)
}
