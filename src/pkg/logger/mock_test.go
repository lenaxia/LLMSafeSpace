package logger

import (
	"errors"
	"testing"

	mocklogger "github.com/lenaxia/llmsafespace/pkg/mocks/logger"
	"github.com/stretchr/testify/assert"
)

func TestMockLogger(t *testing.T) {
	// Create a mock logger
	mockLogger := mocklogger.NewMockLogger()
	
	// Setup expectations
	mockLogger.On("Debug", "Debug message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Info", "Info message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Warn", "Warning message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Error", "Error message", errors.New("test error"), []interface{}{"key", "value"}).Return()
	mockLogger.On("With", []interface{}{"context", "value"}).Return(mockLogger)
	mockLogger.On("Sync").Return(nil)
	
	// Use the logger
	mockLogger.Debug("Debug message", "key", "value")
	mockLogger.Info("Info message", "key", "value")
	mockLogger.Warn("Warning message", "key", "value")
	mockLogger.Error("Error message", errors.New("test error"), "key", "value")
	
	// Test With method
	contextLogger := mockLogger.With("context", "value")
	assert.Equal(t, mockLogger, contextLogger)
	
	// Test Sync method
	err := mockLogger.Sync()
	assert.NoError(t, err)
	
	// Verify expectations
	mockLogger.AssertExpectations(t)
}

func TestMockLoggerChaining(t *testing.T) {
	// Create a mock logger
	mockLogger := mocklogger.NewMockLogger()
	
	// Setup expectations for chained calls
	contextLogger := mocklogger.NewMockLogger()
	mockLogger.On("With", []interface{}{"context", "value"}).Return(contextLogger)
	contextLogger.On("Info", "Contextual message", []interface{}{"key", "value"}).Return()
	
	// Use the logger with chaining
	mockLogger.With("context", "value").Info("Contextual message", "key", "value")
	
	// Verify expectations
	mockLogger.AssertExpectations(t)
	contextLogger.AssertExpectations(t)
}
