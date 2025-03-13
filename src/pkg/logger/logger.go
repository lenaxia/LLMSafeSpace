package logger

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)


// Logger provides structured logging
type Logger struct {
	logger *zap.Logger
}

// Ensure Logger implements LoggerInterface
var _ LoggerInterface = (*Logger)(nil)

// New creates a new logger
func New(development bool, level string, encoding string) (*Logger, error) {
	// Parse log level
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	// Create logger config
	var config zap.Config
	if development {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		config = zap.NewProductionConfig()
		config.EncoderConfig.TimeKey = "timestamp"
		config.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}

	// Set encoding
	if encoding == "console" {
		config.Encoding = "console"
	} else {
		config.Encoding = "json"
	}

	// Set level
	config.Level = zap.NewAtomicLevelAt(zapLevel)

	// Build logger
	logger, err := config.Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build logger: %w", err)
	}

	return &Logger{logger: logger}, nil
}

// Sync flushes any buffered log entries
func (l *Logger) Sync() error {
	return l.logger.Sync()
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.logger.Debug(msg, fieldsFromKeysAndValues(keysAndValues)...)
}

// Info logs an info message
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, fieldsFromKeysAndValues(keysAndValues)...)
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.logger.Warn(msg, fieldsFromKeysAndValues(keysAndValues)...)
}

// Error logs an error message
func (l *Logger) Error(msg string, err error, keysAndValues ...interface{}) {
	fields := fieldsFromKeysAndValues(keysAndValues)
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	l.logger.Error(msg, fields...)
}

// Fatal logs a fatal message and exits
func (l *Logger) Fatal(msg string, err error, keysAndValues ...interface{}) {
	fields := fieldsFromKeysAndValues(keysAndValues)
	if err != nil {
		fields = append(fields, zap.Error(err))
	}
	l.logger.Fatal(msg, fields...)
}

// With returns a logger with additional fields
func (l *Logger) With(keysAndValues ...interface{}) LoggerInterface {
	return &Logger{
		logger: l.logger.With(fieldsFromKeysAndValues(keysAndValues)...),
	}
}

// fieldsFromKeysAndValues converts a list of key-value pairs to zap fields
func fieldsFromKeysAndValues(keysAndValues []interface{}) []zap.Field {
	if len(keysAndValues) == 0 {
		return []zap.Field{}
	}

	fields := make([]zap.Field, 0, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok {
			key = fmt.Sprintf("INVALID_KEY_%d", i)
		}

		var value interface{}
		if i+1 < len(keysAndValues) {
			value = keysAndValues[i+1]
		} else {
			value = "MISSING_VALUE"
		}

		fields = append(fields, zap.Any(key, value))
	}

	return fields
}
