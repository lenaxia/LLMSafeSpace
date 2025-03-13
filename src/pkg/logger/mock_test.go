package logger

import (
	"errors"
	"testing"

	"github.com/lenaxia/llmsafespace/pkg/mocks/logger"
	"github.com/stretchr/testify/assert"
)

func TestMockLogger(t *testing.T) {
	// Create a mock logger
	mockLogger := mocks.NewMockLogger()
	
	// Setup expectations
	mockLogger.On("Debug", "Debug message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Info", "Info message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Warn", "Warning message", []interface{}{"key", "value"}).Return()
	mockLogger.On("Error", "Error message", errors.New("test error"), []interface{}{"key", "value"}).Return()
	mockLogger.On("With", []interface{}{"context", "value"}).Return(mockLogger)
	mockLogger.On("Sync").Return(nil)
	
	// Use the logger
	logger.Debug("Debug message", "key", "value")
	logger.Info("Info message", "key", "value")
	logger.Warn("Warning message", "key", "value")
	logger.Error("Error message", errors.New("test error"), "key", "value")
	
	// Test With method
	contextLogger := logger.With("context", "value")
	assert.Equal(t, logger, contextLogger)
	
	// Test Sync method
	err := logger.Sync()
	assert.NoError(t, err)
	
	// Verify expectations
	logger.AssertExpectations(t)
}

func TestMockLoggerChaining(t *testing.T) {
	// Create a mock logger
	logger := logger.NewMockLogger()
	
	// Setup expectations for chained calls
	contextLogger := logger.NewMockLogger()
	logger.On("With", []interface{}{"context", "value"}).Return(contextLogger)
	contextLogger.On("Info", "Contextual message", []interface{}{"key", "value"}).Return()
	
	// Use the logger with chaining
	logger.With("context", "value").Info("Contextual message", "key", "value")
	
	// Verify expectations
	logger.AssertExpectations(t)
	contextLogger.AssertExpectations(t)
}
