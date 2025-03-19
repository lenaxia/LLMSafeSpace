package logger

import (
	"github.com/lenaxia/llmsafespace/pkg/interfaces"
	"github.com/stretchr/testify/mock"
)

// MockLogger is a mock implementation of the logger interface
type MockLogger struct {
	mock.Mock
}

// NewMockLogger creates a new mock logger for testing
func NewMockLogger() *MockLogger {
	return &MockLogger{}
}

// Debug logs a debug message
func (m *MockLogger) Debug(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Info logs an info message
func (m *MockLogger) Info(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Warn logs a warning message
func (m *MockLogger) Warn(msg string, keysAndValues ...interface{}) {
	m.Called(msg, keysAndValues)
}

// Error logs an error message
func (m *MockLogger) Error(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

// Fatal logs a fatal message and exits
func (m *MockLogger) Fatal(msg string, err error, keysAndValues ...interface{}) {
	m.Called(msg, err, keysAndValues)
}

// With returns a logger with additional fields
func (m *MockLogger) With(keysAndValues ...interface{}) interfaces.LoggerInterface {
	args := m.Called(keysAndValues)
	if ret, ok := args.Get(0).(interfaces.LoggerInterface); ok {
		return ret
	}
	return m
}

// Sync flushes any buffered log entries
func (m *MockLogger) Sync() error {
	args := m.Called()
	return args.Error(0)
}
