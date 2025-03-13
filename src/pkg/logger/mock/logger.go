package mock

import (
	"github.com/stretchr/testify/mock"
)

// Logger is a mock implementation of the logger interface
type Logger struct {
	mock.Mock
}

// Debug logs a debug message
func (m *Logger) Debug(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Info logs an info message
func (m *Logger) Info(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Warn logs a warning message
func (m *Logger) Warn(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Error logs an error message
func (m *Logger) Error(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

// Fatal logs a fatal message and exits
func (m *Logger) Fatal(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

// With returns a logger with additional fields
func (m *Logger) With(keysAndValues ...interface{}) *Logger {
	args := m.Called(keysAndValues)
	return args.Get(0).(*Logger)
}

// Sync flushes any buffered log entries
func (m *Logger) Sync() error {
	args := m.Called()
	return args.Error(0)
}

// NewTestLogger creates a new mock logger for testing
func NewTestLogger() *Logger {
	return &Logger{}
}
